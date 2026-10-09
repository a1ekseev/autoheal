package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a1ekseev/autoheal/internal/logging"
)

var fixed = time.Date(2026, 10, 6, 13, 42, 1, 123_456_789, time.UTC)

func newLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	h := logging.NewHandler(buf, level)
	h.Now = func() time.Time { return fixed }
	return slog.New(h)
}

func TestHandlerFormat(t *testing.T) {
	tests := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "2026-10-06 13:42:01,123 [DEBUG] hello\n"},
		{slog.LevelInfo, "2026-10-06 13:42:01,123 [INFO] hello\n"},
		{slog.LevelWarn, "2026-10-06 13:42:01,123 [WARNING] hello\n"},
		{slog.LevelError, "2026-10-06 13:42:01,123 [ERROR] hello\n"},
		{logging.LevelCritical, "2026-10-06 13:42:01,123 [CRITICAL] hello\n"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			var buf bytes.Buffer
			newLogger(&buf, slog.LevelDebug).Log(context.Background(), tt.level, "hello")
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestHandlerAppendsAttrs(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, slog.LevelInfo).With(slog.String("a", "b")).Info("msg", slog.Int("n", 1))
	assert.Equal(t, "2026-10-06 13:42:01,123 [INFO] msg a=b n=1\n", buf.String())
}

func TestHandlerRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, slog.LevelWarn)
	l.Info("skipped")
	l.Warn("kept")
	assert.Equal(t, "2026-10-06 13:42:01,123 [WARNING] kept\n", buf.String())
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"DEBUG": slog.LevelDebug, "info": slog.LevelInfo, "Warning": slog.LevelWarn,
		"WARN": slog.LevelWarn, "ERROR": slog.LevelError, "CRITICAL": logging.LevelCritical,
		"FATAL": logging.LevelCritical, "NOTSET": slog.LevelDebug,
		"\tinfo\n": slog.LevelInfo, // surrounding whitespace is ignored
	}
	for in, want := range tests {
		got, err := logging.ParseLevel(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := logging.ParseLevel("BASIC_FORMAT")
	require.Error(t, err)
}

func TestLevelUnmarshalText(t *testing.T) {
	var l logging.Level
	require.NoError(t, l.UnmarshalText([]byte("warning")))
	assert.Equal(t, slog.LevelWarn, l.Level())

	require.Error(t, l.UnmarshalText([]byte("loud")))
}
