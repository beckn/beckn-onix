package catalogcrawler

import "log/slog"

// NewSlogLogger adapts a *slog.Logger to the engine's Logger interface, so
// drivers get structured JSON events without the module importing a logger
// globally. Passing nil uses slog.Default().
func NewSlogLogger(l *slog.Logger) Logger {
	if l == nil {
		l = slog.Default()
	}
	return slogLogger{l: l}
}

type slogLogger struct{ l *slog.Logger }

func (s slogLogger) Info(event string, kv ...any)  { s.l.Info(event, kv...) }
func (s slogLogger) Warn(event string, kv ...any)  { s.l.Warn(event, kv...) }
func (s slogLogger) Error(event string, kv ...any) { s.l.Error(event, kv...) }
