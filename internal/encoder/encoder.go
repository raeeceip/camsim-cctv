// File: internal/encoder/encoder.go
package encoder

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

// checkDependencies verifies that required external dependencies are available
func checkDependencies() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in system PATH: %w. Please install FFmpeg before running the application", err)
	}
	return nil
}

type EncoderConfig struct {
	Codec            string
	Bitrate          int
	Framerate        int
	KeyframeInterval int
	Width            int
	Height           int
}

type Encoder struct {
	config    EncoderConfig
	inputPipe io.WriteCloser
	cmd       *exec.Cmd
}

func NewEncoder(config EncoderConfig) (*Encoder, error) {
	// Check dependencies first
	if err := checkDependencies(); err != nil {
		return nil, fmt.Errorf("dependency check failed: %w", err)
	}

	// Build FFmpeg command for streaming
	args := []string{
		"-f", "rawvideo",
		"-pix_fmt", "yuv420p",
		"-s", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-r", strconv.Itoa(config.Framerate),
		"-i", "-", // Read from stdin
		"-c:v", config.Codec,
		"-b:v", fmt.Sprintf("%dk", config.Bitrate),
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-g", strconv.Itoa(config.KeyframeInterval),
		"-f", "rtp",
		"rtp://127.0.0.1:5004",
	}

	cmd := exec.Command("ffmpeg", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start encoder: %w", err)
	}

	return &Encoder{
		config:    config,
		cmd:       cmd,
		inputPipe: stdin,
	}, nil
}

func (e *Encoder) Encode(frameData []byte, isKeyFrame bool) error {
	_, err := e.inputPipe.Write(frameData)
	return err
}

func (e *Encoder) Close() error {
	if e.inputPipe != nil {
		e.inputPipe.Close()
	}

	if e.cmd != nil && e.cmd.Process != nil {
		return e.cmd.Process.Kill()
	}

	return nil
}
