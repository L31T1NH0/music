package main

import (
	"context"
	"sync"
	"time"
)

// taskControl gives long-running Bubble Tea commands a cancellation signal and
// lets shutdown wait until their child processes and temporary files are gone.
type taskControl struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once

	resourceMu      sync.Mutex
	resourceAdopted bool
	resourceCleanup func()
}

func newTaskControl(parent context.Context) (context.Context, *taskControl) {
	ctx, cancel := context.WithCancel(parent)
	return ctx, &taskControl{
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

func newTimedTaskControl(parent context.Context, timeout time.Duration) (context.Context, *taskControl) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, &taskControl{
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

func (t *taskControl) finish() {
	if t == nil {
		return
	}
	t.once.Do(func() { close(t.done) })
	t.resourceMu.Lock()
	resourcePending := !t.resourceAdopted && t.resourceCleanup != nil
	t.resourceMu.Unlock()
	if !resourcePending {
		t.cancelOnly()
	}
}

func (t *taskControl) cancelOnly() {
	if t != nil && t.cancel != nil {
		t.cancel()
	}
}

func (t *taskControl) cancelAndWait(timeout time.Duration) {
	if t == nil {
		return
	}
	t.cancelOnly()
	select {
	case <-t.done:
	case <-time.After(timeout):
	}
	t.cleanupUnadoptedResource()
}

func (t *taskControl) setResourceCleanup(cleanup func()) {
	if t == nil {
		return
	}
	t.resourceMu.Lock()
	defer t.resourceMu.Unlock()
	if t.resourceAdopted {
		return
	}
	t.resourceCleanup = cleanup
}

func (t *taskControl) adoptResource() {
	if t == nil {
		return
	}
	t.resourceMu.Lock()
	t.resourceAdopted = true
	t.resourceCleanup = nil
	t.resourceMu.Unlock()
	t.cancelOnly()
}

func (t *taskControl) cleanupUnadoptedResource() {
	if t == nil {
		return
	}
	t.resourceMu.Lock()
	if t.resourceAdopted || t.resourceCleanup == nil {
		t.resourceMu.Unlock()
		return
	}
	cleanup := t.resourceCleanup
	t.resourceCleanup = nil
	t.resourceMu.Unlock()
	cleanup()
}
