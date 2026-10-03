package providerproc

import (
	"context"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

// Decide invokes only processes that negotiated decision support.
func (c *Client) Decide(ctx context.Context, request llm.DecisionRequest) (*llm.DecisionResponse, error) {
	if !c.info.Capabilities.Decisions {
		return nil, &llm.Error{Kind: llm.KindInvalidRequest, Provider: c.Name(), Message: "provider does not support decisions"}
	}
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	var response llmv1.DecideResponse
	if err := c.call(ctx, llmv1.MethodProviderDecide, llmv1.ProviderDecideRequest{
		CorrelationID: jsonrpc.RequestID(ctx), Request: request,
	}, &response); err != nil {
		return nil, err
	}
	return &response.Response, nil
}
