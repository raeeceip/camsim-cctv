package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

	// Initialize enhanced logger with UI disabled initially
	logConfig := logger.Config{
		OutputPath: "logs/cctv.log",
		EnableUI:   false, // Start with UI disabled
		UseConsole: true,  // Enable console output
	}

	log, err := logger.NewLogger(cfg.LogLevel, logConfig)
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}

	// Setup signal handling
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cleanup handling
	defer func() {
		// Allow time for cleanup
		time.Sleep(200 * time.Millisecond)

		// Attempt to close logger gracefully
		if err := log.Close(); err != nil {
			if !os.IsNotExist(err) && !strings.Contains(err.Error(), "file already closed") {
				fmt.Printf("Error closing logger: %v\n", err)
			}
		}
	}()

	// Handle shutdown signal in a separate goroutine
	go func() {
		sig := <-signalChan
		log.Info("Received shutdown signal", zap.String("signal", sig.String()))
		cancel()
	}()

	// Initialize and start server
	srv, err := server.New(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize server", zap.Error(err))
	}

	// Start server
	if err := srv.Start(ctx); err != nil && err != context.Canceled {
		log.Error("Server error", zap.Error(err))
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		srv.Stop()
		close(done)
	}()

	// Wait for shutdown to complete or timeout
	select {
	case <-shutdownCtx.Done():
		log.Warn("Shutdown timed out")
	case <-done:
		log.Info("Server shutdown completed successfully")
	}

	// Final cleanup
	time.Sleep(100 * time.Millisecond) // Brief pause for final logs
	log.Info("Server shutdown complete")
}
