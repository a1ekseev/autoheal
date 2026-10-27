package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a1ekseev/autoheal/internal/config"
)

func TestStartupMessageMatchesPython(t *testing.T) {
	cfg, err := config.Load(map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "Starting monitor with CHECK_INTERVAL=10s, FAIL_THRESHOLD=3, PING_TIMEOUT=10.0s, CURL_TIMEOUT=10.0s", StartupMessage(cfg))

	cfg, err = config.Load(map[string]string{"PING_TIMEOUT": "0.5", "CURL_TIMEOUT": "2.25"})
	require.NoError(t, err)
	assert.Contains(t, StartupMessage(cfg), "PING_TIMEOUT=0.5s, CURL_TIMEOUT=2.25s")
}

func TestRunInvalidConfigExitsWithError(t *testing.T) {
	var stderr bytes.Buffer
	code := run(t.Context(), map[string]string{"FAIL_THRESHOLD": "0"}, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "FAIL_THRESHOLD")
}
