package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// HumanHandler implements slog.Handler with the format:
// 2006-01-02 15:04:05.000 [LEVEL] message key=value key=value
type HumanHandler struct {
	level slog.Leveler
	w     io.Writer
	mu    sync.Mutex
	attrs []slog.Attr
}

// NewHumanHandler creates a handler that writes human-readable logs to w.
func NewHumanHandler(w io.Writer, level slog.Leveler) *HumanHandler {
	return &HumanHandler{level: level, w: w}
}

func (h *HumanHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *HumanHandler) Handle(_ context.Context, r slog.Record) error {
	var buf []byte

	// Timestamp
	buf = append(buf, r.Time.Format("2006-01-02 15:04:05.000")...)

	// Level
	buf = append(buf, " ["...)
	switch {
	case r.Level >= slog.LevelError:
		buf = append(buf, "ERROR"...)
	case r.Level >= slog.LevelWarn:
		buf = append(buf, "WARN"...)
	case r.Level >= slog.LevelInfo:
		buf = append(buf, "INFO"...)
	default:
		buf = append(buf, "DEBUG"...)
	}
	buf = append(buf, ']')

	// Message
	buf = append(buf, "  "...)
	buf = append(buf, r.Message...)

	// Pre-set attrs
	for _, a := range h.attrs {
		buf = appendAttr(buf, a)
	}

	// Record attrs
	r.Attrs(func(a slog.Attr) bool {
		buf = appendAttr(buf, a)
		return true
	})

	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf)
	return err
}

func (h *HumanHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &HumanHandler{level: h.level, w: h.w, attrs: newAttrs}
}

func (h *HumanHandler) WithGroup(_ string) slog.Handler {
	return h // groups not needed for this use case
}

func appendAttr(buf []byte, a slog.Attr) []byte {
	if a.Equal(slog.Attr{}) {
		return buf
	}
	buf = append(buf, ' ')
	buf = append(buf, a.Key...)
	buf = append(buf, '=')

	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindTime:
		buf = append(buf, v.Time().Format(time.RFC3339Nano)...)
	case slog.KindDuration:
		buf = append(buf, v.Duration().String()...)
	case slog.KindString:
		s := v.String()
		if needsQuoting(s) {
			buf = append(buf, fmt.Sprintf("%q", s)...)
		} else {
			buf = append(buf, s...)
		}
	default:
		buf = append(buf, v.String()...)
	}
	return buf
}

func needsQuoting(s string) bool {
	for _, c := range s {
		if c == ' ' || c == '"' || c == '=' || c == '\n' || c == '\r' || c == '\t' {
			return true
		}
	}
	return false
}

// ParseLevel converts a string to slog.Level.
func ParseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
