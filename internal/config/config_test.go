package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a1ekseev/autoheal/internal/config"
	"github.com/a1ekseev/autoheal/internal/logging"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(map[string]string{})
	require.NoError(t, err)

	assert.Equal(t, 10*time.Second, cfg.CheckInterval())
	assert.Equal(t, 3, cfg.FailThreshold)
	assert.Equal(t, 10*time.Second, cfg.PingTimeout())
	assert.Equal(t, 10*time.Second, cfg.CurlTimeout())
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel.Level())
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(map[string]string{
		"CHECK_INTERVAL": "5",
		"FAIL_THRESHOLD": "1",
		"PING_TIMEOUT":   "0.5",
		"CURL_TIMEOUT":   "2.25",
		"LOG_LEVEL":      "critical",
	})
	require.NoError(t, err)

	assert.Equal(t, 5*time.Second, cfg.CheckInterval())
	assert.Equal(t, 1, cfg.FailThreshold)
	assert.Equal(t, 500*time.Millisecond, cfg.PingTimeout())
	assert.Equal(t, 2250*time.Millisecond, cfg.CurlTimeout())
	assert.Equal(t, logging.LevelCritical, cfg.LogLevel.Level())
}

func TestLoadLogLevelAliases(t *testing.T) {
	for name, want := range map[string]slog.Level{
		"DEBUG": slog.LevelDebug, "info": slog.LevelInfo, "Warning": slog.LevelWarn, "WARN": slog.LevelWarn,
		"ERROR": slog.LevelError, "FATAL": logging.LevelCritical, "NOTSET": slog.LevelDebug, "\twarn\n": slog.LevelWarn,
	} {
		cfg, err := config.Load(map[string]string{"LOG_LEVEL": name})
		require.NoError(t, err, name)
		assert.Equal(t, want, cfg.LogLevel.Level(), name)
	}
}

func TestLoadEmptyValueUsesDefault(t *testing.T) {
	cfg, err := config.Load(map[string]string{"CHECK_INTERVAL": "", "LOG_LEVEL": ""})
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, cfg.CheckInterval())
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel.Level())
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"interval not int", map[string]string{"CHECK_INTERVAL": "10.5"}, "CHECK_INTERVAL"},
		{"interval zero", map[string]string{"CHECK_INTERVAL": "0"}, "CHECK_INTERVAL must be 1 or greater"},
		{"interval negative", map[string]string{"CHECK_INTERVAL": "-1"}, "CHECK_INTERVAL must be 1 or greater"},
		{"threshold zero", map[string]string{"FAIL_THRESHOLD": "0"}, "FAIL_THRESHOLD must be 1 or greater"},
		{"threshold text", map[string]string{"FAIL_THRESHOLD": "three"}, "FAIL_THRESHOLD"},
		{"ping timeout zero", map[string]string{"PING_TIMEOUT": "0"}, "PING_TIMEOUT must be greater than 0"},
		{"ping timeout nan", map[string]string{"PING_TIMEOUT": "nan"}, "PING_TIMEOUT"},
		{"ping timeout inf", map[string]string{"PING_TIMEOUT": "inf"}, "PING_TIMEOUT"},
		{"curl timeout negative", map[string]string{"CURL_TIMEOUT": "-3"}, "CURL_TIMEOUT must be greater than 0"},
		{"curl timeout too large", map[string]string{"CURL_TIMEOUT": "1e12"}, "CURL_TIMEOUT must be 86,400 or less"},
		{"interval too large", map[string]string{"CHECK_INTERVAL": "100000"}, "CHECK_INTERVAL must be 86,400 or less"},
		{"log level unknown", map[string]string{"LOG_LEVEL": "VERBOSE"}, `LOG_LEVEL must be a valid logging.Level: unknown log level "VERBOSE"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(tt.env)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := config.Load(map[string]string{"CHECK_INTERVAL": "0", "FAIL_THRESHOLD": "0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CHECK_INTERVAL")
	assert.Contains(t, err.Error(), "FAIL_THRESHOLD")
}

func TestLoadReportsParseAndRangeErrorsTogether(t *testing.T) {
	_, err := config.Load(map[string]string{"CHECK_INTERVAL": "abc", "FAIL_THRESHOLD": "0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CHECK_INTERVAL")
	assert.Contains(t, err.Error(), "FAIL_THRESHOLD must be 1 or greater")
	assert.NotContains(t, err.Error(), "CHECK_INTERVAL must be 1 or greater", "unparsable field must not also fail range checks")
}
