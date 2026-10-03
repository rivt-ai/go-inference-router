package router

import (
	"context"
	"slices"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/router/config"
)

// Decide executes a decision using only the profile's explicitly enabled model.
func (r *Router) Decide(ctx context.Context, profileID string, request llm.DecisionRequest) (response *llm.DecisionResponse, err error) {
	started := r.started(ctx, llm.Observation{Operation: llm.ObservationDecide, ProfileID: profileID})
	defer func() {
		var usage llm.Usage
		if response != nil {
			usage = response.Usage
		}
		r.finished(ctx, started, err, usage, 0)
	}()
	if err = r.decisionProfile(profileID, request.Model); err != nil {
		return nil, err
	}
	if err = request.Validate(); err != nil {
		return nil, err
	}
	profile, provider, callCtx, done, err := r.profileProvider(ctx, profileID)
	if err != nil {
		return nil, err
	}
	defer done()
	// Recheck the selected snapshot if configuration changed while opening.
	if !profile.Decisions || (request.Model != "" && request.Model != profile.Model) {
		return nil, &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: "decision profile changed"}
	}
	request.Model = profile.Model
	started.ProviderID, started.Model = profile.Provider, profile.Model
	decider, ok := provider.(llm.Decider)
	if !ok {
		return nil, &llm.Error{Kind: llm.KindInvalidRequest, Provider: provider.Name(), Message: "provider does not support decisions"}
	}
	if reporter, ok := provider.(llm.CapabilityReporter); ok {
		caps, err := reporter.Capabilities(callCtx, profile.Model)
		if err != nil {
			return nil, err
		}
		if !caps.Decisions {
			return nil, &llm.Error{Kind: llm.KindInvalidRequest, Provider: provider.Name(), Message: "provider does not support decisions"}
		}
	}
	return decider.Decide(callCtx, request)
}

func (r *Router) decisionProfile(id, model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return routerClosedError()
	}
	profile, ok := r.cfg.Models[id]
	if !ok || !profile.Decisions {
		return &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: "profile does not enable decisions"}
	}
	if model != "" && model != profile.Model {
		return &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: "decision model must match profile"}
	}
	return nil
}

// Derived allowlists participate in provider identity, so Apply retires an
// adapter when decision permissions change, including profile-only edits.
func decisionDefinition(cfg config.Config, id string) (config.Provider, bool) {
	definition, ok := cfg.Providers[id]
	if !ok || definition.Type != "openai-compatible" {
		return definition, ok
	}
	var models []string
	for _, profile := range cfg.Models {
		if profile.Provider == id && profile.Decisions {
			models = append(models, profile.Model)
		}
	}
	if len(models) == 0 {
		return definition, true
	}
	slices.Sort(models)
	definition.Options = merge(definition.Options, map[string]any{"decision_models": slices.Compact(models)})
	return definition, true
}

func decisionModels(definition config.Provider) []string {
	models, _ := definition.Options["decision_models"].([]string)
	return models
}
