// File: internal/camera/camera.go
package camera

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// SignalMessage represents a message from the camera
type SignalMessage struct {
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
	Camera string          `json:"camera"`
	Time   time.Time       `json:"time"`
}

// CameraEvent represents processed camera data
type CameraEvent struct {
	CameraID  string    `json:"camera_id"`
	EventType string    `json:"event_type"`
	Data      []byte    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

type CameraManager struct {
	logger        *zap.Logger
	signalAddr    string
	streamAddr    string
	eventHandlers map[string][]EventHandler
	cameras       sync.Map
	upgrader      websocket.Upgrader
}

type EventHandler func(event CameraEvent) error

func NewCameraManager(signalAddr, streamAddr string, logger *zap.Logger) (*CameraManager, error) {
	if signalAddr == "" || streamAddr == "" {
		return nil, fmt.Errorf("signalAddr and streamAddr must be provided")
	}
	return &CameraManager{
		logger:        logger,
		signalAddr:    signalAddr,
		streamAddr:    streamAddr,
		eventHandlers: make(map[string][]EventHandler),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // In production, implement proper origin checking
			},
		},
	}, nil
}

func (cm *CameraManager) Start(ctx context.Context) error {
	// Start signal server
	signalServer := &http.Server{
		Addr:    cm.signalAddr,
		Handler: cm.setupSignalHandler(),
	}

	// Start stream server
	streamServer := &http.Server{
		Addr:    cm.streamAddr,
		Handler: cm.setupStreamHandler(),
	}

	errChan := make(chan error, 2)

	// Start signal server
	go func() {
		cm.logger.Info("starting signal server", zap.String("addr", cm.signalAddr))
		if err := signalServer.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- fmt.Errorf("signal server error: %w", err)
		}
	}()

	// Start stream server
	go func() {
		cm.logger.Info("starting stream server", zap.String("addr", cm.streamAddr))
		if err := streamServer.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- fmt.Errorf("stream server error: %w", err)
		}
	}()

	// Handle shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := signalServer.Shutdown(shutdownCtx); err != nil {
			cm.logger.Error("signal server shutdown failed", zap.Error(err))
		}
		if err := streamServer.Shutdown(shutdownCtx); err != nil {
			cm.logger.Error("stream server shutdown failed", zap.Error(err))
		}
	}()

	// Wait for any server errors
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (cm *CameraManager) setupSignalHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/camera/connect", func(w http.ResponseWriter, r *http.Request) {
		conn, err := cm.upgrader.Upgrade(w, r, nil)
		if err != nil {
			cm.logger.Error("websocket upgrade failed", zap.Error(err))
			return
		}

		go cm.handleCameraConnection(conn)
	})

	return mux
}

func (cm *CameraManager) setupStreamHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		cameraID := r.URL.Path[len("/stream/"):]
		cm.handleStreamRequest(w, cameraID)
	})

	return mux
}

func (cm *CameraManager) handleCameraConnection(conn *websocket.Conn) {
	defer conn.Close()

	for {
		var msg SignalMessage
		if err := conn.ReadJSON(&msg); err != nil {
			cm.logger.Error("failed to read message", zap.Error(err))
			return
		}

		event := CameraEvent{
			CameraID:  msg.Camera,
			EventType: msg.Type,
			Data:      []byte(msg.Data),
			Timestamp: msg.Time,
		}

		// Process the event
		if handlers, ok := cm.eventHandlers[msg.Type]; ok {
			for _, handler := range handlers {
				if err := handler(event); err != nil {
					cm.logger.Error("event handler failed",
						zap.String("type", msg.Type),
						zap.Error(err))
				}
			}
		}
	}
}

func (cm *CameraManager) handleStreamRequest(w http.ResponseWriter, cameraID string) {
	// Set up streaming response headers
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Pragma", "no-cache")

	// Create a channel for this stream
	streamChan := make(chan []byte, 100)
	defer close(streamChan)

	// Register stream handler
	cm.RegisterEventHandler("frame", func(event CameraEvent) error {
		if event.CameraID == cameraID {
			select {
			case streamChan <- event.Data:
			default:
				// Drop frame if channel is full
			}
		}
		return nil
	})

	// Stream frames
	for frame := range streamChan {
		if _, err := w.Write([]byte("--frame\r\n")); err != nil {
			return
		}
		if _, err := w.Write([]byte("Content-Type: image/jpeg\r\n\r\n")); err != nil {
			return
		}
		if _, err := w.Write(frame); err != nil {
			return
		}
		if _, err := w.Write([]byte("\r\n")); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func (cm *CameraManager) RegisterEventHandler(eventType string, handler EventHandler) {
	cm.eventHandlers[eventType] = append(cm.eventHandlers[eventType], handler)
}

func (cm *CameraManager) Stop() {
	// Stop streaming for all cameras
	cm.cameras.Range(func(key, value interface{}) bool {
		cameraID := key.(string)
		cm.cameras.Delete(cameraID)
		return true
	})

}
