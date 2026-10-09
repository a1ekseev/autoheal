// Package monitor implements the polling loop that checks labelled containers
// and restarts them after repeated failures.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/a1ekseev/autoheal/internal/probe"
)

// Container labels understood by the monitor.
const (
	LabelEnable       = "autoheal.monitor.enable"
	LabelPing         = "autoheal.monitor.ping"
	LabelCurl         = "autoheal.monitor.curl"
	LabelCurlResponse = "autoheal.monitor.curl.response"
)

// Container is a running container as seen by the monitor.
type Container struct {
	ID     string
	Name   string
	Labels map[string]string
}

// DockerAPI is the subset of the Docker Engine API the monitor needs.
type DockerAPI interface {
	ListRunning(ctx context.Context) ([]Container, error)
	Restart(ctx context.Context, id string) error
}

// Pinger sends an ICMP echo and returns the round-trip time.
type Pinger interface {
	Ping(ctx context.Context, host string) (time.Duration, error)
}

// HTTPGetter performs an HTTP GET.
type HTTPGetter interface {
	Get(ctx context.Context, url string) (probe.HTTPResult, error)
}

// Options configures a Monitor.
type Options struct {
	FailThreshold int
	Interval      time.Duration
	Logger        *slog.Logger
}

// Monitor tracks consecutive failures per container ID.
type Monitor struct {
	docker   DockerAPI
	pinger   Pinger
	http     HTTPGetter
	opts     Options
	log      *slog.Logger
	failures map[string]int
}

// New creates a Monitor.
func New(docker DockerAPI, pinger Pinger, http HTTPGetter, opts Options) *Monitor {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Monitor{
		docker:   docker,
		pinger:   pinger,
		http:     http,
		opts:     opts,
		log:      log,
		failures: make(map[string]int),
	}
}

// Run executes a cycle immediately and then one cycle Interval after the
// previous one finished, until ctx is cancelled. It returns nil on cancellation.
func (m *Monitor) Run(ctx context.Context) error {
	for {
		m.RunOnce(ctx)
		timer := time.NewTimer(m.opts.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// RunOnce checks every enabled running container once, sequentially.
func (m *Monitor) RunOnce(ctx context.Context) {
	containers, err := m.docker.ListRunning(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.errorf(ctx, "[ERROR] Failed to list containers: %v", err)
		}
		return
	}

	seen := make(map[string]struct{}, len(containers))
	for _, c := range containers {
		if ctx.Err() != nil {
			return
		}
		seen[c.ID] = struct{}{}
		if strings.ToLower(c.Labels[LabelEnable]) != "true" {
			continue
		}
		m.check(ctx, c)
	}

	for id := range m.failures {
		if _, ok := seen[id]; !ok {
			delete(m.failures, id)
		}
	}
}

// result is the outcome of a single check.
type result int

const (
	passed result = iota
	failed
	// inconclusive: the monitor itself could not check (shutdown, missing
	// ICMP permission); the counter is neither incremented nor reset.
	inconclusive
)

func (m *Monitor) check(ctx context.Context, c Container) {
	m.infof(ctx, "Monitoring container: %s", c.Name)

	res := passed
	if host, ok := c.Labels[LabelPing]; ok {
		res = m.checkPing(ctx, c.Name, host)
	} else if url, ok := c.Labels[LabelCurl]; ok {
		res = m.checkCurl(ctx, c.Name, url, c.Labels[LabelCurlResponse])
	}

	switch res {
	case inconclusive:
		return
	case passed:
		m.failures[c.ID] = 0
		return
	case failed:
	}

	m.failures[c.ID]++
	if m.failures[c.ID] < m.opts.FailThreshold {
		return
	}
	m.errorf(ctx, "[RESTARTING] %s: Exceeded %d failures, restarting container", c.Name, m.opts.FailThreshold)
	if err := m.docker.Restart(ctx, c.ID); err != nil {
		m.errorf(ctx, "[ERROR] Failed to restart %s: %v", c.Name, err)
		return
	}
	m.failures[c.ID] = 0
}

func (m *Monitor) checkPing(ctx context.Context, name, host string) result {
	rtt, err := m.pinger.Ping(ctx, host)
	switch {
	case ctx.Err() != nil:
		return inconclusive
	case errors.Is(err, probe.ErrPingNotPermitted):
		m.errorf(ctx, "[ERROR] Cannot ping %s: %v", name, err)
		return inconclusive
	case errors.Is(err, probe.ErrNoReply):
		m.warnf(ctx, "[PING FAIL] %s: No response from %s", name, host)
		return failed
	case err != nil:
		m.warnf(ctx, "[PING FAIL] %s: %s unreachable — %v", name, host, err)
		return failed
	default:
		m.infof(ctx, "[PING OK] %s: %s responded in %.2f ms", name, host, float64(rtt)/float64(time.Millisecond))
		return passed
	}
}

func (m *Monitor) checkCurl(ctx context.Context, name, url, expected string) result {
	res, err := m.http.Get(ctx, url)
	switch {
	case ctx.Err() != nil:
		return inconclusive
	case err != nil:
		m.errorf(ctx, "[CURL ERROR] %s: Failed to request %s — %v", name, url, err)
		return failed
	}
	m.infof(ctx, "HTTP Request: GET %s %q", url, res.Proto+" "+res.Status)
	if !strings.Contains(res.Body, expected) {
		m.warnf(ctx, "[CURL FAIL] %s: Unexpected response from %s", name, url)
		return failed
	}
	m.infof(ctx, "[CURL OK] %s: Response matched", name)
	return passed
}

// The messages intentionally mirror the original Python monitor verbatim, so
// they are formatted rather than structured.
func (m *Monitor) infof(ctx context.Context, format string, args ...any) {
	m.log.InfoContext(ctx, fmt.Sprintf(format, args...))
}

func (m *Monitor) warnf(ctx context.Context, format string, args ...any) {
	m.log.WarnContext(ctx, fmt.Sprintf(format, args...))
}

func (m *Monitor) errorf(ctx context.Context, format string, args ...any) {
	m.log.ErrorContext(ctx, fmt.Sprintf(format, args...))
}
