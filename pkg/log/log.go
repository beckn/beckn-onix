package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Error definitions for logging configuration.
var (
	ErrInvalidLogLevel   = errors.New("invalid log level")
	ErrLogDestinationNil = errors.New("log Destinations cant be empty")
	ErrMissingFilePath   = errors.New("file path missing in destination config for file logging")
)

// DestinationType represents the type of logging destination.
type DestinationType string

// Supported logging destinations.
const (
	Stdout DestinationType = "stdout"
	File   DestinationType = "file"
)

// Destination defines a log output destination.
type Destination struct {
	Type   DestinationType   `yaml:"type"`   // Specifies destination type
	Config map[string]string `yaml:"config"` // holds destination-specific configuration.
}

// Level represents logging levels.
type Level string

// Supported log levels.
const (
	DebugLevel Level = "debug"
	InfoLevel  Level = "info"
	WarnLevel  Level = "warn"
	ErrorLevel Level = "error"
	FatalLevel Level = "fatal"
	PanicLevel Level = "panic"
)

// Mapping of Level to zerolog.Level.
var logLevels = map[Level]zerolog.Level{
	DebugLevel: zerolog.DebugLevel,
	InfoLevel:  zerolog.InfoLevel,
	WarnLevel:  zerolog.WarnLevel,
	ErrorLevel: zerolog.ErrorLevel,
	FatalLevel: zerolog.FatalLevel,
	PanicLevel: zerolog.PanicLevel,
}

// Config represents the logging configuration.
type Config struct {
	Level        Level         `yaml:"level"`        //Logging Level
	Destinations []Destination `yaml:"destinations"` // List of log destinations
	ContextKeys  []string      `yaml:"contextKeys"`  // List of context keys to extract
}

// Logger Instance
var (
	logger zerolog.Logger
	once   sync.Once
	cfg    Config
)

// init initializes the logger with default configuration.
func init() {
	logger, _ = getLogger(defaultConfig)
}

// InitLogger initializes the logger with the provided configuration.
//
// It ensures that the logger is initialized only once using sync.Once.
// Returns an error if the configuration is invalid.
func InitLogger(c Config) error {
	var err error
	once.Do(func() { // makes it singleton
		err = c.validate()
		if err != nil {
			return
		}
		logger, err = getLogger(c)
		if err != nil {
			return
		}

	})
	return err
}

// getLogger creates and configures a new logger based on the given configuration.
// Returns an initialized zerolog.Logger or an error if configuration is invalid.
func getLogger(config Config) (zerolog.Logger, error) {
	var newLogger zerolog.Logger

	// Multiwriter for multiple log destinations
	var writers []io.Writer
	for _, dest := range config.Destinations {
		switch dest.Type {
		case Stdout:
			writers = append(writers, os.Stdout)
		case File:
			filePath := dest.Config["path"]

			// File rotation
			lumberjackLogger := &lumberjack.Logger{
				Filename: filePath,
			}

			setConfigValue := func(key string, target *int) {
				if valStr, ok := dest.Config[key]; ok {
					if val, err := strconv.Atoi(valStr); err == nil {
						*target = val
					}
				}
			}

			setConfigValue("maxSize", &lumberjackLogger.MaxSize)
			setConfigValue("maxBackups", &lumberjackLogger.MaxBackups)
			setConfigValue("maxAge", &lumberjackLogger.MaxAge)

			if compress, ok := dest.Config["compress"]; ok {
				lumberjackLogger.Compress = compress == "true"
			}
			writers = append(writers, lumberjackLogger)
		}
	}

	multiwriter := io.MultiWriter(writers...)

	newLogger = zerolog.New(multiwriter).
		Level(logLevels[config.Level]).
		With().
		Timestamp().
		Caller().
		Logger()

	// Replace the cfg with given config
	cfg = config

	return newLogger, nil

}

