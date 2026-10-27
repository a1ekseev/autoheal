package probe_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a1ekseev/autoheal/internal/probe"
)

func TestHTTPGetReturnsBodyAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "autoheal", r.UserAgent())
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("status: OK"))
	}))
	t.Cleanup(srv.Close)

	res, err := probe.NewHTTP(time.Second).Get(t.Context(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "status: OK", res.Body)
	assert.Equal(t, "HTTP/1.1", res.Proto)
	assert.Equal(t, "500 Internal Server Error", res.Status)
}

func TestHTTPGetDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			_, _ = w.Write([]byte("target"))
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	res, err := probe.NewHTTP(time.Second).Get(t.Context(), srv.URL+"/start")
	require.NoError(t, err)
	assert.Equal(t, "302 Found", res.Status)
	assert.NotEqual(t, "target", res.Body, "the redirect target must not be fetched")
}

func TestHTTPGetTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	_, err := probe.NewHTTP(100*time.Millisecond).Get(t.Context(), srv.URL)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestHTTPGetConnectionRefused(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	_, err = probe.NewHTTP(time.Second).Get(t.Context(), "http://"+addr)
	require.Error(t, err)
}

func TestHTTPGetInvalidURL(t *testing.T) {
	for _, u := range []string{"", "ftp://example.com", "://bad"} {
		_, err := probe.NewHTTP(time.Second).Get(t.Context(), u)
		require.Error(t, err, u)
	}
}

func TestHTTPGetLimitsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", probe.MaxBodyBytes+100)))
	}))
	t.Cleanup(srv.Close)

	res, err := probe.NewHTTP(time.Second).Get(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Len(t, res.Body, probe.MaxBodyBytes)
}
