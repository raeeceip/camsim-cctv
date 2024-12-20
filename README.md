# Enterprise CCTV Streaming System

A lightweight, Go-based CCTV streaming system with real-time frame processing and video consolidation capabilities. The system supports websocket-based camera connections, frame processing, and automatic video generation.

## Features

### Core Capabilities

- Real-time frame streaming over WebSocket
- Automatic frame processing and storage
- Video consolidation from captured frames
- Configurable frame rates and quality settings
- Health monitoring and metrics
- Debug endpoints for system inspection

### Technical Features

- WebSocket-based camera connections with ping-pong keep-alive
- Base64 frame encoding/decoding
- JPEG image processing
- FFmpeg video consolidation
- Prometheus metrics export
- Graceful shutdown handling

## Architecture

```
├── cmd/
│   ├── cctvserver/
│   │   └── main.go           # Server entry point
│   └── camsim/
│       └── main.go           # Camera simulator
├── internal/
│   ├── config/              # Configuration management
│   │   └── config.go
│   ├── processor/           # Frame processing
│   │   └── processor.go
│   ├── server/             # WebSocket server
│   │   └── server.go
│   └── stream/             # Stream management
│       └── stream.go
├── pkg/
│   ├── logger/             # Logging utilities
│   │   └── logger.go
│   └── metrics/            # Prometheus metrics
│       └── metrics.go
└── setup.sh               # Setup and build script
```

## Getting Started

### Prerequisites

- Go 1.21 or higher
- FFmpeg (for video consolidation)
- WSL or Git Bash (for Windows users)

### Quick Start

1. Clone the repository:

```bash
git clone https://github.com/yourusername/cctv-system.git
cd cctv-system
```

2. Run the setup script:

```bash
chmod +x setup.sh
./setup.sh
```

The setup script will:

- Install required dependencies
- Build both server and camera simulator
- Generate SSL certificates
- Start the system
- Record and process frames
- Generate video output

### Manual Setup

1. Build the applications:

```bash
go build -o bin/cctvserver cmd/cctvserver/main.go
go build -o bin/camerasim cmd/camsim/main.go
```

2. Start the server:

```bash
./bin/cctvserver
```

3. Start the camera simulator:

```bash
./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect"
```

### Configuration

The system is configured through `config.yaml`:

```yaml
log_level: "debug"

server:
  host: "localhost"
  port: 8080

stream:
  video_codec: "h264"
  video_bitrate: 2000
  framerate: 30
  width: 1280
  height: 720

storage:
  output_dir: "./frames"
  save_frames: true
  max_file_size: 104857600 # 100MB
  max_disk_usage: 1073741824 # 1GB
  max_frame_count: 1000
```

## API Endpoints

### Camera Connection

```http
GET /camera/connect    # WebSocket endpoint for camera connections
```

### Monitoring

```http
GET /health           # Health check endpoint
GET /metrics          # Prometheus metrics endpoint
GET /debug/frames     # Debug endpoint for frame processing status
```

## Frame Processing

The system processes frames in the following steps:

1. Camera connects via WebSocket
2. Frames are sent as base64-encoded JPEG images
3. Frames are decoded and saved to disk
4. Frames are consolidated into video files
5. Optional cleanup of processed frames

## Camera Simulator

The included camera simulator provides test functionality:

- Generates test patterns (gradient, sine wave, checkerboard, moving circle)
- Configurable resolution and frame rate
- Automatic WebSocket reconnection
- Frame counting and statistics

## Monitoring

### Health Checks

```http
GET /health
```

Response:

```json
{
	"status": "healthy",
	"time": "2024-12-19T20:20:56Z"
}
```

### Metrics

The system exports Prometheus metrics for:

- Frame processing rates
- Processing latency
- Error counts
- Connection status

## Development

### Building

```bash
make build      # Build all components
make run-server # Run the server
make run-sim    # Run the simulator
```

### Testing

```bash
go test ./...
```

## Contributing

1. Fork the repository
2. Create your feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

Distributed under the MIT License. See `LICENSE` for more information.

## Contact

- Discord: [discord.com/guynamedchiso](https://discord.com/guynamedchiso)
- Email: [ chiboguchisomu@gmail.com ](mailto: chiboguchisomu@gmail.com)
