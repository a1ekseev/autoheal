package probe_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a1ekseev/autoheal/internal/probe"
)

func TestPingLoopback(t *testing.T) {
	rtt, err := probe.NewICMP(2*time.Second).Ping(t.Context(), "127.0.0.1")
	if errors.Is(err, probe.ErrPingNotPermitted) {
		t.Skip("ICMP sockets not permitted in this environment")
	}
	require.NoError(t, err)
	assert.Greater(t, rtt, time.Duration(0))
}

func TestPingUnknownHost(t *testing.T) {
	_, err := probe.NewICMP(time.Second).Ping(t.Context(), "host.does-not-exist.invalid")
	require.Error(t, err)
	assert.NotErrorIs(t, err, probe.ErrNoReply)
}

func TestPingNoReply(t *testing.T) {
	// TEST-NET-3 (RFC 5737) is never routed, so no echo reply can arrive.
	start := time.Now()
	_, err := probe.NewICMP(300*time.Millisecond).Ping(t.Context(), "203.0.113.7")
	if errors.Is(err, probe.ErrPingNotPermitted) {
		t.Skip("ICMP sockets not permitted in this environment")
	}
	require.ErrorIs(t, err, probe.ErrNoReply)
	assert.Less(t, time.Since(start), 3*time.Second)
}
