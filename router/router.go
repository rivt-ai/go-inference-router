// Package router resolves Model Profiles and routes requests to built-in or
// process-backed providers.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/install"
)

// Source is the seam between profile routing and provider lifecycle.
type Source interface {
	Available(context.Context, string, config.Provider) bool
	Open(context.Context, string, config.Provider) (llm.Provider, error)
}

// Router resolves Model Profiles and owns lazily opened providers.
type Router struct {
	cfg      config.Config
	source   Source
	observer llm.Observer
	mu       sync.Mutex
	open     map[string]*providerEntry
	removing map[string]string
	closed   bool

	// Set by Open only; nil for a Router built with New.
	installer *install.Installer
	reload    func(context.Context) (config.Config, error)
}

type providerEntry struct {
	definition config.Provider
	ctx        context.Context
	cancel     context.CancelFunc
	ready      chan struct{}
	provider   llm.Provider
	err        error
	closeOnce  sync.Once
}

// New creates a Router using source for provider lifecycle.
func New(cfg config.Config, source Source, observer llm.Observer) (*Router, error) {
	if source == nil {
		return nil, errors.New("router source is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Router{
		cfg: cloneConfig(cfg), source: source, observer: observer,
		open: map[string]*providerEntry{}, removing: map[string]string{},
	}, nil
}

// Apply atomically replaces Provider Definitions and Model Profiles. Registry
// settings are process-bootstrap state and require a restart.
func (r *Router) Apply(ctx context.Context, next config.Config) error {
	started := r.started(ctx, llm.Observation{Operation: llm.ObservationConfigApply})
	if err := next.Validate(); err != nil {
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return err
	}
	next = cloneConfig(next)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		err := routerClosedError()
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return err
	}
	for _, definition := range next.Providers {
		if version, removing := r.removing[definition.Type]; removing && definition.Version == version {
			r.mu.Unlock()
			err := &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: "provider version removal is in progress"}
			r.finished(ctx, started, err, llm.Usage{}, 0)
			return err
		}
	}
	if !next.Registry.Equal(r.cfg.Registry) {
		r.mu.Unlock()
		err := &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: "registry changes require a Router restart"}
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return err
	}
	var retired map[string]*providerEntry
	for id, entry := range r.open {
		definition, ok := next.Providers[id]
		if ok && reflect.DeepEqual(entry.definition, definition) {
			continue
		}
		if retired == nil {
			retired = map[string]*providerEntry{}
		}
		retired[id] = entry
		delete(r.open, id)
	}
	r.cfg = next
	r.mu.Unlock()
	for id, entry := range retired {
		_ = r.retire(ctx, id, entry)
	}
	r.finished(ctx, started, nil, llm.Usage{}, 0)
	return nil
}

// StopProvider cancels and closes one opened Provider Definition. The next
// request lazily opens it again.
func (r *Router) StopProvider(ctx context.Context, id string) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return routerClosedError()
	}
	if _, ok := r.cfg.Providers[id]; !ok {
		r.mu.Unlock()
		return &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: fmt.Sprintf("unknown provider %q", id)}
	}
	entry := r.open[id]
	delete(r.open, id)
	r.mu.Unlock()
	return r.retire(ctx, id, entry)
}

// RemoveProviderVersion stops unpinned users of a provider type while remove
// atomically deletes one cached version.
func (r *Router) RemoveProviderVersion(
	ctx context.Context,
	providerType, version string,
	remove func(context.Context, string, string) (bool, error),
) (bool, error) {
	if remove == nil {
		return false, errors.New("provider version remover is required")
	}
	retired, err := r.beginRemoval(providerType, version)
	if err != nil {
		return false, err
	}
	defer func() {
		r.mu.Lock()
		delete(r.removing, providerType)
		r.mu.Unlock()
	}()
	// Every detached entry is retired even after the first failure: they are
	// already out of r.open, so nothing else would ever close them.
	var retireErr error
	for id, entry := range retired {
		if err := r.retireAndWait(ctx, id, entry); err != nil && retireErr == nil {
			retireErr = err
		}
	}
	if retireErr != nil {
		return false, retireErr
	}
	return remove(ctx, providerType, version)
}

