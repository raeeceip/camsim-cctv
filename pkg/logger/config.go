// File: pkg/logger/config.go
package logger

// Config represents logger configuration
type Config struct {
	// Level is the minimum logging level (debug, info, warn, error)
	Level string

	// OutputPath is the path where log files will be written
	OutputPath string `mapstructure:"output_path"`

	// MaxSize is the maximum size in megabytes of the log file before it gets rotated
	MaxSize int `mapstructure:"max_size"`

	// MaxBackups is the maximum number of old log files to retain
	MaxBackups int `mapstructure:"max_backups"`

	// MaxAge is the maximum number of days to retain old log files
	MaxAge int `mapstructure:"max_age"`

	// Compress determines if the rotated log files should be compressed
	Compress bool `mapstructure:"compress"`

	// UseConsole determines if logs should also be written to console
	UseConsole bool `mapstructure:"use_console"`

	// UseJSON determines if logs should be formatted as JSON
	UseJSON bool `mapstructure:"use_json"`

	// EnableUI determines if the Bubble Tea UI should be started
	EnableUI bool `mapstructure:"enable_ui"`

	// UIRefreshRate is the refresh rate of the UI in milliseconds
	UIRefreshRate int `mapstructure:"ui_refresh_rate"`
}

// DefaultConfig returns a default logger configuration
func DefaultConfig() Config {
	return Config{
		Level:         "info",
		OutputPath:    "logs/cctv.log",
		MaxSize:       100,
		MaxBackups:    3,
		MaxAge:        7,
		Compress:      true,
		UseConsole:    true,
		UseJSON:       false,
		EnableUI:      true,
		UIRefreshRate: 100,
	}
}
