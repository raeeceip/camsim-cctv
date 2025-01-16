package processor

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	OutputDir          string        `json:"output_dir"`
	MaxFrames          int           `json:"max_frames"`
	RetentionTime      time.Duration `json:"retention_time"`
	BufferSize         int           `json:"buffer_size"`
	VideoInterval      time.Duration `json:"video_interval"`
	DeleteOriginals    bool          `json:"delete_originals"`
	VideoConsolidation bool          `json:"video_consolidation"`
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

func (fp *FrameProcessor) testFFmpeg() error {
	// Test FFmpeg installation and capabilities
	cmd := exec.Command("ffmpeg", "-version")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("FFmpeg test failed: %v\nOutput: %s", err, stderr.String())
	}

	// Test a simple conversion to ensure FFmpeg is working
	testDir := filepath.Join(fp.config.OutputDir, "test")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		return fmt.Errorf("failed to create test directory: %w", err)
	}
	defer os.RemoveAll(testDir)

	// Create a simple test image
	testImage := filepath.Join(testDir, "test.jpg")
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	file, err := os.Create(testImage)
	if err != nil {
		return fmt.Errorf("failed to create test image file: %w", err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		return fmt.Errorf("failed to create test image: %w", err)
	}

	// Try to create a test video
	testVideo := filepath.Join(testDir, "test.mp4")
	cmd = exec.Command("ffmpeg", "-y", "-i", testImage, "-frames:v", "1", "-c:v", "libx264", testVideo)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("FFmpeg conversion test failed: %v\nOutput: %s", err, stderr.String())
	}

	return nil
}

func (fp *FrameProcessor) saveFrame(frame FrameData) ProcessResult {
	startTime := time.Now()
	result := ProcessResult{
		CameraID:      frame.CameraID,
		FrameNumber:   frame.Number,
		ProcessedTime: startTime,
	}

	// Validate frame data
	if len(frame.Data) == 0 {
		result.Error = fmt.Errorf("empty frame data")
		return result
	}

	if frame.CameraID == "" {
		result.Error = fmt.Errorf("missing camera ID")
		return result
	}

	// Create camera directory
	cameraDir := filepath.Join(fp.config.OutputDir, frame.CameraID)
	if err := os.MkdirAll(cameraDir, 0755); err != nil {
		result.Error = fmt.Errorf("failed to create camera directory: %w", err)
		return result
	}

	// Process frame data with validation
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

	// Validate decoded data
	if len(frameData) == 0 {
		result.Error = fmt.Errorf("invalid frame data after decoding")
		return result
	}

	// Verify JPEG format
	if _, err := jpeg.DecodeConfig(bytes.NewReader(frameData)); err != nil {
		result.Error = fmt.Errorf("invalid JPEG format: %w", err)
		return result
	}

	// Create filename with proper frame number
	filename := filepath.Join(cameraDir,
		fmt.Sprintf("frame_%05d_%s.jpg",
			frame.Number,
			frame.Timestamp.Format("20060102_150405.000")))

	// Save the frame
	if err := os.WriteFile(filename, frameData, 0644); err != nil {
		result.Error = fmt.Errorf("failed to write frame file: %w", err)
		return result
	}

	result.FilePath = filename
	result.Duration = time.Since(startTime)

	return result
}

// ProcessFrame with improved error handling
func (fp *FrameProcessor) ProcessFrame(frame FrameData) error {
	if frame.CameraID == "" || frame.Number == 0 || len(frame.Data) == 0 {
		return fmt.Errorf("invalid frame data")
	}

	select {
	case fp.frameChan <- frame:
		fp.logger.Debug("Queued frame for processing",
			zap.String("camera", frame.CameraID),
			zap.Uint64("frame", frame.Number))
		return nil
	default:
		return fmt.Errorf("frame processing queue full")
	}
}

