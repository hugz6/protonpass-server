package broker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingCheck returns err and counts its calls.
type countingCheck struct {
	calls atomic.Int32
	err   error
}

func (c *countingCheck) check(context.Context) error {
	c.calls.Add(1)
	return c.err
}

func probe(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
	return rec
}

func TestReadyOK(t *testing.T) {
	c := &countingCheck{}
	rec := probe(t, NewReadyHandler(c.check, time.Minute, slog.New(slog.DiscardHandler)))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyNotReady(t *testing.T) {
	c := &countingCheck{err: errors.New("no session: s3cret detail")}
	rec := probe(t, NewReadyHandler(c.check, time.Minute, slog.New(slog.DiscardHandler)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Errorf("response body leaks error details: %q", rec.Body.String())
	}
}

func TestReadyCachesResult(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"success", nil},
		{"failure", errors.New("no session")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &countingCheck{err: tt.err}
			h := NewReadyHandler(c.check, 100*time.Millisecond, slog.New(slog.DiscardHandler))

			for range 5 {
				probe(t, h)
			}
			if got := c.calls.Load(); got != 1 {
				t.Errorf("check ran %d times within the ttl, want 1", got)
			}

			time.Sleep(150 * time.Millisecond)
			probe(t, h)
			if got := c.calls.Load(); got != 2 {
				t.Errorf("check ran %d times after the ttl, want 2", got)
			}
		})
	}
}
