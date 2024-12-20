// File: internal/server/server.go
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/raeeceip/cctv/internal/config"
	"github.com/raeeceip/cctv/internal/processor"
	"github.com/raeeceip/cctv/pkg/logger"
	"go.uber.org/zap"
)

type CameraMessage struct {
	Type     string    `json:"type"`
	Data     string    `json:"data"`
	Camera   string    `json:"camera"`
	Time     time.Time `json:"time"`
	FrameNum uint64    `json:"frame_num"`
	Pattern  string    `json:"pattern"`
}

type Server struct {
	router          *gin.Engine
	logger          *logger.Logger
	config          *config.Config
	processor       *processor.FrameProcessor
	upgrader        websocket.Upgrader
	connections     sync.Map
	shutdown        chan struct{}
	activeProcesses sync.WaitGroup
	shutdownOnce    sync.Once
}

func New(cfg *config.Config, log *logger.Logger) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if log == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(cfg.Storage.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Initialize processor with configuration
	proc, err := processor.NewFrameProcessor(processor.ProcessorConfig{
		OutputDir:       cfg.Storage.OutputDir,
		MaxFrames:       cfg.Storage.MaxFrames,
		RetentionTime:   time.Duration(cfg.Storage.RetentionHours) * time.Hour,
		BufferSize:      100,
		VideoInterval:   10 * time.Second,
		DeleteOriginals: false,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create processor: %w", err)
	}

	// Initialize server
	server := &Server{
		router:    gin.Default(),
		logger:    log,
		config:    cfg,
		processor: proc,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
			ReadBufferSize:  1024 * 1024, // 1MB
			WriteBufferSize: 1024 * 1024, // 1MB
		},
		shutdown: make(chan struct{}),
	}

	// Setup routes
	server.setupRoutes()
	return server, nil
}

func (s *Server) handleCameraConnection(cameraID string, conn *websocket.Conn) {
	s.activeProcesses.Add(1)
	defer s.activeProcesses.Done()

	s.logger.Info("Starting camera connection handler",
		zap.String("camera_id", cameraID))

	defer func() {
		// Ensure clean connection closure
		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		conn.Close()
		s.connections.Delete(cameraID)
		s.logger.Info("Camera disconnected", zap.String("id", cameraID))
	}()

	// Set up connection parameters
	conn.SetReadLimit(32 * 1024 * 1024)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Start ping handler
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		s.handlePing(conn, cameraID)
	}()

	// Create camera-specific directory
	cameraDir := filepath.Join(s.config.Storage.OutputDir, cameraID)
	if err := os.MkdirAll(cameraDir, 0755); err != nil {
		s.logger.Error("Failed to create camera directory",
			zap.String("camera", cameraID),
			zap.Error(err))
		return
	}

	// Message handling loop
	for {
		var msg CameraMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				s.logger.Error("Websocket read error",
					zap.String("camera", cameraID),
					zap.Error(err))
			}
			return
		}

		s.logger.Debug("Received frame message",
			zap.String("camera", cameraID),
			zap.Uint64("frame", msg.FrameNum),
			zap.Int("data_length", len(msg.Data)))

		// Process frame
		if s.processor != nil {
			s.processor.ProcessFrame(processor.FrameData{
				CameraID:  cameraID,
				Data:      []byte(msg.Data),
				Timestamp: msg.Time,
				Number:    msg.FrameNum,
			})
		}
	}
}

func (s *Server) handlePing(conn *websocket.Conn, cameraID string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
				s.logger.Error("Failed to write ping",
					zap.String("camera", cameraID),
					zap.Error(err))
				return
			}
		case <-s.shutdown:
			return
		}
	}
}

func (s *Server) setupRoutes() {
	// WebSocket endpoint for camera connections
	s.router.GET("/camera/connect", func(c *gin.Context) {
		conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			s.logger.Error("Websocket upgrade failed", zap.Error(err))
			return
		}

		cameraID := fmt.Sprintf("cam-%d", time.Now().Unix())
		s.connections.Store(cameraID, conn)
		s.logger.Info("Camera connected", zap.String("id", cameraID))

		// Handle camera connection in a goroutine
		go s.handleCameraConnection(cameraID, conn)
	})

	// Debug endpoint
	s.router.GET("/debug/frames", func(c *gin.Context) {
		// Get frame directories info
		info := make(map[string]interface{})

		// List all files in the output directory
		outputDir := s.config.Storage.OutputDir
		files, err := filepath.Glob(filepath.Join(outputDir, "cam-*", "frame_*.jpg"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Count frames per camera
		frameCounts := make(map[string]int)
		for _, file := range files {
			cameraID := filepath.Base(filepath.Dir(file))
			frameCounts[cameraID]++
		}

		info["frame_counts"] = frameCounts
		info["total_frames"] = len(files)

		var activeConns []string
		s.connections.Range(func(key, value interface{}) bool {
			activeConns = append(activeConns, key.(string))
			return true
		})
		info["active_connections"] = activeConns

		c.JSON(http.StatusOK, gin.H{
			"frame_dirs": info,
			"time":       time.Now(),
		})
	})

	// Health check endpoint
	s.router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
			"time":   time.Now(),
		})
	})

	// Metrics endpoint
	s.router.GET("/metrics", gin.WrapH(promhttp.Handler()))
}
func (s *Server) Start(ctx context.Context) error {
	// Start the processor if it hasn't been started
	if err := s.processor.Start(ctx); err != nil {
		return fmt.Errorf("failed to start processor: %w", err)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port),
		Handler: s.router,
	}

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		s.shutdownOnce.Do(func() {
			s.logger.Info("Shutting down server...")

			// Signal ping handlers to stop
			close(s.shutdown)

			// Close all connections gracefully
			s.connections.Range(func(key, value interface{}) bool {
				if conn, ok := value.(*websocket.Conn); ok {
					conn.WriteMessage(
						websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server shutdown"))
					conn.Close()
				}
				return true
			})

			// Wait for all active processes to complete
			processDone := make(chan struct{})
			go func() {
				s.activeProcesses.Wait()
				close(processDone)
			}()

			// Wait for processes with timeout
			select {
			case <-processDone:
				s.logger.Info("All processes completed gracefully")
			case <-time.After(5 * time.Second):
				s.logger.Warn("Timeout waiting for processes to complete")
			}

			// Shutdown server
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := srv.Shutdown(shutdownCtx); err != nil {
				s.logger.Error("Server shutdown error", zap.Error(err))
			}
		})
	}()

	s.logger.Info("Server starting",
		zap.String("address", srv.Addr))

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func (s *Server) Stop() {
	s.shutdownOnce.Do(func() {
		close(s.shutdown)
		s.processor.Stop()

		// Wait for active processes with timeout
		done := make(chan struct{})
		go func() {
			s.activeProcesses.Wait()
			close(done)
		}()

		select {
		case <-done:
			s.logger.Info("All processes completed gracefully")
		case <-time.After(5 * time.Second):
			s.logger.Warn("Timeout waiting for processes to complete")
		}
	})
}
