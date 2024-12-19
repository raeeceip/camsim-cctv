// File: internal/server/server.go
package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/raeeceip/cctv/internal/config"
	"github.com/raeeceip/cctv/internal/processor"
	"github.com/raeeceip/cctv/internal/stream"
)

type CameraMessage struct {
	Type     string    `json:"type"`
	Data     string    `json:"data"`
	Camera   string    `json:"camera"`
	Time     time.Time `json:"time"`
	FrameNum uint64    `json:"frame_num,omitempty"`
	Pattern  string    `json:"pattern,omitempty"`
}

type Server struct {
	router      *gin.Engine
	stream      *stream.StreamManager
	logger      *zap.Logger
	config      *config.Config
	upgrader    websocket.Upgrader
	connections map[string]*websocket.Conn
	processor   *processor.FrameProcessor
}

func New(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.Default()

	// Initialize frame processor
	proc, err := processor.NewFrameProcessor(processor.Processor{
		OutputDir:     cfg.Storage.OutputDir,
		MaxFrames:     cfg.Storage.MaxFrames,
		RetentionTime: time.Duration(cfg.Storage.RetentionHours) * time.Hour,
		BufferSize:    100,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create frame processor: %w", err)
	}

	// Start the processor
	if err := proc.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to start frame processor: %w", err)
	}

	streamMgr, err := stream.NewStreamManager(cfg.Stream, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream manager: %w", err)
	}

	server := &Server{
		router:    router,
		stream:    streamMgr,
		processor: proc,
		logger:    logger,
		config:    cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		connections: make(map[string]*websocket.Conn),
	}

	server.setupRoutes()
	return server, nil
}

func (s *Server) handleCameraConnection(cameraID string, conn *websocket.Conn) {
	defer func() {
		conn.Close()
		delete(s.connections, cameraID)
		s.logger.Info("camera disconnected",
			zap.String("id", cameraID),
			zap.Int("active_connections", len(s.connections)))
	}()

	var frameCount uint64

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

		frameCount++

		// Decode base64 frame data
		frameData, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			s.logger.Error("failed to decode frame data",
				zap.String("camera", cameraID),
				zap.Error(err))
			continue
		}

		// Process frame
		s.processor.ProcessFrame(processor.FrameData{
			CameraID:  cameraID,
			Data:      frameData,
			Timestamp: msg.Time,
			Number:    frameCount,
		})

		// Log frame reception (every 30 frames)
		if frameCount%30 == 0 {
			s.logger.Info("processed frame",
				zap.String("camera", cameraID),
				zap.Uint64("frame", frameCount),
				zap.String("pattern", msg.Pattern),
				zap.Time("timestamp", msg.Time))
		}
	}
}

func (s *Server) setupRoutes() {
	// WebSocket endpoint for camera connections
	s.router.GET("/camera/connect", s.handleCameraWebSocket)

	// Stream control endpoints
	s.router.POST("/stream/start", s.handleStartStream)
	s.router.POST("/stream/stop", s.handleStopStream)
	s.router.GET("/stream/status", s.handleStreamStatus)

	// Metrics endpoint
	s.router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Health check endpoint
	s.router.GET("/health", s.handleHealthCheck)
}

func (s *Server) handleCameraWebSocket(c *gin.Context) {
	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", zap.Error(err))
		return
	}

	cameraID := c.Query("id")
	if cameraID == "" {
		cameraID = fmt.Sprintf("cam-%d", time.Now().Unix())
	}

	s.connections[cameraID] = conn
	s.logger.Info("camera connected", zap.String("id", cameraID))

	// Handle camera connection
	go s.handleCameraConnection(cameraID, conn)
}

func (s *Server) handleStartStream(c *gin.Context) {
	if err := s.stream.Start(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "streaming started"})
}

func (s *Server) handleStopStream(c *gin.Context) {
	if err := s.stream.Stop(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "streaming stopped"})
}

func (s *Server) handleStreamStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":      "active",
		"connections": len(s.connections),
	})
}

func (s *Server) handleHealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"time":   time.Now(),
	})
}

func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port),
		Handler: s.router,
	}

	go func() {
		<-ctx.Done()
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
