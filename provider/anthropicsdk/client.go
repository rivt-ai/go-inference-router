// Package anthropicsdk drives Anthropic's Messages API through the official Go
// SDK, github.com/anthropics/anthropic-sdk-go.
//
// This package lives in its own Go module. The core module's zero-dependency
// promise is a design constraint (ADR 0001), and the SDK brings eight
// transitive dependencies; keeping it out of the core module means importing
// go-inference-router never drags them in.
package anthropicsdk

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/driver"
)

const (
	// DefaultMaxTokens is sent when a request leaves MaxOutputTokens unset.
	// The API requires the field and has no server-side default.
	DefaultMaxTokens = 16384
	// DefaultStallTimeout bounds silence, not total duration.
	DefaultStallTimeout = 5 * time.Minute
)

// Config configures a Client. Every field is optional; with a zero Config the
// SDK resolves credentials from the environment exactly as it does for any
// other consumer.
type Config struct {
	// Name overrides the provider name reported in errors and logs.
	Name string
	// APIKey is the Anthropic API key. Empty defers to the SDK's own
	// resolution (ANTHROPIC_API_KEY, then ANTHROPIC_AUTH_TOKEN, then a stored
	// auth profile), so a zero Config works wherever the SDK does.
	APIKey string
	// BaseURL overrides the API root (for a gateway or a test server).
	BaseURL string
	// HTTPClient overrides the SDK's HTTP client.
	HTTPClient *http.Client
	// MaxRetries bounds the SDK's own retry loop. Negative disables retries;
	// zero leaves the SDK default (2) in place.
	MaxRetries int
	// RequestTimeout bounds a single attempt, retries excluded.
	RequestTimeout time.Duration
	// StallTimeout bounds the gap between stream reads. Zero means
	// DefaultStallTimeout; negative disables it.
	StallTimeout time.Duration
	// MaxTokens is sent when a request leaves MaxOutputTokens unset.
	MaxTokens int
	// Options are passed to the SDK verbatim, applied last. This is the escape
	// hatch for anything Config does not model — beta headers, middleware,
	// a different auth scheme.
	Options []option.RequestOption
	// Observer receives provider retry attempts without request or response bodies.
	Observer inference.Observer
}

// Client is an Anthropic driver backed by the official SDK.
type Client struct {
	cfg  Config
	base driver.Base
	sdk  anthropic.Client
}

// New builds a Client from cfg.
func New(cfg Config) *Client {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "anthropic-sdk"
	}
	cfg.Name = name
	return &Client{
		cfg: cfg, base: driver.New(name, cfg.StallTimeout, DefaultStallTimeout),
		sdk: anthropic.NewClient(sdkOptions(cfg)...),
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

func (c *Client) maxTokens(req inference.Request) int64 {
	if req.MaxOutputTokens > 0 {
		return int64(req.MaxOutputTokens)
	}
	if c.cfg.MaxTokens > 0 {
		return int64(c.cfg.MaxTokens)
	}
	return DefaultMaxTokens
}

// Anthropic has no embeddings endpoint, so this client deliberately does not
// implement Embedder.
var (
	_ inference.Provider         = (*Client)(nil)
	_ inference.Streamer         = (*Client)(nil)
	_ inference.ModelLister      = (*Client)(nil)
	_ inference.MetadataReporter = (*Client)(nil)
)
