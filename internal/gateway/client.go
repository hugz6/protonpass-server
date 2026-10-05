package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/hugoz6/protonpass-server/internal/protocol"
)

// maxResponseBody matches the 1 MiB limit of a Kubernetes Secret: the broker
// never sends more, and the gateway does not trust it to.
const maxResponseBody = 1 << 20

var errResponseTooLarge = errors.New("broker response too large")

// Client implements Broker by talking HTTP to the broker over its unix socket.
type Client struct {
	http *http.Client
}

// NewClient returns a Client for the broker socket at socketPath. timeout
// bounds a whole call, and must outlast the broker's own pass-cli timeout.
func NewClient(socketPath string, timeout time.Duration) *Client {
	return &Client{
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				// every connection goes through the socket, whatever the url host
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// View asks the broker for the item at uri and maps its status codes to
// ErrInvalid and ErrTimeout.
func (c *Client) View(ctx context.Context, uri string) (json.RawMessage, error) {
	// build url, the host is ignored by the unix dialer
	target := "http://broker" + protocol.ItemsPath + "?" + url.Values{protocol.URIParam: {uri}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("error while building broker request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// a client timeout or an expired caller context is a timeout for ESO too
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, fmt.Errorf("%w: %w", ErrTimeout, err)
		}
		return nil, fmt.Errorf("error while calling broker: %w", err)
	}
	defer resp.Body.Close()

	// read one byte more than allowed to detect an oversized body
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("error while reading broker response: %w", err)
	}
	if len(body) > maxResponseBody {
		return nil, errResponseTooLarge
	}

	// map broker status to errors the handler understands
	if resp.StatusCode == http.StatusBadRequest {
		return nil, ErrInvalid
	}
	if resp.StatusCode == http.StatusGatewayTimeout {
		return nil, ErrTimeout
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("broker returned status %d", resp.StatusCode)
	}

	// never forward something that is not json to ESO
	if !json.Valid(body) {
		return nil, errors.New("broker returned invalid json")
	}
	return body, nil
}
