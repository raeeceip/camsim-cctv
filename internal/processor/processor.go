// File: internal/processor/frameprocessor.go
package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

type FrameData struct {
	CameraID  string
	Data      []byte
	Timestamp time.Time
	Number    uint64
}

type Processor struct {
	OutputDir     string
	MaxFrames     int           // Maximum frames to store per camera
	RetentionTime time.Duration // How long to keep frames
	BufferSize    int           // Channel buffer size
}

type FrameProcessor struct {
	config     Processor
	logger     *zap.Logger
	frameChan  chan FrameData
	done       chan struct{}
	wg         sync.WaitGroup
	frameCount map[string]uint64
	mu         sync.RWMutex
}

func NewFrameProcessor(config Processor, logger *zap.Logger) (*FrameProcessor, error) {
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	return &FrameProcessor{
		config:     config,
		logger:     logger,
		frameChan:  make(chan FrameData, config.BufferSize),
		done:       make(chan struct{}),
		frameCount: make(map[string]uint64),
	}, nil
}

func (fp *FrameProcessor) Start(ctx context.Context) error {
	fp.wg.Add(2)

	// Start frame processing
	go fp.processFrames(ctx)

	// Start cleanup routine
	go fp.cleanupRoutine(ctx)

	return nil
}

func (fp *FrameProcessor) processFrames(ctx context.Context) {
	defer fp.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-fp.frameChan:
			if err := fp.saveFrame(frame); err != nil {
				fp.logger.Error("failed to save frame",
					zap.String("camera", frame.CameraID),
					zap.Uint64("frame", frame.Number),
					zap.Error(err))
			}
		}
	}
}

func (fp *FrameProcessor) saveFrame(frame FrameData) error {
	// Create camera-specific directory
	cameraDir := filepath.Join(fp.config.OutputDir, frame.CameraID)
	if err := os.MkdirAll(cameraDir, 0755); err != nil {
		return fmt.Errorf("failed to create camera directory: %w", err)
	}

	// Generate filename with timestamp and frame number
	filename := filepath.Join(cameraDir, fmt.Sprintf(
		"frame_%d_%s.jpg",
		frame.Number,
		frame.Timestamp.Format("20060102_150405.000")))

	// Save frame data
	if err := os.WriteFile(filename, frame.Data, 0644); err != nil {
		return fmt.Errorf("failed to write frame file: %w", err)
	}

	fp.mu.Lock()
	fp.frameCount[frame.CameraID]++
	count := fp.frameCount[frame.CameraID]
	fp.mu.Unlock()

	// Log every 30th frame
	if count%30 == 0 {
		fp.logger.Info("frame saved",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number),
			zap.String("file", filename))
	}

	return nil
}

func (fp *FrameProcessor) cleanupRoutine(ctx context.Context) {
	defer fp.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := fp.cleanup(); err != nil {
				fp.logger.Error("cleanup failed", zap.Error(err))
			}
		}
	}
}

func (fp *FrameProcessor) cleanup() error {
	cutoff := time.Now().Add(-fp.config.RetentionTime)

	return filepath.Walk(fp.config.OutputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Remove old files
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				fp.logger.Error("failed to remove old frame",
					zap.String("file", path),
					zap.Error(err))
				return nil // Continue despite error
			}
			fp.logger.Debug("removed old frame", zap.String("file", path))
		}

		return nil
	})
}

func (fp *FrameProcessor) ProcessFrame(frame FrameData) {
	select {
	case fp.frameChan <- frame:
	default:
		fp.logger.Warn("frame processing queue full, dropping frame",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number))
	}
}

func (fp *FrameProcessor) Stop() error {
	close(fp.done)
	fp.wg.Wait()
	return nil
}
