package openaicompat

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/internal/driver"
)

// toolCallParseMarker is the fragment llama.cpp's server puts in the error
// message (as HTTP 500, type "server_error") when it fails to parse
// model-generated tool-call arguments, e.g. "Failed to parse tool call
// arguments as JSON: ...". No structured code distinguishes it, so the message
// is the only available signal — the substring match is confined to this file
// and surfaces as llm.KindToolCallParse, so callers still classify by
// kind rather than by text. If a provider starts sending a distinguishing
// code, prefer that here and keep TestClassifyToolCallParse in sync.
const toolCallParseMarker = "tool call arguments"

// maxErrorBody caps how much of an error response is read, so a provider
// returning an HTML error page cannot flood a log line.
const maxErrorBody = 8 << 10

type wireError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// httpError converts a non-2xx response into a classified llm.Error. The
// body is consumed.
func (c *Client) httpError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	message := errorMessage(body)
	kind := driver.KindForStatus(resp.StatusCode)
	if strings.Contains(strings.ToLower(message), toolCallParseMarker) {
		kind = inference.KindToolCallParse
	}
	err := c.base.Errf(kind, resp.StatusCode, message, nil)
	err.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
	return err
}

// retryAfter reads the delay-seconds form of the Retry-After header, which is
// what OpenAI-compatible services send on a 429. The HTTP-date form is
// deliberately not parsed: guessing wrong would be worse than reporting no
// hint, and a caller with no hint falls back to its own backoff.
func retryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func errorMessage(body []byte) string {
	var wire wireError
	if err := json.Unmarshal(body, &wire); err == nil && wire.Error.Message != "" {
		if wire.Error.Type != "" {
			return wire.Error.Type + ": " + wire.Error.Message
		}
		return wire.Error.Message
	}
	return strings.TrimSpace(string(body))
}