// beginRemoval claims the removal slot for providerType and detaches the open
// unpinned entries that its caller must then retire.
func (r *Router) beginRemoval(providerType, version string) (map[string]*providerEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, routerClosedError()
	}
	if _, ok := r.removing[providerType]; ok {
		return nil, &llm.Error{Kind: llm.KindUnavailable, Provider: providerType, Message: "provider removal is already in progress"}
	}
	for _, definition := range r.cfg.Providers {
		if definition.Type == providerType && definition.Version == version {
			return nil, &llm.Error{Kind: llm.KindInvalidRequest, Provider: providerType, Message: "provider version is pinned by configuration"}
		}
	}
	r.removing[providerType] = version
	retired := map[string]*providerEntry{}
	for id, entry := range r.open {
		if definition := r.cfg.Providers[id]; definition.Type == providerType && definition.Version == "" {
			retired[id] = entry
			delete(r.open, id)
		}
	}
	return retired, nil
}

// Profiles returns the selectable Model Profiles in stable order.
func (r *Router) Profiles(ctx context.Context) []llmv1.Profile {
	r.mu.Lock()
	cfg := cloneConfig(r.cfg)
	r.mu.Unlock()
	ids := sortedKeys(cfg.Models)
	profiles := make([]llmv1.Profile, 0, len(ids))
	for _, id := range ids {
		profile := cfg.Models[id]
		definition, ok := cfg.Providers[profile.Provider]
		available := ok && r.source.Available(ctx, profile.Provider, definition)
		profiles = append(profiles, llmv1.Profile{
			ID: id, Provider: profile.Provider, Model: profile.Model, Available: available,
		})
	}
	return profiles
}

// Discover returns informational raw model IDs.
func (r *Router) Discover(ctx context.Context, providerID string) ([]llmv1.DiscoveredModel, error) {
	started := r.started(ctx, llm.Observation{Operation: llm.ObservationDiscover, ProviderID: providerID})
	var resultErr error
	defer func() { r.finished(ctx, started, resultErr, llm.Usage{}, 0) }()
	r.mu.Lock()
	cfg := cloneConfig(r.cfg)
	closed := r.closed
	r.mu.Unlock()
	if closed {
		resultErr = routerClosedError()
		return nil, resultErr
	}
	ids := []string{providerID}
	if providerID == "" {
		ids = sortedKeys(cfg.Providers)
	}
	var discovered []llmv1.DiscoveredModel
	for _, id := range ids {
		entry, err := r.providerEntry(ctx, id)
		if err != nil {
			resultErr = err
			return nil, resultErr
		}
		callCtx, done := generationContext(ctx, entry.ctx)
		lister, ok := entry.provider.(llm.ModelLister)
		if !ok {
			done()
			continue
		}
		models, err := lister.ListModels(callCtx)
		done()
		if err != nil {
			resultErr = err
			return nil, resultErr
		}
		for _, model := range models {
			discovered = append(discovered, llmv1.DiscoveredModel{Provider: id, Model: model})
		}
	}
	return discovered, nil
}

// Capabilities reports behavior and metadata for one Model Profile.
func (r *Router) Capabilities(ctx context.Context, profileID string) (llm.Capabilities, llm.Metadata, error) {
	started := r.started(ctx, llm.Observation{Operation: llm.ObservationCapabilities, ProfileID: profileID})
	profile, provider, callCtx, done, err := r.profileProvider(ctx, profileID)
	if err != nil {
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return llm.Capabilities{}, llm.Metadata{}, err
	}
	defer done()
	started.ProviderID, started.Model = profile.Provider, profile.Model
	capabilities := inferredCapabilities(provider)
	if reporter, ok := provider.(llm.CapabilityReporter); ok {
		capabilities, err = reporter.Capabilities(callCtx, profile.Model)
		if err != nil {
			r.finished(ctx, started, err, llm.Usage{}, 0)
			return llm.Capabilities{}, llm.Metadata{}, err
		}
	}
	var metadata llm.Metadata
	if reporter, ok := provider.(llm.MetadataReporter); ok {
		metadata, err = reporter.ModelMetadata(callCtx, profile.Model)
	}
	r.finished(ctx, started, err, llm.Usage{}, 0)
	return capabilities, metadata, err
}

// Chat resolves profileID and runs a streaming or non-streaming model call.
func (r *Router) Chat(
	ctx context.Context,
	profileID string,
	request llm.Request,
	onEvent func(llm.Event) error,
) (*llm.Response, error) {
	started := r.started(ctx, llm.Observation{Operation: llm.ObservationChat, ProfileID: profileID, Streaming: onEvent != nil})
	profile, provider, callCtx, done, err := r.profileProvider(ctx, profileID)
	if err != nil {
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return nil, err
	}
	defer done()
	started.ProviderID, started.Model = profile.Provider, profile.Model
	request.Model = profile.Model
	request.Extra = merge(profile.Options, request.Extra)
	if onEvent == nil {
		response, err := provider.Chat(callCtx, request)
		var usage llm.Usage
		if response != nil {
			usage = response.Usage
		}
		r.finished(ctx, started, err, usage, 0)
		return response, err
	}
	streamer, ok := provider.(llm.Streamer)
	if !ok {
		err := &llm.Error{Kind: llm.KindInvalidRequest, Provider: provider.Name(), Message: "profile does not support streaming"}
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return nil, err
	}
	var events uint64
	response, err := streamer.ChatStream(callCtx, request, func(event llm.Event) error {
		events++
		return onEvent(event)
	})
	var usage llm.Usage
	if response != nil {
		usage = response.Usage
	}
	r.finished(ctx, started, err, usage, events)
	return response, err
}

