//go:build integration

// Package integration runs real autoheal containers in docker compose
// against real target containers on a real Docker daemon.
//
// Environment:
//
//	AUTOHEAL_IT_BUILD=0  reuse prebuilt autoheal:it / autoheal-testtarget:it images
//	AUTOHEAL_IT_KEEP=1   leave the compose stack running after the tests
package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	project    = "autoheal-it"
	logLayout  = "2006-01-02 15:04:05,000"
	waitLong   = 90 * time.Second
	pollPeriod = 250 * time.Millisecond
)

var composeFile = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "docker-compose.test.yml")
}()

var (
	// Services the main monitor (FAIL_THRESHOLD=3) must restart.
	restartable = []string{"curl-bad", "curl-refused", "curl-redirect", "curl-slow", "ping-unknown", "ping-noreply"}
	// Services nobody may restart.
	stable = []string{
		"curl-ok", "curl-500-match", "curl-any", "curl-flap", "ping-ok", "ping-wins", "no-check",
		"enable-false", "enable-space", "enable-missing",
	}
)

// docker runs the docker CLI and returns its combined output.
func docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func compose(ctx context.Context, args ...string) (string, error) {
	return docker(ctx, append([]string{"compose", "-f", composeFile}, args...)...)
}

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	_, _ = compose(ctx, "--profile", "*", "down", "-v", "--remove-orphans")
	if os.Getenv("AUTOHEAL_IT_BUILD") != "0" {
		if out, err := compose(ctx, "--profile", "*", "build"); err != nil {
			fmt.Fprintf(os.Stderr, "compose build failed: %v\n%s", err, out)
			return 1
		}
	}
	// Default profile = targets only; monitors are started by the tests.
	if out, err := compose(ctx, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120"); err != nil {
		fmt.Fprintf(os.Stderr, "compose up failed: %v\n%s", err, out)
		return 1
	}

	code := m.Run()

	if os.Getenv("AUTOHEAL_IT_KEEP") != "1" {
		_, _ = compose(ctx, "--profile", "*", "down", "-v", "--remove-orphans", "-t", "1")
	}
	return code
}

type stack struct {
	t   *testing.T
	api *client.Client
}

func newStack(t *testing.T) *stack {
	t.Helper()
	api, err := client.New(client.FromEnv)
	require.NoError(t, err)
	t.Cleanup(func() { _ = api.Close() })
	return &stack{t: t, api: api}
}

func (s *stack) containerID(service string) string {
	s.t.Helper()
	res, err := s.api.ContainerList(s.t.Context(), client.ContainerListOptions{
		All: true,
		Filters: make(client.Filters).
			Add("label", "com.docker.compose.project="+project).
			Add("label", "com.docker.compose.service="+service),
	})
	require.NoError(s.t, err)
	require.Len(s.t, res.Items, 1, "service %s", service)
	return res.Items[0].ID
}

func (s *stack) inspect(id string) (startedAt, status string, exitCode int) {
	s.t.Helper()
	res, err := s.api.ContainerInspect(s.t.Context(), id, client.ContainerInspectOptions{})
	require.NoError(s.t, err)
	st := res.Container.State
	require.NotNil(s.t, st)
	return st.StartedAt, string(st.Status), st.ExitCode
}

func (s *stack) startedAt(service string) string {
	s.t.Helper()
	started, _, _ := s.inspect(s.containerID(service))
	return started
}

func (s *stack) snapshot(services []string) map[string]string {
	s.t.Helper()
	out := make(map[string]string, len(services))
	for _, svc := range services {
		out[svc] = s.startedAt(svc)
	}
	return out
}

func (s *stack) assertNotRestarted(before map[string]string) {
	s.t.Helper()
	for svc, started := range before {
		assert.Equal(s.t, started, s.startedAt(svc), "%s must not be restarted", svc)
	}
}

// monitor is an autoheal compose service (plus its helpers) started for the
// duration of a test.
type monitor struct {
	t       *testing.T
	profile string
	service string
}

