// Command target is a tiny HTTP server used as a monitored container in the
// integration tests. Its behaviour is configured through environment:
//
//	BODY      response body
//	STATUS    response status code (default 200)
//	LOCATION  value of the Location header (for redirects)
//	DELAY     time.Duration to wait before answering
//	FLAP      "true": every second response has the body "FLAP-DOWN"
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	os.Exit(run())
}

func run() int {
	h, err := newHandler()
	if err != nil {
		slog.Error("invalid configuration", slog.Any("error", err))
		return 2
	}
	srv := &http.Server{Addr: ":8080", ReadHeaderTimeout: 5 * time.Second, Handler: h}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("listen", slog.Any("error", err))
		return 1
	}
	return 0
}

func newHandler() (http.Handler, error) {
	status := http.StatusOK
	if s := os.Getenv("STATUS"); s != "" {
		var err error
		if status, err = strconv.Atoi(s); err != nil {
			return nil, err //nolint:wrapcheck // test helper, message is clear enough
		}
	}
	var delay time.Duration
	if s := os.Getenv("DELAY"); s != "" {
		var err error
		if delay, err = time.ParseDuration(s); err != nil {
			return nil, err //nolint:wrapcheck // test helper, message is clear enough
		}
	}
	body, location, flap := os.Getenv("BODY"), os.Getenv("LOCATION"), os.Getenv("FLAP") == "true"

	var requests atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		b := body
		if flap && requests.Add(1)%2 == 0 {
			b = "FLAP-DOWN"
		}
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(b))
	}), nil
}
