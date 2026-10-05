// Command gateway serves the External Secrets Operator webhook API and
// forwards item reads to the broker over a unix socket. It never runs
// pass-cli and holds no Proton credentials.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hugoz6/protonpass-server/internal/gateway"
	"github.com/hugoz6/protonpass-server/internal/graceful"
	"github.com/hugoz6/protonpass-server/internal/httplog"
)

// shutdownTimeout stays under the 30s Kubernetes gives before SIGKILL.
const shutdownTimeout = 20 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("gateway failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	listen := flag.String("listen", ":8443", "https listen address")
	tokenPath := flag.String("token-file", "", "path of the file containing the bearer token expected from ESO")
	certPath := flag.String("tls-cert", "", "path of the tls certificate")
	keyPath := flag.String("tls-key", "", "path of the tls private key")
	socket := flag.String("broker-socket", "/run/protonpass/sock/broker.sock", "broker socket path")
	timeout := flag.Duration("timeout", 35*time.Second, "timeout per broker call, longer than the broker's pass-cli timeout")
	flag.Parse()

	logger.Info("gateway starting")

	// secrets go through this server: no plain http option
	if *tokenPath == "" || *certPath == "" || *keyPath == "" {
		return errors.New("-token-file, -tls-cert and -tls-key must be specified")
	}

	token, err := os.ReadFile(*tokenPath)
	if err != nil {
		return fmt.Errorf("reading token file: %w", err)
	}
	trimmedToken := strings.TrimSpace(string(token))
	if trimmedToken == "" {
		return errors.New("token file must not be empty")
	}

	client := gateway.NewClient(*socket, *timeout)

	// secrets need the token, kubelet probes do not
	mux := http.NewServeMux()
	mux.Handle("/v1/secrets/", gateway.NewHandler(client, trimmedToken, logger))
	probes := gateway.NewProbeHandler(client.Ready, logger)
	mux.Handle("/healthz", probes)
	mux.Handle("/readyz", probes)

	srv := &http.Server{
		Addr:    *listen,
		Handler: httplog.Middleware(logger, mux),
		// slow or idle clients must not hold connections forever
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
		// must outlast a broker call, or the response is cut before it is ready
		WriteTimeout: *timeout + 5*time.Second,
		// keep server errors (tls handshakes...) in the same json logs
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	logger.Info("gateway listening", "addr", *listen, "broker_socket", *socket)

	// kubernetes sends SIGTERM, then SIGKILL after 30s: finish running calls before
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = graceful.Run(ctx, srv, func() error { return srv.ListenAndServeTLS(*certPath, *keyPath) }, shutdownTimeout)
	logger.Info("gateway stopped")
	return err
}
