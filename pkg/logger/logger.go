// File: pkg/logger/logger.go
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF875F")).
			MarginLeft(1)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#61AFEF"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E06C75"))

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D19A66"))

	debugStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#98C379"))

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#56B6C2"))
)

type LogLevel int

const (
	DebugLevel LogLevel = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

type LogEntry struct {
	Level     LogLevel
	Message   string
	Timestamp time.Time
	Fields    []zapcore.Field
}

type Logger struct {
	*zap.Logger
	logChan     chan LogEntry
	done        chan struct{}
	uiProgram   *tea.Program
	outputFile  *os.File
	level       LogLevel
	mu          sync.RWMutex
	initialized bool
}

type UIModel struct {
	viewport    viewport.Model
	spinner     spinner.Model
	logs        []string
	ready       bool
	termWidth   int
	termHeight  int
	logChan     chan LogEntry
	done        chan struct{}
	lastUpdated time.Time
}

func extractFieldValue(field zapcore.Field) interface{} {
	switch field.Type {
	case zapcore.StringType:
		return field.String
	case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
		return field.Integer
	case zapcore.Uint64Type, zapcore.Uint32Type, zapcore.Uint16Type, zapcore.Uint8Type:
		return field.Integer
	case zapcore.Float64Type, zapcore.Float32Type:
		return field.Integer
	case zapcore.TimeType:
		if t, ok := field.Interface.(time.Time); ok {
			return t
		}
		return time.Time{}
	case zapcore.DurationType:
		if d, ok := field.Interface.(time.Duration); ok {
			return d
		}
		return time.Duration(0)
	case zapcore.ErrorType:
		if err, ok := field.Interface.(error); ok {
			return err.Error()
		}
		return "error"
	default:
		return field.Interface
	}
}

func formatFieldValue(value interface{}) string {
	switch v := value.(type) {
	case time.Time:
		return v.Format("15:04:05.000")
	case time.Duration:
		return v.String()
	case nil:
		return "nil"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatFields(fields []zapcore.Field) string {
	if len(fields) == 0 {
		return ""
	}

	var result string
	for _, field := range fields {
		value := extractFieldValue(field)
		formattedValue := formatFieldValue(value)
		if formattedValue != "nil" && formattedValue != "" {
			result += fmt.Sprintf(" %s=%s", field.Key, formattedValue)
		}
	}
	return result
}

func NewLogger(level string, config Config) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(config.OutputPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	f, err := os.OpenFile(config.OutputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	core := zapcore.NewTee(
		zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(f),
			zap.DebugLevel,
		),
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			zapcore.Level(parseLogLevel(level)),
		),
	)

	zapLogger := zap.New(core)

	return &Logger{
		Logger:     zapLogger,
		logChan:    make(chan LogEntry, 1000),
		done:       make(chan struct{}),
		outputFile: f,
		level:      InfoLevel,
	}, nil
}

func parseLogLevel(level string) zapcore.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// Modified logging methods to handle field conversion
func (l *Logger) Debug(msg string, fields ...zap.Field) {
	convertedFields := make([]zapcore.Field, len(fields))
	for i, f := range fields {
		convertedFields[i] = zapcore.Field(f)
	}
	l.Logger.Debug(msg, fields...)
	l.sendToUI(DebugLevel, msg, convertedFields...)
}

func (l *Logger) Info(msg string, fields ...zap.Field) {
	convertedFields := make([]zapcore.Field, len(fields))
	for i, f := range fields {
		convertedFields[i] = zapcore.Field(f)
	}
	l.Logger.Info(msg, fields...)
	l.sendToUI(InfoLevel, msg, convertedFields...)
}

func (l *Logger) Warn(msg string, fields ...zap.Field) {
	convertedFields := make([]zapcore.Field, len(fields))
	for i, f := range fields {
		convertedFields[i] = zapcore.Field(f)
	}
	l.Logger.Warn(msg, fields...)
	l.sendToUI(WarnLevel, msg, convertedFields...)
}

func (l *Logger) Error(msg string, fields ...zap.Field) {
	convertedFields := make([]zapcore.Field, len(fields))
	for i, f := range fields {
		convertedFields[i] = zapcore.Field(f)
	}
	l.Logger.Error(msg, fields...)
	l.sendToUI(ErrorLevel, msg, convertedFields...)
}

func (l *Logger) Fatal(msg string, fields ...zap.Field) {
	convertedFields := make([]zapcore.Field, len(fields))
	for i, f := range fields {
		convertedFields[i] = zapcore.Field(f)
	}
	l.Logger.Fatal(msg, fields...)
}

func (l *Logger) sendToUI(level LogLevel, msg string, fields ...zapcore.Field) {
	l.mu.RLock()
	if !l.initialized {
		l.mu.RUnlock()
		return
	}
	l.mu.RUnlock()

	select {
	case l.logChan <- LogEntry{
		Level:     level,
		Message:   msg,
		Timestamp: time.Now(),
		Fields:    fields,
	}:
	case <-l.done:
		return
	default:
		// Channel is full, log will be dropped
	}
}

func (l *Logger) StartUI() error {
	l.mu.Lock()
	if l.initialized {
		l.mu.Unlock()
		return nil
	}
	l.initialized = true
	l.mu.Unlock()

	model := NewUIModel(l.logChan, l.done)
	program := tea.NewProgram(model)
	l.uiProgram = program

	go func() {
		if _, err := program.Run(); err != nil {
			l.Error("failed to start UI", zap.Error(err))
		}
	}()

	return nil
}

func NewUIModel(logChan chan LogEntry, done chan struct{}) UIModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return UIModel{
		spinner:     s,
		logs:        make([]string, 0),
		logChan:     logChan,
		done:        done,
		lastUpdated: time.Now(),
	}
}

// File: pkg/logger/logger.go

func (m UIModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.waitForLogs,
		tea.EnterAltScreen,
	)
}

