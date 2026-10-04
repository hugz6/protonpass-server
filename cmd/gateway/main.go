// Command gateway serves the External Secrets Operator webhook API and
// forwards item reads to the broker over a unix socket. It never runs
// pass-cli and holds no Proton credentials.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Info("gateway starting")
}
