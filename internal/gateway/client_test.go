package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hugoz6/protonpass-server/internal/protocol"
)

// fakeBrokerSocket serves h on a unix socket until the end of the test and
// returns the socket path. The path stays under the kernel's 108-byte limit.
func fakeBrokerSocket(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ppg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "broker.sock")

	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return path
}

// replyWith returns a handler answering status and body.
func replyWith(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

// slowBroker blocks until the request is cancelled, or 5s at most.
var slowBroker = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-time.After(5 * time.Second):
	}
})

func TestClientOK(t *testing.T) {
	want := `{"password":"s3cret"}`
	uri := "pass://share/item/My Field"
	var gotPath, gotURI string
	path := fakeBrokerSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotURI = r.URL.Path, r.URL.Query().Get(protocol.URIParam)
		io.WriteString(w, want)
	}))

	got, err := NewClient(path, 2*time.Second).View(t.Context(), uri)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if string(got) != want {
		t.Errorf("View = %q, want %q", got, want)
	}
	if gotPath != protocol.ItemsPath {
		t.Errorf("broker got path %q, want %q", gotPath, protocol.ItemsPath)
	}
	if gotURI != uri {
		t.Errorf("broker got uri %q, want %q", gotURI, uri)
	}
}

func TestClientStatusErrors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		wantInvalid bool
		wantTimeout bool
		wantBusy    bool
	}{
		{"bad request", http.StatusBadRequest, true, false, false},
		{"gateway timeout", http.StatusGatewayTimeout, false, true, false},
		{"service unavailable", http.StatusServiceUnavailable, false, false, true},
		{"bad gateway", http.StatusBadGateway, false, false, false},
		{"internal error", http.StatusInternalServerError, false, false, false},
		{"not found", http.StatusNotFound, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := fakeBrokerSocket(t, replyWith(tt.status, "detail: s3cret"))

			_, err := NewClient(path, 2*time.Second).View(t.Context(), "pass://share/item")
			if err == nil {
				t.Fatal("View returned nil error, want error")
			}
			if got := errors.Is(err, ErrInvalid); got != tt.wantInvalid {
				t.Errorf("errors.Is(err, ErrInvalid) = %v, want %v (err: %v)", got, tt.wantInvalid, err)
			}
			if got := errors.Is(err, ErrTimeout); got != tt.wantTimeout {
				t.Errorf("errors.Is(err, ErrTimeout) = %v, want %v (err: %v)", got, tt.wantTimeout, err)
			}
			if got := errors.Is(err, ErrBusy); got != tt.wantBusy {
				t.Errorf("errors.Is(err, ErrBusy) = %v, want %v (err: %v)", got, tt.wantBusy, err)
			}
		})
	}
}

func TestClientBodyLimit(t *testing.T) {
	path := fakeBrokerSocket(t, replyWith(http.StatusOK, `"`+strings.Repeat("x", 2<<20)+`"`))

	if _, err := NewClient(path, 2*time.Second).View(t.Context(), "pass://share/item"); !errors.Is(err, errResponseTooLarge) {
		t.Errorf("View error = %v, want errResponseTooLarge", err)
	}
}

func TestClientAtBodyLimit(t *testing.T) {
	want := `"` + strings.Repeat("x", maxResponseBody-2) + `"`
	path := fakeBrokerSocket(t, replyWith(http.StatusOK, want))

	got, err := NewClient(path, 2*time.Second).View(t.Context(), "pass://share/item")
	if err != nil {
		t.Fatalf("View with a body of exactly maxResponseBody bytes: %v", err)
	}
	if len(got) != maxResponseBody {
		t.Errorf("View returned %d bytes, want %d", len(got), maxResponseBody)
	}
}

func TestClientInvalidJSON(t *testing.T) {
	path := fakeBrokerSocket(t, replyWith(http.StatusOK, "not json"))

	if _, err := NewClient(path, 2*time.Second).View(t.Context(), "pass://share/item"); err == nil {
		t.Error("View returned nil error for a non-JSON body")
	}
}

func TestClientNoBroker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sock")

	_, err := NewClient(path, 2*time.Second).View(t.Context(), "pass://share/item")
	if err == nil {
		t.Fatal("View returned nil error without a broker")
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrTimeout) {
		t.Errorf("View error = %v, want a plain connection error", err)
	}
}

func TestClientTimeout(t *testing.T) {
	path := fakeBrokerSocket(t, slowBroker)

	start := time.Now()
	_, err := NewClient(path, 200*time.Millisecond).View(t.Context(), "pass://share/item")
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Errorf("View error = %v, want ErrTimeout", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("View returned after %v, want < 3s", elapsed)
	}
}

func TestClientCallerCancel(t *testing.T) {
	path := fakeBrokerSocket(t, slowBroker)
	// ESO gives up before the client's own timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewClient(path, 5*time.Second).View(ctx, "pass://share/item")
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Errorf("View error = %v, want ErrTimeout", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("View returned after %v, want < 3s", elapsed)
	}
}
