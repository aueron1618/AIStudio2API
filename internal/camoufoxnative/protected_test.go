package camoufoxnative

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
)

func writeProtectedTestMessage(t *testing.T, peer *websocket.Conn, requestID, value string) {
	t.Helper()
	writeBiDiTestMessage(t, peer, map[string]any{
		"type": "event", "method": "script.message",
		"params": map[string]any{"channel": requestID, "data": map[string]any{"type": "string", "value": value}},
	})
}

func completeProtectedTestCancel(t *testing.T, peer *websocket.Conn, commands <-chan bidiTestCommand) {
	t.Helper()
	command := requireBiDiTestCommand(t, commands)
	target, _ := command.Params["target"].(map[string]any)
	if command.Method != "script.callFunction" || command.Params["functionDeclaration"] != protectedCancel || target["context"] != "test-context" {
		t.Fatalf("unexpected cancellation: %+v", command)
	}
	writeBiDiTestMessage(t, peer, map[string]any{
		"type": "success", "id": command.ID,
		"result": map[string]any{"type": "success", "result": map[string]any{"type": "undefined"}},
	})
}

func TestProtectedResponseBodyReadContext(t *testing.T) {
	for _, test := range []struct {
		name    string
		wantErr error
	}{
		{name: "cancel", wantErr: context.Canceled},
		{name: "deadline", wantErr: context.DeadlineExceeded},
		{name: "response-after-five-seconds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, peer, commands := newBiDiTestClient(t)
				timeout := 30 * time.Second
				if test.wantErr == context.DeadlineExceeded {
					timeout = time.Second
				}
				ctx, cancel := context.WithTimeout(t.Context(), timeout)
				defer cancel()
				channel, err := client.openChannel("test-request")
				if err != nil {
					t.Fatal(err)
				}
				body := &protectedResponseBody{
					ctx: ctx, cancel: cancel, requestID: "test-request",
					worker: &Worker{client: client, contextID: "test-context", page: "test-page"}, channel: channel,
				}
				var data []byte
				var readErr error
				readDone := make(chan struct{})
				go func() {
					data, readErr = io.ReadAll(body)
					close(readDone)
				}()
				requireNoBiDiTestCommand(t, commands)
				switch test.wantErr {
				case context.Canceled:
					cancel()
				case context.DeadlineExceeded:
					<-ctx.Done()
				default:
					// Advance only the virtual clock beyond the old independent timeout.
					time.Sleep(6 * time.Second)
				}
				if test.wantErr != nil {
					synctest.Wait()
					select {
					case <-readDone:
					default:
						t.Fatal("canceled Read waited for page cleanup")
					}
					completeProtectedTestCancel(t, peer, commands)
					writeProtectedTestMessage(t, peer, "test-request", "bGF0ZQ==")
				} else {
					select {
					case <-readDone:
						t.Fatal("Read timed out independently of the caller")
					default:
					}
					writeProtectedTestMessage(t, peer, "test-request", "YQ==")
					writeProtectedTestMessage(t, peer, "test-request", "YmM=")
					writeProtectedTestMessage(t, peer, "test-request", `{"done":true}`)
				}
				synctest.Wait()
				select {
				case <-readDone:
				default:
					t.Fatal("Read did not return after the response")
				}
				if !errors.Is(readErr, test.wantErr) {
					t.Fatalf("Read error = %v, want %v", readErr, test.wantErr)
				}
				if test.wantErr == nil && string(data) != "abc" || test.wantErr != nil && len(data) != 0 {
					t.Fatalf("Read data = %q", data)
				}
				if body.buffer != nil || !body.done {
					t.Fatal("completed body retained data or remained active")
				}
				channel.mu.Lock()
				retained := len(channel.queue)
				channel.mu.Unlock()
				if retained != 0 {
					t.Fatal("completed channel retained queued or late data")
				}
				if err := body.Close(); err != nil {
					t.Fatal(err)
				}
				requireNoBiDiTestCommand(t, commands)
			})
		})
	}
}

func TestProtectedResponseMalformedChunkCancelsXHR(t *testing.T) {
	for _, value := range []string{"not-base64", "YQ==!", "{invalid"} {
		t.Run(value, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, peer, commands := newBiDiTestClient(t)
				channel, err := client.openChannel("test-request")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				body := &protectedResponseBody{ctx: ctx, cancel: cancel, requestID: "test-request", channel: channel,
					worker: &Worker{client: client, contextID: "test-context", page: "test-page"}}
				done := make(chan error, 1)
				go func() { _, err := io.ReadAll(body); done <- err }()
				writeProtectedTestMessage(t, peer, "test-request", value)
				completeProtectedTestCancel(t, peer, commands)
				if err := <-done; err == nil {
					t.Fatal("malformed response was accepted")
				}
				if body.buffer != nil || !body.done {
					t.Fatal("malformed response retained partial data")
				}
				if err := body.Close(); err != nil {
					t.Fatal(err)
				}
				requireNoBiDiTestCommand(t, commands)
			})
		})
	}
}

func TestProtectedResponseCreditWaitsForConsumedBlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		channel, err := client.openChannel("test-request")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		body := &protectedResponseBody{ctx: ctx, cancel: cancel, requestID: "test-request", channel: channel,
			worker: &Worker{client: client, contextID: "test-context", page: "test-page"}}
		for range 8 {
			writeProtectedTestMessage(t, peer, "test-request", "YWI=")
		}
		for range 7 {
			if n, err := body.Read(make([]byte, 1)); n != 1 || err != nil {
				t.Fatalf("Read = %d, %v", n, err)
			}
		}
		requireNoBiDiTestCommand(t, commands)
		if body.consumed != 3 || len(body.buffer) != 1 {
			t.Fatal("partial block was counted as consumed")
		}
		if _, err := body.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := body.Read(make([]byte, 1)); done <- err }()
		grant := requireBiDiTestCommand(t, commands)
		arguments, _ := grant.Params["arguments"].([]any)
		if grant.Params["functionDeclaration"] != protectedGrant || len(arguments) != 3 || arguments[2].(map[string]any)["value"] != "4" {
			t.Fatalf("unexpected credit grant: %+v", grant)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": grant.ID, "result": map[string]any{"type": "success", "result": map[string]any{"type": "undefined"}}})
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		for range 4 {
			writeProtectedTestMessage(t, peer, "test-request", "YWI=")
		}
		synctest.Wait()
		if len(channel.queue) != 7 || len(body.buffer) != 1 {
			t.Fatal("credit exceeded eight outstanding blocks")
		}
		go func() { done <- body.Close() }()
		completeProtectedTestCancel(t, peer, commands)
		if err := <-done; err != nil || channel.queue != nil || body.buffer != nil {
			t.Fatal("Close retained queued or partially read data")
		}
	})
}

func TestProtectedResponseCloseAfterConnectionFailureReleasesQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, _ := newBiDiTestClient(t)
		channel, err := client.openChannel("test-request")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		body := &protectedResponseBody{ctx: ctx, cancel: cancel, requestID: "test-request", channel: channel,
			worker: &Worker{client: client, contextID: "test-context", page: "test-page"}}
		writeProtectedTestMessage(t, peer, "test-request", "YWI=")
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		<-client.done
		synctest.Wait()
		if len(channel.queue) != 1 {
			t.Fatal("connection failure discarded a readable queued chunk")
		}
		_ = body.Close()
		if channel.queue != nil || body.buffer != nil {
			t.Fatal("Close retained data after the disconnected client unregistered its channel")
		}
	})
}
