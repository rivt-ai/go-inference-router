package inference

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryPolicyRetriesRetryableKindsAndStops(t *testing.T) {
	calls := 0
	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond}
	err := policy.Do(context.Background(), func(context.Context) error {
		calls++
		return &Error{Kind: KindUnavailable}
	})
	if calls != 3 || !IsKind(err, KindUnavailable) {
		t.Fatalf("calls = %d, err = %v", calls, err)
	}
	calls = 0
	err = policy.Do(context.Background(), func(context.Context) error {
		calls++
		if calls < 2 {
			return &Error{Kind: KindTransport}
		}
		return nil
	})
	if calls != 2 || err != nil {
		t.Fatalf("calls = %d, err = %v", calls, err)
	}
}

func TestRetryPolicyDoesNotRetryUnsafeErrors(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}
	for _, err := range []error{&Error{Kind: KindAuth}, &Error{Kind: KindStalled}, errors.New("plain")} {
		calls := 0
		_ = policy.Do(context.Background(), func(context.Context) error {
			calls++
			return err
		})
		if calls != 1 {
			t.Fatalf("%v: calls = %d, want 1", err, calls)
		}
	}
	calls := 0
	stalled := RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, RetryStalled: true}
	_ = stalled.Do(context.Background(), func(context.Context) error {
		calls++
		return &Error{Kind: KindStalled}
	})
	if calls != 2 {
		t.Fatalf("opt-in stalled retry calls = %d, want 2", calls)
	}
}

func TestRetryPolicyHonorsRetryAfterAndContext(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond}
	var waited time.Duration
	policy.Observer = ObserverFunc(func(_ context.Context, event Observation) {
		if event.Operation == ObservationRetry {
			waited = event.Duration
		}
	})
	started := time.Now()
	_ = policy.Do(context.Background(), func(context.Context) error {
		return &Error{Kind: KindRateLimit, RetryAfter: 30 * time.Millisecond}
	})
	if waited != 30*time.Millisecond {
		t.Fatalf("observed wait = %v, want the RetryAfter hint", waited)
	}
	if elapsed := time.Since(started); elapsed < 30*time.Millisecond {
		t.Fatalf("returned after %v, before the hint elapsed", elapsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := policy.Do(ctx, func(context.Context) error {
		calls++
		return &Error{Kind: KindUnavailable}
	})
	if calls != 1 || !IsKind(err, KindUnavailable) {
		t.Fatalf("canceled ctx: calls = %d, err = %v", calls, err)
	}
}
