// Command broker holds the Proton Pass session and runs pass-cli on behalf
// of the gateway. It listens only on a unix socket.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Info("broker starting")
}
