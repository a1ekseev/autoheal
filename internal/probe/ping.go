package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

var (
	// ErrNoReply means no echo reply arrived before the timeout.
	ErrNoReply = errors.New("no echo reply")
	// ErrPingNotPermitted means neither unprivileged nor raw ICMP sockets are allowed.
	ErrPingNotPermitted = errors.New("ICMP sockets not permitted")
)

// ICMP sends a single IPv4 ICMP echo request, like ping3.ping.
type ICMP struct {
	timeout time.Duration
}

// NewICMP returns an ICMP prober waiting at most timeout for a reply.
func NewICMP(timeout time.Duration) *ICMP {
	return &ICMP{timeout: timeout}
}

// Ping resolves host (bounded by the timeout) and returns the round-trip time.
// It tries an unprivileged datagram ICMP socket first and falls back to a raw
// socket, mirroring ping3's behaviour in reverse order of privilege.
func (p *ICMP) Ping(ctx context.Context, host string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return 0, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return 0, fmt.Errorf("resolve %q: no IPv4 address", host)
	}
	ip := &net.IPAddr{IP: addrs[0]}

	rtt, err := p.run(ctx, ip, false)
	if isPermission(err) {
		rtt, err = p.run(ctx, ip, true)
		if isPermission(err) {
			return 0, fmt.Errorf("%w: %w", ErrPingNotPermitted, err)
		}
	}
	return rtt, err
}

func (p *ICMP) run(ctx context.Context, ip *net.IPAddr, privileged bool) (time.Duration, error) {
	pinger := probing.New(ip.String())
	pinger.SetIPAddr(ip)
	pinger.SetNetwork("ip4")
	pinger.SetPrivileged(privileged)
	pinger.Count = 1
	pinger.Size = 56
	pinger.Timeout = p.timeout
	if deadline, ok := ctx.Deadline(); ok {
		pinger.Timeout = time.Until(deadline)
	}

	if err := pinger.RunWithContext(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return 0, fmt.Errorf("ping %s: %w", ip, err)
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return 0, ErrNoReply
	}
	return stats.AvgRtt, nil
}

func isPermission(err error) bool {
	return err != nil && (errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES))
}
