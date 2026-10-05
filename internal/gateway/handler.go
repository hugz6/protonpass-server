// Package gateway serves the External Secrets Operator webhook API and
// forwards item reads to the broker. It never runs pass-cli itself.
package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hugoz6/protonpass-server/internal/protocol"
)

// Errors a Broker returns so the handler can pick the HTTP status.
var (
	ErrInvalid = errors.New("broker rejected the request") // → 400
	ErrTimeout = errors.New("broker timed out")            // → 504
	ErrBusy    = errors.New("broker busy")                 // → 503
)

// Broker reads a Proton Pass item through the broker.
type Broker interface {
	View(ctx context.Context, uri string) (json.RawMessage, error)
}

// authorized reports whether header is "Bearer <token>". An empty token never
// authorizes anything. The comparison takes the same time whatever the input.
func authorized(header, token string) bool {
	// a misconfigured gateway must not accept everyone
	if token == "" {
		return false
	}

	// check if scheme is valid
	sentToken, validScheme := strings.CutPrefix(header, "Bearer ")
	if !validScheme {
		return false
	}

	// constant time compare, == would leak the token through response time
	return subtle.ConstantTimeCompare([]byte(sentToken), []byte(token)) == 1
}

// NewHandler serves GET /v1/secrets/{share}/{item}?field=FIELD for ESO,
// authenticated by a bearer token, and reads items through b.
func NewHandler(b Broker, token string, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/secrets/{share}/{item}", func(w http.ResponseWriter, r *http.Request) {
		// first authenticate, an anonymous caller must learn nothing
		if !authorized(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// get share and item from url path, values are already decoded
		share := r.PathValue("share")
		item := r.PathValue("item")

		// a decoded %2F would silently shift item into field
		if strings.Contains(share, "/") || strings.Contains(item, "/") {
			http.Error(w, "invalid secret reference", http.StatusBadRequest)
			return
		}

		// build uri, an empty field means the whole item
		uri := "pass://" + share + "/" + item
		if field := r.URL.Query().Get("field"); field != "" {
			uri += "/" + field
		}

		// validate uri before bothering the broker
		if err := protocol.ValidateURI(uri); err != nil {
			http.Error(w, "invalid secret reference", http.StatusBadRequest)
			return
		}

		out, err := b.View(r.Context(), uri)
		if err != nil {
			log.Error("broker request failed", "uri", uri, "err", err)
			if errors.Is(err, ErrInvalid) {
				http.Error(w, "invalid secret reference", http.StatusBadRequest)
				return
			}
			if errors.Is(err, ErrTimeout) {
				http.Error(w, "broker timeout", http.StatusGatewayTimeout)
				return
			}
			if errors.Is(err, ErrBusy) {
				http.Error(w, "broker busy", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "broker failed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	})
	return mux
}
