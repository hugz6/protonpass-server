package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "s3cr3t-token"

// fakeBroker returns out and err, and records how it was called.
type fakeBroker struct {
	out    json.RawMessage
	err    error
	called bool
	uri    string
}

func (f *fakeBroker) View(_ context.Context, uri string) (json.RawMessage, error) {
	f.called, f.uri = true, uri
	return f.out, f.err
}

// send sends one request to a handler built on b, with the given
// Authorization header ("" for none), and returns the recorded response.
func send(t *testing.T, b Broker, method, target, auth string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(b, testToken, slog.New(slog.DiscardHandler))
	req := httptest.NewRequest(method, target, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthorized(t *testing.T) {
	tests := []struct {
		name   string
		header string
		token  string
		want   bool
	}{
		{"valid", "Bearer abc", "abc", true},
		{"no header", "", "abc", false},
		{"wrong token", "Bearer abd", "abc", false},
		{"token prefix only", "Bearer ab", "abc", false},
		{"other scheme", "Basic abc", "abc", false},
		{"no scheme", "abc", "abc", false},
		{"no space", "Bearerabc", "abc", false},
		{"empty token sent", "Bearer ", "abc", false},
		{"empty token configured", "Bearer ", "", false},
		{"empty token configured, empty header", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := authorized(tt.header, tt.token); got != tt.want {
				t.Errorf("authorized(%q, %q) = %v, want %v", tt.header, tt.token, got, tt.want)
			}
		})
	}
}

func TestHandlerOK(t *testing.T) {
	want := `{"password":"s3cret"}`
	fake := &fakeBroker{out: json.RawMessage(want)}

	rec := send(t, fake, http.MethodGet, "/v1/secrets/share/item", "Bearer "+testToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if fake.uri != "pass://share/item" {
		t.Errorf("broker got uri %q, want %q", fake.uri, "pass://share/item")
	}
}

func TestHandlerBuildsURI(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantURI string
	}{
		{"no field", "/v1/secrets/share/item", "pass://share/item"},
		{"empty field", "/v1/secrets/share/item?field=", "pass://share/item"},
		{"field", "/v1/secrets/share/item?field=password", "pass://share/item/password"},
		{"field with space", "/v1/secrets/share/item?field=My%20Field", "pass://share/item/My Field"},
		{"base64 ids", "/v1/secrets/abc123==/d-e_f==", "pass://abc123==/d-e_f=="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeBroker{out: json.RawMessage(`{}`)}
			rec := send(t, fake, http.MethodGet, tt.target, "Bearer "+testToken)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if fake.uri != tt.wantURI {
				t.Errorf("broker got uri %q, want %q", fake.uri, tt.wantURI)
			}
		})
	}
}

func TestHandlerRejectsUnauthenticated(t *testing.T) {
	tests := []struct {
		name   string
		target string
		auth   string
	}{
		{"no header", "/v1/secrets/share/item", ""},
		{"other scheme", "/v1/secrets/share/item", "Basic " + testToken},
		{"wrong token", "/v1/secrets/share/item", "Bearer wrong"},
		// Authentication comes first: an anonymous caller learns nothing,
		// not even whether its reference is well formed.
		{"invalid reference", "/v1/secrets/sh;are/item", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeBroker{out: json.RawMessage(`{}`)}
			rec := send(t, fake, http.MethodGet, tt.target, tt.auth)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want %q", got, "Bearer")
			}
			if fake.called {
				t.Error("broker called for an unauthenticated request")
			}
		})
	}
}

func TestHandlerEmptyConfiguredTokenRejectsAll(t *testing.T) {
	fake := &fakeBroker{out: json.RawMessage(`{}`)}
	h := NewHandler(fake, "", slog.New(slog.DiscardHandler))
	req := httptest.NewRequest(http.MethodGet, "/v1/secrets/share/item", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if fake.called {
		t.Error("broker called although no token is configured")
	}
}

func TestHandlerErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		brokerErr  error
		wantStatus int
		wantCalled bool
	}{
		{"invalid share", http.MethodGet, "/v1/secrets/sh;are/item", nil, http.StatusBadRequest, false},
		{"encoded slash in share", http.MethodGet, "/v1/secrets/a%2Fb/item", nil, http.StatusBadRequest, false},
		{"encoded slash in item", http.MethodGet, "/v1/secrets/share/a%2Fb", nil, http.StatusBadRequest, false},
		{"slash in field", http.MethodGet, "/v1/secrets/share/item?field=a/b", nil, http.StatusBadRequest, false},
		{"newline in field", http.MethodGet, "/v1/secrets/share/item?field=a%0Ab", nil, http.StatusBadRequest, false},
		{"broker rejects", http.MethodGet, "/v1/secrets/share/item", fmt.Errorf("broker: %w", ErrInvalid), http.StatusBadRequest, true},
		{"broker timeout", http.MethodGet, "/v1/secrets/share/item", fmt.Errorf("broker: %w", ErrTimeout), http.StatusGatewayTimeout, true},
		{"broker busy", http.MethodGet, "/v1/secrets/share/item", fmt.Errorf("broker: %w", ErrBusy), http.StatusServiceUnavailable, true},
		{"broker failure", http.MethodGet, "/v1/secrets/share/item", errors.New("exit status 1: s3cret"), http.StatusBadGateway, true},
		{"wrong method", http.MethodPost, "/v1/secrets/share/item", nil, http.StatusMethodNotAllowed, false},
		{"unknown path", http.MethodGet, "/v1/other", nil, http.StatusNotFound, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeBroker{out: json.RawMessage(`{}`), err: tt.brokerErr}
			rec := send(t, fake, tt.method, tt.target, "Bearer "+testToken)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if fake.called != tt.wantCalled {
				t.Errorf("broker called = %v, want %v", fake.called, tt.wantCalled)
			}
			// Error details stay in the gateway logs, never in the response.
			if body := rec.Body.String(); strings.Contains(body, "s3cret") {
				t.Errorf("response body leaks error details: %q", body)
			}
		})
	}
}
