package openaicompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rivt-ai/go-inference-router"
)

func TestListModels(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"a","owned_by":"me"},{"id":""},{"id":"b"}]}`)
	})
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "a" || models[0].OwnedBy != "me" || models[1].ID != "b" {
		t.Fatalf("models = %+v", models)
	}
}

func TestListModelsAbsentEndpointIsNotAnError(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		})
		models, err := client.ListModels(context.Background())
		if err != nil || len(models) != 0 {
			t.Errorf("status %d: models=%v err=%v, want empty and nil", status, models, err)
		}
	}
}

func TestModelMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			t.Errorf("path = %q, want /props", r.URL.Path)
		}
		if got := r.URL.Query().Get("model"); got != "m" {
			t.Errorf("model query = %q, want m", got)
		}
		_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":4096}}`)
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, MetadataPath: "/props"})
	meta, err := client.ModelMetadata(context.Background(), "m")
	if err != nil {
		t.Fatalf("ModelMetadata: %v", err)
	}
	if meta.ContextWindowTokens != 4096 || meta.Source != "/props" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestModelMetadataFallsBackToMaxModelLen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"max_model_len":8192}`)
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, MetadataPath: "props"})
	meta, err := client.ModelMetadata(context.Background(), "")
	if err != nil || meta.ContextWindowTokens != 8192 {
		t.Fatalf("meta = %+v err = %v", meta, err)
	}
}

func TestModelMetadataWithoutConfiguredPathIsZero(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("metadata lookup should not hit the network without a configured path")
	})
	meta, err := client.ModelMetadata(context.Background(), "m")
	if err != nil || meta != (inference.Metadata{}) {
		t.Fatalf("meta = %+v err = %v, want zero and nil", meta, err)
	}
}

func TestEmbed(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q, want /v1/embeddings", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"index":1,"embedding":[0.5]},{"index":0,"embedding":[0.25,0.75]}]}`)
	})
	vectors, err := client.Embed(context.Background(), inference.EmbeddingRequest{Texts: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 2 || vectors[0][0] != 0.25 || vectors[1][0] != 0.5 {
		t.Fatalf("vectors = %+v (want request order)", vectors)
	}
}

func TestEmbedCountMismatchIsProtocolError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1]}]}`)
	})
	_, err := client.Embed(context.Background(), inference.EmbeddingRequest{Texts: []string{"a", "b"}})
	if !inference.IsKind(err, inference.KindProtocol) {
		t.Fatalf("kind = %q, want protocol (err: %v)", inference.KindOf(err), err)
	}
}

func TestEmbedWithoutTextsIsInvalidRequest(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("empty embed request should not reach the provider")
	})
	_, err := client.Embed(context.Background(), inference.EmbeddingRequest{})
	if !inference.IsKind(err, inference.KindInvalidRequest) {
		t.Fatalf("kind = %q, want invalid_request (err: %v)", inference.KindOf(err), err)
	}
}
