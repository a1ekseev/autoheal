package monitor_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a1ekseev/autoheal/internal/monitor"
	"github.com/a1ekseev/autoheal/internal/probe"
)

type fakeDocker struct {
	mu         sync.Mutex
	containers []monitor.Container
	listErr    error
	restartErr error
	restarts   []string
}

func (f *fakeDocker) ListRunning(context.Context) ([]monitor.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.containers, f.listErr
}

func (f *fakeDocker) Restart(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts = append(f.restarts, id)
	return f.restartErr
}

func (f *fakeDocker) restartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.restarts)
}

type fakePinger struct {
	rtt   time.Duration
	err   error
	hosts []string
}

func (f *fakePinger) Ping(_ context.Context, host string) (time.Duration, error) {
	f.hosts = append(f.hosts, host)
	return f.rtt, f.err
}

type fakeHTTP struct {
	mu    sync.Mutex
	res   probe.HTTPResult
	err   error
	urls  []string
	onGet func()
}

func (f *fakeHTTP) Get(_ context.Context, url string) (probe.HTTPResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, url)
	if f.onGet != nil {
		f.onGet()
	}
	return f.res, f.err
}

func (f *fakeHTTP) urlsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

type env struct {
	docker *fakeDocker
	pinger *fakePinger
	http   *fakeHTTP
	logs   *bytes.Buffer
	mon    *monitor.Monitor
}

func newEnv(t *testing.T, threshold int, containers ...monitor.Container) *env {
	t.Helper()
	e := &env{
		docker: &fakeDocker{containers: containers},
		pinger: &fakePinger{rtt: 1500 * time.Microsecond},
		http:   &fakeHTTP{res: probe.HTTPResult{Proto: "HTTP/1.1", Status: "200 OK", Body: "all OK"}},
		logs:   &bytes.Buffer{},
	}
	logger := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	e.mon = monitor.New(e.docker, e.pinger, e.http, monitor.Options{
		FailThreshold: threshold,
		Interval:      time.Millisecond,
		Logger:        logger,
	})
	return e
}

func (e *env) cycles(t *testing.T, n int) {
	t.Helper()
	for range n {
		e.mon.RunOnce(t.Context())
	}
}

// curl returns an enabled container "web" checked over HTTP.
func curl(url, expect string) monitor.Container {
	labels := map[string]string{monitor.LabelEnable: "true", monitor.LabelCurl: url}
	if expect != "" {
		labels[monitor.LabelCurlResponse] = expect
	}
	return monitor.Container{ID: "web", Name: "web", Labels: labels}
}

func ping(id, host string) monitor.Container {
	return monitor.Container{ID: id, Name: id, Labels: map[string]string{
		monitor.LabelEnable: "TRUE", monitor.LabelPing: host,
	}}
}

