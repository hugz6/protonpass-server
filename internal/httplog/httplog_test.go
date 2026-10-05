package httplog

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve sends req through the middleware around h and returns the decoded
// log line and the raw log output.
func serve(t *testing.T, h http.Handler, req *http.Request) (map[string]any, string) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	Middleware(log, h).ServeHTTP(httptest.NewRecorder(), req)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decoding log line %q: %v", buf.String(), err)
	}
	return line, buf.String()
}

func TestMiddlewareLogsRequest(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	})

	line, _ := serve(t, h, httptest.NewRequest(http.MethodPost, "/v1/things", nil))

	if line["method"] != "POST" {
		t.Errorf("method = %v, want POST", line["method"])
	}
	if line["path"] != "/v1/things" {
		t.Errorf("path = %v, want /v1/things", line["path"])
	}
	if line["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want %d", line["status"], http.StatusTeapot)
	}
	if _, ok := line["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms = %v, want a number of milliseconds", line["duration_ms"])
	}
}

func TestMiddlewareDefaultStatus(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok") // no explicit WriteHeader
	})

	line, _ := serve(t, h, httptest.NewRequest(http.MethodGet, "/", nil))

	if line["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want %d", line["status"], http.StatusOK)
	}
}

func TestMiddlewareNeverLogsSecrets(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"password":"body-secret"}`)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/secrets/share/item?field=query-secret", nil)
	req.Header.Set("Authorization", "Bearer header-secret")

	_, out := serve(t, h, req)

	for _, secret := range []string{"header-secret", "query-secret", "body-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("log leaks %q: %s", secret, out)
		}
	}
}