func (m UIModel) waitForLogs() tea.Msg {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	select {
	case entry := <-m.logChan:
		return entry
	case <-m.done:
		return tea.Quit
	case <-ticker.C:
		return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
			return t
		})
	}
}

func (m UIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		if !m.ready {
			m.viewport = viewport.New(msg.Width-2, msg.Height-4)
			m.viewport.Style = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62"))
			m.ready = true
		}
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.viewport.Width = msg.Width - 2
		m.viewport.Height = msg.Height - 4

	case LogEntry:
		logLine := formatLogEntry(msg)
		m.logs = append(m.logs, logLine)
		if len(m.logs) > 1000 { // Prevent memory growth
			m.logs = m.logs[len(m.logs)-1000:]
		}
		m.viewport.SetContent(joinLogs(m.logs))
		m.viewport.GotoBottom()
		cmds = append(cmds, m.waitForLogs)

	case spinner.TickMsg:
		var spinnerCmd tea.Cmd
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		cmds = append(cmds, spinnerCmd)

	case time.Time:
		cmds = append(cmds, m.waitForLogs)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Close channels if they're still open
	if l.done != nil {
		close(l.done)
		l.done = nil
	}

	// Stop UI if running
	if l.uiProgram != nil {
		l.uiProgram.Kill()
		l.uiProgram = nil
	}

	// Close file handles
	if l.outputFile != nil {
		if err := l.outputFile.Close(); err != nil {
			return fmt.Errorf("failed to close log file: %w", err)
		}
		l.outputFile = nil
	}

	// Sync underlying logger
	if l.Logger != nil {
		if err := l.Logger.Sync(); err != nil {
			if !strings.Contains(err.Error(), "inappropriate ioctl") {
				return err
			}
		}
	}

	return nil
}

func (m UIModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	title := titleStyle.Render("CCTV System Logs")
	spinner := m.spinner.View() + " "
	timestamp := timestampStyle.Render(time.Now().Format("15:04:05"))

	header := lipgloss.JoinHorizontal(lipgloss.Center, spinner, title, timestamp)
	body := m.viewport.View()

	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

func formatLogEntry(entry LogEntry) string {
	timestamp := timestampStyle.Render(entry.Timestamp.Format("15:04:05.000"))
	var levelStyle lipgloss.Style
	var level string

	switch entry.Level {
	case DebugLevel:
		levelStyle = debugStyle
		level = "DEBUG"
	case InfoLevel:
		levelStyle = infoStyle
		level = "INFO"
	case WarnLevel:
		levelStyle = warnStyle
		level = "WARN"
	case ErrorLevel:
		levelStyle = errorStyle
		level = "ERROR"
	}

	levelStr := levelStyle.Render(fmt.Sprintf("%-5s", level))
	fields := formatFields(entry.Fields)

	return fmt.Sprintf("%s %s %s%s",
		timestamp,
		levelStr,
		entry.Message,
		fields)
}

func joinLogs(logs []string) string {
	var result string
	for i, log := range logs {
		result += log
		if i < len(logs)-1 {
			result += "\n"
		}
	}
	return result
}
