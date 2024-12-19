// File: internal/processor/frameprocessor.go
package processor

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	config     ProcessorConfig
	logger     *zap.Logger
	frameChan  chan FrameData
	done       chan struct{}
	wg         sync.WaitGroup
	frameCount map[string]uint64
	lastVideo  map[string]time.Time
	mu         sync.RWMutex
}

func NewFrameProcessor(config ProcessorConfig, logger *zap.Logger) (*FrameProcessor, error) {
	if config.VideoInterval == 0 {
		config.VideoInterval = 10 * time.Second
	}

	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	return &FrameProcessor{
		config:     config,
		logger:     logger,
		frameChan:  make(chan FrameData, config.BufferSize),
		done:       make(chan struct{}),
		frameCount: make(map[string]uint64),
		lastVideo:  make(map[string]time.Time),
	}, nil
}

// findAllCameraDirectories returns all camera directories including those with timestamps
func (fp *FrameProcessor) findAllCameraDirectories() ([]string, error) {
	var dirs []string
	err := filepath.Walk(fp.config.OutputDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && path != fp.config.OutputDir {
			// Check if directory matches pattern cam-\d+ or cam\d+
			if matched, _ := regexp.MatchString(`cam-?\d+`, filepath.Base(path)); matched {
				dirs = append(dirs, path)
			}
		}
		return nil
	})
	return dirs, err
}

func (fp *FrameProcessor) saveFrame(frame FrameData) error {
	// Create camera-specific directory with timestamp
	cameraDir := filepath.Join(fp.config.OutputDir, frame.CameraID)
	if err := os.MkdirAll(cameraDir, 0755); err != nil {
		return fmt.Errorf("failed to create camera directory: %w", err)
	}

	// Process the frame data
	var imageData []byte
	if isBase64(frame.Data) {
		// Decode base64 data
		decoded, err := base64.StdEncoding.DecodeString(string(frame.Data))
		if err != nil {
			return fmt.Errorf("failed to decode base64 frame: %w", err)
		}
		imageData = decoded
	} else {
		// Assume raw image data
		imageData = frame.Data
	}

	// Ensure valid JPEG data
	img, err := decodeImage(imageData)
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// Re-encode as JPEG with known good parameters
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return fmt.Errorf("failed to encode JPEG: %w", err)
	}

	// Generate filename with timestamp and frame number
	filename := filepath.Join(cameraDir, fmt.Sprintf(
		"frame_%d_%s.jpg",
		frame.Number,
		frame.Timestamp.Format("20060102_150405.000")))

	// Save the processed image
	if err := os.WriteFile(filename, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write frame file: %w", err)
	}

	fp.mu.Lock()
	fp.frameCount[frame.CameraID]++
	count := fp.frameCount[frame.CameraID]
	fp.mu.Unlock()

	if count%30 == 0 {
		fp.logger.Info("frame saved",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number),
			zap.String("file", filename))
	}

	return nil
}

func (fp *FrameProcessor) consolidateAndCleanup() error {
	// Find all camera directories
	cameraDirs, err := fp.findAllCameraDirectories()
	if err != nil {
		return fmt.Errorf("failed to find camera directories: %w", err)
	}

	for _, cameraDir := range cameraDirs {
		cameraID := filepath.Base(cameraDir)
		fp.logger.Debug("processing camera directory",
			zap.String("camera_id", cameraID),
			zap.String("directory", cameraDir))

		if err := fp.createVideo(cameraID, cameraDir); err != nil {
			fp.logger.Error("failed to create video",
				zap.String("camera", cameraID),
				zap.Error(err))
		}
	}

	return nil
}

