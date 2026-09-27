// Package app wires Omni-Data-Bus's components together and runs the HTTP server.
package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Server runs an http.Server until its context is cancelled, then shuts down gracefully.
type Server struct {
	HTTP            *http.Server
	ShutdownTimeout time.Duration
}

// Run serves on ln (or HTTP.Addr when ln is nil). It returns after in-flight requests
// finish or the shutdown timeout elapses.
func (s *Server) Run(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		var err error
		if ln != nil {
			err = s.HTTP.Serve(ln)
		} else {
			err = s.HTTP.ListenAndServe()
		}
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	timeout := s.ShutdownTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	// ctx is already cancelled; keep its values but give shutdown its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := s.HTTP.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-errCh
}
