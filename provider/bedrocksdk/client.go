// Package bedrocksdk adapts Amazon Bedrock Converse through the official AWS SDK.
package bedrocksdk

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/driver"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Config controls AWS credential resolution and the Bedrock endpoint.
type Config struct {
	Name            string
	Region          string
	Profile         string
	BaseURL         string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	HTTPClient      *http.Client
	Observer        llm.Observer
}

// Client implements llm.Provider with Amazon Bedrock Converse.
type Client struct {
	base driver.Base
	sdk  *bedrockruntime.Client
}

// New creates a Bedrock provider using the AWS default credential chain unless overridden.
func New(ctx context.Context, cfg Config) (*Client, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "bedrock-sdk"
	}
	var options []func(*awsconfig.LoadOptions) error
	if cfg.Region != "" {
		options = append(options, awsconfig.WithRegion(cfg.Region))
	}
	if cfg.Profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}
	if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken,
		)))
	}
	if cfg.HTTPClient != nil {
		options = append(options, awsconfig.WithHTTPClient(cfg.HTTPClient))
	}
	loaded, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	sdk := bedrockruntime.NewFromConfig(loaded, func(options *bedrockruntime.Options) {
		if cfg.BaseURL != "" {
			options.BaseEndpoint = aws.String(cfg.BaseURL)
		}
		if cfg.Observer != nil {
			options.APIOptions = append(options.APIOptions, func(stack *middleware.Stack) error {
				return stack.Finalize.Insert(retryObserver(name, cfg.Observer), "RetryMetricsHeader", middleware.After)
			})
		}
	})
	return &Client{base: driver.New(name, 0, 0), sdk: sdk}, nil
}

func retryObserver(name string, observer llm.Observer) middleware.FinalizeMiddleware {
	return middleware.FinalizeMiddlewareFunc("RetryObserver", func(
		ctx context.Context, input middleware.FinalizeInput, next middleware.FinalizeHandler,
	) (middleware.FinalizeOutput, middleware.Metadata, error) {
		request, ok := input.Request.(*smithyhttp.Request)
		attempt := 0
		if ok {
			attempt = awsAttempt(request.Header.Get("Amz-Sdk-Request"))
		}
		if attempt < 2 {
			return next.HandleFinalize(ctx, input)
		}
		started := time.Now().UTC()
		event := llm.Observation{
			Operation: llm.ObservationSDKRetry, Phase: llm.ObservationStarted,
			Time: started, ProviderID: name, Attempt: attempt,
		}
		llm.EmitObservation(ctx, observer, event)
		output, metadata, err := next.HandleFinalize(ctx, input)
		event.Phase, event.Time = llm.ObservationFinished, time.Now().UTC()
		event.Duration, event.Err = time.Since(started), err
		if response, ok := output.Result.(*smithyhttp.Response); ok {
			event.Status = response.StatusCode
		}
		llm.EmitObservation(ctx, observer, event)
		return output, metadata, err
	})
}

func awsAttempt(header string) int {
	for part := range strings.SplitSeq(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && key == "attempt" {
			attempt, _ := strconv.Atoi(value)
			return attempt
		}
	}
	return 0
}

// Name returns the configured provider name.
func (c *Client) Name() string { return c.base.Name }

var _ llm.Provider = (*Client)(nil)
