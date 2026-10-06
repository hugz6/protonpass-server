package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hugz6/protonpass-server/internal/protocol"
)

// fakeViewer returns out and err, and records how it was called
type fakeViewer struct {
	out    json.RawMessage
	err    error
	called bool
	uri    string
}

func (f *fakeViewer) View(_ context.Context, uri string) (json.RawMessage, error) {
	f.called, f.uri = true, uri
	return f.out, f.err
}

// serve sends one request to h and returns the recorded response
func serve(t *testing.T, h http.Handler, method, uri string) *httptest.ResponseRecorder {
	t.Helper()
	target := protocol.ItemsPath + "?" + url.Values{protocol.URIParam: {uri}}.Encode()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandlerOK(t *testing.T) {
	want := `{"password":"s3cret"}`
	uri := "pass://share/item/My Field" // the space checks query encoding
	fake := &fakeViewer{out: json.RawMessage(want)}

	rec := serve(t, NewHandler(fake, slog.New(slog.DiscardHandler)), http.MethodGet, uri)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if fake.uri != uri {
		t.Errorf("viewer got uri %q, want %q", fake.uri, uri)
	}
}

func TestHandlerErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		uri        string
		viewErr    error
		wantStatus int
		wantCalled bool
	}{
		{"invalid uri", http.MethodGet, "pass://share/it;em", nil, http.StatusBadRequest, false},
		{"missing uri", http.MethodGet, "", nil, http.StatusBadRequest, false},
		{"timeout", http.MethodGet, "pass://share/item", fmt.Errorf("pass-cli: %w", context.DeadlineExceeded), http.StatusGatewayTimeout, true},
		{"failure", http.MethodGet, "pass://share/item", errors.New("exit status 1: s3cret"), http.StatusBadGateway, true},
		{"busy", http.MethodGet, "pass://share/item", fmt.Errorf("%w: %w", ErrBusy, context.DeadlineExceeded), http.StatusServiceUnavailable, true},
		{"wrong method", http.MethodPost, "pass://share/item", nil, http.StatusMethodNotAllowed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeViewer{err: tt.viewErr}
			rec := serve(t, NewHandler(fake, slog.New(slog.DiscardHandler)), tt.method, tt.uri)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if fake.called != tt.wantCalled {
				t.Errorf("viewer called = %v, want %v", fake.called, tt.wantCalled)
			}
			// Error details stay in the broker logs, never in the response.
			if body := rec.Body.String(); strings.Contains(body, "s3cret") {
				t.Errorf("response body leaks error details: %q", body)
			}
		})
	}
}
