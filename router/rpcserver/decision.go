package rpcserver

import (
	"context"
	"errors"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

func (s *Server) decide(ctx context.Context, params []byte) (any, error) {
	var request llmv1.DecideRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		// Keep typed rejections (images, streaming) typed for callers.
		var typed *llm.Error
		if errors.As(err, &typed) {
			return nil, llmv1.DomainError(err)
		}
		return nil, jsonrpc.InvalidParams(err)
	}
	response, err := s.router.Decide(ctx, request.ProfileID, request.Request)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.DecideResponse{Response: *response}, nil
}
