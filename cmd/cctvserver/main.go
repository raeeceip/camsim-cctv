package main

import (
	"context"
	"fmt"
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
		OutputPath: "logs/cctv.log",
		EnableUI:   true,
	}

	log, err := logger.NewLogger(cfg.LogLevel, logConfig)
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}

	// Setup signal handling early
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Create a context that will be cancelled on signal
	ctx, cancel := context.WithCancel(context.Background())

	// Handle shutdown signal
	go func() {
		sig := <-signalChan
		log.Info("Received shutdown signal", zap.String("signal", sig.String()))
		cancel()
	}()

	// Ensure proper cleanup
	defer func() {
		cancel() // Ensure context is cancelled
		if err := log.Close(); err != nil {
			fmt.Printf("Error closing logger: %v\n", err)
		}
	}()

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

	// Initialize and start server
	srv, err := server.New(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize server", zap.Error(err))
	}

	// Start the server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Start(ctx); err != nil {
			serverErr <- err
		}
	}()

	// Wait for either server error or context cancellation
	select {
	case err := <-serverErr:
		log.Error("Server error", zap.Error(err))
	case <-ctx.Done():
		log.Info("Context cancelled, shutting down...")
	}

	// Perform graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// Stop the server
	srv.Stop()

	// Wait for shutdown to complete or timeout
	select {
	case <-shutdownCtx.Done():
		log.Warn("Shutdown timed out")
	case <-time.After(100 * time.Millisecond):
		// Brief pause to allow final logs to be written
	}

	log.Info("Server shutdown complete")
}
