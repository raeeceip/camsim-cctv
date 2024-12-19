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
	return &CameraSimulator{
		id:         id,
		signalAddr: signalAddr,
		width:      width,
		height:     height,
		done:       make(chan struct{}),
	}
}

func (cs *CameraSimulator) Connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(cs.signalAddr, nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	cs.conn = conn
	return nil
}

func (cs *CameraSimulator) generateFrame() (*image.RGBA, string) {
	img := image.NewRGBA(image.Rect(0, 0, cs.width, cs.height))
	pattern := ""

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
			for y := 0; y < cs.height; y++ {
				wave := math.Sin(float64(x)*0.05 + offset)
				mid := float64(cs.height) / 2
				pos := mid + wave*50
				if math.Abs(float64(y)-pos) < 2 {
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
	text := fmt.Sprintf("Frame: %d | Time: %s", cs.frameCount, time.Now().Format("15:04:05"))
	col := color.RGBA{255, 255, 255, 255} // White color for text
	point := image.Point{10, 10}
	addLabel(img, point, text, col)
}

func addLabel(img *image.RGBA, pt image.Point, label string, col color.Color) {
	for i := 0; i < len(label); i++ {
		draw.Draw(img, image.Rect(pt.X+i*7, pt.Y, pt.X+(i+1)*7, pt.Y+10), &image.Uniform{col}, image.Point{}, draw.Over)
	}
}

func (cs *CameraSimulator) encodeFrame(img *image.RGBA) (string, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	if err != nil {
		return "", fmt.Errorf("jpeg encoding failed: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func (cs *CameraSimulator) Start(ctx context.Context) error {
	ticker := time.NewTicker(time.Second / 30) // 30 FPS
	defer ticker.Stop()

	cs.wg.Add(1)
	go func() {
		defer cs.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				img, pattern := cs.generateFrame()
				frameData, err := cs.encodeFrame(img)
				if err != nil {
					log.Printf("Frame encoding failed: %v", err)
					continue
				}

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
					FrameNum: cs.frameCount,
				}

				if err := cs.conn.WriteJSON(msg); err != nil {
					log.Printf("Failed to send frame: %v", err)
					return
				}

				cs.frameCount++
				if cs.frameCount%30 == 0 {
					log.Printf("Sent frame %d", cs.frameCount)
				}
			}
		}
	}()

	return nil
}

func (cs *CameraSimulator) Stop() {
	close(cs.done)
	if cs.conn != nil {
		cs.conn.Close()
	}
	cs.wg.Wait()
}

func main() {
	id := flag.String("id", "cam1", "Camera ID")
	addr := flag.String("addr", "ws://localhost:8080/camera/connect", "Signal server address")
	width := flag.Int("width", 640, "Frame width")
	height := flag.Int("height", 480, "Frame height")
	flag.Parse()

	log.Printf("Starting video stream for camera %s", *id)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sim := NewCameraSimulator(*id, *addr, *width, *height)
	if err := sim.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	go func() {
		<-sigChan
		log.Println("Shutting down...")
		cancel()
	}()

	if err := sim.Start(ctx); err != nil {
		log.Fatalf("Failed to start streaming: %v", err)
	}

	sim.wg.Wait()
}
