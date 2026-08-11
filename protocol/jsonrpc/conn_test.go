package jsonrpc_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
)

type numbers struct {
	A int `json:"a"`
	B int `json:"b"`
}

type total struct {
	Value int `json:"value"`
}

func TestConnMultiplexesCallsAndNotifications(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	client := jsonrpc.New(left, left)
	server := jsonrpc.New(right, right)

	events := make(chan int, 2)
	client.OnNotification("stream", func(_ context.Context, params []byte) {
		var event total
		if err := jsonrpc.Decode(params, &event); err != nil {
			t.Errorf("Decode notification: %v", err)
			return
		}
		events <- event.Value
	})
	server.Handle("add", func(ctx context.Context, params []byte) (any, error) {
		if jsonrpc.RequestID(ctx) == "" {
			t.Error("handler context has no request ID")
		}
		var in numbers
		if err := jsonrpc.Decode(params, &in); err != nil {
			return nil, err
		}
		if err := server.Notify(ctx, "stream", total{Value: in.A}); err != nil {
			return nil, err
		}
		return total{Value: in.A + in.B}, nil
	})

	serve(t, client)
	serve(t, server)

	var wg sync.WaitGroup
	for _, in := range []numbers{{A: 2, B: 3}, {A: 10, B: 4}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out total
			if err := client.Call(context.Background(), "add", in, &out); err != nil {
				t.Errorf("Call: %v", err)
			}
			if out.Value != in.A+in.B {
				t.Errorf("sum = %d", out.Value)
			}
		}()
	}
	wg.Wait()
	close(events)
	seen := map[int]bool{}
	for value := range events {
		seen[value] = true
	}
	if !seen[2] || !seen[10] {
		t.Fatalf("events = %#v", seen)
	}
}

func TestInvalidParams(t *testing.T) {
	source := context.Canceled
	err := jsonrpc.InvalidParams(source)
	if err.Code != jsonrpc.CodeInvalidParams || err.Message != source.Error() {
		t.Fatalf("InvalidParams = %#v", err)
	}
}

func TestCallCancellationReachesHandler(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	client := jsonrpc.New(left, left)
	server := jsonrpc.New(right, right)
	canceled := make(chan struct{})
	server.Handle("wait", func(ctx context.Context, _ []byte) (any, error) {
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	serve(t, client)
	serve(t, server)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Call(ctx, "wait", nil, nil) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Call error = %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("handler was not canceled")
	}
}

func serve(t *testing.T, conn *jsonrpc.Conn) {
	t.Helper()
	go func() {
		if err := conn.Serve(context.Background()); err != nil && !jsonrpc.IsClosed(err) {
			t.Errorf("Serve: %v", err)
		}
	}()
}
