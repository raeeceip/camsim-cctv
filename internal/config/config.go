package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	LogLevel string        `mapstructure:"log_level"`
	Server   ServerConfig  `mapstructure:"server"`
	Stream   StreamConfig  `mapstructure:"stream"`
	Storage  StorageConfig `mapstructure:"storage"`
}

type ServerConfig struct {
	Port       int    `mapstructure:"port"`
	Host       string `mapstructure:"host"`
	SignalPort int    `mapstructure:"signal_port"`
	StreamPort int    `mapstructure:"stream_port"`
}

type StreamConfig struct {
	SignalAddress string            `mapstructure:"signal_address"`
	StreamAddress string            `mapstructure:"stream_address"`
	VideoCodec    string            `mapstructure:"video_codec"`
	VideoBitrate  int               `mapstructure:"video_bitrate"`
	Framerate     int               `mapstructure:"framerate"`
	Width         int               `mapstructure:"width"`
	Height        int               `mapstructure:"height"`
	Options       map[string]string `mapstructure:"options"`
}

type StorageConfig struct {
	OutputDir      string `mapstructure:"output_dir"`
	SaveFrames     bool   `mapstructure:"save_frames"`
	MaxFrames      int    `mapstructure:"max_frames"`
	MaxDiskUsage   int64  `mapstructure:"max_disk_usage"`
	RetentionHours int64  `mapstructure:"retention_hours"`
}

func Load() (*Config, error) {
	// Set default configuration values
	setDefaults()

	// Set configuration file paths
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// Try to read configuration file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate and ensure required directories exist
	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

func setDefaults() {
	// Server defaults
	viper.SetDefault("log_level", "info")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.host", "localhost")
	viper.SetDefault("server.signal_port", 8081)
	viper.SetDefault("server.stream_port", 8082)

	// Stream defaults
	viper.SetDefault("stream.video_codec", "h264")
	viper.SetDefault("stream.video_bitrate", 2000)
	viper.SetDefault("stream.framerate", 30)
	viper.SetDefault("stream.width", 1280)
	viper.SetDefault("stream.height", 720)

	// Storage defaults
	viper.SetDefault("storage.output_dir", "frames")
	viper.SetDefault("storage.save_frames", true)
	viper.SetDefault("storage.max_frames", 1000)
	viper.SetDefault("storage.max_disk_usage", 1024*1024*1024) // 1GB
	viper.SetDefault("storage.retention_hours", 24)
}

func validateConfig(cfg *Config) error {
	// Validate log level
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[cfg.LogLevel] {
		cfg.LogLevel = "info" // Set default if invalid
	}

	// Ensure valid server configuration
	if cfg.Server.Port <= 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "localhost"
	}

	// Ensure valid stream configuration
	if cfg.Stream.VideoBitrate <= 0 {
		cfg.Stream.VideoBitrate = 2000
	}
	if cfg.Stream.Framerate <= 0 {
		cfg.Stream.Framerate = 30
	}
	if cfg.Stream.Width <= 0 {
		cfg.Stream.Width = 1280
	}
	if cfg.Stream.Height <= 0 {
		cfg.Stream.Height = 720
	}

	// Ensure valid storage configuration
	if cfg.Storage.OutputDir == "" {
		cfg.Storage.OutputDir = "frames"
	}
	if cfg.Storage.MaxFrames <= 0 {
		cfg.Storage.MaxFrames = 1000
	}
	if cfg.Storage.RetentionHours <= 0 {
		cfg.Storage.RetentionHours = 24
	}

	// Create required directories
	dirs := []string{
		cfg.Storage.OutputDir,
		filepath.Join(cfg.Storage.OutputDir, "videos"),
		"logs",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}
