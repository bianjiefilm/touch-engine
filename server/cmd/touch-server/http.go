package main

import (
	"context"
	"net"
	"net/http"
	"time"
)

// serveHTTP runs the handler until ctx is cancelled, then shuts down cleanly.
// The listen address must be loopback in production discipline (enforced by the
// operator via TOUCH_HTTP_ADDR, default 127.0.0.1:18240).
func serveHTTP(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
