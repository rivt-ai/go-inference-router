package inference

import "context"

// Provider executes provider-neutral chat requests.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req Request) (*Response, error)
}

// Streamer adds incremental chat events to Provider.
type Streamer interface {
	Provider
	ChatStream(ctx context.Context, req Request, onEvent func(Event) error) (*Response, error)
}

// Embedder executes provider-neutral embedding requests.
type Embedder interface {
	Embed(ctx context.Context, req EmbeddingRequest) ([][]float32, error)
}

// ModelLister discovers models exposed by a provider.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// MetadataReporter reports limits for one model.
type MetadataReporter interface {
	ModelMetadata(ctx context.Context, model string) (Metadata, error)
}

// CapabilityReporter reports features available for one model.
type CapabilityReporter interface {
	Capabilities(ctx context.Context, model string) (Capabilities, error)
}
