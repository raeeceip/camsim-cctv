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

	"github.com/raeeceip/cctv/pkg/logger"
	"go.uber.org/zap"
)

type FrameData struct {
	CameraID  string    `json:"camera_id"`
	Data      []byte    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
	Number    uint64    `json:"number"`
}

type ProcessorConfig struct {
	OutputDir       string        `json:"output_dir"`
	MaxFrames       int           `json:"max_frames"`
	RetentionTime   time.Duration `json:"retention_time"`
	BufferSize      int           `json:"buffer_size"`
	VideoInterval   time.Duration `json:"video_interval"`
	DeleteOriginals bool          `json:"delete_originals"`
}

type ProcessResult struct {
	CameraID      string        `json:"camera_id"`
	FrameNumber   uint64        `json:"frame_number"`
	FilePath      string        `json:"file_path"`
	ProcessedTime time.Time     `json:"processed_time"`
	Duration      time.Duration `json:"duration"`
	Error         error         `json:"error,omitempty"`
}

type FrameProcessor struct {
	config          ProcessorConfig
	logger          *logger.Logger
	frameChan       chan FrameData
	consolidateChan chan struct{}
	processingMap   sync.Map
	frameCount      map[string]uint64
	metrics         *ProcessorMetrics
	mu              sync.RWMutex
}

func NewFrameProcessor(config ProcessorConfig, log *logger.Logger) (*FrameProcessor, error) {
	if config.OutputDir == "" {
		return nil, fmt.Errorf("output directory is required")
	}
	if config.BufferSize <= 0 {
		config.BufferSize = 100
	}
	if config.VideoInterval <= 0 {
		config.VideoInterval = 10 * time.Second
	}

	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	log.Info("Initializing frame processor",
		zap.String("output_dir", config.OutputDir),
		zap.Int("buffer_size", config.BufferSize),
		zap.Duration("retention_time", config.RetentionTime))

	return &FrameProcessor{
		config:          config,
		logger:          log,
		frameChan:       make(chan FrameData, config.BufferSize),
		consolidateChan: make(chan struct{}, 1),
		frameCount:      make(map[string]uint64),
		metrics:         &ProcessorMetrics{},
	}, nil
}

func (fp *FrameProcessor) ProcessFrame(frame FrameData) error {
	if frame.CameraID == "" {
		return fmt.Errorf("camera ID is required")
	}

	// Mark camera as active
	fp.processingMap.Store(frame.CameraID, true)

	select {
	case fp.frameChan <- frame:
		fp.logger.Debug("Queued frame for processing",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number),
			zap.Time("timestamp", frame.Timestamp),
			zap.Int("queue_size", len(fp.frameChan)),
			zap.Int("queue_capacity", cap(fp.frameChan)))
		return nil
	default:
		fp.metrics.RecordError()
		return fmt.Errorf("frame processing queue full (capacity: %d)", cap(fp.frameChan))
	}
}

func (fp *FrameProcessor) saveFrame(frame FrameData) ProcessResult {
	startTime := time.Now()
	result := ProcessResult{
		CameraID:      frame.CameraID,
		FrameNumber:   frame.Number,
		ProcessedTime: startTime,
	}

	// Create camera directory
	cameraDir := filepath.Join(fp.config.OutputDir, frame.CameraID)
	if err := os.MkdirAll(cameraDir, 0755); err != nil {
		result.Error = fmt.Errorf("failed to create camera directory: %w", err)
		return result
	}

	// Process frame data
	var frameData []byte
	if isBase64(frame.Data) {
		decoded, err := base64.StdEncoding.DecodeString(string(frame.Data))
		if err != nil {
			result.Error = fmt.Errorf("failed to decode base64: %w", err)
			return result
		}
		frameData = decoded
	} else {
		frameData = frame.Data
	}

	// Create the filename
	filename := filepath.Join(cameraDir,
		fmt.Sprintf("frame_%05d_%s.jpg",
			frame.Number,
			frame.Timestamp.Format("20060102_150405.000")))

	// Save the frame
	file, err := os.Create(filename)
	if err != nil {
		result.Error = fmt.Errorf("failed to create file: %w", err)
		return result
	}
	defer file.Close()

	img, err := jpeg.Decode(bytes.NewReader(frameData))
	if err != nil {
		result.Error = fmt.Errorf("failed to decode JPEG: %w", err)
		return result
	}

	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		result.Error = fmt.Errorf("failed to encode JPEG: %w", err)
		return result
	}

	result.FilePath = filename
	processingTime := time.Since(startTime)
	result.Duration = processingTime

	fp.logger.Debug("Saved frame",
		zap.String("camera", frame.CameraID),
		zap.Uint64("frame", frame.Number),
		zap.String("file", filename),
		zap.Duration("processing_time", processingTime))

	return result
}

func (fp *FrameProcessor) processFrames(ctx context.Context) {
	fp.logger.Info("Starting frame processing routine")

	for {
		select {
		case <-ctx.Done():
			fp.logger.Info("Stopping frame processing routine")
			return
		case frame := <-fp.frameChan:
			processStart := time.Now()
			result := fp.saveFrame(frame)

			if result.Error != nil {
				fp.logger.Error("Failed to save frame",
					zap.String("started_at", processStart.Format(time.RFC3339)),
					zap.String("camera", frame.CameraID),
					zap.Uint64("frame", frame.Number),
					zap.Error(result.Error))
				fp.metrics.RecordError()
			} else {
				fp.mu.Lock()
				fp.frameCount[frame.CameraID]++
				count := fp.frameCount[frame.CameraID]
				fp.mu.Unlock()

				fp.logger.Debug("Frame processed successfully",
					zap.String("camera", frame.CameraID),
					zap.Uint64("frame", frame.Number),
					zap.Duration("processing_time", result.Duration))

				if count%30 == 0 {
					select {
					case fp.consolidateChan <- struct{}{}:
						fp.logger.Debug("Triggered frame consolidation",
							zap.String("camera", frame.CameraID),
							zap.Uint64("frame_count", count))
					default:
					}
				}
			}
		}
	}
}