// startMonitor starts service and its helper services of profile; on cleanup
// exactly those services are removed, the targets stay.
func startMonitor(t *testing.T, profile, service string, helpers ...string) *monitor {
	t.Helper()
	services := append([]string{service}, helpers...)
	out, err := compose(t.Context(), append([]string{"--profile", profile, "up", "-d", "--no-build"}, services...)...)
	require.NoError(t, err, out)
	m := &monitor{t: t, profile: profile, service: service}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("---- %s logs ----\n%s", service, m.logs())
		}
		out, err := compose(context.WithoutCancel(t.Context()), append([]string{"--profile", profile, "rm", "-sfv"}, services...)...)
		assert.NoError(t, err, out)
	})
	return m
}

func (m *monitor) logs() string {
	out, err := compose(context.WithoutCancel(m.t.Context()), "--profile", m.profile, "logs", "--no-color", "--no-log-prefix", m.service)
	require.NoError(m.t, err, out)
	return out
}

// waitLog waits until substr occurs at least n times in the monitor log.
func (m *monitor) waitLog(substr string, n int) string {
	m.t.Helper()
	var logs string
	require.EventuallyWithT(m.t, func(c *assert.CollectT) {
		logs = m.logs()
		assert.GreaterOrEqual(c, strings.Count(logs, substr), n, "%q", substr)
	}, waitLong, pollPeriod)
	return logs
}

func name(service string) string { return project + "-" + service + "-1" }

// events reduces the log of one container to "P" (passed check), "F" (failed
// check) and "R" (restart attempt), in order.
func events(logs, container string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(logs, "\n") {
		switch {
		case strings.Contains(line, "[RESTARTING] "+container+":"):
			b.WriteByte('R')
		case strings.Contains(line, "[CURL OK] "+container+":"),
			strings.Contains(line, "[PING OK] "+container+":"):
			b.WriteByte('P')
		case strings.Contains(line, "[CURL FAIL] "+container+":"),
			strings.Contains(line, "[CURL ERROR] "+container+":"),
			strings.Contains(line, "[PING FAIL] "+container+":"):
			b.WriteByte('F')
		}
	}
	return b.String()
}

func timestamps(t *testing.T, logs, substr string) []time.Time {
	t.Helper()
	var out []time.Time
	for line := range strings.SplitSeq(logs, "\n") {
		if !strings.Contains(line, substr) || len(line) < len(logLayout) {
			continue
		}
		ts, err := time.Parse(logLayout, line[:len(logLayout)])
		require.NoError(t, err, line)
		out = append(out, ts)
	}
	return out
}