// consolidateFrames with better control flow
func (fp *FrameProcessor) consolidateFrames() error {
	fp.mu.Lock()
	defer fp.mu.Unlock()

	// Check if consolidation is enabled
	if !fp.config.VideoConsolidation {
		return nil
	}

	var processedCameras []string
	fp.processingMap.Range(func(key, value interface{}) bool {
		cameraID := key.(string)
		cameraDir := filepath.Join(fp.config.OutputDir, cameraID)

		// Get frames for this camera
		frames, err := filepath.Glob(filepath.Join(cameraDir, "frame_*.jpg"))
		if err != nil {
			fp.logger.Error("Failed to glob frames",
				zap.String("camera", cameraID),
				zap.Error(err))
			return true
		}

		// Skip if not enough frames
		if len(frames) < fp.config.MaxFrames {
			return true
		}

		// Sort frames by number
		sort.Slice(frames, func(i, j int) bool {
			numI := extractFrameNumber(frames[i])
			numJ := extractFrameNumber(frames[j])
			return numI < numJ
		})

		processedCameras = append(processedCameras, cameraID)

		// Process frames in batches
		for i := 0; i < len(frames); i += fp.config.MaxFrames {
			end := i + fp.config.MaxFrames
			if end > len(frames) {
				end = len(frames)
			}

			batch := frames[i:end]
			if err := fp.processFrameBatch(cameraID, batch); err != nil {
				fp.logger.Error("Failed to process frame batch",
					zap.String("camera", cameraID),
					zap.Error(err))
			}
		}

		return true
	})

	return nil
}

// New helper function to process frame batches
func (fp *FrameProcessor) processFrameBatch(cameraID string, frames []string) error {
	if len(frames) == 0 {
		return nil
	}

	videoDir := filepath.Join(fp.config.OutputDir, "videos")
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return fmt.Errorf("failed to create video directory: %w", err)
	}

	videoPath := filepath.Join(videoDir,
		fmt.Sprintf("%s_%s.mp4",
			cameraID,
			time.Now().Format("20060102_150405")))

	if err := fp.createVideo(frames, videoPath); err != nil {
		return fmt.Errorf("failed to create video: %w", err)
	}

	// Clean up processed frames if configured
	if fp.config.DeleteOriginals {
		for _, frame := range frames {
			if err := os.Remove(frame); err != nil {
				fp.logger.Warn("Failed to delete frame",
					zap.String("frame", frame),
					zap.Error(err))
			}
		}
	}

	return nil
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

