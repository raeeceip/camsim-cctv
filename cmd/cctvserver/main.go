package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/raeeceip/cctv/internal/config"
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

	// Initialize and start server
	srv, err := server.New(cfg, l)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Start(ctx); err != nil {
		l.Fatal("Failed to start server", logger.Error(err))
	}
}
