package processor

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
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

type ProcessorConfig struct {
	OutputDir       string
	MaxFrames       int
	RetentionTime   time.Duration
	BufferSize      int
	VideoInterval   time.Duration
	DeleteOriginals bool
}

type FrameProcessor struct {
	config          ProcessorConfig
	logger          *zap.Logger
	frameChan       chan FrameData
	consolidateChan chan struct{}
	processingMap   sync.Map // map[string]bool to track active cameras
	frameCount      map[string]uint64
	mu              sync.RWMutex
}

func NewFrameProcessor(config ProcessorConfig, logger *zap.Logger) (*FrameProcessor, error) {
	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	return &FrameProcessor{
		config:          config,
		logger:          logger,
		frameChan:       make(chan FrameData, config.BufferSize),
		consolidateChan: make(chan struct{}, 1),
		frameCount:      make(map[string]uint64),
	}, nil
}

func (fp *FrameProcessor) Start(ctx context.Context) error {
	// Start frame processing goroutine
	go fp.processFrames(ctx)

	// Start consolidation goroutine
	go fp.consolidationRoutine(ctx)

	return nil
}

func (fp *FrameProcessor) ProcessFrame(frame FrameData) {
	// Mark camera as active
	fp.processingMap.Store(frame.CameraID, true)

	select {
	case fp.frameChan <- frame:
		fp.logger.Debug("queued frame for processing",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number))
	default:
		fp.logger.Warn("frame processing queue full, dropping frame",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number))
	}
}

func (fp *FrameProcessor) processFrames(ctx context.Context) {
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
			} else {
				fp.mu.Lock()
				fp.frameCount[frame.CameraID]++
				count := fp.frameCount[frame.CameraID]
				fp.mu.Unlock()

				// Signal consolidation if we have enough frames
				if count%30 == 0 {
					select {
					case fp.consolidateChan <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}

func (fp *FrameProcessor) saveFrame(frame FrameData) error {
	// Create camera directory
	cameraDir := filepath.Join(fp.config.OutputDir, frame.CameraID)
	if err := os.MkdirAll(cameraDir, 0755); err != nil {
		return fmt.Errorf("failed to create camera directory: %w", err)
	}

	// Process frame data
	var frameData []byte
	if isBase64(frame.Data) {
		decoded, err := base64.StdEncoding.DecodeString(string(frame.Data))
		if err != nil {
			return fmt.Errorf("failed to decode base64: %w", err)
		}
		frameData = decoded
	} else {
		frameData = frame.Data
	}

	// Create a new image from the frame data
	img, err := jpeg.Decode(bytes.NewReader(frameData))
	if err != nil {
		return fmt.Errorf("failed to decode JPEG: %w", err)
	}

	// Save frame
	filename := filepath.Join(cameraDir,
		fmt.Sprintf("frame_%05d_%s.jpg",
			frame.Number,
			frame.Timestamp.Format("20060102_150405.000")))

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		return fmt.Errorf("failed to encode JPEG: %w", err)
	}

	fp.logger.Debug("saved frame",
		zap.String("camera", frame.CameraID),
		zap.Uint64("frame", frame.Number),
		zap.String("file", filename))

	return nil
}

func (fp *FrameProcessor) consolidationRoutine(ctx context.Context) {
	ticker := time.NewTicker(fp.config.VideoInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-fp.consolidateChan:
			// Immediate consolidation when signaled
			fp.consolidateFrames()
		case <-ticker.C:
			// Regular interval consolidation
			fp.consolidateFrames()
		}
	}
}

func (fp *FrameProcessor) consolidateFrames() {
	fp.processingMap.Range(func(key, value interface{}) bool {
		cameraID := key.(string)
		cameraDir := filepath.Join(fp.config.OutputDir, cameraID)

		frames, err := filepath.Glob(filepath.Join(cameraDir, "frame_*.jpg"))
		if err != nil {
			fp.logger.Error("failed to glob frames",
				zap.String("camera", cameraID),
				zap.Error(err))
			return true
		}

		if len(frames) == 0 {
			return true
		}

		fp.logger.Info("consolidating frames",
			zap.String("camera", cameraID),
			zap.Int("frame_count", len(frames)))

		// Create video directory
		videoDir := filepath.Join(fp.config.OutputDir, "videos")
		if err := os.MkdirAll(videoDir, 0755); err != nil {
			fp.logger.Error("failed to create video directory", zap.Error(err))
			return true
		}

		// Create video
		videoPath := filepath.Join(videoDir,
			fmt.Sprintf("%s_%s.mp4",
				cameraID,
				time.Now().Format("20060102_150405")))

		if err := fp.createVideo(frames, videoPath); err != nil {
			fp.logger.Error("failed to create video",
				zap.String("camera", cameraID),
				zap.Error(err))
			return true
		}

		// Cleanup if configured
		if fp.config.DeleteOriginals {
			for _, frame := range frames {
				os.Remove(frame)
			}
		}

		return true
	})
}

func (fp *FrameProcessor) createVideo(frames []string, outputPath string) error {
	if len(frames) == 0 {
		return fmt.Errorf("no frames to process")
	}

	// Create a temporary file with the list of frames
	listFile := filepath.Join(filepath.Dir(frames[0]), "frames.txt")
	var frameList string
	for _, frame := range frames {
		frameList += fmt.Sprintf("file '%s'\nduration 0.033333333\n", filepath.ToSlash(frame))
	}
	if err := os.WriteFile(listFile, []byte(frameList), 0644); err != nil {
		return fmt.Errorf("failed to create frame list: %w", err)
	}
	defer os.Remove(listFile)

	args := []string{
		"-f", "concat",
		"-safe", "0",
		"-i", listFile,
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-y",
		filepath.ToSlash(outputPath),
	}

	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg error: %v\nOutput: %s", err, stderr.String())
	}

	return nil
}

func isBase64(data []byte) bool {
	_, err := base64.StdEncoding.DecodeString(string(data))
	return err == nil && len(data) > 0 && len(data)%4 == 0
}

func (fp *FrameProcessor) Stop() {
	close(fp.frameChan)
	close(fp.consolidateChan)
}
