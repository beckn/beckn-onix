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

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Level string
type DestinationType string
type Destination struct {
	Type   DestinationType   `yaml:"type"`
	Config map[string]string `yaml:"config"`
}

const (
	Stdout DestinationType = "stdout"
	File   DestinationType = "file"
)

const (
	DebugLevel Level = "debug"
	InfoLevel  Level = "info"
	WarnLevel  Level = "warn"
	ErrorLevel Level = "error"
	FatalLevel Level = "fatal"
	PanicLevel Level = "panic"
)

var logLevels = map[Level]zerolog.Level{
	DebugLevel: zerolog.DebugLevel,
	InfoLevel:  zerolog.InfoLevel,
	WarnLevel:  zerolog.WarnLevel,
	ErrorLevel: zerolog.ErrorLevel,
	FatalLevel: zerolog.FatalLevel,
	PanicLevel: zerolog.PanicLevel,
}

type Config struct {
	level        Level         `yaml:"level"`
	destinations []Destination `yaml:"destinations"`
	contextKeys  []any         `yaml:"contextKeys"`
}

var (
	logger zerolog.Logger
	cfg    Config
	once   sync.Once
)

var (
	ErrInvalidLogLevel   = errors.New("invalid log level")
	ErrLogDestinationNil = errors.New("log Destinations cant be empty")
	ErrMissingFilePath   = errors.New("file path missing in destination config for file logging")
)

func (config *Config) validate() error {
	if _, exists := logLevels[config.level]; !exists {
		return ErrInvalidLogLevel
	}

	if len(config.destinations) == 0 {
		return ErrLogDestinationNil
	}

	for _, dest := range config.destinations {
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
	level: InfoLevel,
	destinations: []Destination{
		{Type: Stdout},
	},
	contextKeys: []any{"userID", "requestID"},
}

func init() {
	logger, _ = getLogger(defaultConfig)
}

func getLogger(config Config) (zerolog.Logger, error) {
	var newLogger zerolog.Logger
	var writers []io.Writer
	for _, dest := range config.destinations {
		switch dest.Type {
		case Stdout:
			writers = append(writers, os.Stdout)
		case File:
			filePath := dest.Config["path"]
			dir := filepath.Dir(filePath)
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return newLogger, fmt.Errorf("failed to create log directory: %v", err)
			}

			fmt.Printf("writing test log to file: %v\n", config)
			lumberjackLogger := &lumberjack.Logger{
				Filename:   filePath,
				MaxSize:    500, // Default size in MB if not overridden
				MaxBackups: 15,  // Number of backups
				MaxAge:     30,  // Days to retain
				Compress:   false,
			}
			absPath, err := filepath.Abs(filePath)
			if err != nil {
				return newLogger, fmt.Errorf("failed to get absolute path: %v", err)
			}
			fmt.Printf("Attempting to write logs to: %s\n", absPath)
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
		Level(logLevels[config.level]).
		With().
		Timestamp().
		Caller().
		Logger()

	cfg = config
	return newLogger, nil
}
func InitLogger(c Config) error {

	if err := c.validate(); err != nil {
		return err
	}

	var initErr error
	// once.Do(func() {

	logger, initErr = getLogger(c)
	// })
	return initErr
}
func Debug(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.DebugLevel, msg, nil)
}

func Debugf(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.DebugLevel, msg, nil)
}

func Info(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.InfoLevel, msg, nil)
}

func Infof(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.InfoLevel, msg, nil)
}

func Warn(ctx context.Context, msg string) {
	logEvent(ctx, zerolog.WarnLevel, msg, nil)
}

func Warnf(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.WarnLevel, msg, nil)
}

func Error(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.ErrorLevel, msg, err)
}

func Errorf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.ErrorLevel, msg, err)
}

func Fatal(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.FatalLevel, msg, err)
}

func Fatalf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.FatalLevel, msg, err)
}

func Panic(ctx context.Context, err error, msg string) {
	logEvent(ctx, zerolog.PanicLevel, msg, err)
}

func Panicf(ctx context.Context, err error, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	logEvent(ctx, zerolog.PanicLevel, msg, err)
}

func logEvent(ctx context.Context, level zerolog.Level, msg string, err error) {
	event := logger.WithLevel(level)

	if err != nil {
		event = event.Err(err)
	}
	// fmt.Print("=======>", event, ctx)
	addCtx(ctx, event)
	event.Msg(msg)
}
func Request(ctx context.Context, r *http.Request, body []byte) {
	event := logger.Info()
	addCtx(ctx, event)
	event.Str("method", r.Method).
		Str("url", r.URL.String()).
		Str("body", string(body)).
		Str("remoteAddr", r.RemoteAddr).
		Msg("HTTP Request")
}

func addCtx(ctx context.Context, event *zerolog.Event) {
	for _, key := range cfg.contextKeys {
		val, ok := ctx.Value(key).(string)
		if !ok {
			continue
		}
		keyStr := key.(string)
		event.Str(keyStr, val)
	}
}

func Response(ctx context.Context, r *http.Request, statusCode int, responseTime time.Duration) {
	event := logger.Info()
	addCtx(ctx, event)
	event.Str("method", r.Method).
		Str("url", r.URL.String()).
		Int("statusCode", statusCode).
		Dur("responseTime", responseTime).
		Msg("HTTP Response")
}
