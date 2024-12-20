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
	"os/signal"
	"sync"
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
	done       chan struct{}
	wg         sync.WaitGroup
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
	flag.Parse()

	log.Printf("Starting camera simulator with ID: %s", *id)
	log.Printf("Resolution: %dx%d", *width, *height)
	log.Printf("Server address: %s", *addr)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create and start simulator
	sim := NewCameraSimulator(*id, *addr, *width, *height)
	if err := sim.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	go func() {
		<-sigChan
		log.Println("Received interrupt signal, shutting down...")
		cancel()
	}()

	if err := sim.Start(ctx); err != nil {
		if err.Error() != "not connected" {
			log.Printf("Failed to start streaming: %v", err)
		}
	}

	sim.Stop()
}
