package transport

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func collect(t *testing.T, body string) []string {
	t.Helper()
	var frames []string
	if err := ScanSSE(strings.NewReader(body), func(frame []byte) error {
		frames = append(frames, string(frame))
		return nil
	}); err != nil {
		t.Fatalf("ScanSSE: %v", err)
	}
	return frames
}

// TestScanSSEIgnoresNonDataLines covers what both drivers rely on: named
// events (Anthropic) and keep-alive comments (llama.cpp) must not reach the
// frame handler.
func TestScanSSEIgnoresNonDataLines(t *testing.T) {
	frames := collect(t, ": keep-alive\n\nevent: message_start\ndata: {\"a\":1}\n\nid: 7\ndata: {\"b\":2}\n\n")
	if len(frames) != 2 || frames[0] != `{"a":1}` || frames[1] != `{"b":2}` {
		t.Fatalf("frames = %q, want the two data payloads only", frames)
	}
}

func TestScanSSEStopsAtDoneMarker(t *testing.T) {
	frames := collect(t, "data: {\"a\":1}\n\ndata: "+DoneMarker+"\n\ndata: {\"never\":true}\n\n")
	if len(frames) != 1 {
		t.Fatalf("frames = %q, want only the payload before [DONE]", frames)
	}
}

func TestScanSSEHandlesMissingTrailingNewline(t *testing.T) {
	frames := collect(t, "data: {\"a\":1}")
	if len(frames) != 1 || frames[0] != `{"a":1}` {
		t.Fatalf("frames = %q, want the final unterminated frame", frames)
	}
}

func TestScanSSEPropagatesHandlerError(t *testing.T) {
	want := errors.New("stop")
	err := ScanSSE(strings.NewReader("data: {}\n\ndata: {}\n\n"), func([]byte) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestScanSSEPropagatesReadError(t *testing.T) {
	want := errors.New("boom")
	reader := io.MultiReader(strings.NewReader("data: {}\n\n"), &failingReader{err: want})
	err := ScanSSE(reader, func([]byte) error { return nil })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

type failingReader struct{ err error }

func (r *failingReader) Read([]byte) (int, error) { return 0, r.err }