func (fp *FrameProcessor) consolidateFrames() error {
	fp.processingMap.Range(func(key, value interface{}) bool {
		cameraID := key.(string)
		cameraDir := filepath.Join(fp.config.OutputDir, cameraID)

		frames, err := filepath.Glob(filepath.Join(cameraDir, "frame_*.jpg"))
		if err != nil {
			fp.logger.Error("Failed to glob frames",
				zap.String("camera", cameraID),
				zap.Error(err))
			return true
		}

		if len(frames) == 0 {
			return true
		}

		fp.logger.Info("Consolidating frames",
			zap.String("camera", cameraID),
			zap.Int("frame_count", len(frames)))

		// Create video
		videoDir := filepath.Join(fp.config.OutputDir, "videos")
		if err := os.MkdirAll(videoDir, 0755); err != nil {
			fp.logger.Error("Failed to create video directory",
				zap.String("camera", cameraID),
				zap.Error(err))
			return true
		}

		videoPath := filepath.Join(videoDir,
			fmt.Sprintf("%s_%s.mp4",
				cameraID,
				time.Now().Format("20060102_150405")))

		if err := fp.createVideo(frames, videoPath); err != nil {
			fp.logger.Error("Failed to create video",
				zap.String("camera", cameraID),
				zap.String("video_path", videoPath),
				zap.Error(err))
			return true
		}

		fp.logger.Info("Video consolidation complete",
			zap.String("camera", cameraID),
			zap.String("video_path", videoPath),
			zap.Int("processed_frames", len(frames)))

		// Cleanup if configured
		if fp.config.DeleteOriginals {
			for _, frame := range frames {
				if err := os.Remove(frame); err != nil {
					fp.logger.Warn("Failed to delete frame",
						zap.String("camera", cameraID),
						zap.String("frame", frame),
						zap.Error(err))
				}
			}
			fp.logger.Debug("Cleaned up original frames",
				zap.String("camera", cameraID),
				zap.Int("frames_deleted", len(frames)))
		}

		return true
	})

	return nil
}

func (fp *FrameProcessor) createVideo(frames []string, outputPath string) error {
	if len(frames) == 0 {
		return fmt.Errorf("no frames to process")
	}

	// Verify FFmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in system PATH: %w", err)
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
		"-y", // Overwrite output file
		"-f", "concat",
		"-safe", "0",
		"-i", listFile,
		"-c:v", "libx264",
		"-preset", "medium",
		"-crf", "23",
		"-movflags", "+faststart",
		"-pix_fmt", "yuv420p",
		filepath.ToSlash(outputPath),
	}

	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg error: %v\nOutput: %s", err, stderr.String())
	}

	// Verify the output file exists and has size
	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("video file creation failed or file is empty")
	}

	return nil
}

func (fp *FrameProcessor) consolidationRoutine(ctx context.Context) {
	fp.logger.Info("Starting consolidation routine",
		zap.Duration("interval", fp.config.VideoInterval))

	ticker := time.NewTicker(fp.config.VideoInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fp.logger.Info("Stopping consolidation routine")
			return
		case <-fp.consolidateChan:
			fp.logger.Debug("Received immediate consolidation signal")
			if err := fp.consolidateFrames(); err != nil {
				fp.logger.Error("Consolidation failed",
					zap.Error(err),
					zap.Time("timestamp", time.Now()),
					zap.Duration("interval", fp.config.VideoInterval))
			}
		case <-ticker.C:
			fp.logger.Debug("Running scheduled consolidation",
				zap.Time("timestamp", time.Now()),
				zap.Duration("interval", fp.config.VideoInterval))
			if err := fp.consolidateFrames(); err != nil {
				fp.logger.Error("Scheduled consolidation failed",
					zap.Error(err),
					zap.Time("timestamp", time.Now()),
					zap.Duration("interval", fp.config.VideoInterval))
			}
		}
	}
}

func isBase64(data []byte) bool {
	_, err := base64.StdEncoding.DecodeString(string(data))
	return err == nil && len(data) > 0 && len(data)%4 == 0
}

func (fp *FrameProcessor) Start(ctx context.Context) error {
	if fp.logger == nil {
		return fmt.Errorf("logger not initialized")
	}

	// Start frame processing goroutine
	go fp.processFrames(ctx)

	// Start consolidation goroutine
	go fp.consolidationRoutine(ctx)

	fp.logger.Info("Frame processor started successfully",
		zap.Int("max_frames", fp.config.MaxFrames),
		zap.Duration("video_interval", fp.config.VideoInterval),
		zap.String("output_dir", fp.config.OutputDir))

	return nil
}

func (fp *FrameProcessor) Stop() {
	fp.logger.Info("Stopping frame processor",
		zap.Time("timestamp", time.Now()))

	close(fp.frameChan)
	close(fp.consolidateChan)

	fp.logger.Info("Frame processor stopped",
		zap.Time("timestamp", time.Now()))
}
