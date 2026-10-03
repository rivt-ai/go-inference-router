package providerhost

import (
	"context"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

func (h *host) decide(ctx context.Context, params []byte) (any, error) {
	var request llmv1.ProviderDecideRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	ctx = context.WithValue(ctx, correlationKey{}, request.CorrelationID)
	current, err := h.current()
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	decider, ok := current.(llm.Decider)
	if !ok {
		return nil, llmv1.DomainError(&llm.Error{
			Kind: llm.KindInvalidRequest, Provider: current.Name(), Message: "decisions are unsupported",
		})
	}
	if err := request.Request.Validate(); err != nil {
		return nil, llmv1.DomainError(err)
	}
	response, err := decider.Decide(ctx, request.Request)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.DecideResponse{Response: *response}, nil
}