func (fp *FrameProcessor) createVideo(frames []string, outputPath string) error {
	if len(frames) == 0 {
		return fmt.Errorf("no frames to process")
	}

	// Verify FFmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in system PATH: %w", err)
	}

	// Get the camera directory and ensure video directory exists
	cameraDir := filepath.Dir(frames[0])
	videoDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return fmt.Errorf("failed to create video directory: %w", err)
	}

	// Create a temporary frame list file
	tempListFile := filepath.Join(cameraDir, fmt.Sprintf("frames_list_%d.txt", time.Now().UnixNano()))
	defer os.Remove(tempListFile) // Clean up temp file after we're done

	// Sort frames by frame number
	sort.Slice(frames, func(i, j int) bool {
		numI := extractFrameNumber(frames[i])
		numJ := extractFrameNumber(frames[j])
		return numI < numJ
	})

	// Create frame list content with absolute paths and proper escaping
	var frameList strings.Builder
	for _, frame := range frames {
		absPath, err := filepath.Abs(frame)
		if err != nil {
			return fmt.Errorf("failed to get absolute path: %w", err)
		}
		// Convert to forward slashes and escape for FFmpeg
		framePath := strings.ReplaceAll(absPath, "\\", "/")
		// Escape single quotes in path
		framePath = strings.ReplaceAll(framePath, "'", "'\\''")
		frameList.WriteString(fmt.Sprintf("file '%s'\n", framePath))
		frameList.WriteString("duration 0.0333333333\n") // 30fps
	}

	// Add last frame one more time (required for duration of last frame)
	if len(frames) > 0 {
		absPath, err := filepath.Abs(frames[len(frames)-1])
		if err != nil {
			return fmt.Errorf("failed to get absolute path for last frame: %w", err)
		}
		framePath := strings.ReplaceAll(absPath, "\\", "/")
		framePath = strings.ReplaceAll(framePath, "'", "'\\''")
		frameList.WriteString(fmt.Sprintf("file '%s'\n", framePath))
	}

	// Write the list file
	if err := os.WriteFile(tempListFile, []byte(frameList.String()), 0644); err != nil {
		return fmt.Errorf("failed to create frame list: %w", err)
	}

	fp.logger.Info("Starting video creation",
		zap.Int("frame_count", len(frames)),
		zap.String("first_frame", frames[0]),
		zap.String("output", outputPath))

	// Convert paths to use forward slashes for FFmpeg
	outputPathFFmpeg := strings.ReplaceAll(outputPath, "\\", "/")
	tempListFileFFmpeg := strings.ReplaceAll(tempListFile, "\\", "/")

	// Prepare FFmpeg command
	args := []string{
		"-y",           // Overwrite output file
		"-f", "concat", // Use concat demuxer
		"-safe", "0", // Allow absolute paths
		"-i", tempListFileFFmpeg, // Input from list file
		"-vcodec", "libx264", // Use H.264 codec
		"-preset", "medium", // Balanced encoding speed/quality
		"-crf", "23", // Quality factor
		"-pix_fmt", "yuv420p", // Standard pixel format
		"-movflags", "+faststart", // Enable fast start
		outputPathFFmpeg, // Output file
	}

	// Create command
	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Log the exact command being run
	fp.logger.Debug("Running FFmpeg command",
		zap.String("command", fmt.Sprintf("ffmpeg %s", strings.Join(args, " "))),
		zap.String("working_dir", cmd.Dir))

	// Run FFmpeg
	if err := cmd.Run(); err != nil {
		// Check if the output file exists despite the error
		if _, statErr := os.Stat(outputPath); statErr == nil {
			// If the file exists and has content, log a warning but don't treat as error
			if info, _ := os.Stat(outputPath); info.Size() > 0 {
				fp.logger.Warn("FFmpeg reported error but output file was created",
					zap.Error(err),
					zap.String("stderr", stderr.String()))
				return nil
			}
		}
		return fmt.Errorf("ffmpeg error: %v\nOutput: %s", err, stderr.String())
	}

	// Verify the output file exists and has size
	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("video file creation failed or is empty")
	}

	fp.logger.Info("Video created successfully",
		zap.String("output", outputPath),
		zap.Int("frame_count", len(frames)))

	return nil
}

// extractFrameNumber gets the frame number from a filename like frame_00001_20241220_001507.773.jpg
func extractFrameNumber(filename string) int {
	base := filepath.Base(filename)
	// Handle frame_XXXXX_YYYYMMDD_HHMMSS.mmm.jpg format
	parts := strings.Split(base, "_")
	if len(parts) >= 2 {
		numStr := parts[1]
		if num, err := strconv.Atoi(numStr); err == nil {
			return num
		}
	}
	return 0
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
	// test ffmpeg
	if err := fp.testFFmpeg(); err != nil {
		return fmt.Errorf("FFmpeg validation failed: %w", err)
	}
	//quit if logger is not initialized
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

func (fp *FrameProcessor) cleanup() error {
	fp.logger.Info("Running final cleanup...")

	return fp.consolidateFrames()
}

// Update the Stop function
func (fp *FrameProcessor) Stop() {
	fp.logger.Info("Stopping frame processor",
		zap.Time("timestamp", time.Now()))

	// Process any remaining frames
	if err := fp.cleanup(); err != nil {
		fp.logger.Error("Cleanup failed", zap.Error(err))
	}

	close(fp.frameChan)
	close(fp.consolidateChan)

	fp.logger.Info("Frame processor stopped",
		zap.Time("timestamp", time.Now()))
}