// Embed resolves profileID and returns vectors from an embedding-capable provider.
func (r *Router) Embed(ctx context.Context, profileID string, request llm.EmbeddingRequest) ([][]float32, error) {
	started := r.started(ctx, llm.Observation{Operation: llm.ObservationEmbed, ProfileID: profileID})
	profile, provider, callCtx, done, err := r.profileProvider(ctx, profileID)
	if err != nil {
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return nil, err
	}
	defer done()
	started.ProviderID, started.Model = profile.Provider, profile.Model
	embedder, ok := provider.(llm.Embedder)
	if !ok {
		err := &llm.Error{Kind: llm.KindInvalidRequest, Provider: provider.Name(), Message: "profile does not support embeddings"}
		r.finished(ctx, started, err, llm.Usage{}, 0)
		return nil, err
	}
	request.Model = profile.Model
	vectors, err := embedder.Embed(callCtx, request)
	r.finished(ctx, started, err, llm.Usage{}, 0)
	return vectors, err
}

// Status reports configured provider availability and lifecycle state.
func (r *Router) Status(ctx context.Context) []llmv1.ProviderStatus {
	r.mu.Lock()
	cfg := cloneConfig(r.cfg)
	opened := make(map[string]bool, len(r.open))
	for id, entry := range r.open {
		opened[id] = entry.provider != nil
	}
	r.mu.Unlock()
	ids := sortedKeys(cfg.Providers)
	statuses := make([]llmv1.ProviderStatus, 0, len(ids))
	for _, id := range ids {
		definition := cfg.Providers[id]
		state := llmv1.ProviderStopped
		if !r.source.Available(ctx, id, definition) {
			state = llmv1.ProviderInstallRequired
		}
		if opened[id] {
			state = llmv1.ProviderAvailable
		}
		statuses = append(statuses, llmv1.ProviderStatus{ID: id, Type: definition.Type, State: state, Version: definition.Version})
	}
	return statuses
}

// Close stops every opened provider.
func (r *Router) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	opened := r.open
	r.open = map[string]*providerEntry{}
	r.mu.Unlock()
	var errs []error
	for id, entry := range opened {
		if err := r.retire(context.Background(), id, entry); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Router) profileProvider(ctx context.Context, profileID string) (config.ModelProfile, llm.Provider, context.Context, func(), error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return config.ModelProfile{}, nil, nil, nil, routerClosedError()
	}
	profile, ok := r.cfg.Models[profileID]
	r.mu.Unlock()
	if !ok {
		return config.ModelProfile{}, nil, nil, nil, &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: fmt.Sprintf("unknown model profile %q", profileID)}
	}
	entry, err := r.providerEntry(ctx, profile.Provider)
	if err != nil {
		return config.ModelProfile{}, nil, nil, nil, err
	}
	callCtx, done := generationContext(ctx, entry.ctx)
	return profile, entry.provider, callCtx, done, nil
}

