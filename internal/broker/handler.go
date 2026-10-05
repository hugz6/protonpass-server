// Package broker serves pass-cli reads to the gateway over a unix socket.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/hugoz6/protonpass-server/internal/protocol"
)

// Viewer reads a Proton Pass item. *passcli.Runner implements it.
type Viewer interface {
	View(ctx context.Context, uri string) (json.RawMessage, error)
}

// NewHandler serves protocol.ItemsPath, reading items through v.
func NewHandler(v Viewer, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+protocol.ItemsPath, func(w http.ResponseWriter, r *http.Request) {
		// get uri from url params
		uri := r.URL.Query().Get(protocol.URIParam)

		// first validate uri
		if err := protocol.ValidateURI(uri); err != nil {
			http.Error(w, "invalid uri", http.StatusBadRequest)
			return
		}

		out, err := v.View(r.Context(), uri)
		if err != nil {
			log.Error("view failed", "uri", uri, "err", err)
			if errors.Is(err, context.DeadlineExceeded) {
				http.Error(w, "deadline exceeded", http.StatusGatewayTimeout)
				return
			} else {
				http.Error(w, "internal error", http.StatusBadGateway)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	})
	return mux
}
