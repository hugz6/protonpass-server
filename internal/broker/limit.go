package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrBusy is returned when no pass-cli slot freed up before the request gave up.
var ErrBusy = errors.New("too many pass-cli calls running")

// limitedViewer lets at most cap(slots) View calls run at the same time.
type limitedViewer struct {
	v     Viewer
	slots chan struct{}
}

// Limit wraps v so that at most n calls run at once. ESO refreshes in bursts:
// without a limit, a burst would start one pass-cli per secret.
func Limit(v Viewer, n int) Viewer {
	return &limitedViewer{v: v, slots: make(chan struct{}, n)}
}

func (l *limitedViewer) View(ctx context.Context, uri string) (json.RawMessage, error) {
	// take a slot, or give up with the caller
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	}
	// free the slot whatever happens
	defer func() { <-l.slots }()

	return l.v.View(ctx, uri)
}