func TestRestartAfterThresholdFailures(t *testing.T) {
	e := newEnv(t, 3, curl("http://web/health", "OK"))
	e.http.res.Body = "DOWN"

	e.cycles(t, 2)
	assert.Empty(t, e.docker.restarts)

	e.cycles(t, 1)
	assert.Equal(t, []string{"web"}, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[RESTARTING] web: Exceeded 3 failures, restarting container")
	assert.Contains(t, e.logs.String(), "[CURL FAIL] web: Unexpected response from http://web/health")

	// Counter is reset after a successful restart.
	e.cycles(t, 2)
	assert.Len(t, e.docker.restarts, 1)
	e.cycles(t, 1)
	assert.Len(t, e.docker.restarts, 2)
}

func TestSuccessResetsCounter(t *testing.T) {
	e := newEnv(t, 2, curl("http://web", "OK"))

	e.http.res.Body = "nope"
	e.cycles(t, 1)
	e.http.res.Body = "OK"
	e.cycles(t, 1)
	e.http.res.Body = "nope"
	e.cycles(t, 1)

	assert.Empty(t, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[CURL OK] web: Response matched")
}

func TestFailedRestartKeepsCounterAndRetries(t *testing.T) {
	e := newEnv(t, 2, curl("http://web", "OK"))
	e.http.res.Body = "nope"
	e.docker.restartErr = errors.New("boom")

	e.cycles(t, 3)

	assert.Equal(t, []string{"web", "web"}, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[ERROR] Failed to restart web: boom")
}

func TestCurlErrorCountsAsFailure(t *testing.T) {
	e := newEnv(t, 1, curl("http://web", ""))
	e.http.err = errors.New("connection refused")

	e.cycles(t, 1)

	assert.Equal(t, []string{"web"}, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[CURL ERROR] web: Failed to request http://web — connection refused")
}

func TestCurlEmptyExpectationAcceptsAnyResponse(t *testing.T) {
	e := newEnv(t, 1, curl("http://web", ""))
	e.http.res = probe.HTTPResult{Proto: "HTTP/1.1", Status: "500 Internal Server Error", Body: ""}

	e.cycles(t, 1)

	assert.Empty(t, e.docker.restarts)
	assert.Contains(t, e.logs.String(), `HTTP Request: GET http://web \"HTTP/1.1 500 Internal Server Error\"`)
}

func TestPingOK(t *testing.T) {
	e := newEnv(t, 1, ping("db", "10.0.0.5"))

	e.cycles(t, 1)

	assert.Empty(t, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[PING OK] db: 10.0.0.5 responded in 1.50 ms")
}

func TestPingNoReplyFails(t *testing.T) {
	e := newEnv(t, 1, ping("db", "10.0.0.5"))
	e.pinger.err = probe.ErrNoReply

	e.cycles(t, 1)

	assert.Equal(t, []string{"db"}, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[PING FAIL] db: No response from 10.0.0.5")
}

func TestPingErrorFails(t *testing.T) {
	// Deliberate fix: Python's ping3 returned False here and it was logged as OK.
	e := newEnv(t, 1, ping("db", "nowhere.invalid"))
	e.pinger.err = errors.New("no such host")

	e.cycles(t, 1)

	assert.Equal(t, []string{"db"}, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[PING FAIL] db: nowhere.invalid unreachable — no such host")
}

func TestPingNotPermittedIsAMonitorFaultNotATargetFailure(t *testing.T) {
	// Without CAP_NET_RAW / ping_group_range the monitor cannot ping at all;
	// that must never restart healthy targets.
	e := newEnv(t, 1, ping("db", "10.0.0.5"))
	e.pinger.err = fmt.Errorf("%w: socket: operation not permitted", probe.ErrPingNotPermitted)

	e.cycles(t, 3)

	assert.Empty(t, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "[ERROR] Cannot ping db: ICMP sockets not permitted")
	assert.NotContains(t, e.logs.String(), "[PING FAIL]")
}

func TestPingNotPermittedNeitherCountsNorResets(t *testing.T) {
	e := newEnv(t, 3, ping("db", "10.0.0.5"))
	for _, err := range []error{probe.ErrNoReply, probe.ErrPingNotPermitted, probe.ErrNoReply} {
		e.pinger.err = err
		e.cycles(t, 1)
	}
	assert.Empty(t, e.docker.restarts, "inconclusive check must not count as a failure")

	e.pinger.err = probe.ErrNoReply
	e.cycles(t, 1)
	assert.Equal(t, []string{"db"}, e.docker.restarts, "inconclusive check must not reset the counter")
}

func TestCheckInterruptedByShutdownIsNotCounted(t *testing.T) {
	e := newEnv(t, 1, curl("http://web", "OK"))
	ctx, cancel := context.WithCancel(t.Context())
	e.http.onGet = func() { cancel() }
	e.http.err = context.Canceled

	e.mon.RunOnce(ctx)

	assert.Empty(t, e.docker.restarts)
	assert.NotContains(t, e.logs.String(), "[CURL ERROR]")
	assert.NotContains(t, e.logs.String(), "[RESTARTING]")
}

func TestPingTakesPriorityOverCurl(t *testing.T) {
	c := ping("both", "10.0.0.5")
	c.Labels[monitor.LabelCurl] = "http://both"
	e := newEnv(t, 1, c)

	e.cycles(t, 1)

	assert.Equal(t, []string{"10.0.0.5"}, e.pinger.hosts)
	assert.Empty(t, e.http.urls)
}

func TestEnableLabelSemantics(t *testing.T) {
	tests := []struct {
		value     string
		monitored bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{" true", false},
		{"1", false},
		{"yes", false},
		{"false", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			c := curl("http://web", "OK")
			c.Labels[monitor.LabelEnable] = tt.value
			e := newEnv(t, 1, c)
			e.http.res.Body = "bad"

			e.cycles(t, 1)

			assert.Equal(t, tt.monitored, e.docker.restartCount() == 1)
		})
	}

	e := newEnv(t, 1, monitor.Container{ID: "x", Name: "x", Labels: map[string]string{monitor.LabelCurl: "http://x"}})
	e.cycles(t, 1)
	assert.Empty(t, e.http.urls, "container without enable label must be ignored")
}

func TestEnabledWithoutCheckIsNeverRestarted(t *testing.T) {
	e := newEnv(t, 1, monitor.Container{ID: "c", Name: "c", Labels: map[string]string{monitor.LabelEnable: "true"}})

	e.cycles(t, 5)

	assert.Empty(t, e.docker.restarts)
	assert.Contains(t, e.logs.String(), "Monitoring container: c")
}

func TestListErrorIsLoggedAndCycleContinues(t *testing.T) {
	e := newEnv(t, 1)
	e.docker.listErr = errors.New("daemon down")

	e.cycles(t, 1)

	assert.Contains(t, e.logs.String(), "[ERROR] Failed to list containers: daemon down")
}

func TestCountersOfVanishedContainersArePruned(t *testing.T) {
	e := newEnv(t, 2, curl("http://web", "OK"))
	e.http.res.Body = "bad"
	e.cycles(t, 1)

	// Container disappears, then comes back (same ID) - the old failure is forgotten.
	saved := e.docker.containers
	e.docker.containers = nil
	e.cycles(t, 1)
	e.docker.containers = saved
	e.cycles(t, 1)

	assert.Empty(t, e.docker.restarts)
}

func TestRunStopsOnContextCancel(t *testing.T) {
	e := newEnv(t, 1, curl("http://web", "OK"))
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() { done <- e.mon.Run(ctx) }()

	require.Eventually(t, func() bool { return e.docker.restartCount() == 0 && len(e.http.urlsSnapshot()) >= 2 }, time.Second, time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestLogsMonitoringLine(t *testing.T) {
	e := newEnv(t, 1, curl("http://web", "OK"))
	e.cycles(t, 1)
	assert.Equal(t, 1, strings.Count(e.logs.String(), "Monitoring container: web"))
}
