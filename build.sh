#!/bin/bash

set -e

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Log functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check dependencies
check_dependencies() {
    log_info "Checking dependencies..."
    
    # Check Go installation
    if ! command -v go &> /dev/null; then
        log_error "Go is not installed"
        exit 1
    fi
        
    version=$(go version)
    log_info "Found Go: $version"
    
    ffmpeg_version=$(ffmpeg -version | head -n 1)
    log_info "Found FFmpeg: $ffmpeg_version"

    log_info "Dependencies check completed"


    # Check FFmpeg installation
    if ! command -v ffmpeg &> /dev/null; then
        log_error "FFmpeg is not installed"
        exit 1
    }




# Create necessary directories
create_directories() {
    log_info "Creating necessary directories..."
    
    mkdir -p \
        bin \
        frames/videos \
        logs \
        config
    
    log_info "Directory structure created"
}

# Download dependencies
download_dependencies() {
    log_info "Downloading Go dependencies..."
    
    go mod download
    go mod tidy
    
    log_info "Dependencies downloaded"
}

# Build binaries
build_binaries() {
    log_info "Building applications..."
    
    # Build server
    log_info "Building server..."
    go build -o bin/cctvserver cmd/cctvserver/main.go
    
    # Build camera simulator
    log_info "Building camera simulator..."
    go build -o bin/camerasim cmd/camsim/main.go
    
    log_info "Build completed successfully"
}

# Run tests
run_tests() {
    log_info "Running tests..."
    
    go test -v ./...
    
    log_info "Tests completed"
}

# Copy configuration
copy_config() {
    log_info "Copying configuration..."
    
    if [ ! -f config.yaml ]; then
        cp config.yaml.example config.yaml
        log_info "Created default configuration file"
    else
        log_info "Configuration file already exists"
    fi
}

# Verify installation
verify_installation() {
    log_info "Verifying installation..."
    
    # Check binaries
    if [ ! -f bin/cctvserver ] || [ ! -f bin/camerasim ]; then
        log_error "Binary files are missing"
        exit 1
    }
    
    # Check configuration
    if [ ! -f config.yaml ]; then
        log_error "Configuration file is missing"
        exit 1
    }
    
    # Check directories
    for dir in frames/videos logs config; do
        if [ ! -d "$dir" ]; then
            log_error "Directory $dir is missing"
            exit 1
        fi
    done
    
    log_info "Installation verified successfully"
}

# Main function
main() {
    log_info "Starting build process..."
    
    check_dependencies
    create_directories
    download_dependencies
    build_binaries
    run_tests
    copy_config
    verify_installation
    
    log_info "Build completed successfully!"
    cat << EOF

${GREEN}Build completed successfully!${NC}

To start the CCTV system:

1. Start the server:
   ${YELLOW}./bin/cctvserver${NC}

2. In a new terminal, start the camera simulator:
   ${YELLOW}./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect"${NC}

Configuration file: config.yaml
Log files: logs/
Frame storage: frames/

For more information, please refer to the README.md file.
EOF
}

# Run main function
main