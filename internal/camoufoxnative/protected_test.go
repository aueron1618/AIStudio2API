package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
)

func TestSetCookiesDeletesOnlyExpiredTargets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		worker := &Worker{client: client}
		cookies := []storageCookie{
			{Name: "sid", Domain: ".google.com", Path: "/", Expires: 0},
			{Name: "partitioned", Domain: ".google.com", Path: "/session", Expires: 1, PartitionKey: "https://aistudio.google.com"},
			{Name: "fresh", Value: "fixture", Domain: ".google.com", Path: "/", Expires: -1, Secure: true},
		}
		encoded, err := json.Marshal(cookies)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- worker.SetCookies(t.Context(), encoded) }()
		for index, cookie := range cookies {
			command := requireBiDiTestCommand(t, commands)
			if index < 2 {
				filter, _ := command.Params["filter"].(map[string]any)
				if command.Method != "storage.deleteCookies" || len(filter) != 3 ||
					filter["name"] != cookie.Name || filter["domain"] != cookie.Domain || filter["path"] != cookie.Path {
					t.Fatalf("expired cookie did not produce a targeted deletion: %+v", command)
				}
			} else {
				value, _ := command.Params["cookie"].(map[string]any)
				if command.Method != "storage.setCookie" || value["name"] != cookie.Name {
					t.Fatalf("valid cookie was deleted: %+v", command)
				}
			}
			partition, _ := command.Params["partition"].(map[string]any)
			if cookie.PartitionKey != "" && (partition["type"] != "storageKey" || partition["sourceOrigin"] != cookie.PartitionKey) ||
				cookie.PartitionKey == "" && partition != nil {
				t.Fatal("cookie update lost its partition boundary")
			}
			writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": command.ID, "result": map[string]any{}})
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		requireNoBiDiTestCommand(t, commands)
	})
}

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
					ctx: ctx, cancel: cancel, requestID: "test-request", contextID: "test-context", page: "test-page",
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
				body := &protectedResponseBody{ctx: ctx, cancel: cancel, requestID: "test-request", channel: channel, contextID: "test-context", page: "test-page",
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
		body := &protectedResponseBody{ctx: ctx, cancel: cancel, requestID: "test-request", channel: channel, contextID: "test-context", page: "test-page",
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
		body := &protectedResponseBody{ctx: ctx, cancel: cancel, requestID: "test-request", channel: channel, contextID: "test-context", page: "test-page",
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

func TestProtectedResponseStaysOnOriginalTabAfterReload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		channel, err := client.openChannel("test-request")
		if err != nil {
			t.Fatal(err)
		}
		worker := &Worker{client: client, contextID: "test-context", page: "test-page", tabs: map[string]*tabUse{"test-context": {requests: 1}}, chats: []*chatTab{{contextID: "test-context", page: "test-page"}}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		body := &protectedResponseBody{ctx: ctx, cancel: cancel, worker: worker, contextID: "test-context", page: "test-page", requestID: "test-request", channel: channel, consumed: 4}
		if err := worker.switchTab("new-context", loadedPage{page: "new-page"}); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := body.Read(make([]byte, 1)); done <- err }()
		grant := requireBiDiTestCommand(t, commands)
		target := grant.Params["target"].(map[string]any)
		arguments := grant.Params["arguments"].([]any)
		if grant.Params["functionDeclaration"] != protectedGrant || target["context"] != "test-context" || arguments[0].(map[string]any)["handle"] != "test-page" {
			t.Fatalf("old stream sent credit to the replacement page: %+v", grant)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": grant.ID, "result": map[string]any{"type": "success", "result": map[string]any{"type": "undefined"}}})
		writeProtectedTestMessage(t, peer, "test-request", "YQ==")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		go func() { done <- body.Close() }()
		completeProtectedTestCancel(t, peer, commands)
		closeTab := requireBiDiTestCommand(t, commands)
		if closeTab.Method != "browsingContext.close" || closeTab.Params["context"] != "test-context" {
			t.Fatalf("old response closed the wrong tab: %+v", closeTab)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": closeTab.ID, "result": map[string]any{}})
		if err := <-done; err != nil || len(worker.tabs) != 0 || worker.contextID != "new-context" {
			t.Fatal("retired tab survived its last response or damaged the replacement")
		}
		requireNoBiDiTestCommand(t, commands)
	})
}

func TestProtectedCanceledCloseJoinsStopBeforeNextTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		tab := &chatTab{contextID: "test-context", page: "test-page", busy: true}
		worker := &Worker{client: client, contextID: tab.contextID, selected: tab.contextID, page: tab.page,
			chats: []*chatTab{tab}, tabs: map[string]*tabUse{tab.contextID: {requests: 1}}}
		channel, err := client.openChannel("test-request")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stopStarted, released := make(chan struct{}, 2), make(chan bool, 2)
		allowStop := make(chan struct{})
		body := &protectedResponseBody{
			ctx: ctx, cancel: cancel, worker: worker, contextID: tab.contextID, page: tab.page, requestID: "test-request", channel: channel,
			stop: func() error { stopStarted <- struct{}{}; <-allowStop; return worker.stopTurn(tab) },
			release: func(completed bool) {
				worker.releaseChat(tab, completed)
				released <- completed
			},
		}
		// Delay the old Stop before it can acquire inputMu and inspect the tab.
		read := make(chan error, 1)
		go func() { _, err := body.Read(make([]byte, 1)); read <- err }()
		cancel()
		synctest.Wait()
		select {
		case err := <-read:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Read error = %v", err)
			}
		default:
			t.Error("canceled Read waited for Stop")
		}
		<-stopStarted
		closed, duplicate := make(chan error, 1), make(chan error, 1)
		next := make(chan *chatTab, 1)
		go func() {
			closed <- body.Close()
			acquired, err := worker.acquireChat(t.Context())
			if err != nil {
				t.Error(err)
			}
			next <- acquired
		}()
		go func() { duplicate <- body.Close() }()
		synctest.Wait()
		if len(closed) != 0 || len(duplicate) != 0 || len(next) != 0 || len(released) != 0 {
			t.Error("Close released the canceled turn before its blocked Stop completed")
		}
		close(allowStop)
		running := requireBiDiTestCommand(t, commands)
		if running.Method != "script.evaluate" || running.Params["expression"] != generationRunningExpression {
			t.Fatalf("Stop did not inspect the original turn: %+v", running)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": running.ID,
			"result": map[string]any{"type": "success", "result": map[string]any{"type": "boolean", "value": true}}})
		target := requireBiDiTestCommand(t, commands)
		if target.Params["functionDeclaration"] != pointerTargetFunction {
			t.Fatalf("Stop target = %+v", target)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": target.ID,
			"result": map[string]any{"type": "success", "result": map[string]any{"type": "string", "value": `{"x":1,"y":1}`}}})
		click := requireBiDiTestCommand(t, commands)
		if click.Method != "input.performActions" || click.Params["context"] != tab.contextID {
			t.Fatalf("Stop click = %+v", click)
		}
		if len(next) != 0 {
			t.Error("the old Stop reached a tab already reused by the next turn")
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": click.ID, "result": map[string]any{}})
		completeProtectedTestCancel(t, peer, commands)
		if err := <-closed; err != nil {
			t.Error(err)
		}
		if err := <-duplicate; err != nil {
			t.Error(err)
		}
		if acquired := <-next; acquired != tab {
			t.Fatal("next turn did not reuse the cleaned primary tab")
		}
		if completed := <-released; completed || len(released) != 0 || len(stopStarted) != 0 {
			t.Fatal("cancellation was completed or stopped/released more than once")
		}
		worker.releaseChat(tab, false)
		requireNoBiDiTestCommand(t, commands)
	})
}
