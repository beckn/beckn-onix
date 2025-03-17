package logpackage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/yaml.v2"
)

type LoggerConfig struct {
	Level       string   `yaml:"level"`
	FilePath    string   `yaml:"file_path"`
	MaxSize     int      `yaml:"max_size"`
	MaxBackups  int      `yaml:"max_backups"`
	MaxAge      int      `yaml:"max_age"`
	ContextKeys []string `yaml:"context_keys"`
}

var (
	logger zerolog.Logger
	cfg    LoggerConfig
	once   sync.Once

	getConfigPath = func() (string, error) {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			return "", fmt.Errorf("failed to get runtime caller")
		}
		dir := filepath.Dir(file)
		return filepath.Join(dir, "log.yaml"), nil
	}
)

func loadConfig() (LoggerConfig, error) {
	var config LoggerConfig

	configPath, err := getConfigPath()
	if err != nil {
		return config, fmt.Errorf("error finding config path: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config, fmt.Errorf("failed to read config file: %w", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return config, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return config, nil
}

func InitLogger(configs ...LoggerConfig) {
	once.Do(func() {
		var err error

		if len(configs) > 0 {
			cfg = configs[0]
		} else {
			cfg, err = loadConfig()
			if err != nil {
				fmt.Println("Logger initialization failed:", err)
				return
			}
		}

		level, err := zerolog.ParseLevel(cfg.Level)
		if err != nil {
			level = zerolog.InfoLevel
		}

		zerolog.SetGlobalLevel(level)
		zerolog.TimeFieldFormat = time.RFC3339

		logWriter := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
		fileWriter := &lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
		}

		multi := zerolog.MultiLevelWriter(logWriter, fileWriter)
		logger = zerolog.New(multi).With().Timestamp().Logger()
	})
}

func Debug(ctx context.Context, msg string) {
	logEvent(ctx, logger.Debug(), msg)
}

func Debugf(ctx context.Context, format string, v ...any) {
	logEvent(ctx, logger.Debug(), fmt.Sprintf(format, v...))
}

func Info(ctx context.Context, msg string) {
	logEvent(ctx, logger.Info(), msg)
}

func Infof(ctx context.Context, format string, v ...any) {
	logEvent(ctx, logger.Info(), fmt.Sprintf(format, v...))
}

func Warn(ctx context.Context, msg string) {
	logEvent(ctx, logger.Warn(), msg)
}

func Warnf(ctx context.Context, format string, v ...any) {
	logEvent(ctx, logger.Warn(), fmt.Sprintf(format, v...))
}

func Error(ctx context.Context, err error, msg string) {
	logEvent(ctx, logger.Error().Err(err), msg)
}

func Errorf(ctx context.Context, err error, format string, v ...any) {
	logEvent(ctx, logger.Error().Err(err), fmt.Sprintf(format, v...))
}

var ExitFunc = func(code int) {
	os.Exit(code)
}

func Fatal(ctx context.Context, msg string) {
	logEvent(ctx, logger.Error(), msg)
	ExitFunc(1)
}

func Fatalf(ctx context.Context, format string, v ...any) {
	logEvent(ctx, logger.Fatal(), fmt.Sprintf(format, v...))
	ExitFunc(1)
}

func logEvent(ctx context.Context, event *zerolog.Event, msg string) {
	for _, key := range cfg.ContextKeys {
		if val, ok := ctx.Value(key).(string); ok && val != "" {
			event.Str(key, val)
		}
	}
	event.Msg(msg)
}