func (fp *FrameProcessor) createVideo(cameraID, cameraDir string) error {
	// Find all frame files
	framePattern := filepath.Join(cameraDir, "frame_*.jpg")
	frames, err := filepath.Glob(framePattern)
	if err != nil {
		return fmt.Errorf("failed to glob frames: %w", err)
	}

	if len(frames) == 0 {
		fp.logger.Debug("no frames found",
			zap.String("camera", cameraID),
			zap.String("pattern", framePattern))
		return nil
	}

	fp.logger.Info("creating video",
		zap.String("camera", cameraID),
		zap.Int("frame_count", len(frames)))

	// Create videos directory
	videosDir := filepath.Join(fp.config.OutputDir, "videos")
	if err := os.MkdirAll(videosDir, 0755); err != nil {
		return fmt.Errorf("failed to create videos directory: %w", err)
	}

	// Generate video filename
	videoPath := filepath.Join(videosDir,
		fmt.Sprintf("%s_%s.mp4", cameraID, time.Now().Format("20060102_150405")))

	// Ensure paths use forward slashes for ffmpeg
	framePattern = filepath.ToSlash(framePattern)
	videoPath = filepath.ToSlash(videoPath)

	// Create video using ffmpeg
	cmd := exec.Command("ffmpeg",
		"-framerate", "30",
		"-pattern_type", "glob",
		"-i", framePattern,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-y",
		videoPath)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg error: %v\nOutput: %s", err, stderr.String())
	}

	fp.logger.Info("video created",
		zap.String("camera", cameraID),
		zap.String("path", videoPath),
		zap.Int("frames", len(frames)))

	// Delete original frames if configured
	if fp.config.DeleteOriginals {
		for _, frame := range frames {
			if err := os.Remove(frame); err != nil {
				fp.logger.Error("failed to delete frame",
					zap.String("frame", frame),
					zap.Error(err))
			}
		}
		// Try to remove the empty directory
		os.Remove(cameraDir)
	}

	return nil
}

func (fp *FrameProcessor) GetFrameDirectories() (map[string]interface{}, error) {
	info := make(map[string]interface{})

	// Find all camera directories
	cameraDirs, err := fp.findAllCameraDirectories()
	if err != nil {
		return nil, fmt.Errorf("failed to find camera directories: %w", err)
	}

	for _, dir := range cameraDirs {
		frames, err := filepath.Glob(filepath.Join(dir, "frame_*.jpg"))
		if err != nil {
			return nil, err
		}

		info[filepath.Base(dir)] = map[string]interface{}{
			"frame_count": len(frames),
			"path":        dir,
			"last_frame":  time.Now(),
		}
	}

	// Add processing statistics
	fp.mu.RLock()
	info["stats"] = map[string]interface{}{
		"total_frames_processed": fp.frameCount,
		"active_cameras":         len(fp.frameCount),
	}
	fp.mu.RUnlock()

	return info, nil
}

// Debug log the frame processing
func (fp *FrameProcessor) ProcessFrame(frame FrameData) {
	fp.logger.Debug("received frame for processing",
		zap.String("camera", frame.CameraID),
		zap.Uint64("frame", frame.Number),
		zap.Int("data_size", len(frame.Data)))

	select {
	case fp.frameChan <- frame:
		fp.logger.Debug("frame queued for processing",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number))
	default:
		fp.logger.Warn("frame processing queue full, dropping frame",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number))
	}
}

// Helper functions

func decodeImage(data []byte) (image.Image, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		// If JPEG decode fails, try other formats or handle raw data
		// Add additional format decoders here if needed
		return nil, fmt.Errorf("failed to decode image data: %w", err)
	}
	return img, nil
}

func isBase64(data []byte) bool {
	_, err := base64.StdEncoding.DecodeString(string(data))
	return err == nil && len(data) > 0 && len(data)%4 == 0
}

// Start starts the frame processor
func (fp *FrameProcessor) Start(ctx context.Context) error {
	fp.wg.Add(2)

	go fp.processFrames(ctx)
	go fp.maintenanceRoutine(ctx)

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

func (fp *FrameProcessor) maintenanceRoutine(ctx context.Context) {
	defer fp.wg.Done()

	ticker := time.NewTicker(fp.config.VideoInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := fp.consolidateAndCleanup(); err != nil {
				fp.logger.Error("maintenance failed", zap.Error(err))
			}
		}
	}
}

func (fp *FrameProcessor) Stop() error {
	close(fp.done)
	fp.wg.Wait()
	return nil
}
