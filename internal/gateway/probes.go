package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// probeTimeout bounds a readiness check: kubelet probes give up after 1s by
// default, there is no point waiting longer.
const probeTimeout = 2 * time.Second

// NewReadyHandler serves the kubelet probe, 200 only while ready succeeds, 503 otherwise (readiness).
func NewReadyHandler(ready func(context.Context) error, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		defer cancel()

		// a gateway whose broker is down must not receive traffic
		if err := ready(ctx); err != nil {
			log.Warn("not ready", "err", err)
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
}