func TestMainMonitor(t *testing.T) {
	s := newStack(t)

	// A labelled container that exists but is not running must be ignored.
	created := project + "-created"
	out, err := docker(t.Context(), "create", "--name", created,
		"--label", "autoheal.monitor.enable=true", "--label", "autoheal.monitor.curl=http://nowhere.invalid/",
		"autoheal-testtarget:it")
	require.NoError(t, err, out)
	t.Cleanup(func() { _, _ = docker(context.WithoutCancel(t.Context()), "rm", "-f", created) })

	initialRestartable := s.snapshot(restartable)
	initialStable := s.snapshot(stable)

	m := startMonitor(t, "main", "autoheal")

	t.Run("every failing container is restarted", func(t *testing.T) {
		for _, svc := range restartable {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				assert.NotEqual(c, initialRestartable[svc], s.startedAt(svc), "%s not restarted yet", svc)
			}, waitLong, pollPeriod, svc)
		}
	})

	// Two restarts of curl-bad take at least 6 cycles: plenty for every
	// other container to have been checked repeatedly as well.
	logs := m.waitLog("[RESTARTING] "+name("curl-bad")+":", 2)

	t.Run("restart happens after exactly FAIL_THRESHOLD failures and resets the counter", func(t *testing.T) {
		assert.True(t, strings.HasPrefix(events(logs, name("curl-bad")), "FFFRFFFR"), events(logs, name("curl-bad")))
		for _, svc := range restartable {
			assert.True(t, strings.HasPrefix(events(logs, name(svc)), "FFFR"), "%s: %s", svc, events(logs, name(svc)))
		}
	})

	t.Run("a success resets the counter", func(t *testing.T) {
		// Without the reset, F P F P F would reach FAIL_THRESHOLD=3.
		ev := events(m.waitLog("[CURL FAIL] "+name("curl-flap")+":", 3), name("curl-flap"))
		assert.GreaterOrEqual(t, strings.Count(ev, "F"), 3, ev)
		assert.NotContains(t, ev, "FF", ev)
		assert.NotContains(t, ev, "R", ev)
	})

	t.Run("healthy, unmonitored and stopped containers are left alone", func(t *testing.T) {
		s.assertNotRestarted(initialStable)
		_, status, _ := s.inspect(created)
		assert.Equal(t, "created", status)
		assert.NotContains(t, logs, created)
	})

	t.Run("checks and log lines match the original monitor", func(t *testing.T) {
		assert.Contains(t, logs, "Starting monitor with CHECK_INTERVAL=1s, FAIL_THRESHOLD=3, PING_TIMEOUT=1.0s, CURL_TIMEOUT=1.0s")

		assert.Contains(t, logs, `HTTP Request: GET http://curl-ok:8080/health "HTTP/1.1 200 OK"`)
		assert.Contains(t, logs, "[CURL OK] "+name("curl-ok")+": Response matched")
		assert.Contains(t, logs, `HTTP Request: GET http://curl-500-match:8080/ "HTTP/1.1 500 Internal Server Error"`)
		assert.Contains(t, logs, "[CURL OK] "+name("curl-500-match")+": Response matched")
		assert.Contains(t, logs, `HTTP Request: GET http://curl-any:8080/ "HTTP/1.1 503 Service Unavailable"`)
		assert.Contains(t, logs, "[CURL OK] "+name("curl-any")+": Response matched")
		assert.Contains(t, logs, `HTTP Request: GET http://curl-redirect:8080/ "HTTP/1.1 302 Found"`)
		assert.Contains(t, logs, "[CURL FAIL] "+name("curl-redirect")+": Unexpected response from http://curl-redirect:8080/")
		assert.Contains(t, logs, "[CURL FAIL] "+name("curl-bad")+": Unexpected response from http://curl-bad:8080/health")
		assert.Contains(t, logs, "[CURL ERROR] "+name("curl-refused")+": Failed to request http://curl-refused:9999/ — ")
		assert.Contains(t, logs, "[CURL ERROR] "+name("curl-slow")+": Failed to request http://curl-slow:8080/ — ")

		assert.Contains(t, logs, "[PING OK] "+name("ping-ok")+": ping-ok responded in ")
		assert.Contains(t, logs, "[PING FAIL] "+name("ping-noreply")+": No response from 203.0.113.7")
		assert.Contains(t, logs, "[PING FAIL] "+name("ping-unknown")+": host.does-not-exist.invalid unreachable — ")
		assert.Contains(t, logs, "[PING OK] "+name("ping-wins")+": ping-wins responded in ")
		assert.NotContains(t, logs, "http://ping-wins:8080/", "curl must not run when ping is configured")

		for _, svc := range restartable {
			assert.Contains(t, logs, "[RESTARTING] "+name(svc)+": Exceeded 3 failures, restarting container")
		}
		assert.Contains(t, logs, "Monitoring container: "+name("no-check"))
		for _, svc := range []string{"enable-false", "enable-space", "enable-missing"} {
			assert.NotContains(t, logs, name(svc))
		}
		for line := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
			assert.Regexp(t, `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3} \[(DEBUG|INFO|WARNING|ERROR|CRITICAL)\] `, line)
		}
	})

	t.Run("first cycle is immediate, then CHECK_INTERVAL between cycles", func(t *testing.T) {
		start := timestamps(t, logs, "Starting monitor with")
		checks := timestamps(t, logs, "Monitoring container: "+name("curl-ok"))
		require.Len(t, start, 1)
		require.GreaterOrEqual(t, len(checks), 3)
		assert.Less(t, timestamps(t, logs, "Monitoring container: ")[0].Sub(start[0]), time.Second)
		for i := 1; i < len(checks); i++ {
			assert.GreaterOrEqual(t, checks[i].Sub(checks[i-1]), time.Second, "cycle %d", i)
		}
	})

	t.Run("stops promptly with exit code 0 on SIGTERM", func(t *testing.T) {
		id := s.containerID("autoheal")
		begin := time.Now()
		out, err := compose(t.Context(), "--profile", "main", "stop", "-t", "10", "autoheal")
		require.NoError(t, err, out)
		assert.Less(t, time.Since(begin), 5*time.Second)

		_, status, code := s.inspect(id)
		assert.Equal(t, "exited", status)
		assert.Equal(t, 0, code)
		assert.Contains(t, m.logs(), "Shutting down")
	})
}

