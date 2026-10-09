// Command autoheal monitors labelled Docker containers with ping/HTTP checks
// and restarts them after repeated failures.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/caarlos0/env/v11"

	"github.com/a1ekseev/autoheal/internal/config"
	"github.com/a1ekseev/autoheal/internal/docker"
	"github.com/a1ekseev/autoheal/internal/logging"
	"github.com/a1ekseev/autoheal/internal/monitor"
	"github.com/a1ekseev/autoheal/internal/probe"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, env.ToMap(os.Environ()), os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, environ map[string]string, stderr io.Writer) int {
	cfg, err := config.Load(environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	log := logging.New(stderr, cfg.LogLevel)

	dc, err := docker.New()
	if err != nil {
		log.ErrorContext(ctx, err.Error())
		return 1
	}
	defer func() { _ = dc.Close() }()

	log.DebugContext(ctx, "autoheal build", slog.String("version", version))
	log.InfoContext(ctx, StartupMessage(cfg))

	m := monitor.New(dc, probe.NewICMP(cfg.PingTimeout()), probe.NewHTTP(cfg.CurlTimeout()), monitor.Options{
		FailThreshold: cfg.FailThreshold,
		Interval:      cfg.CheckInterval(),
		Logger:        log,
	})
	if err := m.Run(ctx); err != nil {
		log.ErrorContext(ctx, err.Error())
		return 1
	}
	log.InfoContext(ctx, "Shutting down")
	return 0
}

// StartupMessage reproduces the Python startup line, including float
// formatting of the timeouts (e.g. "10.0s").
func StartupMessage(cfg config.Config) string {
	return fmt.Sprintf("Starting monitor with CHECK_INTERVAL=%ds, FAIL_THRESHOLD=%d, PING_TIMEOUT=%ss, CURL_TIMEOUT=%ss",
		cfg.CheckIntervalSeconds, cfg.FailThreshold, pyFloat(cfg.PingTimeoutSeconds), pyFloat(cfg.CurlTimeoutSeconds))
}

func pyFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
