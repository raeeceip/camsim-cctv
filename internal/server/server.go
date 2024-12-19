package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/raeeceip/cctv/internal/config"
	"github.com/raeeceip/cctv/internal/processor"
)
// i swear this was working an hour ago 
type CameraMessage struct {
	Type     string    `json:"type"`
	Data     string    `json:"data"`
	Camera   string    `json:"camera"`
	Time     time.Time `json:"time"`
	FrameNum uint64    `json:"frame_num"`
	Pattern  string    `json:"pattern"`
}

type Server struct {
	router      *gin.Engine
	logger      *zap.Logger
	config      *config.Config
	upgrader    websocket.Upgrader
	processor   *processor.FrameProcessor
	connections sync.Map
}

func New(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	// Ensure output directory exists
	if err := os.MkdirAll(cfg.Storage.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Initialize processor
	proc, err := processor.NewFrameProcessor(processor.ProcessorConfig{
		OutputDir:       cfg.Storage.OutputDir,
		MaxFrames:       cfg.Storage.MaxFrames,
		RetentionTime:   time.Duration(cfg.Storage.RetentionHours) * time.Hour,
		BufferSize:      100,
		VideoInterval:   10 * time.Second,
		DeleteOriginals: false,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create processor: %w", err)
	}

	// Start the processor
	if err := proc.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to start processor: %w", err)
	}

	server := &Server{
		router:    gin.Default(),
		logger:    logger,
		config:    cfg,
		processor: proc,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	server.setupRoutes()
	return server, nil
}

func (s *Server) handleCameraConnection(cameraID string, conn *websocket.Conn) {
	s.logger.Info("starting camera connection handler",
		zap.String("camera_id", cameraID))

	defer func() {
		conn.Close()
		s.connections.Delete(cameraID)
		s.logger.Info("camera disconnected", zap.String("id", cameraID))
	}()

	// Create camera-specific directory
	cameraDir := filepath.Join(s.config.Storage.OutputDir, cameraID)
	if err := os.MkdirAll(cameraDir, 0755); err != nil {
		s.logger.Error("failed to create camera directory",
			zap.String("camera", cameraID),
			zap.Error(err))
		return
	}

	s.logger.Info("created camera directory",
		zap.String("camera", cameraID),
		zap.String("path", cameraDir))

	for {
		var msg CameraMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				s.logger.Error("websocket read error",
					zap.String("camera", cameraID),
					zap.Error(err))
			}
			return
		}

		s.logger.Debug("received frame message",
			zap.String("camera", cameraID),
			zap.Uint64("frame", msg.FrameNum),
			zap.Int("data_length", len(msg.Data)))

		// Decode base64 frame data
		frameData, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			s.logger.Error("failed to decode frame data",
				zap.String("camera", cameraID),
				zap.Error(err))
			continue
		}

		s.logger.Debug("decoded frame data",
			zap.String("camera", cameraID),
			zap.Uint64("frame", msg.FrameNum),
			zap.Int("decoded_size", len(frameData)))

		// Process frame
		s.processor.ProcessFrame(processor.FrameData{
			CameraID:  cameraID,
			Data:      frameData,
			Timestamp: msg.Time,
			Number:    msg.FrameNum,
		})

		// Log every 30th frame
		if msg.FrameNum%30 == 0 {
			s.logger.Info("processed frame",
				zap.String("camera", cameraID),
				zap.Uint64("frame", msg.FrameNum),
				zap.String("pattern", msg.Pattern))
		}
	}
}

func (s *Server) setupRoutes() {
	// WebSocket endpoint for camera connections
	s.router.GET("/camera/connect", func(c *gin.Context) {
		conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			s.logger.Error("websocket upgrade failed", zap.Error(err))
			return
		}

		cameraID := fmt.Sprintf("cam-%d", time.Now().Unix())
		s.connections.Store(cameraID, conn)
		s.logger.Info("camera connected", zap.String("id", cameraID))

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
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port),
		Handler: s.router,
	}

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down server...")

		// Close all connections
		s.connections.Range(func(key, value interface{}) bool {
			if conn, ok := value.(*websocket.Conn); ok {
				conn.Close()
			}
			return true
		})

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("server shutdown failed", zap.Error(err))
		}
	}()

	s.logger.Info("server starting", zap.String("addr", srv.Addr))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}
