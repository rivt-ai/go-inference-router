// Package transport holds the HTTP mechanics every driver needs and no driver
// should reimplement: server-sent-event framing and the stall watchdog. It is
// internal because it is plumbing, not contract — the seam lives in
// package llm.
package transport

import (
	"bufio"
	"bytes"
	"io"
)

// DoneMarker terminates an OpenAI-compatible SSE stream. Anthropic streams end
// with a typed `message_stop` frame instead and never send it.
const DoneMarker = "[DONE]"

// ScanSSE reads `data:` payloads from an SSE body and hands each raw JSON frame
// to onFrame, stopping at the [DONE] marker. Comment lines, `event:`/`id:`
// fields, and blank separators are skipped — providers that name their events
// carry the same name inside the JSON payload, so the field is redundant, and
// providers that pad the stream with keep-alive comments do not trip the
// caller.
func ScanSSE(body io.Reader, onFrame func([]byte) error) error {
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadBytes('\n')
		if payload, ok := ssePayload(line); ok {
			if string(payload) == DoneMarker {
				return nil
			}
			if frameErr := onFrame(payload); frameErr != nil {
				return frameErr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func ssePayload(line []byte) ([]byte, bool) {
	trimmed := bytes.TrimRight(line, "\r\n")
	if len(trimmed) == 0 || trimmed[0] == ':' {
		return nil, false
	}
	field, value, found := bytes.Cut(trimmed, []byte(":"))
	if !found || string(bytes.TrimSpace(field)) != "data" {
		return nil, false
	}
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return nil, false
	}
	return value, true
}
