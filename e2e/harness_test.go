//go:build e2e

// Package e2e drives the openaicompat driver against a real llama.cpp server
// running a real model. Unit tests prove the driver against httptest fakes we
// wrote ourselves, which can only ever confirm our own reading of the wire
// format; this suite is what catches the reading being wrong.
//
// There is no skip tier. The engine and model are provisioned by
// scripts/provision-e2e-llamacpp.sh before the suite runs, and a missing
// fixture fails the run — a skipped engine test is indistinguishable from a
// passing one in a green check.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/provider/openaicompat"
)

// Environment supplied by scripts/provision-e2e-llamacpp.sh.
const (
	binEnv   = "INFROUTER_LLM_E2E_LLAMACPP_BIN"
	libEnv   = "INFROUTER_LLM_E2E_LLAMACPP_LIB_DIR"
	modelEnv = "INFROUTER_LLM_E2E_MODEL"
)

const (
	// contextSize is asserted against /props, so it must stay in sync with the
	// --ctx-size the server is launched with.
	contextSize = 512
	// startupBudget bounds model load plus first health response. The pinned
	// model is 135M parameters; this is generous for a cold CI runner.
	startupBudget = 3 * time.Minute
)

// sharedServer serves every test that only needs plain chat. Tests needing
// different engine flags (embeddings) launch their own.
var sharedServer *server

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	srv, err := launch("--jinja")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: cannot start llama.cpp: %v\n", err)
		return 1
	}
	defer srv.stop()
	sharedServer = srv
	return m.Run()
}

// server is a llama.cpp process owned by this suite.
type server struct {
	baseURL string
	cmd     *exec.Cmd
	logs    *syncBuffer
}

// launch starts llama-server on a free port and waits until it reports ready.
func launch(extraArgs ...string) (*server, error) {
	bin, err := requireEnv(binEnv)
	if err != nil {
		return nil, err
	}
	model, err := requireEnv(modelEnv)
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}

	args := append([]string{
		"--model", model,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--ctx-size", strconv.Itoa(contextSize),
		"--parallel", "1",
		"--threads", "2",
	}, extraArgs...)

	logs := &syncBuffer{}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = logs
	cmd.Stderr = logs
	// The release tarball keeps its shared libraries beside the binary; without
	// this the loader fails with an error that names neither.
	if libDir := os.Getenv(libEnv); libDir != "" {
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+libDir)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}

	srv := &server{baseURL: "http://127.0.0.1:" + strconv.Itoa(port), cmd: cmd, logs: logs}
	if err := srv.waitReady(); err != nil {
		srv.stop()
		return nil, fmt.Errorf("%w\n--- llama.cpp output ---\n%s", err, srv.logs.String())
	}
	return srv, nil
}

// launchT is launch for a test that owns its own server.
func launchT(t *testing.T, extraArgs ...string) *server {
	t.Helper()
	srv, err := launch(extraArgs...)
	if err != nil {
		t.Fatalf("launch llama.cpp: %v", err)
	}
	t.Cleanup(srv.stop)
	return srv
}

// waitReady polls /health, which llama.cpp answers 503 while the model loads
// and 200 once it can serve. A process that exits during the wait fails
// immediately rather than burning the whole budget.
func (s *server) waitReady() error {
	deadline := time.Now().Add(startupBudget)
	client := &http.Client{Timeout: 5 * time.Second}
	for time.Now().Before(deadline) {
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			return fmt.Errorf("llama.cpp exited during startup: %s", s.cmd.ProcessState)
		}
		resp, err := client.Get(s.baseURL + "/health") //nolint:noctx // bounded by the client timeout and the deadline loop
		if err == nil {
			status := resp.StatusCode
			_ = resp.Body.Close()
			if status == http.StatusOK {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("llama.cpp not ready within %s", startupBudget)
}

func (s *server) stop() {
	if s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
}

// client builds a driver pointed at this server. MetadataPath is set because
// /props is exactly the llama.cpp-specific endpoint the seam claims to cover
// through configuration rather than through a dedicated driver.
func (s *server) client() *openaicompat.Client {
	return openaicompat.New(openaicompat.Config{
		Name:         "llamacpp-e2e",
		BaseURL:      s.baseURL,
		MetadataPath: "/props",
		StallTimeout: 2 * time.Minute,
	})
}

// request builds a greedy, short, reproducible request. Determinism is what
// lets the suite assert on content rather than on shape alone.
func request(prompt string) inference.Request {
	return inference.Request{
		Model:           "e2e",
		Messages:        []inference.Message{inference.UserMessage(prompt)},
		Temperature:     inference.Float(0),
		Seed:            inference.Int64(42),
		MaxOutputTokens: 24,
	}
}

// testContext bounds a single call. A 135M model on CPU answers in well under
// this; exceeding it is a failure, not something to wait out.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is unset: run `make e2e`, which provisions the pinned engine and model first", name)
	}
	return value, nil
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// syncBuffer collects engine output from the process's writer goroutines while
// the test goroutine may be reading it for a failure message.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
