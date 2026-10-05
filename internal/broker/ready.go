package broker

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// readiness caches the result of check for ttl.
type readiness struct {
	check func(context.Context) error
	ttl   time.Duration
	log   *slog.Logger

	mu        sync.Mutex
	checkedAt time.Time
	err       error
}

// NewReadyHandler answers 200 while check succeeds, 503 otherwise. The result
// is cached for ttl: probes come often, and each check runs pass-cli.
func NewReadyHandler(check func(context.Context) error, ttl time.Duration, log *slog.Logger) http.Handler {
	rd := &readiness{check: check, ttl: ttl, log: log}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := rd.result(r.Context()); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
}

// result returns the cached check result, running check again once it expired.
func (rd *readiness) result(ctx context.Context) error {
	// one check at a time: concurrent probes wait for it instead of piling up
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if !rd.checkedAt.IsZero() && time.Since(rd.checkedAt) < rd.ttl {
		return rd.err
	}
	rd.err = rd.check(ctx)
	rd.checkedAt = time.Now()
	if rd.err != nil {
		rd.log.Warn("not ready", "err", rd.err)
	}
	return rd.err
}
