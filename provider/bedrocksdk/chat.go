package bedrocksdk

import (
	"context"

	llm "github.com/rivt-ai/go-inference-router"
)

// Chat executes one non-streaming Bedrock Converse request.
func (c *Client) Chat(ctx context.Context, request llm.Request) (*llm.Response, error) {
	input, err := buildInput(request)
	if err != nil {
		return nil, c.base.Errf(llm.KindInvalidRequest, 0, err.Error(), err)
	}
	output, err := c.sdk.Converse(ctx, input)
	if err != nil {
		return nil, c.classify(ctx, err)
	}
	return decodeResponse(request.Model, output), nil
}
