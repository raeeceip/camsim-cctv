# setup.ps1
# PowerShell script for Windows setup of CCTV system

# Ensure script is running with administrator privileges
if (-NOT ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")) {
    Write-Warning "Please run this script as Administrator!"
    Break
}

# Function to write colored output
function Write-ColorOutput($ForegroundColor) {
    $fc = $host.UI.RawUI.ForegroundColor
    $host.UI.RawUI.ForegroundColor = $ForegroundColor
    if ($args) {
        Write-Output $args
    }
    $host.UI.RawUI.ForegroundColor = $fc
}

function Write-Info($message) {
    Write-ColorOutput Green "[INFO] $message"
}

function Write-Warning($message) {
    Write-ColorOutput Yellow "[WARN] $message"
}

function Write-Error($message) {
    Write-ColorOutput Red "[ERROR] $message"
}

# Check if Chocolatey is installed
function Install-Chocolatey {
    Write-Info "Checking for Chocolatey installation..."
    if (!(Test-Path "$env:ProgramData\chocolatey\choco.exe")) {
        Write-Info "Installing Chocolatey..."
        Set-ExecutionPolicy Bypass -Scope Process -Force
        [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072
        Invoke-Expression ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))
    }
    else {
        Write-Info "Chocolatey is already installed"
    }
}

# Install required dependencies using Chocolatey
function Install-Dependencies {
    Write-Info "Installing dependencies..."
    
    # Install required packages
    choco install -y `
        git `
        golang `
        ffmpeg `
        openssl `
        mingw `
        make
    
    # Refresh environment variables
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
}

# Setup Go environment
function Setup-GoEnvironment {
    Write-Info "Setting up Go environment..."
    
    # Set GOPATH if not already set
    if (-not $env:GOPATH) {
        $gopath = "$env:USERPROFILE\go"
        [System.Environment]::SetEnvironmentVariable("GOPATH", $gopath, "User")
        [System.Environment]::SetEnvironmentVariable("Path", $env:Path + ";$gopath\bin", "User")
        
        # Create Go directories
        New-Item -ItemType Directory -Force -Path "$gopath\src", "$gopath\bin", "$gopath\pkg" | Out-Null
    }
    
    # Refresh environment variables
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
}

# Generate SSL certificates
function Generate-SSLCertificates {
    Write-Info "Generating SSL certificates..."
    
    # Create certificates directory
    New-Item -ItemType Directory -Force -Path "certs" | Out-Null
    
    # Generate SSL certificates using OpenSSL
    openssl req -x509 -newkey rsa:4096 `
        -keyout certs/key.pem `
        -out certs/cert.pem `
        -days 365 -nodes `
        -subj "/CN=localhost" `
        -addext "subjectAltName = DNS:localhost,IP:127.0.0.1"
}

# Build application
function Build-Application {
    Write-Info "Building CCTV application..."
    
    # Create necessary directories
    New-Item -ItemType Directory -Force -Path "bin", "frames\videos", "logs" | Out-Null
    
    # Download Go dependencies
    go mod download
    
    # Build server
    Write-Info "Building server..."
    go build -o bin/cctvserver.exe cmd/cctvserver/main.go
    
    # Build camera simulator
    Write-Info "Building camera simulator..."
    go build -o bin/camerasim.exe cmd/camsim/main.go
}

# Run tests
function Invoke-Tests {
    Write-Info "Running tests..."
    go test ./... -v
}

# Main setup process
function Main {
    Write-Info "Starting CCTV system setup..."
    
    # Install Chocolatey if not present
    Install-Chocolatey
    
    # Install dependencies
    Install-Dependencies
    
    # Setup Go environment
    Setup-GoEnvironment
    
    # Generate SSL certificates
    Generate-SSLCertificates
    
    # Build application
    Build-Application
    
    # Run tests
    Invoke-Tests
    
    Write-Info "Setup completed successfully!"
    Write-Info @"

To start the CCTV system:

1. Start the server:
   .\bin\cctvserver.exe

2. In a new terminal, start the camera simulator:
   .\bin\camerasim.exe -id cam1 -addr "ws://localhost:8080/camera/connect"

The system will begin capturing frames and processing them into videos.

Configuration files can be found in:
- Main config: config.yaml
- SSL certificates: certs\
- Log files: logs\

For more information, please refer to the README.md file.
"@
}

# Error handling
try {
    Main
}
catch {
    Write-Error "An error occurred during setup: $_"
    exit 1
}