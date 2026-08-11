package llmv1

import (
	"encoding/json"
	"errors"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
)

// CodeDomainError identifies a provider-neutral error carried by llm.v1.
const CodeDomainError = -32000

// DomainError converts err into the portable llm.v1 JSON-RPC envelope.
func DomainError(err error) *jsonrpc.RPCError {
	data := ErrorDataOf(err)
	encoded, _ := json.Marshal(data)
	return &jsonrpc.RPCError{Code: CodeDomainError, Message: err.Error(), Data: encoded}
}

// ErrorDataOf converts err into its portable representation.
func ErrorDataOf(err error) ErrorData {
	data := ErrorData{Kind: llm.KindOf(err), Message: err.Error()}
	var typed *llm.Error
	if errors.As(err, &typed) {
		data.Provider = typed.Provider
		data.Status = typed.Status
		data.Message = typed.Message
	}
	return data
}
