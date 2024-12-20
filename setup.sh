#!/bin/bash

set -e

echo "=== CCTV System Setup Script ==="

# Track PIDs and state
PID_FILE=".running_pids"
CLEANUP_LOCK=".cleanup_lock"
SHUTDOWN_REQUESTED=0

# Function to check if a process is running
check_process() {
    if [ -n "$1" ] && kill -0 $1 2>/dev/null; then
        return 0
    fi
    return 1
}

# Function to gracefully stop a process
stop_process() {
    local pid=$1
    local name=$2
    local timeout=5

    if check_process "$pid"; then
        echo "Stopping $name (PID: $pid)..."
        kill -TERM "$pid" 2>/dev/null || true
        
        # Wait for process to stop
        for ((i=1; i<=timeout; i++)); do
            if ! check_process "$pid"; then
                echo "$name stopped."
                return 0
            fi
            sleep 1
        done
        
        # Force kill if still running
        if check_process "$pid"; then
            echo "Force stopping $name..."
            kill -9 "$pid" 2>/dev/null || true
            sleep 1
        fi
    fi
}

# Function to clean up processes
cleanup() {
    # Prevent multiple cleanup runs
    if [ -f "$CLEANUP_LOCK" ]; then
        return
    fi
    touch "$CLEANUP_LOCK"
    
    echo "Cleaning up processes..."
    SHUTDOWN_REQUESTED=1

    if [ -f "$PID_FILE" ]; then
        while IFS=: read -r pid name; do
            stop_process "$pid" "$name"
        done < "$PID_FILE"
        rm -f "$PID_FILE"
    fi

    # Clean up lock file
    rm -f "$CLEANUP_LOCK"
    
    echo "Cleanup complete."
}

# Set up trap for cleanup
trap cleanup EXIT INT TERM

# Create necessary directories
mkdir -p frames/videos logs

# Build the applications
echo "Building applications..."
if ! go build -o bin/cctvserver cmd/cctvserver/main.go; then
    echo "Failed to build server"
    exit 1
fi

if ! go build -o bin/camerasim cmd/camsim/main.go; then
    echo "Failed to build camera simulator"
    exit 1
fi

# Start the server
echo "Starting server..."
LOG_LEVEL=info ./bin/cctvserver &
SERVER_PID=$!
echo "$SERVER_PID:server" > "$PID_FILE"

# Wait for server to start
echo "Waiting for server to initialize..."
sleep 3

# Check if server is running
if ! check_process $SERVER_PID; then
    echo "Server failed to start"
    cleanup
    exit 1
fi

# Start camera simulator
echo "Starting camera simulator..."
LOG_LEVEL=info ./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect" &
SIM_PID=$!
echo "$SIM_PID:simulator" >> "$PID_FILE"

# Wait to ensure simulator connects
sleep 2

# Check if simulator is running
if ! check_process $SIM_PID; then
    echo "Camera simulator failed to start"
    cleanup
    exit 1
fi

# Monitor processes and collect frames
echo "Recording frames (30 seconds)..."
end=$((SECONDS + 30))

while [ $SECONDS -lt $end ] && [ $SHUTDOWN_REQUESTED -eq 0 ]; do
    # Check both processes
    if ! check_process $SERVER_PID || ! check_process $SIM_PID; then
        echo "Process died unexpectedly"
        cleanup
        exit 1
    fi

    # Print status every 5 seconds
    if [ $((SECONDS % 5)) -eq 0 ]; then
        echo "Recording in progress... ($(($end - SECONDS))s remaining)"
        frame_count=$(find frames -name "frame_*.jpg" 2>/dev/null | wc -l)
        echo "Current frame count: $frame_count"
    fi
    sleep 1
done

echo "Recording complete. Processing video..."

# Clean up processes gracefully
cleanup

echo "Setup complete!"