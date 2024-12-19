// File: cmd/camerasim/main.go
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
)

type VideoSimulator struct {
	id         string
	signalAddr string
	conn       *websocket.Conn
	width      int
	height     int
	frameCount uint64
	ffmpeg     *exec.Cmd
	tlsConfig  *tls.Config
}

func NewVideoSimulator(id, signalAddr string, width, height int, certFile string) (*VideoSimulator, error) {
	// Load the certificate for secure connection
	cert, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cert: %v", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(cert) {
		return nil, fmt.Errorf("failed to append cert")
	}

	tlsConfig := &tls.Config{
		RootCAs:            certPool,
		InsecureSkipVerify: false, // Set to true only for development
	}

	return &VideoSimulator{
		id:         id,
		signalAddr: signalAddr,
		width:      width,
		height:     height,
		tlsConfig:  tlsConfig,
	}, nil
}

func (vs *VideoSimulator) Connect() error {
	dialer := websocket.Dialer{
		TLSClientConfig: vs.tlsConfig,
	}

	conn, _, err := dialer.Dial(vs.signalAddr, nil)
	if err != nil {
		return err
	}
	vs.conn = conn
	return nil
}

func (vs *VideoSimulator) StartStreaming() error {
	// FFmpeg command to generate video stream
	args := []string{
		"-re",         // Read input at native framerate
		"-f", "lavfi", // Use lavfi input
		"-i", fmt.Sprintf("testsrc=size=%dx%d:rate=30", vs.width, vs.height), // Generate test pattern
		"-c:v", "libx264", // Use H.264 codec
		"-preset", "ultrafast", // Use ultrafast preset for low latency
		"-tune", "zerolatency", // Tune for low latency
		"-f", "rawvideo", // Output raw video
		"-pix_fmt", "yuv420p", // Use YUV420P pixel format
		"pipe:1", // Output to stdout
	}

	vs.ffmpeg = exec.Command("ffmpeg", args...)
	stdout, err := vs.ffmpeg.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	if err := vs.ffmpeg.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	// Calculate frame size
	frameSize := vs.width * vs.height * 3 // 3 bytes per pixel (RGB)
	frame := make([]byte, frameSize)

	// Start frame reading loop
	go func() {
		for {
			_, err := stdout.Read(frame)
			if err != nil {
				log.Printf("Error reading frame: %v", err)
				return
			}

			vs.frameCount++

			// Encode frame as base64
			encodedFrame := base64.StdEncoding.EncodeToString(frame)

			// Send frame over WebSocket
			msg := struct {
				Type       string    `json:"type"`
				Data       string    `json:"data"`
				Camera     string    `json:"camera"`
				Time       time.Time `json:"time"`
				FrameNum   uint64    `json:"frame_num"`
				IsKeyFrame bool      `json:"is_keyframe"`
			}{
				Type:       "frame",
				Data:       encodedFrame,
				Camera:     vs.id,
				Time:       time.Now(),
				FrameNum:   vs.frameCount,
				IsKeyFrame: vs.frameCount%30 == 0, // Mark every 30th frame as keyframe
			}

			if err := vs.conn.WriteJSON(msg); err != nil {
				log.Printf("Error sending frame: %v", err)
				return
			}

			if vs.frameCount%30 == 0 {
				log.Printf("Sent frame %d", vs.frameCount)
			}
		}
	}()

	return nil
}

func (vs *VideoSimulator) Stop() {
	if vs.ffmpeg != nil && vs.ffmpeg.Process != nil {
		vs.ffmpeg.Process.Kill()
	}
	if vs.conn != nil {
		vs.conn.Close()
	}
}

func main() {
	id := flag.String("id", "cam1", "Camera ID")
	addr := flag.String("addr", "wss://localhost:8080/camera/connect", "Signal server address")
	width := flag.Int("width", 1280, "Frame width")
	height := flag.Int("height", 720, "Frame height")
	certFile := flag.String("cert", "certs/cert.pem", "Path to certificate file")
	flag.Parse()

	sim, err := NewVideoSimulator(*id, *addr, *width, *height, *certFile)
	if err != nil {
		log.Fatalf("Failed to create simulator: %v", err)
	}

	if err := sim.Connect(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	log.Printf("Starting video stream for camera %s", *id)
	if err := sim.StartStreaming(); err != nil {
		log.Fatalf("Failed to start streaming: %v", err)
	}

	// Wait for interrupt signal
	select {}
}
