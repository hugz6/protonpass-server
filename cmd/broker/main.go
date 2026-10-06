// Command broker holds the Proton Pass session and runs pass-cli on behalf
// of the gateway. It listens only on a unix socket.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hugz6/protonpass-server/internal/broker"
	"github.com/hugz6/protonpass-server/internal/graceful"
	"github.com/hugz6/protonpass-server/internal/httplog"
	"github.com/hugz6/protonpass-server/internal/passcli"
	"github.com/hugz6/protonpass-server/internal/protocol"
)

// shutdownTimeout stays under the 30s Kubernetes gives before SIGKILL.
const shutdownTimeout = 20 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("broker failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	socket := flag.String("socket", "/run/protonpass/sock/broker.sock", "socket path")
	allowedUID := flag.Int("allowed-uid", -1, "uid of the gateway")
	binPath := flag.String("pass-cli", "pass-cli", "pass-cli bin")
	homePath := flag.String("home", "", "home")
	patPath := flag.String("pat-file", "", "path of the file containing the pat for pass-cli")
	timeout := flag.Duration("timeout", 30*time.Second, "timeout per pass-cli call")
	maxConcurrent := flag.Int("max-concurrent", 4, "maximum pass-cli calls running at the same time")
	flag.Parse()

	logger.Info("broker starting")

	if *allowedUID <= 0 {
		return errors.New("-allowed-uid must be > 0 (non root)")
	}
	if *maxConcurrent <= 0 {
		return errors.New("-max-concurrent must be > 0")
	}
	if *homePath == "" || *patPath == "" {
		return errors.New("-home and -pat-file must be specified")
	}
	if _, err := os.Stat(*homePath); err != nil {
		return fmt.Errorf("home path: %w", err)
	}

	// The runner clears PATH, so pass-cli needs an absolute path.
	absBinPath, err := exec.LookPath(*binPath)
	if err != nil {
		return fmt.Errorf("finding pass-cli: %w", err)
	}
	if absBinPath, err = filepath.Abs(absBinPath); err != nil {
		return fmt.Errorf("finding pass-cli: %w", err)
	}

	pat, err := os.ReadFile(*patPath)
	if err != nil {
		return fmt.Errorf("reading pat file: %w", err)
	}
	trimmedPat := strings.TrimSpace(string(pat))
	if trimmedPat == "" {
		return errors.New("pat file must not be empty")
	}

	passCliRunner := &passcli.Runner{
		Bin:     absBinPath,
		Home:    *homePath,
		Timeout: *timeout,
	}
	// a restarted container finds the previous session in HOME: reuse it
	reused, err := passCliRunner.EnsureSession(context.Background(), trimmedPat)
	if err != nil {
		return fmt.Errorf("logging in: %w", err)
	}
	logger.Info("pass-cli session ready", "reused", reused)

	l, err := broker.Listen(*socket, *allowedUID, logger)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}

	// items go through the concurrency limit, readiness reads a cached session check
	mux := http.NewServeMux()
	mux.Handle(protocol.ItemsPath, broker.NewHandler(broker.Limit(passCliRunner, *maxConcurrent), logger))
	mux.Handle("GET "+protocol.ReadyPath, broker.NewReadyHandler(passCliRunner.Info, 10*time.Second, logger))

	srv := &http.Server{
		Handler:           httplog.Middleware(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
		// Must outlast a pass-cli call, or the response is cut before it is ready.
		WriteTimeout: *timeout + 5*time.Second,
	}
	logger.Info("broker listening", "socket", *socket, "allowed_uid", *allowedUID)

	// kubernetes sends SIGTERM, then SIGKILL after 30s: finish running calls before
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = graceful.Run(ctx, srv, func() error { return srv.Serve(l) }, shutdownTimeout)
	logger.Info("broker stopped")
	return err
}
