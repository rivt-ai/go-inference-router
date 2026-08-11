//go:build e2e

package e2e

import (
	"errors"
	"testing"

	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/provider/openaicompat"
)

// TestProviderSatisfiesTheSeam is the reason the optional interfaces exist:
// a host type-asserts for what it needs, so this asserts the same way a host
// would.
func TestProviderSatisfiesTheSeam(t *testing.T) {
	var provider inference.Provider = sharedServer.client()

	if _, ok := provider.(inference.Streamer); !ok {
		t.Error("driver does not implement Streamer")
	}
	if _, ok := provider.(inference.Embedder); !ok {
		t.Error("driver does not implement Embedder")
	}
	if _, ok := provider.(inference.ModelLister); !ok {
		t.Error("driver does not implement ModelLister")
	}
	if _, ok := provider.(inference.MetadataReporter); !ok {
		t.Error("driver does not implement MetadataReporter")
	}
	if provider.Name() == "" {
		t.Error("driver reported an empty name")
	}
}

func TestListModelsReportsTheLoadedModel(t *testing.T) {
	models, err := sharedServer.client().ListModels(testContext(t))
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("provider reported no models")
	}
	for _, model := range models {
		if model.ID == "" {
			t.Errorf("model entry has an empty id: %+v", model)
		}
	}
}

// TestModelMetadataReportsContextWindow pins the /props path: the context
// window must be the one the engine was launched with, not a default we
// invented.
func TestModelMetadataReportsContextWindow(t *testing.T) {
	meta, err := sharedServer.client().ModelMetadata(testContext(t), "")
	if err != nil {
		t.Fatalf("ModelMetadata: %v", err)
	}
	if meta.ContextWindowTokens != contextSize {
		t.Errorf("context window = %d, want %d (the --ctx-size the server was launched with)",
			meta.ContextWindowTokens, contextSize)
	}
	if meta.Source == "" {
		t.Error("metadata did not record its source endpoint")
	}
}

// TestMetadataIsAbsentWithoutAConfiguredPath is the other half of the contract:
// a provider with no metadata endpoint reports nothing rather than failing, so
// a host can call it unconditionally.
func TestMetadataIsAbsentWithoutAConfiguredPath(t *testing.T) {
	plain := openaicompat.New(openaicompat.Config{BaseURL: sharedServer.baseURL})

	meta, err := plain.ModelMetadata(testContext(t), "")
	if err != nil {
		t.Fatalf("ModelMetadata without a configured path: %v", err)
	}
	if meta != (inference.Metadata{}) {
		t.Errorf("meta = %+v, want zero", meta)
	}
}

// TestEmbeddings needs the engine started with --embeddings, so it owns its
// own server rather than reconfiguring the shared one.
func TestEmbeddings(t *testing.T) {
	client := launchT(t, "--embeddings", "--pooling", "mean").client()

	texts := []string{"the sea is calm tonight", "the sea is calm tonight", "a lighthouse"}
	vectors, err := client.Embed(testContext(t), inference.EmbeddingRequest{Texts: texts})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != len(texts) {
		t.Fatalf("got %d vectors for %d texts", len(vectors), len(texts))
	}
	if len(vectors[0]) == 0 {
		t.Fatal("first vector is empty")
	}
	for i, vector := range vectors {
		if len(vector) != len(vectors[0]) {
			t.Fatalf("vector %d has dimension %d, want %d", i, len(vector), len(vectors[0]))
		}
	}
	// Identical inputs must produce identical vectors; if they do not, the
	// vectors are being returned out of request order.
	for i := range vectors[0] {
		if vectors[0][i] != vectors[1][i] {
			t.Fatalf("identical texts produced different vectors at index %d — request order is not preserved", i)
		}
	}
}

// asInferenceError is errors.As with the suite's concrete type, kept here so
// the chat tests read as assertions rather than as plumbing.
func asInferenceError(err error, target **inference.Error) bool {
	return errors.As(err, target)
}
