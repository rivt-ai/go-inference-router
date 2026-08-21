package inference

import (
	"context"
	"errors"
	"time"
)

// ObservationRetry reports one retry attempt made by RetryPolicy.
const ObservationRetry ObservationOperation = "request.retry"

// RetryPolicy retries an operation with exponential backoff, honoring the
// provider's RetryAfter hint when one was given.
//
// It is opt-in: the router itself never retries, because Retryable says a
// retry may help, not that it is safe. Safety stays the caller's — an agent
// that already showed streamed output to a user expresses its veto by
// returning a non-retryable error from fn or by not using the policy for that
// call. KindStalled is excluded by default for the same reason: a stalled
// stream has usually produced visible output already.
type RetryPolicy struct {
	// MaxAttempts is the total number of tries including the first.
	// Values below 1 mean 1: a single attempt, no retries.
	MaxAttempts int
	// BaseDelay seeds the exponential backoff (doubling per retry). Zero
	// means 500ms.
	BaseDelay time.Duration
	// MaxDelay caps the backoff. Zero means 30s. A larger RetryAfter hint
	// still wins: the provider's ask is authoritative.
	MaxDelay time.Duration
	// RetryStalled also retries KindStalled failures. Off by default: a
	// stalled stream has usually already produced caller-visible output, so
	// re-sending is not safe without the caller saying so.
	RetryStalled bool
	// Observer, when non-nil, receives an ObservationRetry per retry attempt.
	Observer Observer
}

// Do runs fn until it succeeds, exhausts MaxAttempts, fails with a
// non-retryable error, or ctx ends. It returns fn's last error.
func (p RetryPolicy) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	attempts, delay, maxDelay := p.settings()
	var err error
	for attempt := 1; ; attempt++ {
		if err = fn(ctx); err == nil || attempt >= attempts || !p.retryable(err) {
			return err
		}
		wait := retryWait(err, delay, maxDelay)
		EmitObservation(ctx, p.Observer, Observation{
			Operation: ObservationRetry, Phase: ObservationStarted, Time: time.Now().UTC(),
			Attempt: attempt, Duration: wait, Err: err,
		})
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
		delay *= 2
	}
}

// settings normalizes the zero values to the documented defaults.
func (p RetryPolicy) settings() (attempts int, base, maxDelay time.Duration) {
	attempts, base, maxDelay = p.MaxAttempts, p.BaseDelay, p.MaxDelay
	if attempts < 1 {
		attempts = 1
	}
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	return attempts, base, maxDelay
}

// retryWait caps the backoff at maxDelay, then lets a larger RetryAfter hint
// win: the provider's ask is authoritative.
func retryWait(err error, delay, maxDelay time.Duration) time.Duration {
	wait := delay
	if wait > maxDelay {
		wait = maxDelay
	}
	var llmErr *Error
	if errors.As(err, &llmErr) && llmErr.RetryAfter > wait {
		wait = llmErr.RetryAfter
	}
	return wait
}

func (p RetryPolicy) retryable(err error) bool {
	if IsKind(err, KindStalled) {
		return p.RetryStalled
	}
	return Retryable(err)
}
