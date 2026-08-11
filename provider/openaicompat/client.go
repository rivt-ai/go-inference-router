// Package openaicompat drives any HTTP service that speaks the OpenAI
// chat-completions wire format — OpenAI itself, llama.cpp, vLLM, Ollama's
// compatibility endpoint, OpenRouter, and the rest. It implements the
// llm.Provider seam using only net/http, so hosts inherit no vendor SDK.
package openaicompat

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/driver"
)

// Default timeouts. HTTP is generous because local models can take minutes on
// prompt eval; the stall timeout is what actually protects a turn, since it
// bounds silence rather than total duration.
const (
	DefaultHTTPTimeout  = 10 * time.Minute
	DefaultStallTimeout = 5 * time.Minute
)

// Config configures a Client. Only BaseURL is required.
type Config struct {
	// Name overrides the provider name reported in errors and logs.
	Name string
	// BaseURL is the provider root, with or without a trailing "/v1".
	BaseURL string
	// APIKey is sent as a bearer token when non-empty.
	APIKey string
	// Headers are added to every request (vendor routing headers, etc.).
	Headers map[string]string
	// HTTPClient overrides the default client. Its own Timeout wins over
	// HTTPTimeout when set.
	HTTPClient *http.Client
	// HTTPTimeout bounds a whole request when HTTPClient is not supplied.
	HTTPTimeout time.Duration
	// StallTimeout bounds the gap between stream reads, including the wait
	// for the first chunk. Zero means DefaultStallTimeout; negative disables
	// the watchdog.
	StallTimeout time.Duration
	// MetadataPath is the provider-specific model-metadata endpoint, relative
	// to the root (llama.cpp serves "/props"). Empty disables metadata
	// lookups, which then report a zero Metadata and no error.
	MetadataPath string
}

// Client is an OpenAI-compatible provider driver.
type Client struct {
	cfg     Config
	base    driver.Base
	root    string
	apiBase string
	http    *http.Client
}

// New builds a Client from cfg.
func New(cfg Config) *Client {
	root := strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/v1")
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "openai-compatible"
	}
	return &Client{
		cfg:     cfg,
		base:    driver.New(name, cfg.StallTimeout, DefaultStallTimeout),
		root:    root,
		apiBase: root + "/v1",
		http:    httpClient(cfg),
	}
}

func httpClient(cfg Config) *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = DefaultHTTPTimeout
	}
	return &http.Client{Timeout: timeout}
}

// Name implements llm.Provider.
func (c *Client) Name() string { return c.base.Name }

// Capabilities implements llm.CapabilityReporter.
func (c *Client) Capabilities(context.Context, string) (inference.Capabilities, error) {
	return inference.Capabilities{
		Streaming: true, Tools: true, StructuredOutput: true, Embeddings: true,
		InputModalities: []inference.Modality{inference.ModalityText}, MaxConcurrency: 8,
	}, nil
}

func (c *Client) applyHeaders(req *http.Request) {
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
}

// Compile-time proof that the driver satisfies the whole seam.
var (
	_ inference.Provider           = (*Client)(nil)
	_ inference.Streamer           = (*Client)(nil)
	_ inference.Embedder           = (*Client)(nil)
	_ inference.ModelLister        = (*Client)(nil)
	_ inference.MetadataReporter   = (*Client)(nil)
	_ inference.CapabilityReporter = (*Client)(nil)
)
