// Package graceful runs an HTTP server until its context ends, then lets
// running requests finish.
package graceful

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Run calls start (srv.Serve, srv.ListenAndServeTLS...) and blocks until it
// fails or ctx ends. In the second case it shuts srv down: no new connections,
// running requests get up to timeout to finish.
func Run(ctx context.Context, srv *http.Server, start func() error, timeout time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- start() }()

	select {
	case err := <-errc:
		// the server stopped on its own: that is always a failure
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Shutdown makes start return ErrServerClosed at once, but only returns
	// itself once running requests are done: wait for Shutdown, not start
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
