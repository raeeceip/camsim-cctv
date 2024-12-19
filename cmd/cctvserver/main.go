package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/raeeceip/cctv/internal/config"
	"github.com/raeeceip/cctv/internal/processor"
	"github.com/raeeceip/cctv/internal/server"
	"github.com/raeeceip/cctv/pkg/logger"
)

func main() {
	// Initialize configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Set Gin mode based on environment
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize logger
	l, err := logger.New(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	// Initialize frame processor with enhanced configuration
	proc, err := processor.NewFrameProcessor(processor.ProcessorConfig{
		OutputDir:       cfg.Storage.OutputDir,
		MaxFrames:       cfg.Storage.MaxFrames,
		RetentionTime:   time.Duration(cfg.Storage.RetentionHours) * time.Hour,
		BufferSize:      100,
		VideoInterval:   10 * time.Second, // Consolidate every 10 seconds
		DeleteOriginals: true,             // Delete frames after video creation
	}, l)
	if err != nil {
		log.Fatalf("Failed to initialize frame processor: %v", err)
	}

	// Start the processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := proc.Start(ctx); err != nil {
		l.Fatal("Failed to start frame processor", logger.Error(err))
	}

	// Initialize and start server with the processor
	srv, err := server.New(cfg, l)
	if err != nil {
		l.Fatal("Failed to initialize server", logger.Error(err))
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		l.Info("Shutting down...")
		cancel()
	}()

	if err := srv.Start(ctx); err != nil {
		l.Fatal("Failed to start server", logger.Error(err))
	}
}
