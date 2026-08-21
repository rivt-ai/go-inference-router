package router_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/router"
	"github.com/rivt-ai/go-inference-router/router/config"
)

type source struct{ provider *fakeProvider }

func (s source) Available(_ context.Context, _ string, _ config.Provider) bool { return true }
func (s source) Open(_ context.Context, _ string, _ config.Provider) (llm.Provider, error) {
	return s.provider, nil
}

type lifecycleSource struct {
	mu     sync.Mutex
	opened []*lifecycleProvider
}

func (*lifecycleSource) Available(context.Context, string, config.Provider) bool { return true }
func (s *lifecycleSource) Open(_ context.Context, _ string, definition config.Provider) (llm.Provider, error) {
	provider := &lifecycleProvider{
		block: definition.Type == "blocked", closeErr: definition.Type == "uncloseable",
		started: make(chan struct{}), closed: make(chan struct{}),
	}
	s.mu.Lock()
	s.opened = append(s.opened, provider)
	s.mu.Unlock()
	return provider, nil
}
func (s *lifecycleSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.opened)
}
func (s *lifecycleSource) provider(index int) *lifecycleProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened[index]
}

type lifecycleProvider struct {
	mu        sync.Mutex
	request   llm.Request
	block     bool
	closeErr  bool
	started   chan struct{}
	startOnce sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func (*lifecycleProvider) Name() string { return "lifecycle" }
func (p *lifecycleProvider) Chat(ctx context.Context, request llm.Request) (*llm.Response, error) {
	p.mu.Lock()
	p.request = request
	p.mu.Unlock()
	p.startOnce.Do(func() { close(p.started) })
	if p.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &llm.Response{Message: llm.AssistantMessage("ok")}, nil
}
func (p *lifecycleProvider) nestedOption() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.request.Extra["nested"].([]string)[0]
}
func (p *lifecycleProvider) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })
	if p.closeErr {
		return errors.New("close failed")
	}
	return nil
}

type observationRecorder struct {
	mu     sync.Mutex
	events []llm.Observation
}

func (r *observationRecorder) Observe(_ context.Context, event llm.Observation) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}
func (r *observationRecorder) contains(operation llm.ObservationOperation, phase llm.ObservationPhase) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.Operation == operation && event.Phase == phase {
			return true
		}
	}
	return false
}

func lifecycleConfig(providerType, model string) config.Config {
	return config.Config{
		Version:   config.Version,
		Providers: map[string]config.Provider{"p": {Type: providerType}},
		Models:    map[string]config.ModelProfile{"m": {Provider: "p", Model: model}},
	}
}

type fakeProvider struct{ request llm.Request }

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) Chat(_ context.Context, req llm.Request) (*llm.Response, error) {
	p.request = req
	return &llm.Response{Model: req.Model, Message: llm.AssistantMessage("ok")}, nil
}

func TestApplyCancelsChangedProviderAndPreservesProfileOnlyChange(t *testing.T) {
	source := &lifecycleSource{}
	recorder := &observationRecorder{}
	r, err := router.New(lifecycleConfig("blocked", "old"), source, recorder)
	if err != nil {
		t.Fatal(err)
	}
	chatDone := make(chan error, 1)
	go func() {
		_, err := r.Chat(context.Background(), "m", llm.Request{}, nil)
		chatDone <- err
	}()
	for source.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	first := source.provider(0)
	<-first.started
	if err := r.Apply(context.Background(), lifecycleConfig("ready", "new")); err != nil {
		t.Fatal(err)
	}
	if err := <-chatDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("changed provider call error = %v", err)
	}
	select {
	case <-first.closed:
	case <-time.After(time.Second):
		t.Fatal("changed provider was not closed")
	}
	if _, err := r.Chat(context.Background(), "m", llm.Request{}, nil); err != nil || source.count() != 2 {
		t.Fatalf("reopened provider count=%d, err=%v", source.count(), err)
	}
	if err := r.Apply(context.Background(), lifecycleConfig("ready", "another-model")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Chat(context.Background(), "m", llm.Request{}, nil); err != nil || source.count() != 2 {
		t.Fatalf("profile-only apply reopened provider: count=%d, err=%v", source.count(), err)
	}
	if !recorder.contains(llm.ObservationConfigApply, llm.ObservationFinished) ||
		!recorder.contains(llm.ObservationProviderClose, llm.ObservationFinished) {
		t.Fatal("missing apply or close observation")
	}
}

