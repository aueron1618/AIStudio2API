package camoufoxnative

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

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
				body := &protectedResponseBody{
					ctx: ctx, cancel: cancel, requestID: "test-request",
					worker: &Worker{client: client, contextID: "test-context"},
				}
				var data []byte
				var readErr error
				readDone := make(chan struct{})
				go func() {
					data, readErr = io.ReadAll(body)
					close(readDone)
				}()
				command := requireBiDiTestCommand(t, commands)
				expression, _ := command.Params["expression"].(string)
				target, _ := command.Params["target"].(map[string]any)
				if command.Method != "script.evaluate" || target["context"] != "test-context" ||
					!strings.Contains(expression, "state.chunks.splice(0)") || !strings.Contains(expression, `"test-request"`) {
					t.Fatalf("unexpected chunk read: %+v", command)
				}
				switch test.wantErr {
				case context.Canceled:
					cancel()
				case context.DeadlineExceeded:
					<-ctx.Done()
				default:
					// Advance only the virtual clock beyond the old independent timeout.
					time.Sleep(6 * time.Second)
				}
				requireNoBiDiTestCommand(t, commands)
				select {
				case <-readDone:
					t.Fatalf("Read returned before the chunk or cleanup response: %q, %v", data, readErr)
				default:
				}
				writeBiDiTestMessage(t, peer, map[string]any{
					"type": "success", "id": command.ID,
					"result": map[string]any{
						"type": "success",
						"result": map[string]any{
							"type": "string", "value": `{"data":["YQ==","YmM="],"done":true}`,
						},
					},
				})
				if test.wantErr != nil {
					cleanup := requireBiDiTestCommand(t, commands)
					expression, _ := cleanup.Params["expression"].(string)
					if cleanup.ID != command.ID+1 || cleanup.Method != "script.evaluate" ||
						!strings.Contains(expression, "state.controller.abort()") || !strings.Contains(expression, `"test-request"`) {
						t.Fatalf("expected serialized request cleanup, got %+v", cleanup)
					}
					writeBiDiTestMessage(t, peer, map[string]any{
						"type": "success", "id": cleanup.ID,
						"result": map[string]any{"type": "success", "result": map[string]any{"type": "boolean", "value": true}},
					})
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
				if err := body.Close(); err != nil {
					t.Fatal(err)
				}
				requireNoBiDiTestCommand(t, commands)
			})
		})
	}
}
