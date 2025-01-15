#!/bin/bash

# Enable error handling
set -e

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
RECORD_TIME=${RECORD_TIME:-0}  # 0 means run indefinitely
LOG_DIR="logs"
FRAME_DIR="frames"
CONFIG_DIR="config"
PID_FILE=".running_pids"
CLEANUP_LOCK=".cleanup_lock"
SHUTDOWN_REQUESTED=0

# Print functions
print_header() {
    echo -e "\n${BLUE}=== $1 ===${NC}\n"
}

print_step() {
    echo -e "${GREEN}→${NC} $1"
}

print_error() {
    echo -e "${RED}ERROR:${NC} $1" >&2
}

# Process management functions
check_process() {
    if [ -n "$1" ] && kill -0 $1 2>/dev/null; then
        return 0
    fi
    return 1
}

stop_process() {
    local pid=$1
    local name=$2
    local timeout=10

    if check_process "$pid"; then
        print_step "Stopping $name (PID: $pid)..."
        kill -TERM "$pid" 2>/dev/null || true
        
        for ((i=1; i<=timeout; i++)); do
            if ! check_process "$pid"; then
                print_step "$name stopped gracefully."
                return 0
            fi
            sleep 1
        done
        
        if check_process "$pid"; then
            print_step "Force stopping $name..."
            kill -9 "$pid" 2>/dev/null || true
            sleep 1
        fi
    fi
}

cleanup() {
    # Prevent multiple cleanup runs
    if [ -f "$CLEANUP_LOCK" ]; then
        return
    fi
    touch "$CLEANUP_LOCK"
    
    print_header "Cleaning up processes"
    SHUTDOWN_REQUESTED=1

    if [ -f "$PID_FILE" ]; then
        while IFS=: read -r pid name; do
            stop_process "$pid" "$name"
        done < "$PID_FILE"
        rm -f "$PID_FILE"
    fi

    rm -f "$CLEANUP_LOCK"
    print_step "Cleanup complete"
    exit 0
}

# Set up signal handlers
trap cleanup SIGINT SIGTERM

# Directory setup
print_header "Setting up directories"
for dir in "$FRAME_DIR" "$LOG_DIR" "$CONFIG_DIR" "$FRAME_DIR/videos"; do
    mkdir -p "$dir"
    print_step "Created $dir"
done

# Build applications
print_header "Building applications"
print_step "Building server..."
if ! go build -o bin/cctvserver cmd/cctvserver/main.go; then
    print_error "Failed to build server"
    exit 1
fi

print_step "Building camera simulator..."
if ! go build -o bin/camerasim cmd/camsim/main.go; then
    print_error "Failed to build camera simulator"
    exit 1
fi

# Start server
print_header "Starting server"
LOG_LEVEL=info ./bin/cctvserver &
SERVER_PID=$!
echo "$SERVER_PID:server" > "$PID_FILE"

# Wait for server startup
print_step "Waiting for server to initialize..."
sleep 3

if ! check_process $SERVER_PID; then
    print_error "Server failed to start"
    cleanup
    exit 1
fi

# Start camera simulator
print_header "Starting camera simulator"
LOG_LEVEL=info ./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect" &
SIM_PID=$!
echo "$SIM_PID:simulator" >> "$PID_FILE"

sleep 2
if ! check_process $SIM_PID; then
    print_error "Camera simulator failed to start"
    cleanup
    exit 1
fi

if [ "$RECORD_TIME" -gt 0 ]; then
    print_header "Recording for ${RECORD_TIME} seconds"
    progress_bar() {
        local current=$1
        local total=$2
        local width=50
        local percentage=$((current * 100 / total))
        local completed=$((width * current / total))
        local remaining=$((width - completed))
        printf "\r[%${completed}s%${remaining}s] %d%%" | tr ' ' '=' | tr ' ' ' '
        printf " (%ds/%ds)" "$current" "$total"
    }

    end=$((SECONDS + RECORD_TIME))
    while [ $SECONDS -lt $end ] && [ $SHUTDOWN_REQUESTED -eq 0 ]; do
        if ! check_process $SERVER_PID || ! check_process $SIM_PID; then
            print_error "\nProcess died unexpectedly"
            cleanup
            exit 1
        fi
        progress_bar $((SECONDS - (end - RECORD_TIME))) $RECORD_TIME
        sleep 1
    done
    echo # New line after progress bar
    cleanup
else
    print_header "System running"
    print_step "Press Ctrl+C to stop"
    print_step "Access logs at: http://localhost:8080/logs"
    
    # Monitor processes indefinitely
    while [ $SHUTDOWN_REQUESTED -eq 0 ]; do
        if ! check_process $SERVER_PID || ! check_process $SIM_PID; then
            print_error "Process died unexpectedly"
            cleanup
            exit 1
        fi
        sleep 1
    done
fi