func TestWithoutICMPPermissionNothingIsRestartedForPing(t *testing.T) {
	s := newStack(t)
	pingTargets := s.snapshot([]string{"ping-ok", "ping-wins"})

	m := startMonitor(t, "noicmp", "autoheal-noicmp")
	// FAIL_THRESHOLD=1: a single counted failure would restart immediately.
	logs := m.waitLog("[ERROR] Cannot ping "+name("ping-ok")+": ICMP sockets not permitted", 3)

	s.assertNotRestarted(pingTargets)
	assert.NotContains(t, logs, "[RESTARTING] "+name("ping-ok"))
	assert.NotContains(t, logs, "[RESTARTING] "+name("ping-wins"))
	// HTTP checks keep working.
	assert.Contains(t, logs, "[RESTARTING] "+name("curl-bad")+":")

	t.Run("LOG_LEVEL=WARNING hides INFO and DEBUG lines", func(t *testing.T) {
		assert.NotContains(t, logs, "[INFO]")
		assert.NotContains(t, logs, "[DEBUG]")
		assert.Contains(t, logs, "[WARNING] [CURL FAIL]")
	})
}

func TestDockerHostAndFailedRestart(t *testing.T) {
	s := newStack(t)
	before := s.snapshot([]string{"curl-bad"})

	m := startMonitor(t, "proxied", "autoheal-proxied", "socket-proxy")
	failed := "[ERROR] Failed to restart " + name("curl-bad") + ": "
	logs := m.waitLog(failed, 3)

	// Listing works through DOCKER_HOST=tcp://socket-proxy:2375 ...
	assert.Contains(t, logs, "[CURL OK] "+name("curl-ok")+": Response matched")
	// ... but the proxy forbids POST, so the container is never restarted.
	s.assertNotRestarted(before)
	// The counter is kept after a failed restart (FAIL_THRESHOLD=2): after the
	// first attempt, every following failure retries the restart.
	assert.True(t, strings.HasPrefix(events(logs, name("curl-bad")), "FFRFRFR"), events(logs, name("curl-bad")))
}

func TestDockerUnavailableKeepsRunning(t *testing.T) {
	const container = project + "-nodocker"
	out, err := docker(t.Context(), "run", "-d", "--name", container,
		"-e", "DOCKER_HOST=unix:///nonexistent.sock", "-e", "CHECK_INTERVAL=1", "autoheal:it")
	require.NoError(t, err, out)
	t.Cleanup(func() { _, _ = docker(context.WithoutCancel(t.Context()), "rm", "-f", container) })

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		logs, err := docker(t.Context(), "logs", container)
		assert.NoError(c, err)
		assert.GreaterOrEqual(c, strings.Count(logs, "[ERROR] Failed to list containers: "), 3)
	}, waitLong, pollPeriod)

	s := newStack(t)
	_, status, _ := s.inspect(container)
	assert.Equal(t, "running", status)
}

func TestInvalidConfigFailsFast(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "docker", "run", "--rm",
		"-e", "FAIL_THRESHOLD=0", "-e", "CHECK_INTERVAL=abc", "-e", "LOG_LEVEL=loud", "autoheal:it").CombinedOutput()
	require.Error(t, err)
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	require.True(t, ok)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Contains(t, string(out), "CHECK_INTERVAL")
	assert.Contains(t, string(out), "FAIL_THRESHOLD must be 1 or greater")
	assert.Contains(t, string(out), "LOG_LEVEL")
}