func (r *Router) providerEntry(ctx context.Context, id string) (*providerEntry, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, routerClosedError()
	}
	definition, ok := r.cfg.Providers[id]
	if !ok {
		r.mu.Unlock()
		return nil, &llm.Error{Kind: llm.KindInvalidRequest, Provider: "router", Message: fmt.Sprintf("unknown provider %q", id)}
	}
	if _, removing := r.removing[definition.Type]; removing {
		r.mu.Unlock()
		return nil, &llm.Error{Kind: llm.KindUnavailable, Provider: id, Message: "provider removal is in progress"}
	}
	if entry := r.open[id]; entry != nil {
		r.mu.Unlock()
		select {
		case <-entry.ready:
			return entry, entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	entryCtx, cancel := context.WithCancel(context.Background())
	entry := &providerEntry{definition: definition, ctx: entryCtx, cancel: cancel, ready: make(chan struct{})}
	r.open[id] = entry
	r.mu.Unlock()
	openCtx, done := generationContext(ctx, entryCtx)
	started := r.started(ctx, llm.Observation{
		Operation: llm.ObservationProviderOpen, ProviderID: id,
		ProviderType: definition.Type, Version: definition.Version,
	})
	provider, err := r.source.Open(openCtx, id, definition)
	done()
	r.mu.Lock()
	entry.provider, entry.err = provider, err
	stale := r.open[id] != entry
	if err != nil && !stale {
		delete(r.open, id)
	}
	close(entry.ready)
	r.mu.Unlock()
	r.finished(ctx, started, err, llm.Usage{}, 0)
	if stale {
		_ = r.closeEntry(ctx, id, entry)
	}
	if err != nil {
		return nil, err
	}
	return entry, nil
}

func (r *Router) retire(ctx context.Context, id string, entry *providerEntry) error {
	if entry == nil {
		return nil
	}
	entry.cancel()
	select {
	case <-entry.ready:
		return r.closeEntry(ctx, id, entry)
	default:
		return nil
	}
}

func (r *Router) retireAndWait(ctx context.Context, id string, entry *providerEntry) error {
	if entry == nil {
		return nil
	}
	entry.cancel()
	select {
	case <-entry.ready:
		return r.closeEntry(ctx, id, entry)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Router) closeEntry(ctx context.Context, id string, entry *providerEntry) (closeErr error) {
	entry.closeOnce.Do(func() {
		if entry.provider == nil {
			return
		}
		started := r.started(ctx, llm.Observation{
			Operation: llm.ObservationProviderClose, ProviderID: id,
			ProviderType: entry.definition.Type, Version: entry.definition.Version,
		})
		if closer, ok := entry.provider.(io.Closer); ok {
			closeErr = closer.Close()
		}
		r.finished(ctx, started, closeErr, llm.Usage{}, 0)
	})
	return closeErr
}

func (r *Router) started(ctx context.Context, event llm.Observation) llm.Observation {
	event.Phase = llm.ObservationStarted
	event.Time = time.Now().UTC()
	llm.EmitObservation(ctx, r.observer, event)
	return event
}

func (r *Router) finished(ctx context.Context, event llm.Observation, err error, usage llm.Usage, streamEvents uint64) {
	event.Phase = llm.ObservationFinished
	event.Duration = time.Since(event.Time)
	event.Time = time.Now().UTC()
	event.Err = err
	event.Usage = usage
	event.StreamEvents = streamEvents
	llm.EmitObservation(ctx, r.observer, event)
}

func generationContext(parent, generation context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(generation, cancel)
	return ctx, func() { stop(); cancel() }
}

func routerClosedError() error {
	return &llm.Error{Kind: llm.KindUnavailable, Provider: "router", Message: "Router is closed"}
}

func cloneConfig(cfg config.Config) config.Config {
	cloned := cfg
	cloned.Registry.PublicKeys = slices.Clone(cfg.Registry.PublicKeys)
	cloned.Providers = make(map[string]config.Provider, len(cfg.Providers))
	for id, definition := range cfg.Providers {
		definition.Headers = maps.Clone(definition.Headers)
		definition.Secrets = maps.Clone(definition.Secrets)
		definition.Options = cloneOptions(definition.Options)
		cloned.Providers[id] = definition
	}
	cloned.Models = make(map[string]config.ModelProfile, len(cfg.Models))
	for id, profile := range cfg.Models {
		profile.Options = cloneOptions(profile.Options)
		cloned.Models[id] = profile
	}
	return cloned
}

func cloneOptions(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	cloned := cloneReflect(reflect.ValueOf(value))
	if !cloned.IsValid() {
		return nil
	}
	return cloned.Interface()
}

func cloneReflect(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneReflect(value.Elem())
		result := reflect.New(value.Type()).Elem()
		result.Set(cloned)
		return result
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			cloned.SetMapIndex(iterator.Key(), cloneReflect(iterator.Value()))
		}
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			cloned.Index(index).Set(cloneReflect(value.Index(index)))
		}
		return cloned
	default:
		return value
	}
}

func inferredCapabilities(provider llm.Provider) llm.Capabilities {
	_, streaming := provider.(llm.Streamer)
	_, embeddings := provider.(llm.Embedder)
	return llm.Capabilities{Streaming: streaming, Embeddings: embeddings, InputModalities: []llm.Modality{llm.ModalityText}, MaxConcurrency: 1}
}

func merge(defaults, overrides map[string]any) map[string]any {
	if len(defaults) == 0 && len(overrides) == 0 {
		return nil
	}
	result := make(map[string]any, len(defaults)+len(overrides))
	for key, value := range defaults {
		result[key] = value
	}
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
