// File: internal/config/config.go
package config

import (
	"fmt"

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
	SignalAddress  string            `mapstructure:"signal_address"`  // WebSocket signaling address
	StreamAddress  string            `mapstructure:"stream_address"`  // HTTP stream addressa
	CameraURL      string            `mapstructure:"camera_url"`      // URL of the camera stream (RTSP/HTTP)
	CameraUsername string            `mapstructure:"camera_username"` // Camera authentication username
	CameraPassword string            `mapstructure:"camera_password"` // Camera authentication password
	VideoCodec     string            `mapstructure:"video_codec"`     // Video codec to use (e.g., h264)
	VideoBitrate   int               `mapstructure:"video_bitrate"`   // Video bitrate in kbps
	Framerate      int               `mapstructure:"framerate"`       // Target framerate
	Width          int               `mapstructure:"width"`           // Video width
	Height         int               `mapstructure:"height"`          // Video height
	Options        map[string]string `mapstructure:"options"`         // Additional camera options
}

type StorageConfig struct {
	OutputDir      string `mapstructure:"output_dir"`
	SaveFrames     bool   `mapstructure:"save_frames"`
	MaxFrames      int    `mapstructure:"max_frames"`
	MaxDiskUsage   int64  `mapstructure:"max_disk_usage"`
	RetentionHours int64  `mapstructure:"retention_hours"`
}

func Load() (*Config, error) {
	viper.SetDefault("log_level", "info")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.host", "localhost")
	viper.SetDefault("server.signal_port", 8081)

	// Stream defaults
	viper.SetDefault("stream.video_codec", "h264")
	viper.SetDefault("stream.video_bitrate", 2000)
	viper.SetDefault("stream.framerate", 30)
	viper.SetDefault("stream.width", 1280)
	viper.SetDefault("stream.height", 720)

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	if config.Stream.SignalAddress == "" {
		config.Stream.SignalAddress = fmt.Sprintf("%s:8081", config.Server.Host)
	}
	if config.Stream.StreamAddress == "" {
		config.Stream.StreamAddress = fmt.Sprintf("%s:8082", config.Server.Host)
	}

	return &config, nil
}
