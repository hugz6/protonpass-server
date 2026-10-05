package broker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingViewer counts concurrent View calls and blocks them until release
// is closed.
type blockingViewer struct {
	running, maxRunning atomic.Int32
	started             chan struct{}
	release             chan struct{}
}

func (b *blockingViewer) View(_ context.Context, _ string) (json.RawMessage, error) {
	n := b.running.Add(1)
	for {
		max := b.maxRunning.Load()
		if n <= max || b.maxRunning.CompareAndSwap(max, n) {
			break
		}
	}
	b.started <- struct{}{}
	<-b.release
	b.running.Add(-1)
	return json.RawMessage(`{}`), nil
}

func TestLimitCapsConcurrency(t *testing.T) {
	const limit, calls = 3, 10
	b := &blockingViewer{started: make(chan struct{}, calls), release: make(chan struct{})}
	v := Limit(b, limit)

	var wg sync.WaitGroup
	for range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v.View(t.Context(), "pass://share/item")
		}()
	}
	// wait until the limit is reached, then make sure nothing else starts
	for range limit {
		<-b.started
	}
	select {
	case <-b.started:
		t.Fatalf("a call started beyond the limit of %d", limit)
	case <-time.After(100 * time.Millisecond):
	}
	close(b.release)
	wg.Wait()

	if got := b.maxRunning.Load(); got != limit {
		t.Errorf("max concurrent calls = %d, want %d", got, limit)
	}
}

func TestLimitBusyWhenCallerGivesUp(t *testing.T) {
	b := &blockingViewer{started: make(chan struct{}, 2), release: make(chan struct{})}
	defer close(b.release)
	v := Limit(b, 1)

	// fill the only slot
	go v.View(t.Context(), "pass://share/item")
	<-b.started

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := v.View(ctx, "pass://share/item")

	if !errors.Is(err, ErrBusy) {
		t.Errorf("View error = %v, want ErrBusy", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("View error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

func TestLimitReleasesSlotOnError(t *testing.T) {
	v := Limit(&fakeViewer{err: errors.New("pass-cli failed")}, 1)

	// with a leaked slot, the second call would block until the deadline
	for i := range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		_, err := v.View(ctx, "pass://share/item")
		cancel()
		if errors.Is(err, ErrBusy) {
			t.Fatalf("call %d: slot not released after an error", i+1)
		}
	}
}
