package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

type level string

type destinationType string

type destination struct {
	Type   destinationType   `yaml:"type"`
	Config map[string]string `yaml:"config"`
}

// Destination types for logging output.
const (
	Stdout destinationType = "stdout"
	File   destinationType = "file"
)

// Log levels define the severity of log messages.
const (
	DebugLevel level = "debug"
	InfoLevel  level = "info"
	WarnLevel  level = "warn"
	ErrorLevel level = "error"
	FatalLevel level = "fatal"
	PanicLevel level = "panic"
)

var logLevels = map[level]zerolog.Level{
	DebugLevel: zerolog.DebugLevel,
	InfoLevel:  zerolog.InfoLevel,
	WarnLevel:  zerolog.WarnLevel,
	ErrorLevel: zerolog.ErrorLevel,
	FatalLevel: zerolog.FatalLevel,
	PanicLevel: zerolog.PanicLevel,
}

// Config represents the configuration for logging.
type Config struct {
	Level        level              `yaml:"level"`
	Destinations []destination      `yaml:"destinations"`
	ContextKeys  []model.ContextKey `yaml:"contextKeys"`
}

var (
	logger zerolog.Logger
	cfg    Config
	once   sync.Once
)

// Logger instance and configuration.
var (
	ErrInvalidLogLevel   = errors.New("invalid log level")
	ErrLogDestinationNil = errors.New("log Destinations cant be empty")
	ErrMissingFilePath   = errors.New("file path missing in destination config for file logging")
)

func (config *Config) validate() error {
	if _, exists := logLevels[config.Level]; !exists {
		return ErrInvalidLogLevel
	}

	if len(config.Destinations) == 0 {
		return ErrLogDestinationNil
	}

	for _, dest := range config.Destinations {
		switch dest.Type {
		case Stdout:
		case File:
			if _, exists := dest.Config["path"]; !exists {
				return ErrMissingFilePath
			}

			for _, key := range []string{"maxSize", "maxBackups", "maxAge"} {
				if valStr, ok := dest.Config[key]; ok {
					if _, err := strconv.Atoi(valStr); err != nil {
						return fmt.Errorf("invalid %s: %w", key, err)
					}
				}
			}
		default:
			return fmt.Errorf("invalid destination type '%s'", dest.Type)
		}
	}
	return nil
}

var defaultConfig = Config{
	Level: InfoLevel,
	Destinations: []destination{
		{Type: Stdout},
	},
}

func init() {
	logger, _ = getLogger(defaultConfig)
}

func getLogger(config Config) (zerolog.Logger, error) {
	var newLogger zerolog.Logger
	var writers []io.Writer
	for _, dest := range config.Destinations {
		switch dest.Type {
		case Stdout:
			writers = append(writers, os.Stdout)
		case File:
			filePath := dest.Config["path"]
			dir := filepath.Dir(filePath)
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return newLogger, fmt.Errorf("failed to create log directory: %v", err)
			}
			lumberjackLogger := &lumberjack.Logger{
				Filename: filePath,
				Compress: false,
			}
			absPath, err := filepath.Abs(filePath)
			if err != nil {
				return newLogger, fmt.Errorf("failed to get absolute path: %v", err)
			}
			lumberjackLogger.Filename = absPath

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
	defer func() {
		if closer, ok := multiwriter.(io.Closer); ok {
			closer.Close()
		}
	}()
	newLogger = zerolog.New(multiwriter).
		Level(logLevels[config.Level]).
		With().
		Timestamp().
		Logger()

	cfg = config
	return newLogger, nil
}

// InitLogger initializes the logger with the given configuration.
// It ensures that the logger is initialized only once using sync.Once.
func InitLogger(c Config) error {
	var initErr error
	once.Do(func() {
		if initErr = c.validate(); initErr != nil {
			return
		}

		logger, initErr = getLogger(c)
	})
	return initErr
}

// Debug logs a debug-level message with the provided context.
func Debug(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.DebugLevel, msg, nil)
}

// Debugf logs a formatted debug-level message with the provided context.
func Debugf(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.DebugLevel, msg, nil)
}

// Info logs an info-level message with the provided context.
func Info(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.InfoLevel, msg, nil)
}

// Infof logs a formatted info-level message with the provided context.
func Infof(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.InfoLevel, msg, nil)
}

// Warn logs a warning-level message with the provided context.
func Warn(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.WarnLevel, msg, nil)
}

// Warnf logs a formatted warning-level message with the provided context.
func Warnf(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.WarnLevel, msg, nil)
}

// Error logs an error-level message along with an error object.
func Error(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.ErrorLevel, msg, err)
}

// Errorf logs a formatted error-level message along with an error object.
func Errorf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.ErrorLevel, msg, err)
}

// Fatal logs a fatal-level message along with an error object and exits the application.
func Fatal(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.FatalLevel, msg, err)
}

// Fatalf logs a formatted fatal-level message along with an error object and exits the application.
func Fatalf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.FatalLevel, msg, err)
}

// Panic logs a panic-level message along with an error object and panics.
func Panic(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.PanicLevel, msg, err)
}

// Panicf logs a formatted panic-level message along with an error object and panics.
func Panicf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.PanicLevel, msg, err)
}

// logEvent logs an event at the specified log level with an optional error message.
// It adds contextual information before logging the message.
func logEvent(ctx context.Context, level zerolog.Level, msg string, err error) {
	event := logger.WithLevel(level)

	if err != nil {
		event = event.Err(err)
	}
	addCtx(ctx, event)
	event.Msg(msg)
}

// Request logs details of an incoming HTTP request, including method, URL, body, and remote address.
func Request(ctx context.Context, r *http.Request, body []byte) {
	event := logger.Info()
	addCtx(ctx, event)
	event.Str("method", r.Method).
		Str("url", r.URL.String()).
		Str("body", string(body)).
		Str("remoteAddr", r.RemoteAddr).
		Msg("HTTP Request")
}

// addCtx adds context values to the log event based on configured context keys.
func addCtx(ctx context.Context, event *zerolog.Event) {
	for _, key := range cfg.ContextKeys {
		val, ok := ctx.Value(key).(string)
		if !ok {
			continue
		}
		keyStr := string(key)
		event.Any(keyStr, val)
	}
}

// Response logs details of an outgoing HTTP response, including method, URL, status code, and response time.
func Response(ctx context.Context, r *http.Request, statusCode int, responseTime time.Duration) {
	event := logger.Info()
	addCtx(ctx, event)
	event.Str("method", r.Method).
		Str("url", r.URL.String()).
		Int("statusCode", statusCode).
		Dur("responseTime", responseTime).
		Msg("HTTP Response")
}
