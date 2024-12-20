#!/bin/bash

set -e

echo "=== CCTV System Setup Script ==="

# Function to check if a process is running
check_process() {
    if [ -n "$1" ] && kill -0 $1 2>/dev/null; then
        return 0
    fi
    return 1
}

# Function to clean up processes
cleanup() {
    echo "Cleaning up processes..."
    [ -n "$SERVER_PID" ] && kill $SERVER_PID 2>/dev/null || true
    [ -n "$SIM_PID" ] && kill $SIM_PID 2>/dev/null || true
    wait
}

# Set up trap for cleanup
trap cleanup EXIT INT TERM

# Create necessary directories
mkdir -p frames/videos

# Build the applications
echo "Building applications..."
go build -o bin/cctvserver cmd/cctvserver/main.go
go build -o bin/camerasim cmd/camsim/main.go

# Start the server
echo "Starting server..."
LOG_LEVEL=debug ./bin/cctvserver &
SERVER_PID=$!

# Wait for server to start
echo "Waiting for server to initialize..."
sleep 3

# Check if server is running
if ! check_process $SERVER_PID; then
    echo "Server failed to start"
    exit 1
fi

# Start camera simulator
echo "Starting camera simulator..."
LOG_LEVEL=debug ./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect" &
SIM_PID=$!

# Wait to ensure simulator connects
sleep 2

# Check if simulator is running
if ! check_process $SIM_PID; then
    echo "Camera simulator failed to start"
    exit 1
fi

# Monitor processes and collect frames
echo "Recording frames (30 seconds)..."
end=$((SECONDS + 30))

while [ $SECONDS -lt $end ]; do
    # Check both processes
    if ! check_process $SERVER_PID; then
        echo "Server process died unexpectedly"
        exit 1
    fi
    if ! check_process $SIM_PID; then
        echo "Camera simulator died unexpectedly"
        exit 1
    fi

    # Print status every 5 seconds
    if [ $((SECONDS % 5)) -eq 0 ]; then
        echo "Recording in progress... ($(($end - SECONDS))s remaining)"
        # Check if frames are being generated
        frame_count=$(find frames -name "frame_*.jpg" | wc -l)
        echo "Current frame count: $frame_count"
    fi
    sleep 1
done

echo "Recording complete. Processing video..."

# Clean up processes gracefully
cleanup

# Wait a moment for any final frames to be written
sleep 2

# Check for frames and create video
frame_count=$(find frames -name "frame_*.jpg" | wc -l)
if [ $frame_count -eq 0 ]; then
    echo "No frames were captured!"
    exit 1
else
    echo "Found $frame_count frames"
    
    # Create video from frames
    for camera_dir in frames/cam*; do
        if [ -d "$camera_dir" ]; then
            camera_name=$(basename "$camera_dir")
            echo "Processing frames for $camera_name..."
            
            ffmpeg -framerate 30 -pattern_type glob -i "$camera_dir/frame_*.jpg" \
                   -c:v libx264 -pix_fmt yuv420p -y "frames/videos/${camera_name}_output.mp4"
                   
            if [ $? -eq 0 ]; then
                echo "Video created successfully: frames/videos/${camera_name}_output.mp4"
            else
                echo "Failed to create video for $camera_name"
            fi
        fi
    done
fi

echo "Setup complete!"