package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/raeeceip/cctv/internal/config"
	"github.com/raeeceip/cctv/internal/server"
	"github.com/raeeceip/cctv/pkg/logger"
	"go.uber.org/zap"
)

const (
	Version = "1.0.0"
)

func main() {
	// Initialize configuration
	cfg, err := config.Load()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}

	// Initialize enhanced logger
	logConfig := logger.Config{
		Level:         cfg.LogLevel,
		OutputPath:    "logs/cctv.log",
		MaxSize:       100,
		MaxBackups:    3,
		MaxAge:        7,
		Compress:      true,
		UseConsole:    true,
		UseJSON:       cfg.LogLevel == "debug",
		EnableUI:      true,
		UIRefreshRate: 100,
	}

	log, err := logger.NewLogger(logConfig.Level, logConfig.OutputPath)
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer log.Close()

	// Start logger UI if enabled
	if logConfig.EnableUI {
		if err := log.StartUI(); err != nil {
			log.Error("Failed to start logger UI", zap.Error(err))
			os.Exit(1)
		}
	}

	// Set Gin mode based on environment
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Ensure required directories exist
	requiredDirs := []string{
		cfg.Storage.OutputDir,
		"logs",
		"frames",
		"frames/videos",
	}

	for _, dir := range requiredDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatal("Failed to create required directory",
				zap.String("directory", dir),
				zap.Error(err))
		}
	}

	log.Info("Starting CCTV System",
		zap.String("version", Version),
		zap.String("log_level", cfg.LogLevel),
		zap.String("environment", gin.Mode()))

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize and start server with the processor
	srv, err := server.New(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize server", zap.Error(err))
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Info("Received shutdown signal",
			zap.String("signal", sig.String()))
		cancel()
	}()

	log.Info("System initialized successfully",
		zap.String("server_address", cfg.Server.Host),
		zap.Int("server_port", cfg.Server.Port))

	// Start the server
	if err := srv.Start(ctx); err != nil {
		log.Fatal("Failed to start server", zap.Error(err))
	}

	// Wait for graceful shutdown
	<-ctx.Done()

	// Allow some time for cleanup
	time.Sleep(2 * time.Second)

	// Stop the server
	srv.Stop()

	log.Info("System shutdown complete")
}
