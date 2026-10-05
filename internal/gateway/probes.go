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

// NewProbeHandler serves the kubelet probes, without authentication:
//   - GET /healthz: 200 while the process serves HTTP (liveness);
//   - GET /readyz: 200 only while ready succeeds, 503 otherwise (readiness).
func NewProbeHandler(ready func(context.Context) error, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
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
	return mux
}
