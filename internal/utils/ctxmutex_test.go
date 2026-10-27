package utils_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hugz6/protonpass-server/internal/utils"
)

// TestLockContextShouldTimeout check if a timeout context prevent Lock() from blocking the goroutine.
func TestLockContextShouldTimeout(t *testing.T) {
	var wg sync.WaitGroup
	m := utils.NewCtxMutex()

	s2 := make(chan struct{})

	// first goroutine should acquire Lock
	wg.Go(func() {
		b := m.Lock(t.Context())
		if !b {
			t.Errorf("b = %t, want %t", b, true)
		}
		// let second goroutine start
		close(s2)
		time.Sleep(100 * time.Millisecond)
		m.Unlock()
	})

	// second goroutines wait for the first to have acquired lock then should failed to acquire lock within timeout
	wg.Go(func() {
		// wait for the first goroutine to acquire lock
		<-s2

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		b := m.Lock(ctx)
		if b {
			t.Errorf("b = %t, want %t", b, false)
			m.Unlock()
		}
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("error = %v, want %v", ctx.Err(), context.DeadlineExceeded)
		}
	})
	wg.Wait()
}

// TestLockNormal checks the normal behavior of the CtxMutex.
func TestLockNormal(t *testing.T) {
	var wg sync.WaitGroup
	m := utils.NewCtxMutex()
	s2 := make(chan any, 1)

	wg.Go(func() {
		b := m.Lock(t.Context())
		if !b {
			t.Errorf("b = %t, want %t", b, true)
		}
		close(s2)
		defer m.Unlock()
		time.Sleep(50 * time.Millisecond)
	})

	wg.Go(func() {
		// wait for first goroutine to have acquired lock
		<-s2
		b := m.Lock(t.Context())
		defer m.Unlock()
		if !b {
			t.Errorf("b = %t, want %t", b, false)
		}
	})
	wg.Wait()
}

func TestUnlockUnlockedPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Unlock of an unlocked CtxMutex did not panic")
		}
	}()
	utils.NewCtxMutex().Unlock()
}
