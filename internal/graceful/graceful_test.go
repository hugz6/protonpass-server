package graceful

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// startServer returns a server on a random local port whose handler signals
// started, then waits for release before answering "done".
func startServer(t *testing.T, started, release chan struct{}) (*http.Server, net.Listener) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		io.WriteString(w, "done")
	})}
	return srv, l
}

func TestRunLetsRunningRequestFinish(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv, l := startServer(t, started, release)
	ctx, cancel := context.WithCancel(t.Context())

	runErr := make(chan error, 1)
	go func() { runErr <- Run(ctx, srv, func() error { return srv.Serve(l) }, 5*time.Second) }()

	// a request is in flight when the shutdown begins
	respc := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + l.Addr().String())
		if err != nil {
			respc <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		respc <- string(body)
	}()
	<-started
	cancel()

	// Run must wait for the request instead of returning right away
	select {
	case err := <-runErr:
		t.Fatalf("Run returned %v while a request was still running", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	if got := <-respc; got != "done" {
		t.Errorf("in-flight response = %q, want %q", got, "done")
	}
	if err := <-runErr; err != nil {
		t.Errorf("Run = %v, want nil after a clean shutdown", err)
	}
}

func TestRunReturnsStartError(t *testing.T) {
	want := errors.New("cannot listen")
	srv := &http.Server{}

	if err := Run(t.Context(), srv, func() error { return want }, time.Second); !errors.Is(err, want) {
		t.Errorf("Run = %v, want %v", err, want)
	}
}

func TestRunGivesUpAfterTimeout(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	srv, l := startServer(t, started, release)
	ctx, cancel := context.WithCancel(t.Context())

	runErr := make(chan error, 1)
	go func() { runErr <- Run(ctx, srv, func() error { return srv.Serve(l) }, 200*time.Millisecond) }()
	go http.Get("http://" + l.Addr().String())
	<-started
	cancel()

	// the request never ends: Run must stop waiting after the timeout
	select {
	case err := <-runErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not give up after its timeout")
	}
}
