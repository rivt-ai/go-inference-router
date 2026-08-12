// Package openaisdk drives OpenAI's chat-completions API through the official
// Go SDK, github.com/openai/openai-go.
//
// It is the OpenAI counterpart to provider/anthropicsdk, and the SDK-backed
// sibling of provider/openaicompat. Prefer this one when talking to OpenAI
// itself and you want upstream to carry retries, new fields, and wire-format
// drift; prefer openaicompat when talking to the long tail of
// OpenAI-compatible servers (llama.cpp, vLLM, Ollama) or when the dependency
// is what you are avoiding.
//
// This package is its own Go module. The core module promises zero
// third-party dependencies, so every SDK-backed driver lives outside it.
package openaisdk

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/driver"
)

// DefaultStallTimeout bounds silence between stream reads, not total duration.
const DefaultStallTimeout = 5 * time.Minute

// Config configures a Client. Every field is optional; with a zero Config the
// SDK resolves credentials from the environment as it does for any consumer.
type Config struct {
	// Name overrides the provider name reported in errors and logs.
	Name string
	// APIKey is the OpenAI API key. Empty defers to the SDK's own resolution
	// (OPENAI_API_KEY), so a zero Config works wherever the SDK does.
	APIKey string
	// BaseURL overrides the API root (a gateway, a proxy, or a test server).
	BaseURL string
	// Organization and Project scope the request when set.
	Organization string
	Project      string
	// HTTPClient overrides the SDK's HTTP client.
	HTTPClient *http.Client
	// MaxRetries bounds the SDK's retry loop. Negative disables retries; zero
	// leaves the SDK default in place.
	MaxRetries int
	// RequestTimeout bounds a single attempt, retries excluded.
	RequestTimeout time.Duration
	// StallTimeout bounds the gap between stream reads. Zero means
	// DefaultStallTimeout; negative disables it.
	StallTimeout time.Duration
	// Options are passed to the SDK verbatim, applied last — the escape hatch
	// for anything Config does not model.
	Options []option.RequestOption
	// Observer receives provider retry attempts without request or response bodies.
	Observer inference.Observer
}

// Client is an OpenAI driver backed by the official SDK.
type Client struct {
	cfg  Config
	base driver.Base
	sdk  openai.Client
}

// New builds a Client from cfg.
func New(cfg Config) *Client {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "openai-sdk"
	}
	cfg.Name = name
	return &Client{
		cfg: cfg, base: driver.New(name, cfg.StallTimeout, DefaultStallTimeout),
		sdk: openai.NewClient(sdkOptions(cfg)...),
	}
}

func sdkOptions(cfg Config) []option.RequestOption {
	var opts []option.RequestOption
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		opts = append(opts, option.WithAPIKey(key))
	}
	if url := strings.TrimSpace(cfg.BaseURL); url != "" {
		opts = append(opts, option.WithBaseURL(url))
	}
	if org := strings.TrimSpace(cfg.Organization); org != "" {
		opts = append(opts, option.WithOrganization(org))
	}
	if project := strings.TrimSpace(cfg.Project); project != "" {
		opts = append(opts, option.WithProject(project))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	if cfg.MaxRetries > 0 {
		opts = append(opts, option.WithMaxRetries(cfg.MaxRetries))
	} else if cfg.MaxRetries < 0 {
		opts = append(opts, option.WithMaxRetries(0))
	}
	if cfg.RequestTimeout > 0 {
		opts = append(opts, option.WithRequestTimeout(cfg.RequestTimeout))
	}
	if cfg.Observer != nil {
		opts = append(opts, option.WithMiddleware(retryMiddleware(cfg.Name, cfg.Observer)))
	}
	return append(opts, cfg.Options...)
}

func retryMiddleware(name string, observer inference.Observer) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		retry, err := strconv.Atoi(req.Header.Get("X-Stainless-Retry-Count"))
		if err != nil || retry < 1 {
			return next(req)
		}
		started := time.Now().UTC()
		event := inference.Observation{
			Operation: inference.ObservationSDKRetry, Phase: inference.ObservationStarted,
			Time: started, ProviderID: name, Attempt: retry + 1,
		}
		inference.EmitObservation(req.Context(), observer, event)
		response, callErr := next(req)
		event.Phase, event.Time = inference.ObservationFinished, time.Now().UTC()
		event.Duration, event.Err = time.Since(started), callErr
		if response != nil {
			event.Status = response.StatusCode
		}
		inference.EmitObservation(req.Context(), observer, event)
		return response, callErr
	}
}

// Name implements llm.Provider.
func (c *Client) Name() string { return c.base.Name }

// This driver implements Embedder — OpenAI has an embeddings endpoint where
// Anthropic does not — but not MetadataReporter: the models endpoint reports
// no context window, so there is nothing truthful to return. Each provider's
// gap is different, which is why the seam makes capabilities optional.
var (
	_ inference.Provider    = (*Client)(nil)
	_ inference.Streamer    = (*Client)(nil)
	_ inference.Embedder    = (*Client)(nil)
	_ inference.ModelLister = (*Client)(nil)
)
