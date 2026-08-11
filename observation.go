package inference

import (
	"context"
	"time"
)

// ObservationOperation identifies one observable runtime operation.
type ObservationOperation string

// Observable runtime operations.
const (
	ObservationConfigApply      ObservationOperation = "config.apply"
	ObservationProviderLocate   ObservationOperation = "provider.locate"
	ObservationProviderOpen     ObservationOperation = "provider.open"
	ObservationProviderClose    ObservationOperation = "provider.close"
	ObservationChat             ObservationOperation = "request.chat"
	ObservationEmbed            ObservationOperation = "request.embed"
	ObservationDiscover         ObservationOperation = "request.discover"
	ObservationCapabilities     ObservationOperation = "request.capabilities"
	ObservationInstallPlan      ObservationOperation = "install.plan"
	ObservationInstallApprove   ObservationOperation = "install.approve"
	ObservationInstallAvailable ObservationOperation = "install.available"
	ObservationInstallRemove    ObservationOperation = "install.remove"
	ObservationSDKRetry         ObservationOperation = "sdk.retry"
)

// ObservationPhase identifies the start or finish of an operation.
type ObservationPhase string

// Observation phases.
const (
	ObservationStarted  ObservationPhase = "started"
	ObservationFinished ObservationPhase = "finished"
)

// Observation is metadata about runtime behavior. It deliberately excludes
// prompts, model output, credentials, headers, environment, URLs, and paths.
type Observation struct {
	Operation    ObservationOperation
	Phase        ObservationPhase
	Time         time.Time
	Duration     time.Duration
	RequestID    string
	ProfileID    string
	ProviderID   string
	ProviderType string
	Model        string
	Version      string
	Streaming    bool
	Attempt      int
	Status       int
	StreamEvents uint64
	Usage        Usage
	Err          error
}

// Observer receives runtime observations. Implementations must be safe for
// concurrent use.
type Observer interface {
	Observe(context.Context, Observation)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(context.Context, Observation)

// Observe implements Observer.
func (f ObserverFunc) Observe(ctx context.Context, event Observation) { f(ctx, event) }

// EmitObservation reports event without allowing observer failures to affect
// inference.
func EmitObservation(ctx context.Context, observer Observer, event Observation) {
	if observer == nil {
		return
	}
	defer func() { _ = recover() }()
	observer.Observe(ctx, event)
}
