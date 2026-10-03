package rpcserver

import (
	"context"

	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
)

func (s *Server) decide(ctx context.Context, params []byte) (any, error) {
	var request llmv1.DecideRequest
	if err := jsonrpc.Decode(params, &request); err != nil {
		return nil, jsonrpc.InvalidParams(err)
	}
	response, err := s.router.Decide(ctx, request.ProfileID, request.Request)
	if err != nil {
		return nil, llmv1.DomainError(err)
	}
	return llmv1.DecideResponse{Response: *response}, nil
}
