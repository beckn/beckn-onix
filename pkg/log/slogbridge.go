package log

// slogbridge.go lets a dependency-free library that logs via the standard
// library's log/slog (e.g. pkg/catalog/store, pkg/catalog/publisher) show
// up in this package's own log stream, at whatever level this package is
// configured for (InitLogger's Config.Level) -- with no separate level to
// keep in sync. Handler.Enabled always returns true; the real gate is
// this package's own zerolog logger, applied inside Debug/Info/Warn/Error
// exactly as it is for every other pkg/log call site -- a Debug record
// forwarded here is just as much a no-op as a direct Debug(ctx, ...) call
// is whenever the configured level is above debug.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// slogHandler forwards every slog.Record to this package's own
// Debug/Info/Warn/Error, folding any attrs (including ones attached via
// WithAttrs) into the message as "key=value" pairs, since this package's
// own logging functions take a plain string, not structured fields. An
// attr keyed "error" whose value is an error is passed through as the
// real error argument to Error instead of being flattened into the
// message text.
type slogHandler struct{ attrs []slog.Attr }

// NewSlogHandler constructs the bridge handler described above. Use it
// to build an *slog.Logger (slog.New(log.NewSlogHandler())) for any
// library that accepts one.
func NewSlogHandler() slog.Handler { return &slogHandler{} }

func (h *slogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	msg := r.Message
	var errAttr error
	collect := func(a slog.Attr) bool {
		if a.Key == "error" {
			if e, ok := a.Value.Any().(error); ok {
				errAttr = e
				return true
			}
		}
		msg += fmt.Sprintf(" %s=%v", a.Key, a.Value.Any())
		return true
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(collect)

	switch {
	case r.Level >= slog.LevelError:
		if errAttr == nil {
			errAttr = errors.New(msg)
		}
		Error(ctx, errAttr, msg)
	case r.Level >= slog.LevelWarn:
		Warn(ctx, msg)
	case r.Level >= slog.LevelInfo:
		Info(ctx, msg)
	default:
		Debug(ctx, msg)
	}
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogHandler{attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

// WithGroup is unsupported (groups would need namespaced keys this
// package's plain-string logging has no room for) -- returns h unchanged
// rather than erroring, since slog.Logger.WithGroup callers expect a
// Handler back, not an error.
func (h *slogHandler) WithGroup(string) slog.Handler { return h }
