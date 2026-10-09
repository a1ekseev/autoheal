// Package probe implements the health checks used by the monitor: an ICMP
// echo ("ping") and an HTTP GET ("curl").
package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MaxBodyBytes caps how much of a response body is read for matching.
const MaxBodyBytes = 1 << 20

// HTTPResult is the part of an HTTP response the monitor inspects.
type HTTPResult struct {
	Proto  string
	Status string
	Body   string
}

// HTTP performs GET requests with a total timeout. Redirects are not
// followed and the status code is not interpreted, matching httpx.get.
type HTTP struct {
	client *http.Client
}

// NewHTTP returns an HTTP prober whose requests are bounded by timeout.
func NewHTTP(timeout time.Duration) *HTTP {
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // stdlib guarantees the type
	transport.DisableKeepAlives = true
	// HTTP/1.1 only, like httpx in the original monitor.
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	return &HTTP{client: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Get fetches url and returns the status line and (truncated) body.
func (h *HTTP) Get(ctx context.Context, url string) (HTTPResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return HTTPResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "autoheal")

	resp, err := h.client.Do(req)
	if err != nil {
		return HTTPResult{}, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return HTTPResult{}, fmt.Errorf("read body: %w", err)
	}
	return HTTPResult{Proto: resp.Proto, Status: resp.Status, Body: string(body)}, nil
}
