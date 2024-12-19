// File: internal/stream/stream.go
package stream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/raeeceip/cctv/internal/camera"
	"github.com/raeeceip/cctv/internal/config"
	"github.com/raeeceip/cctv/internal/encoder"
	"github.com/raeeceip/cctv/pkg/metrics"
)

// Frame represents a video frame
type Frame struct {
	Data      []byte
	Timestamp time.Time
	KeyFrame  bool
}

type StreamManager struct {
	camera  *camera.CameraManager
	encoder *encoder.Encoder
	metrics *metrics.StreamMetrics
	logger  *zap.Logger

	frameChan chan Frame
	done      chan struct{}
	wg        sync.WaitGroup

	// Stream status
	isStreaming bool
	mu          sync.RWMutex
}

func NewStreamManager(cfg config.StreamConfig, logger *zap.Logger) (*StreamManager, error) {

	cam, err := camera.NewCameraManager(cfg.SignalAddress, cfg.StreamAddress, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create camera manager: %w", err)

	}
	enc, err := encoder.NewEncoder(encoder.EncoderConfig{
		Codec:            cfg.VideoCodec,
		Bitrate:          cfg.VideoBitrate,
		Framerate:        cfg.Framerate,
		KeyframeInterval: 2 * cfg.Framerate, // 2-second keyframe interval
	})
	if err != nil {
		// Clean up camera resources if encoder creation fails
		cam.Stop()
		return nil, fmt.Errorf("failed to create encoder (ensure FFmpeg is installed and in PATH): %w", err)
	}

	metrics := metrics.NewStreamMetrics()

	return &StreamManager{
		camera:    cam,
		encoder:   enc,
		metrics:   metrics,
		logger:    logger,
		frameChan: make(chan Frame, 30), // Buffer 30 frames
		done:      make(chan struct{}),
	}, nil
}

func (sm *StreamManager) Start(ctx context.Context) error {
	sm.mu.Lock()
	if sm.isStreaming {
		sm.mu.Unlock()
		return fmt.Errorf("stream already running")
	}
	sm.isStreaming = true
	sm.mu.Unlock()

	sm.wg.Add(2)

	// Start camera frame capture
	go func() {
		defer sm.wg.Done()
		sm.captureFrames(ctx)
	}()

	// Start frame processing
	go func() {
		defer sm.wg.Done()
		sm.processFrames(ctx)
	}()

	return nil
}

func (sm *StreamManager) captureFrames(ctx context.Context) {
	// Start metrics tracking
	uptime := time.NewTicker(time.Second)
	defer uptime.Stop()

	// Track stream uptime
	go func() {
		for range uptime.C {
			sm.metrics.StreamUptime.Inc()
		}
	}()

	// Handle camera events
	sm.camera.RegisterEventHandler("frame", func(event camera.CameraEvent) error {
		// Create frame from camera event
		frame := Frame{
			Data:      event.Data,
			Timestamp: event.Timestamp,
			KeyFrame:  event.EventType == "keyframe",
		}

		// Send frame to processing
		select {
		case sm.frameChan <- frame:
		default:
			// Drop frame if channel is full
			sm.logger.Warn("dropping frame due to full buffer")
		}
		return nil
	})

	// Start the camera
	if err := sm.camera.Start(ctx); err != nil {
		sm.logger.Error("camera start failed", zap.Error(err))
	}
}

func (sm *StreamManager) processFrames(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	frameCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-sm.frameChan:
			start := time.Now()

			// Encode the frame
			if err := sm.encoder.Encode(frame.Data, frame.KeyFrame); err != nil {
				sm.logger.Error("encoding failed", zap.Error(err))
				sm.metrics.EncodeErrors.Inc()
			}

			frameCount++
			sm.metrics.EncodeLatency.Observe(time.Since(start).Seconds())

		case <-ticker.C:
			sm.metrics.FramesPerSecond.Set(float64(frameCount))
			frameCount = 0
		}
	}
}

func (sm *StreamManager) Stop() error {
	sm.mu.Lock()
	if !sm.isStreaming {
		sm.mu.Unlock()
		return fmt.Errorf("stream not running")
	}
	sm.isStreaming = false
	sm.mu.Unlock()

	close(sm.done)
	sm.wg.Wait()

	// Stop encoder
	if err := sm.encoder.Close(); err != nil {
		sm.logger.Error("failed to close encoder", zap.Error(err))
	}

	// Stop camera
	sm.camera.Stop()

	return nil
}

// GetStreamStatus returns the current streaming status
func (sm *StreamManager) GetStreamStatus() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.isStreaming
}

// GetMetrics returns the current stream metrics
func (sm *StreamManager) GetMetrics() *metrics.StreamMetrics {
	return sm.metrics
}
