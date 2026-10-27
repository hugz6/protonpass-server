// Package utils gives useful functions.
package utils

import "context"

// CtxMutex is a mutex whose Lock can give up when a context ends, which
// sync.Mutex cannot.
type CtxMutex struct {
	c chan struct{}
}

// NewCtxMutex creates and initialize a new CtxMutex.
func NewCtxMutex() *CtxMutex {
	m := &CtxMutex{}
	m.c = make(chan struct{}, 1)
	return m
}

// Lock will block until it acquires lock or context is cancelled.
// If it acquires lock it will returns true, if not false.
func (m *CtxMutex) Lock(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case m.c <- struct{}{}:
		return true
	}
}

// Unlock release lock. Unlock panics if lock was never acquired.
func (m *CtxMutex) Unlock() {
	select {
	case <-m.c:
	default:
		panic("try to unlock non-locked CtxMutex")
	}
}
