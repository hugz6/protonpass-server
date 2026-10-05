package broker

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// socketPath returns a socket path short enough for the kernel (108 bytes).
// Its parent directory exists; the "sock" directory does not: Listen creates it.
func socketPath(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "pps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	return filepath.Join(base, "sock", "broker.sock")
}

// listen calls Listen and closes the listener at the end of the test.
func listen(t *testing.T, path string, allowedUID int) net.Listener {
	t.Helper()
	l, err := Listen(path, allowedUID, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// serveOK serves "ok" on l until the end of the test.
func serveOK(t *testing.T, l net.Listener) {
	t.Helper()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
}

// unixClient returns an HTTP client connecting through the socket at path.
// The host in request URLs is ignored.
func unixClient(path string) *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		},
	}
}

func TestListenPermissions(t *testing.T) {
	path := socketPath(t)
	listen(t, path, os.Getuid())

	tests := []struct {
		name string
		path string
		want os.FileMode
	}{
		{"socket directory", filepath.Dir(path), 0o750},
		{"socket", path, 0o660},
	}
	for _, tt := range tests {
		fi, err := os.Stat(tt.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tt.name, err)
		}
		if got := fi.Mode().Perm(); got != tt.want {
			t.Errorf("%s mode = %o, want %o", tt.name, got, tt.want)
		}
	}
}

func TestListenSocketIsASocket(t *testing.T) {
	path := socketPath(t)
	listen(t, path, os.Getuid())

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Type() != os.ModeSocket {
		t.Errorf("%s is %v, want a unix socket", path, fi.Mode().Type())
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := socketPath(t)
	// Simulate a file left behind by a crashed broker.
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	listen(t, path, os.Getuid())
}

func TestListenTightensExistingDirectory(t *testing.T) {
	path := socketPath(t)
	// The directory already exists with permissions that are too open.
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}

	listen(t, path, os.Getuid())

	fi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o750 {
		t.Errorf("socket directory mode = %o, want %o", got, 0o750)
	}
}

func TestListenAcceptsAllowedUID(t *testing.T) {
	path := socketPath(t)
	serveOK(t, listen(t, path, os.Getuid()))

	resp, err := unixClient(path).Get("http://broker/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestListenRejectsOtherUID(t *testing.T) {
	path := socketPath(t)
	serveOK(t, listen(t, path, os.Getuid()+1))

	if resp, err := unixClient(path).Get("http://broker/"); err == nil {
		resp.Body.Close()
		t.Fatal("request from a non-allowed uid succeeded")
	}
	// A rejection must not stop the listener: Accept keeps serving.
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("listener stopped accepting after a rejection: %v", err)
	}
	c.Close()
}

func TestListenFailsOnTooLongPath(t *testing.T) {
	// The kernel limits unix socket paths to 108 bytes: net.Listen fails, and
	// Listen must report it instead of returning a nil listener.
	path := filepath.Join(socketPath(t)+strings.Repeat("x", 120), "broker.sock")

	l, err := Listen(path, os.Getuid(), slog.New(slog.DiscardHandler))
	if err == nil {
		if l != nil {
			l.Close()
		}
		t.Fatal("Listen returned nil error for a path longer than 108 bytes")
	}
	if l != nil {
		t.Errorf("Listen returned a non-nil listener with error %v", err)
	}
}

func TestListenFailsWhenStaleSocketCannotBeRemoved(t *testing.T) {
	path := socketPath(t)
	// A non-empty directory where the socket should be: os.Remove fails with
	// an error other than "does not exist", which must not be ignored.
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o750); err != nil {
		t.Fatal(err)
	}

	l, err := Listen(path, os.Getuid(), slog.New(slog.DiscardHandler))
	if err == nil {
		if l != nil {
			l.Close()
		}
		t.Fatal("Listen returned nil error although the old socket could not be removed")
	}
}
