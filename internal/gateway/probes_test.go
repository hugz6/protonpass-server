package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hugoz6/protonpass-server/internal/protocol"
)

func TestProbes(t *testing.T) {
	notReady := func(context.Context) error { return errors.New("broker down: s3cret detail") }
	ready := func(context.Context) error { return nil }

	tests := []struct {
		name       string
		ready      func(context.Context) error
		path       string
		wantStatus int
	}{
		{"healthz while ready", ready, "/healthz", http.StatusOK},
		{"healthz while not ready", notReady, "/healthz", http.StatusOK},
		{"readyz while ready", ready, "/readyz", http.StatusOK},
		{"readyz while not ready", notReady, "/readyz", http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewProbeHandler(tt.ready, slog.New(slog.DiscardHandler))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if strings.Contains(rec.Body.String(), "s3cret") {
				t.Errorf("response body leaks error details: %q", rec.Body.String())
			}
		})
	}
}

func TestReadyzBoundsSlowCheck(t *testing.T) {
	slow := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	h := NewProbeHandler(slow, slog.New(slog.DiscardHandler))

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if elapsed := time.Since(start); elapsed > probeTimeout+time.Second {
		t.Errorf("readyz answered after %v, want about %v", elapsed, probeTimeout)
	}
}

func TestClientReady(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"ready", http.StatusOK, false},
		{"not ready", http.StatusServiceUnavailable, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			path := fakeBrokerSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(tt.status)
			}))

			err := NewClient(path, 2*time.Second).Ready(t.Context())
			if (err != nil) != tt.wantErr {
				t.Errorf("Ready error = %v, want error: %v", err, tt.wantErr)
			}
			if gotPath != protocol.ReadyPath {
				t.Errorf("broker got path %q, want %q", gotPath, protocol.ReadyPath)
			}
		})
	}
}

func TestClientReadyNoBroker(t *testing.T) {
	if err := NewClient("/tmp/no-such-broker.sock", 2*time.Second).Ready(t.Context()); err == nil {
		t.Error("Ready returned nil error without a broker")
	}
}
