// File: cmd/camerasim/main.go
package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
	"time"

	"github.com/gorilla/websocket"
)

type CameraSimulator struct {
	id         string
	signalAddr string
	conn       *websocket.Conn
	width      int
	height     int
	frameCount uint64
	startTime  time.Time
}

type FrameStats struct {
	FrameNumber uint64
	Timestamp   time.Time
	Size        int
	Pattern     string
}

func NewCameraSimulator(id, signalAddr string, width, height int) *CameraSimulator {
	return &CameraSimulator{
		id:         id,
		signalAddr: signalAddr,
		width:      width,
		height:     height,
		startTime:  time.Now(),
	}
}

func (cs *CameraSimulator) Connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(cs.signalAddr, nil)
	if err != nil {
		return err
	}
	cs.conn = conn
	return nil
}

func (cs *CameraSimulator) SimulateFrames() error {
	ticker := time.NewTicker(time.Second / 30) // 30 FPS
	defer ticker.Stop()

	statsTicker := time.NewTicker(5 * time.Second) // Print stats every 5 seconds
	defer statsTicker.Stop()

	log.Printf("Starting frame simulation for camera %s", cs.id)
	log.Printf("Resolution: %dx%d", cs.width, cs.height)

	for {
		select {
		case <-ticker.C:
			stats, err := cs.sendFrame()
			if err != nil {
				return err
			}

			// Print minimal frame info
			if cs.frameCount%30 == 0 { // Print every 30 frames
				log.Printf("Frame %d sent (Pattern: %s, Size: %d bytes)",
					stats.FrameNumber, stats.Pattern, stats.Size)
			}

		case <-statsTicker.C:
			// Print detailed stats every 5 seconds
			duration := time.Since(cs.startTime)
			fps := float64(cs.frameCount) / duration.Seconds()
			log.Printf("\n=== Camera %s Statistics ===\n"+
				"Uptime: %s\n"+
				"Total Frames: %d\n"+
				"Average FPS: %.2f\n"+
				"Current Resolution: %dx%d\n",
				cs.id, duration.Round(time.Second),
				cs.frameCount, fps, cs.width, cs.height)
		}
	}
}

func (cs *CameraSimulator) sendFrame() (FrameStats, error) {
	cs.frameCount++
	frame, pattern := cs.generateFrame()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, frame, &jpeg.Options{Quality: 75}); err != nil {
		return FrameStats{}, err
	}

	base64Data := base64.StdEncoding.EncodeToString(buf.Bytes())

	msg := struct {
		Type   string    `json:"type"`
		Data   string    `json:"data"`
		Camera string    `json:"camera"`
		Time   time.Time `json:"time"`
	}{
		Type:   "frame",
		Data:   base64Data,
		Camera: cs.id,
		Time:   time.Now(),
	}

	if err := cs.conn.WriteJSON(msg); err != nil {
		return FrameStats{}, err
	}

	return FrameStats{
		FrameNumber: cs.frameCount,
		Timestamp:   time.Now(),
		Size:        buf.Len(),
		Pattern:     pattern,
	}, nil
}

func (cs *CameraSimulator) generateFrame() (image.Image, string) {
	img := image.NewRGBA(image.Rect(0, 0, cs.width, cs.height))
	pattern := ""

	// Choose pattern based on time
	switch (cs.frameCount / 150) % 4 { // Change pattern every 5 seconds (at 30 FPS)
	case 0:
		// Gradient pattern
		pattern = "Gradient"
		for y := 0; y < cs.height; y++ {
			for x := 0; x < cs.width; x++ {
				gradient := uint8((float64(x) / float64(cs.width)) * 255)
				img.Set(x, y, color.Gray{Y: gradient})
			}
		}

	case 1:
		// Moving sine wave
		pattern = "Sine Wave"
		offset := float64(cs.frameCount) * 0.1
		for x := 0; x < cs.width; x++ {
			for y := 0; y < cs.height; y++ {
				wave := math.Sin(float64(x)*0.05 + offset)
				mid := float64(cs.height) / 2
				pos := mid + wave*50
				if math.Abs(float64(y)-pos) < 2 {
					img.Set(x, y, color.White)
				}
			}
		}

	case 2:
		// Checkerboard
		pattern = "Checkerboard"
		squareSize := 40
		for y := 0; y < cs.height; y++ {
			for x := 0; x < cs.width; x++ {
				if ((x/squareSize)+(y/squareSize))%2 == 0 {
					img.Set(x, y, color.White)
				}
			}
		}

	case 3:
		// Moving circle
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
					img.Set(x, y, color.White)
				}
			}
		}
	}

	// Add timestamp
	addTimestamp(img, cs.frameCount)
	return img, pattern
}

func addTimestamp(img *image.RGBA, frameNum uint64) {
	timestamp := fmt.Sprintf("Frame: %d | Time: %s", frameNum, time.Now().Format("15:04:05"))
	draw.Draw(img, image.Rect(10, 10, 300, 30), &image.Uniform{color.Black}, image.Point{}, draw.Over)
	// Use the timestamp variable to avoid the declared and not used error
	fmt.Println(timestamp)
	// Note: In a real implementation, you'd want to use a proper font rendering library
}

func main() {
	id := flag.String("id", "cam1", "Camera ID")
	addr := flag.String("addr", "ws://localhost:8080/camera/connect", "Signal server address")
	width := flag.Int("width", 640, "Frame width")
	height := flag.Int("height", 480, "Frame height")
	flag.Parse()

	sim := NewCameraSimulator(*id, *addr, *width, *height)
	if err := sim.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	log.Printf("Camera %s initialized successfully", *id)
	log.Printf("Connecting to: %s", *addr)
	log.Printf("Resolution: %dx%d", *width, *height)

	if err := sim.SimulateFrames(); err != nil {
		log.Fatalf("Streaming failed: %v", err)
	}
}
