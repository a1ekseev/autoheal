package probe

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// httpx (used by the Python original) speaks HTTP/1.1 only; so must we, even
// against an HTTPS server offering h2.
func TestHTTPUsesHTTP11OverTLS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	h := NewHTTP(time.Second)
	tr, ok := h.client.Transport.(*http.Transport)
	require.True(t, ok)
	// Trust the test certificate only; ALPN is left to the transport.
	tr.TLSClientConfig = &tls.Config{RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}

	res, err := h.Get(t.Context(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1", res.Proto)
}
