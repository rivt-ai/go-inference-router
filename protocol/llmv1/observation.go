package llmv1

import (
	"errors"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
)

// ObservationNotificationOf converts an in-process observation for llm.v1.
func ObservationNotificationOf(event llm.Observation) ObservationNotification {
	notification := ObservationNotification{
		Operation: event.Operation, Phase: event.Phase,
		Time: event.Time.UTC().Format(time.RFC3339Nano), DurationMillis: event.Duration.Milliseconds(),
		RequestID: event.RequestID, ProfileID: event.ProfileID, ProviderID: event.ProviderID,
		ProviderType: event.ProviderType, Model: event.Model, Version: event.Version,
		Streaming: event.Streaming, Attempt: event.Attempt, Status: event.Status,
		StreamEvents: event.StreamEvents, Usage: event.Usage,
	}
	if event.Err != nil {
		data := ErrorDataOf(event.Err)
		notification.Error = &data
	}
	return notification
}

// Observation converts a portable notification for in-process observers.
func (n ObservationNotification) Observation() llm.Observation {
	when, _ := time.Parse(time.RFC3339Nano, n.Time)
	event := llm.Observation{
		Operation: n.Operation, Phase: n.Phase, Time: when,
		Duration:  time.Duration(n.DurationMillis) * time.Millisecond,
		RequestID: n.RequestID, ProfileID: n.ProfileID, ProviderID: n.ProviderID,
		ProviderType: n.ProviderType, Model: n.Model, Version: n.Version,
		Streaming: n.Streaming, Attempt: n.Attempt, Status: n.Status,
		StreamEvents: n.StreamEvents, Usage: n.Usage,
	}
	if n.Error != nil {
		event.Err = &llm.Error{
			Kind: n.Error.Kind, Provider: n.Error.Provider, Status: n.Error.Status,
			Message: n.Error.Message, Err: errors.New(n.Error.Message),
		}
	}
	return event
}
