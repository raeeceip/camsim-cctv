#!/bin/bash

# Exit on error
set -e

echo "=== CCTV System Setup Script ==="

# Function to check if we're running in WSL
check_wsl() {
    if grep -q Microsoft /proc/version; then
        return 0
    else
        return 1
    fi
}

# Function to generate certificates
generate_certificates() {
    echo "Generating SSL certificates..."
    mkdir -p certs
    
    # Different command format based on environment
    if check_wsl; then
        openssl req -x509 -newkey rsa:4096 \
            -keyout certs/key.pem \
            -out certs/cert.pem \
            -days 365 \
            -nodes \
            -subj "/C=US/ST=State/L=City/O=Organization/CN=localhost"
    else
        # For Git Bash/Windows
        MSYS_NO_PATHCONV=1 openssl req -x509 -newkey rsa:4096 \
            -keyout certs/key.pem \
            -out certs/cert.pem \
            -days 365 \
            -nodes \
            -subj "/C=US/ST=State/L=City/O=Organization/CN=localhost" \
            -addext "subjectAltName=DNS:localhost"
    fi
}

# Function to check if a command exists
check_command() {
    if ! command -v $1 &> /dev/null; then
        echo "$1 is required but not installed."
        if check_wsl; then
            echo "Installing using apt..."
            return 1
        else
            echo "Please install $1 manually and try again."
            exit 1
        fi
    fi
    return 0
}

# Function to install dependencies (WSL only)
install_dependencies() {
    if check_wsl; then
        sudo apt-get update
        sudo apt-get install -y \
            golang \
            ffmpeg \
            make \
            gcc \
            g++ \
            python3-pip \
            python3-opencv \
            libopencv-dev
    else
        echo "Please install the following dependencies manually:"
        echo "- Go"
        echo "- FFmpeg"
        echo "- Make"
        echo "- GCC"
        echo "- Python3"
        echo "- OpenCV"
        exit 1
    fi

    # Install Go dependencies
    go mod tidy
}

# Function to setup Makefile
setup_makefile() {
    echo "Creating Makefile..."
    cat > Makefile << 'EOF'
.PHONY: build run clean test

build:
	go build -o bin/cctvserver cmd/cctvserver/main.go
	go build -o bin/camerasim cmd/camsim/main.go

run-server:
	./bin/cctvserver

run-sim:
	./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect"

clean:
	rm -rf bin/
	rm -rf frames/

test:
	go test ./...

video:
	ffmpeg -framerate 30 -pattern_type glob -i 'frames/cam1/*.jpg' \
		-c:v libx264 -pix_fmt yuv420p -t 120 output.mp4
EOF
}

# Function to consolidate frames into video
consolidate_frames() {
    echo "Consolidating frames into video..."
    if [ -d "frames/cam1" ]; then
        ffmpeg -framerate 30 -pattern_type glob -i 'frames/cam1/*.jpg' \
            -c:v libx264 -pix_fmt yuv420p -t 120 output.mp4
        echo "Video created as output.mp4"
    else
        echo "No frames found to consolidate"
    fi
}

# Function to enhance camera simulator security
enhance_security() {
    echo "Generating certificates and updating config..."
    generate_certificates

    # Update config.yaml with SSL settings
    cat > config.yaml << EOF
log_level: "debug"

server:
  host: "localhost"
  port: 8080
  ssl:
    enabled: true
    cert_file: "certs/cert.pem"
    key_file: "certs/key.pem"

stream:
  signal_address: "localhost:8081"
  stream_address: "localhost:8082"
  video_codec: "h264"
  video_bitrate: 2000
  framerate: 30
  width: 1280
  height: 720

storage:
  output_dir: "./frames"
  save_frames: true
  max_file_size: 104857600
  max_disk_usage: 1073741824
  max_frame_count: 1000
EOF
}

# Function to check FFmpeg installation
check_ffmpeg() {
    if ! command -v ffmpeg &> /dev/null; then
        echo "FFmpeg is not installed. Please install FFmpeg first."
        echo "Windows: Download from https://www.ffmpeg.org/download.html"
        echo "WSL/Linux: sudo apt-get install ffmpeg"
        exit 1
    fi

    # Test FFmpeg functionality
    if ! ffmpeg -version &> /dev/null; then
        echo "FFmpeg is installed but not working properly."
        exit 1
    fi
}

# Main setup process
main() {
    # Check FFmpeg first
    check_ffmpeg
    # Create necessary directories
    mkdir -p bin frames

    echo "Checking environment..."
    if check_wsl; then
        echo "Running in WSL environment"
    else
        echo "Running in Git Bash environment"
    fi

    echo "Checking and installing dependencies..."
    check_command go || install_dependencies
    check_command ffmpeg || install_dependencies

    echo "Enhancing security..."
    enhance_security

    echo "Building application..."
    make build

    echo "Starting server in background..."
    make run-server &
    SERVER_PID=$!

    echo "Waiting for server to start..."
    sleep 3

    echo "Running camera simulator..."
    make run-sim &
    SIM_PID=$!

    # Monitor both processes
    echo "Recording frames..."
    TIMEOUT=30  # 30 seconds of recording
    START_TIME=$SECONDS
    
    while [ $(( SECONDS - START_TIME )) -lt $TIMEOUT ]; do
        if ! kill -0 $SERVER_PID 2>/dev/null || ! kill -0 $SIM_PID 2>/dev/null; then
            echo "One of the processes died unexpectedly"
            break
        fi
        sleep 1
    done

}

# Run main function
main