func TestStopProviderReopensLazilyAndApplySnapshotsConfig(t *testing.T) {
	source := &lifecycleSource{}
	cfg := lifecycleConfig("ready", "model")
	cfg.Models["m"] = config.ModelProfile{
		Provider: "p", Model: "model", Options: map[string]any{"nested": []string{"original"}},
	}
	r, err := router.New(cfg, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Models["m"].Options["nested"].([]string)[0] = "mutated"
	if _, err := r.Chat(context.Background(), "m", llm.Request{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := source.provider(0).nestedOption(); got != "original" {
		t.Fatalf("Router config was mutated through caller map: %q", got)
	}
	if err := r.StopProvider(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Chat(context.Background(), "m", llm.Request{}, nil); err != nil || source.count() != 2 {
		t.Fatalf("stop did not lazily reopen: count=%d, err=%v", source.count(), err)
	}
	next := lifecycleConfig("ready", "model")
	next.Registry.URL = "https://example.test/registry"
	if err := r.Apply(context.Background(), next); !llm.IsKind(err, llm.KindInvalidRequest) {
		t.Fatalf("registry apply error = %v", err)
	}
}

func TestRemoveProviderVersionStopsUnpinnedAndProtectsPins(t *testing.T) {
	source := &lifecycleSource{}
	r, err := router.New(lifecycleConfig("ready", "model"), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Chat(context.Background(), "m", llm.Request{}, nil); err != nil {
		t.Fatal(err)
	}
	first := source.provider(0)
	removing := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := r.RemoveProviderVersion(context.Background(), "ready", "v1.0.0", func(context.Context, string, string) (bool, error) {
			select {
			case <-first.closed:
			case <-time.After(time.Second):
				return false, errors.New("provider was not closed before removal")
			}
			close(removing)
			<-release
			return true, nil
		})
		done <- err
	}()
	<-removing
	if _, err := r.Chat(context.Background(), "m", llm.Request{}, nil); !llm.IsKind(err, llm.KindUnavailable) {
		t.Fatalf("Chat during removal error = %v", err)
	}
	pinned := lifecycleConfig("ready", "model")
	pinned.Providers["p"] = config.Provider{Type: "ready", Version: "v1.0.0"}
	if err := r.Apply(context.Background(), pinned); !llm.IsKind(err, llm.KindInvalidRequest) {
		t.Fatalf("Apply pin during removal error = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := r.Chat(context.Background(), "m", llm.Request{}, nil); err != nil || source.count() != 2 {
		t.Fatalf("provider did not reopen after removal: count=%d, err=%v", source.count(), err)
	}
	if err := r.Apply(context.Background(), pinned); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := r.RemoveProviderVersion(context.Background(), "ready", "v1.0.0", func(context.Context, string, string) (bool, error) {
		called = true
		return true, nil
	}); !llm.IsKind(err, llm.KindInvalidRequest) || called {
		t.Fatalf("pinned Remove = called %v, err %v", called, err)
	}
}

func TestRemoveProviderVersionRetiresEveryEntryDespiteCloseErrors(t *testing.T) {
	source := &lifecycleSource{}
	cfg := lifecycleConfig("uncloseable", "model")
	cfg.Providers["q"] = config.Provider{Type: "uncloseable"}
	cfg.Models["n"] = config.ModelProfile{Provider: "q", Model: "model"}
	r, err := router.New(cfg, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"m", "n"} {
		if _, err := r.Chat(context.Background(), profile, llm.Request{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	_, err = r.RemoveProviderVersion(context.Background(), "uncloseable", "v1.0.0", func(context.Context, string, string) (bool, error) {
		called = true
		return true, nil
	})
	if err == nil || called {
		t.Fatalf("RemoveProviderVersion = called %v, err %v", called, err)
	}
	// A detached entry is no longer in the Router's open set, so a bailout after
	// the first close error would strand the second provider process forever.
	for index := 0; index < 2; index++ {
		select {
		case <-source.provider(index).closed:
		case <-time.After(time.Second):
			t.Fatalf("provider %d was not closed", index)
		}
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	_, err := router.New(config.Config{}, &lifecycleSource{}, nil)
	if err == nil {
		t.Fatal("New accepted invalid config")
	}
}
func (p *fakeProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "raw-model"}}, nil
}
func (p *fakeProvider) Capabilities(context.Context, string) (llm.Capabilities, error) {
	return llm.Capabilities{Streaming: true, Tools: true, InputModalities: []llm.Modality{llm.ModalityText}}, nil
}

func TestRouterSelectsProfilesAndKeepsDiscoveryInformational(t *testing.T) {
	provider := &fakeProvider{}
	r, err := router.New(config.Config{
		Version:   1,
		Providers: map[string]config.Provider{"openai": {Type: "openai"}},
		Models: map[string]config.ModelProfile{
			"gpt": {Provider: "openai", Model: "gpt-5", Options: map[string]any{"verbosity": "low"}},
		},
	}, source{provider: provider}, nil)
	if err != nil {
		t.Fatal(err)
	}

	profiles := r.Profiles(context.Background())
	if len(profiles) != 1 || profiles[0].ID != "gpt" || !profiles[0].Available {
		t.Fatalf("profiles = %#v", profiles)
	}
	models, err := r.Discover(context.Background(), "openai")
	if err != nil || len(models) != 1 || models[0].Model.ID != "raw-model" {
		t.Fatalf("Discover = %#v, %v", models, err)
	}
	resp, err := r.Chat(context.Background(), "gpt", llm.Request{}, nil)
	if err != nil || resp.Model != "gpt-5" {
		t.Fatalf("Chat = %#v, %v", resp, err)
	}
	if provider.request.Extra["verbosity"] != "low" {
		t.Fatalf("request options = %#v", provider.request.Extra)
	}
	if _, err := r.Chat(context.Background(), "raw-model", llm.Request{}, nil); !llm.IsKind(err, llm.KindInvalidRequest) {
		t.Fatalf("raw model error = %v", err)
	}
}

// A non-empty request model must win over the profile pin while the profile
// keeps supplying provider and options; hosts switch models at runtime this
// way instead of mutating configuration through Apply.
func TestChatRequestModelOverridesProfilePin(t *testing.T) {
	provider := &fakeProvider{}
	r, err := router.New(config.Config{
		Version:   1,
		Providers: map[string]config.Provider{"openai": {Type: "openai"}},
		Models: map[string]config.ModelProfile{
			"gpt": {Provider: "openai", Model: "gpt-5", Options: map[string]any{"verbosity": "low"}},
		},
	}, source{provider: provider}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Chat(context.Background(), "gpt", llm.Request{Model: "gpt-5-mini"}, nil)
	if err != nil || resp.Model != "gpt-5-mini" {
		t.Fatalf("override Chat = %#v, %v", resp, err)
	}
	if provider.request.Extra["verbosity"] != "low" {
		t.Fatalf("profile options must still apply: %#v", provider.request.Extra)
	}
	resp, err = r.Chat(context.Background(), "gpt", llm.Request{}, nil)
	if err != nil || resp.Model != "gpt-5" {
		t.Fatalf("pinned Chat = %#v, %v", resp, err)
	}
}