// validate checks if the provided logging configuration is valid.
// It ensures that a valid log level is provided and that at least one
// destination is specified. Returns an error if validation fails
func (config *Config) validate() error {
	// Log Level is valid
	if _, exists := logLevels[config.Level]; !exists {
		return ErrInvalidLogLevel
	}

	// Log Destinations is not empty
	if len(config.Destinations) == 0 {
		return ErrLogDestinationNil
	}

	// File path exists in destination config for File type destination
	for _, dest := range config.Destinations {
		switch dest.Type {
		case Stdout:

		case File:
			if _, exists := dest.Config["path"]; !exists {
				return ErrMissingFilePath
			}
			// Validate lumberjack config if present
			for _, key := range []string{"maxSize", "maxBackups", "maxAge"} {
				if valStr, ok := dest.Config[key]; ok {
					if _, err := strconv.Atoi(valStr); err != nil {
						return fmt.Errorf("invalid %s: %w", key, err)
					}
				}
			}
		default:
			return fmt.Errorf("Invalid destination type '%s'", dest.Type)
		}
	}
	return nil
}

// Default Config
var defaultConfig = Config{
	Level: InfoLevel,
	Destinations: []Destination{
		{Type: Stdout},
	},
	ContextKeys: []string{},
}

// Debug logs a debug-level message.
func Debug(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.DebugLevel, msg, nil)
}

// Debugf logs a formatted debug-level message.
func Debugf(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.DebugLevel, msg, nil)
}

// Info logs an info-level message.
func Info(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.InfoLevel, msg, nil)
}

// Infof logs a formatted info-level message.
func Infof(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.InfoLevel, msg, nil)
}

// Warn logs a warning-level message.
func Warn(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.WarnLevel, msg, nil)
}

// Warnf logs a formatted warning-level message.
func Warnf(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.WarnLevel, msg, nil)
}

// Error logs an error-level message.
func Error(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.ErrorLevel, msg, err)
}

// Errorf logs a formatted error-level message.
func Errorf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.ErrorLevel, msg, err)
}

// Fatal logs a fatal-level message and exits the application.
func Fatal(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.FatalLevel, msg, err)
}

// Fatalf logs a formatted fatal-level message and exits the application.
func Fatalf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.FatalLevel, msg, err)
}

// Panic logs a panic-level message.
func Panic(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.PanicLevel, msg, err)
}

// Panicf logs a formatted panic-level message.
func Panicf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.PanicLevel, msg, err)
}

// Request logs an HTTP request.
func Request(ctx context.Context, r *http.Request, body []byte) {
	event := logger.Info()
	// Iterate through headers and log them
	for name, values := range r.Header {
		for _, value := range values {
			event = event.Str(name, value)
		}
	}
	addCtx(ctx, event)

	event.Str("method", r.Method).
		Str("url", r.URL.String()).
		Str("body", string(body)).
		Str("remoteAddr", r.RemoteAddr).
		Msg("HTTP Request")
}

// Response logs an HTTP response.
func Response(ctx context.Context, r *http.Request, statusCode int, responseTime time.Duration) {
	event := logger.Info()

	addCtx(ctx, event)

	event.Str("method", r.Method).
		Str("url", r.URL.String()).
		Int("statusCode", statusCode).
		Dur("responseTime", responseTime).
		Msg("HTTP Response")
}

// logEvent logs messages at the specified level with optional error details.
func logEvent(ctx context.Context, level zerolog.Level, msg string, err error) {
	event := logger.WithLevel(level)

	// Attach error if provided
	if err != nil {
		event = event.Err(err)
	}

	// Add context fields
	addCtx(ctx, event)

	event.Msg(msg)
}

// addCtx adds context-specific fields to log events.
func addCtx(ctx context.Context, event *zerolog.Event) {
	for _, key := range cfg.ContextKeys {
		if val, ok := ctx.Value(key).(string); ok {
			event.Any(key, val)
		}
	}
}
