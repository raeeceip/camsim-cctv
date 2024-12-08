# Enterprise CCTV Streaming System

A robust, Go-based CCTV streaming system designed for enterprise surveillance integration. This system provides real-time video streaming capabilities with support for multiple camera protocols and enterprise-grade security features.

## Features

### Core Capabilities
- Multi-protocol support (RTSP, ONVIF, MJPEG)
- Real-time video streaming with configurable quality
- Camera discovery and auto-configuration
- Recording and playback functionality
- Motion detection and event triggering
- Health monitoring and alerting

### Security
- Role-based access control (RBAC)
- Audit logging
- End-to-end encryption
- Token-based authentication
- Session management

### Integration
- REST API for third-party integration
- Webhook support for event notifications
- Multiple storage backend support
- LDAP/Active Directory integration
- Metrics export for Prometheus

## Architecture

```
├── cmd/
│   └── cctvserver/
│       └── main.go           # Application entry point
├── internal/
│   ├── auth/                 # Authentication & authorization
│   │   ├── jwt.go
│   │   └── rbac.go
│   ├── camera/              # Camera management
│   │   ├── discovery.go
│   │   ├── onvif.go
│   │   └── rtsp.go
│   ├── storage/             # Video storage
│   │   ├── local.go
│   │   └── s3.go
│   ├── streaming/           # Streaming logic
│   │   ├── mjpeg.go
│   │   └── webrtc.go
│   └── monitoring/          # System monitoring
│       ├── health.go
│       └── metrics.go
├── pkg/
│   ├── api/                 # Public API
│   │   └── v1/
│   └── config/             # Configuration
└── web/                    # Web interface
    ├── static/
    └── templates/
```

## Getting Started

### Prerequisites
- Go 1.21 or higher
- FFmpeg
- PostgreSQL
- Redis (for caching)

### Installation

1. Clone the repository:
```bash
git clone https://github.com/yourusername/cctv-system.git
cd cctv-system
```

2. Install dependencies:
```bash
go mod tidy
```

3. Configure the application:
```bash
cp config.example.yaml config.yaml
# Edit config.yaml with your settings
```

4. Run the application:
```bash
go run cmd/cctvserver/main.go
```

### Configuration

The system can be configured through:
- YAML configuration file
- Environment variables
- Command-line flags

Example configuration:
```yaml
server:
  port: 8080
  host: localhost

storage:
  type: local
  path: /var/lib/cctv
  retention: 30d

cameras:
  discovery:
    enabled: true
    interval: 5m
  protocols:
    - rtsp
    - onvif
    - mjpeg

security:
  jwt:
    secret: your-secret-key
    expiry: 24h
  ssl:
    enabled: true
    cert: /path/to/cert.pem
    key: /path/to/key.pem
```

## API Documentation

### Camera Management
```http
GET /api/v1/cameras           # List all cameras
POST /api/v1/cameras          # Add new camera
GET /api/v1/cameras/{id}      # Get camera details
PUT /api/v1/cameras/{id}      # Update camera
DELETE /api/v1/cameras/{id}   # Remove camera
```

### Stream Management
```http
GET /api/v1/streams           # List active streams
POST /api/v1/streams          # Start new stream
DELETE /api/v1/streams/{id}   # Stop stream
```

## Monitoring

### Health Checks
The system provides health check endpoints:
```http
GET /health           # Basic health check
GET /health/detailed  # Detailed system status
```

### Metrics
Prometheus metrics are exposed at:
```http
GET /metrics
```

Key metrics include:
- Stream latency
- Camera uptime
- Storage usage
- Error rates
- Request duration

## Security Considerations

### Authentication
- JWT-based authentication
- Support for API keys
- OAuth2 integration capability

### Authorization
- Role-based access control
- Resource-level permissions
- IP whitelisting support

## Production Deployment

### Docker
```dockerfile
FROM golang:1.21-alpine
WORKDIR /app
COPY . .
RUN go build -o cctvserver cmd/cctvserver/main.go
EXPOSE 8080
CMD ["./cctvserver"]
```

### Kubernetes
Basic deployment manifest provided in `deploy/k8s/`.

## Testing

Run the test suite:
```bash
go test ./...
```

Run with coverage:
```bash
go test -cover ./...
```

## Contributing

1. Fork the repository
2. Create your feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

[Your chosen license]
