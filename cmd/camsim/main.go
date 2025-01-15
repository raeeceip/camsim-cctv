package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type CameraSimulator struct {
	id              string
	signalAddr      string
	conn            *websocket.Conn
	width           int
	height          int
	frameCount      uint64
	done            chan struct{}
	wg              sync.WaitGroup
	frameBuffer     []*image.RGBA
	frameBufferLock sync.Mutex
	videoOutputDir  string
}

func (cs *CameraSimulator) saveVideo() error {
	cs.frameBufferLock.Lock()
	defer cs.frameBufferLock.Unlock()

	if len(cs.frameBuffer) == 0 {
		return fmt.Errorf("no frames to save")
	}

	// Ensure video output directory exists
	if err := os.MkdirAll(cs.videoOutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create video directory: %w", err)
	}

	// Create temporary directory for frames
	tempDir, err := os.MkdirTemp("", "cctv-frames-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Save frames as JPEG files
	// could be a utility function on its own later
	for i, frame := range cs.frameBuffer {
		framePath := filepath.Join(tempDir, fmt.Sprintf("frame_%05d.jpg", i))
		f, err := os.Create(framePath)
		if err != nil {
			return fmt.Errorf("failed to create frame file: %w", err)
		}
		if err := jpeg.Encode(f, frame, &jpeg.Options{Quality: 90}); err != nil {
			f.Close()
			return fmt.Errorf("failed to encode frame: %w", err)
		}
		f.Close()
	}

	// Create video file
	outputPath := filepath.Join(cs.videoOutputDir,
		fmt.Sprintf("%s_%s.mp4", cs.id, time.Now().Format("20060102_150405")))

	// FFmpeg command to create video
	cmd := exec.Command("ffmpeg",
		"-y",
		"-framerate", "30",
		"-i", filepath.Join(tempDir, "frame_%05d.jpg"),
		"-c:v", "libx264",
		"-preset", "medium",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		outputPath)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg error: %v\nOutput: %s", err, stderr.String())
	}

	log.Printf("Created video with %d frames: %s", len(cs.frameBuffer), outputPath)

	// Clear buffer after successful save
	cs.frameBuffer = nil

	return nil
}

func (cs *CameraSimulator) addFrameToBuffer(frame *image.RGBA) {
	cs.frameBufferLock.Lock()
	defer cs.frameBufferLock.Unlock()

	// Create a copy of the frame
	frameCopy := image.NewRGBA(frame.Bounds())
	draw.Draw(frameCopy, frame.Bounds(), frame, frame.Bounds().Min, draw.Src)
	cs.frameBuffer = append(cs.frameBuffer, frameCopy)

	// Save video every 300 frames (10 seconds at 30fps)
	if len(cs.frameBuffer) >= 300 {
		if err := cs.saveVideo(); err != nil {
			log.Printf("Failed to save video: %v", err)
		}
	}
}

func NewCameraSimulator(id, signalAddr string, width, height int) *CameraSimulator {
	if id == "" {
		id = fmt.Sprintf("cam-%d", time.Now().UnixNano())
	}
	if width <= 0 {
		width = 640
	}
	if height <= 0 {
		height = 480
	}

	return &CameraSimulator{
		id:         id,
		signalAddr: signalAddr,
		width:      width,
		height:     height,
		done:       make(chan struct{}),
	}
}

func (cs *CameraSimulator) Connect() error {
	log.Printf("Attempting to connect to %s...", cs.signalAddr)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   1024 * 1024,
		WriteBufferSize:  1024 * 1024,
	}

	conn, _, err := dialer.Dial(cs.signalAddr, nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	cs.conn = conn

	conn.SetReadLimit(32 * 1024 * 1024)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	log.Printf("Connected successfully to %s", cs.signalAddr)
	return nil
}

func (cs *CameraSimulator) handlePing(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := cs.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
				return fmt.Errorf("failed to write ping: %w", err)
			}
		}
	}
}

func (cs *CameraSimulator) Start(ctx context.Context) error {
	if cs.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Start ping handler
	cs.wg.Add(1)
	go func() {
		defer cs.wg.Done()
		if err := cs.handlePing(ctx); err != nil {
			log.Printf("Ping handler error: %v", err)
		}
	}()

	// Start message reader
	cs.wg.Add(1)
	go func() {
		defer cs.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_, _, err := cs.conn.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
						log.Printf("Read error: %v", err)
					}
					return
				}
			}
		}
	}()

	// Start frame generator
	ticker := time.NewTicker(time.Second / 30)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled, stopping frame generation")
			return nil
		case <-cs.done:
			log.Println("Received stop signal, stopping frame generation")
			return nil
		case <-ticker.C:
			if err := cs.sendFrame(); err != nil {
				log.Printf("Failed to send frame: %v", err)
				return err
			}
		}
	}
}

