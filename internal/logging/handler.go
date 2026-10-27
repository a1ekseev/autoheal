// Package logging provides a slog.Handler that reproduces the line format of
// Python's logging.basicConfig(format="%(asctime)s [%(levelname)s] %(message)s").
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// LevelCritical mirrors Python's CRITICAL (and FATAL) level.
const LevelCritical = slog.Level(12)

const timeLayout = "2006-01-02 15:04:05,000"

// ErrUnknownLevel is returned by ParseLevel for an unsupported level name.
var ErrUnknownLevel = errors.New("unknown log level")

// ParseLevel converts a Python-style level name (case-insensitive) to a slog level.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "DEBUG", "NOTSET":
		return slog.LevelDebug, nil
	case "INFO":
		return slog.LevelInfo, nil
	case "WARN", "WARNING":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	case "CRITICAL", "FATAL":
		return LevelCritical, nil
	default:
		return 0, fmt.Errorf("%w %q (want DEBUG, INFO, WARNING, ERROR, CRITICAL or NOTSET)", ErrUnknownLevel, name)
	}
}

// Level is a slog.Leveler that parses Python-style level names, so it can be
// filled directly from an environment variable (encoding.TextUnmarshaler).
type Level slog.Level

// Level implements slog.Leveler.
func (l Level) Level() slog.Level { return slog.Level(l) }

// UnmarshalText implements encoding.TextUnmarshaler via ParseLevel.
func (l *Level) UnmarshalText(text []byte) error {
	v, err := ParseLevel(string(text))
	if err != nil {
		return err
	}
	*l = Level(v)
	return nil
}

func levelName(l slog.Level) string {
	switch {
	case l >= LevelCritical:
		return "CRITICAL"
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// Handler writes "<local time> [LEVEL] message key=value..." lines.
type Handler struct {
	// Now returns the timestamp for a record; overridable in tests.
	Now   func() time.Time
	mu    *sync.Mutex
	w     io.Writer
	level slog.Leveler
	attrs []slog.Attr
	group string
}

// NewHandler returns a Handler writing records at or above level to w.
func NewHandler(w io.Writer, level slog.Leveler) *Handler {
	return &Handler{Now: time.Now, mu: &sync.Mutex{}, w: w, level: level}
}

// New returns a logger backed by a Handler.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(NewHandler(w, level))
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// Handle implements slog.Handler.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(h.Now().Format(timeLayout))
	b.WriteString(" [")
	b.WriteString(levelName(r.Level))
	b.WriteString("] ")
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		writeAttr(&b, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, h.group, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	if err != nil {
		return fmt.Errorf("write log record: %w", err)
	}
	return nil
}

func writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, ga := range a.Value.Group() {
			writeAttr(b, key, ga)
		}
		return
	}
	b.WriteByte(' ')
	b.WriteString(key)
	b.WriteByte('=')
	fmt.Fprintf(b, "%v", a.Value.Any())
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	nh.attrs = append(nh.attrs, h.attrs...)
	for _, a := range attrs {
		if h.group != "" {
			a.Key = h.group + "." + a.Key
		}
		nh.attrs = append(nh.attrs, a)
	}
	return &nh
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := *h
	if nh.group != "" {
		nh.group += "." + name
	} else {
		nh.group = name
	}
	return &nh
}
