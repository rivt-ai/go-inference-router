package transport

import (
	"context"
	"testing"
	"time"
)

func TestStallGuardFiresOnSilence(t *testing.T) {
	guard := NewStallGuard(context.Background(), 20*time.Millisecond)
	defer guard.Stop()

	<-guard.Context().Done()
	if !guard.Stalled() {
		t.Error("guard cancelled the context but did not report a stall")
	}
}

// TestStallGuardResetKeepsRequestAlive is the property the whole design rests
// on: steady progress must never trip the watchdog, however long it runs.
func TestStallGuardResetKeepsRequestAlive(t *testing.T) {
	guard := NewStallGuard(context.Background(), 60*time.Millisecond)
	defer guard.Stop()

	for i := 0; i < 5; i++ {
		time.Sleep(20 * time.Millisecond)
		guard.Reset()
	}
	if guard.Stalled() {
		t.Error("guard fired despite steady progress")
	}
	if err := guard.Context().Err(); err != nil {
		t.Errorf("context ended early: %v", err)
	}
}

func TestStallGuardDistinguishesCallerCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	guard := NewStallGuard(parent, time.Minute)
	defer guard.Stop()

	cancel()
	<-guard.Context().Done()
	if guard.Stalled() {
		t.Error("caller cancellation must not be reported as a stall")
	}
}

func TestStallGuardDisabledByNonPositiveWindow(t *testing.T) {
	guard := NewStallGuard(context.Background(), -1)
	defer guard.Stop()

	guard.Reset() // must not panic without a timer
	time.Sleep(20 * time.Millisecond)
	if guard.Stalled() || guard.Context().Err() != nil {
		t.Error("a disabled guard must never cancel")
	}
}