func (cs *CameraSimulator) sendFrame() error {
	if cs.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Generate frame
	img, pattern := cs.generateFrame()

	// Encode frame
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return fmt.Errorf("jpeg encoding failed: %w", err)
	}

	frameData := base64.StdEncoding.EncodeToString(buf.Bytes())

	// Add frame to buffer for video creation
	cs.addFrameToBuffer(img)

	// Create message
	msg := struct {
		Type     string    `json:"type"`
		Data     string    `json:"data"`
		Camera   string    `json:"camera"`
		Time     time.Time `json:"time"`
		Pattern  string    `json:"pattern"`
		FrameNum uint64    `json:"frame_num"`
	}{
		Type:     "frame",
		Data:     frameData,
		Camera:   cs.id,
		Time:     time.Now(),
		Pattern:  pattern,
		FrameNum: cs.frameCount + 1,
	}

	// Write message with deadline
	cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := cs.conn.WriteJSON(msg); err != nil {
		if closeErr := cs.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); closeErr != nil {
			log.Printf("Error sending close message: %v", closeErr)
		}
		return fmt.Errorf("failed to send frame: %w", err)
	}

	cs.frameCount++
	if cs.frameCount%30 == 0 {
		log.Printf("Sent frame %d (Pattern: %s)", cs.frameCount, pattern)
	}

	return nil
}

func (cs *CameraSimulator) generateFrame() (*image.RGBA, string) {
	img := image.NewRGBA(image.Rect(0, 0, cs.width, cs.height))
	pattern := ""

	// Fill background with dark gray
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{40, 40, 40, 255}}, image.Point{}, draw.Src)

	// Choose pattern based on time
	switch (cs.frameCount / 150) % 4 {
	case 0:
		pattern = "Gradient"
		for y := 0; y < cs.height; y++ {
			for x := 0; x < cs.width; x++ {
				gradient := uint8((float64(x) / float64(cs.width)) * 255)
				img.Set(x, y, color.RGBA{gradient, gradient, gradient, 255})
			}
		}

	case 1:
		pattern = "Sine Wave"
		offset := float64(cs.frameCount) * 0.1
		for x := 0; x < cs.width; x++ {
			wave := math.Sin(float64(x)*0.05 + offset)
			mid := float64(cs.height) / 2
			pos := mid + wave*50

			// Draw thick line
			for y := 0; y < cs.height; y++ {
				if math.Abs(float64(y)-pos) < 3 {
					img.Set(x, y, color.RGBA{255, 255, 255, 255})
				}
			}
		}

	case 2:
		pattern = "Checkerboard"
		squareSize := 40
		for y := 0; y < cs.height; y++ {
			for x := 0; x < cs.width; x++ {
				if ((x/squareSize)+(y/squareSize))%2 == 0 {
					img.Set(x, y, color.RGBA{255, 255, 255, 255})
				}
			}
		}

	case 3:
		pattern = "Moving Circle"
		centerX := cs.width/2 + int(math.Cos(float64(cs.frameCount)*0.05)*100)
		centerY := cs.height/2 + int(math.Sin(float64(cs.frameCount)*0.05)*100)
		radius := 50

		for y := 0; y < cs.height; y++ {
			for x := 0; x < cs.width; x++ {
				dx := float64(x - centerX)
				dy := float64(y - centerY)
				dist := math.Sqrt(dx*dx + dy*dy)
				if dist < float64(radius) {
					img.Set(x, y, color.RGBA{255, 255, 255, 255})
				}
			}
		}
	}

	// Add timestamp
	cs.addTimestamp(img)
	return img, pattern
}

func (cs *CameraSimulator) addTimestamp(img *image.RGBA) {
	timestamp := fmt.Sprintf("Frame: %d | Time: %s", cs.frameCount, time.Now().Format("15:04:05"))
	draw.Draw(img,
		image.Rect(10, 10, 300, 40),
		&image.Uniform{color.RGBA{0, 0, 0, 255}},
		image.Point{},
		draw.Over)

	log.Printf("Timestamp: %s", timestamp)
}

func (cs *CameraSimulator) Stop() {
	log.Println("Stopping camera simulator...")
	close(cs.done)

	if cs.conn != nil {
		if err := cs.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
			log.Printf("Error sending close message: %v", err)
		}
		time.Sleep(time.Second)
		cs.conn.Close()
	}

	cs.wg.Wait()
	log.Println("Camera simulator stopped")
}
func main() {
	// Parse command line flags
	id := flag.String("id", "cam1", "Camera ID")
	addr := flag.String("addr", "ws://localhost:8080/camera/connect", "Signal server address")
	width := flag.Int("width", 640, "Frame width")
	height := flag.Int("height", 480, "Frame height")
	videoDir := flag.String("video-dir", "videos", "Video output directory")
	flag.Parse()

	log.Printf("Starting camera simulator with ID: %s", *id)
	log.Printf("Resolution: %dx%d", *width, *height)
	log.Printf("Server address: %s", *addr)

	// Create and configure simulator
	sim := NewCameraSimulator(*id, *addr, *width, *height)
	sim.videoOutputDir = *videoDir

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Connect and start streaming
	if err := sim.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	if err := sim.Start(ctx); err != nil && err != context.Canceled {
		log.Printf("Streaming error: %v", err)
	}

	// Final cleanup
	sim.Stop()
}
