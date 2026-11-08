package camoufoxnative

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
)

func TestReloadFailureReleasesPageSetupAndClosesInvalidRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		worker := &Worker{client: client, connection: client.connection, contextID: "old", page: "old-page", tabs: make(map[string]*tabUse), chats: []*chatTab{{contextID: "old", page: "old-page"}}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker.Reload(ctx) }()
		setup := map[string]bool{}
		for range 3 {
			command := requireBiDiTestCommand(t, commands)
			setup[command.Method] = true
			result := map[string]any{}
			switch command.Method {
			case "session.subscribe":
			case "network.addDataCollector":
				result["collector"] = "catalog"
			case "script.addPreloadScript":
				result["script"] = "preload"
			default:
				t.Fatalf("unexpected setup: %+v", command)
			}
			writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": command.ID, "result": result})
		}
		if len(setup) != 3 {
			t.Fatal("page setup did not install all three resources")
		}
		navigate := requireBiDiTestCommand(t, commands)
		if navigate.Method != "browsingContext.navigate" || navigate.Params["url"] != aiStudioOrigin+"/prompts/new_chat?model=&temporary=true" {
			t.Fatalf("new cold-start navigation = %+v", navigate)
		}
		cancel()
		removed := map[string]bool{}
		for range 3 {
			command := requireBiDiTestCommand(t, commands)
			removed[command.Method] = true
			writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": command.ID, "result": map[string]any{}})
		}
		for _, method := range []string{"session.unsubscribe", "network.removeDataCollector", "script.removePreloadScript"} {
			if !removed[method] {
				t.Fatalf("canceled page retained %s", method)
			}
		}
		end := requireBiDiTestCommand(t, commands)
		if end.Method != "session.end" {
			t.Fatalf("invalid in-place reload did not close the runtime: %+v", end)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": end.ID, "result": map[string]any{}})
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("reload error = %v", err)
		}
		if !worker.closed || !worker.released || worker.page != "" || len(client.channels) != 0 || len(client.loads) != 0 {
			t.Fatal("failed reload retained page handles or load resources")
		}
	})
}

func TestExtraChatTabIsClosedAfterResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		main := &chatTab{contextID: "main", page: "main-page"}
		extra := &chatTab{contextID: "extra", page: "extra-page", busy: true}
		worker := &Worker{client: client, contextID: "main", page: "main-page", chats: []*chatTab{main, extra}, tabs: map[string]*tabUse{"extra": {requests: 1}}}
		done := make(chan struct{})
		go func() { worker.releaseChat(extra, true); close(done) }()
		command := requireBiDiTestCommand(t, commands)
		if command.Method != "browsingContext.close" || command.Params["context"] != "extra" {
			t.Fatalf("extra chat was kept alive or the primary page was closed: %+v", command)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": command.ID, "result": map[string]any{}})
		<-done
		if len(worker.chats) != 1 || worker.chats[0] != main || len(worker.tabs) != 0 || extra.resetting != nil {
			t.Fatal("completed extra tab retained an idle page or reset task")
		}
		requireNoBiDiTestCommand(t, commands)
	})
}

func TestCloseDoesNotWaitForBusyProofLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		worker := &Worker{client: client, connection: client.connection, contextID: "page", page: "root", tabs: make(map[string]*tabUse)}
		channel, err := client.openChannel("unread-response")
		if err != nil {
			t.Fatal(err)
		}
		channel.push("unread-buffer")
		proofDone := make(chan error, 1)
		go func() { _, err := worker.Proof(t.Context(), "digest", "text"); proofDone <- err }()
		proof := requireBiDiTestCommand(t, commands)
		if proof.Method != "script.callFunction" {
			t.Fatalf("proof command = %+v", proof)
		}
		closed := make(chan error, 1)
		go func() { closed <- worker.Close() }()
		end := requireBiDiTestCommand(t, commands)
		if end.Method != "session.end" {
			t.Fatalf("close blocked behind proof: %+v", end)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": end.ID, "result": map[string]any{}})
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if err := <-proofDone; err == nil {
			t.Fatal("closed proof unexpectedly succeeded")
		}
		if len(client.pending) != 0 || !worker.released || channel.queue != nil {
			t.Fatal("Close retained pending commands")
		}
	})
}

// Exercise the real page/reset/reload code over the offline BiDi transport.
// A page stops consuming a slot only when its close acknowledgement is sent.
type workerPageFixture struct {
	worker        *Worker
	peer          *websocket.Conn
	commands      <-chan bidiTestCommand
	live          map[string]bool
	peak          int
	created       int
	navigated     []string
	closeFailures map[string]int
}

func newWorkerPageFixture(t *testing.T) *workerPageFixture {
	t.Helper()
	client, peer, commands := newBiDiTestClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	worker := &Worker{
		client: client, connection: client.connection, ctx: ctx, cancel: cancel,
		contextID: "main", selected: "main", page: "main-page", options: Options{Model: "fixture", Headless: true},
		chats: []*chatTab{{contextID: "main", page: "main-page"}}, tabs: map[string]*tabUse{"main": {}},
		// No process is started, and Close cannot signal the synthetic PID.
		process: &browserProcess{closed: true, command: &exec.Cmd{Process: &os.Process{Pid: -1}}},
	}
	worker.idle = time.AfterFunc(idleMinimizeDelay, worker.minimizeMemory)
	t.Cleanup(func() {
		if worker.idle != nil {
			worker.idle.Stop()
		}
	})
	return &workerPageFixture{worker: worker, peer: peer, commands: commands, live: map[string]bool{"main": true}, peak: 1, closeFailures: make(map[string]int)}
}

func (fixture *workerPageFixture) reply(t *testing.T, command bidiTestCommand) {
	t.Helper()
	result := map[string]any{}
	switch command.Method {
	case "browsingContext.create":
		fixture.created++
		id := fmt.Sprintf("new-%d", fixture.created)
		fixture.live[id] = true
		fixture.peak = max(fixture.peak, len(fixture.live))
		result["context"] = id
	case "browsingContext.close":
		id := command.Params["context"].(string)
		if fixture.closeFailures[id] > 0 {
			fixture.closeFailures[id]--
			writeBiDiTestMessage(t, fixture.peer, map[string]any{"type": "error", "id": command.ID, "error": "unknown error", "message": "fixture close failed"})
			return
		}
		if !fixture.live[id] {
			t.Errorf("closed an unowned page: %s", id)
		}
		delete(fixture.live, id)
	case "browsingContext.getTree":
		result["contexts"] = []any{map[string]any{"context": "chrome", "url": browserWindowURL}}
	case "browsingContext.navigate":
		id := command.Params["context"].(string)
		fixture.navigated = append(fixture.navigated, id)
		if command.Params["url"] != aiStudioOrigin+"/prompts/new_chat?model=fixture&temporary=true" {
			t.Fatalf("cold-start URL = %v", command.Params["url"])
		}
		var headers []any
		for _, name := range append([]string{"x-aistudio-visit-id"}, publicHeaderNames...) {
			headers = append(headers, map[string]any{"name": name, "value": map[string]any{"type": "string", "value": "fixture"}})
		}
		for _, name := range pageCatalogRPCs {
			request := map[string]any{"request": name, "method": http.MethodPost,
				"url": "https://alkalimakersuite-pa.clients6.google.com/" + name, "headers": headers}
			for _, method := range []string{"network.beforeRequestSent", "network.responseCompleted"} {
				writeBiDiTestMessage(t, fixture.peer, map[string]any{"type": "event", "method": method, "params": map[string]any{"context": id, "request": request}})
			}
		}
		writeBiDiTestMessage(t, fixture.peer, map[string]any{"type": "event", "method": "network.responseCompleted",
			"params": map[string]any{"context": id, "request": map[string]any{"url": aiStudioOrigin + "/generate_204", "method": http.MethodGet}}})
	case "script.addPreloadScript":
		result["script"] = "preload"
		arguments := command.Params["arguments"].([]any)
		channel := arguments[0].(map[string]any)["value"].(map[string]any)["channel"]
		writeBiDiTestMessage(t, fixture.peer, map[string]any{"type": "event", "method": "script.message",
			"params": map[string]any{"channel": channel, "data": map[string]any{"type": "object", "handle": "service"}}})
	case "network.addDataCollector":
		result["collector"] = "catalog"
	case "network.getData":
		result["bytes"] = map[string]any{"type": "string", "value": "[]"}
	case "script.evaluate", "script.callFunction":
		value := map[string]any{"type": "boolean", "value": true}
		if command.Params["expression"] == "location.href" {
			value = map[string]any{"type": "string", "value": aiStudioOrigin + "/prompts/new_chat"}
		}
		switch command.Params["functionDeclaration"] {
		case pointerTargetFunction:
			value = map[string]any{"type": "string", "value": `{"x":1,"y":1}`}
		case pageObjectFunction:
			value = map[string]any{"type": "object", "handle": "new-page"}
		case `page => page.key`:
			value = map[string]any{"type": "string", "value": "snapshot"}
		}
		result = map[string]any{"type": "success", "result": value}
	case "session.end":
		clear(fixture.live)
	case "session.subscribe", "session.unsubscribe", "network.removeDataCollector", "script.removePreloadScript", "script.disown", "input.performActions":
	default:
		t.Fatalf("unexpected page fixture command: %+v", command)
	}
	writeBiDiTestMessage(t, fixture.peer, map[string]any{"type": "success", "id": command.ID, "result": result})
}

func (fixture *workerPageFixture) drain(t *testing.T) {
	t.Helper()
	for {
		synctest.Wait()
		select {
		case command := <-fixture.commands:
			if command.err != nil {
				t.Fatal(command.err)
			}
			fixture.reply(t, command)
		default:
			return
		}
	}
}

func TestCloseDoesNotRearmIdleMemoryTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newWorkerPageFixture(t)
		worker := fixture.worker
		boosted := make(chan func(), 1)
		go func() {
			unboost, err := worker.boostFrames(t.Context())
			if err != nil {
				t.Error(err)
			}
			boosted <- unboost
		}()
		fixture.drain(t)
		unboost := <-boosted
		if unboost == nil {
			t.Fatal("frame boost failed")
		}
		closed := make(chan error, 1)
		go func() { closed <- worker.Close() }()
		end := requireBiDiTestCommand(t, fixture.commands)
		if end.Method != "session.end" {
			t.Fatalf("close command = %+v", end)
		}
		fixture.reply(t, end)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		unboost()
		unboost()
		worker.minimizeMemory()
		if worker.idle != nil || worker.frameBoosts != 0 {
			t.Fatal("late frame cleanup rearmed a closed worker's memory timer")
		}
	})
}

func TestReloadWaitsForAsyncChatResetBeforeBudgetReuse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newWorkerPageFixture(t)
		worker := fixture.worker
		tab, err := worker.acquireChat(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		body := &protectedResponseBody{ctx: ctx, cancel: cancel, worker: worker, done: true, completed: true,
			release: func(completed bool) { worker.releaseChat(tab, completed) }}
		if err := body.Close(); err != nil {
			t.Fatal(err)
		}
		// Reset is a real goroutine, blocked in boostFrames before inputMu.
		reset := requireBiDiTestCommand(t, fixture.commands)
		if reset.Method != "browsingContext.getTree" {
			t.Fatalf("reset barrier = %+v", reset)
		}
		reloaded, reused := make(chan error, 1), make(chan error, 1)
		go func() {
			err := worker.Reload(t.Context())
			reloaded <- err
			if err == nil {
				// The caller can give the extra opening to account B on return.
				_, err = worker.client.command(t.Context(), "browsingContext.create", map[string]any{"type": "tab", "background": true})
			}
			reused <- err
		}()
		fixture.drain(t)
		if len(reloaded) != 0 || fixture.created != 0 {
			t.Errorf("Reload reused its budget before reset completed: live=%d peak=%d created=%d", len(fixture.live), fixture.peak, fixture.created)
		}
		fixture.reply(t, reset)
		fixture.drain(t)
		if err := <-reloaded; err != nil {
			t.Fatal(err)
		}
		if err := <-reused; err != nil {
			t.Fatal(err)
		}
		worker.pending.Wait()
		if fixture.peak > 2 || len(fixture.live) != 2 || len(fixture.navigated) != 1 || fixture.navigated[0] != "main" {
			t.Fatalf("reset/reload exceeded maxActive=2 or replaced an idle page: peak=%d live=%v navigated=%v", fixture.peak, fixture.live, fixture.navigated)
		}
	})
}

func TestRetiredTabCloseRetainsOwnershipAndCanRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newWorkerPageFixture(t)
		worker := fixture.worker
		use := &tabUse{requests: 1, retired: true}
		worker.tabs["old"] = use
		fixture.live["old"] = true
		fixture.closeFailures["old"] = 1
		finished := make(chan struct{})
		go func() { worker.finishRequest("old"); close(finished) }()
		closing := requireBiDiTestCommand(t, fixture.commands)
		if closing.Method != "browsingContext.close" || closing.Params["context"] != "old" {
			t.Fatalf("retired close = %+v", closing)
		}
		if worker.tabs["old"] != use || use.requests != 0 {
			t.Error("retired page lost ownership before physical close")
		}
		joined := make(chan error, 1)
		go func() { joined <- worker.closeTab("old") }()
		requireNoBiDiTestCommand(t, fixture.commands)
		if len(joined) != 0 {
			t.Error("concurrent close did not join the pending acknowledgement")
		}
		fixture.reply(t, closing)
		<-finished
		if err := <-joined; err == nil {
			t.Error("concurrent close lost the shared close failure")
		}
		if worker.tabs["old"] != use || !fixture.live["old"] {
			t.Error("failed close lost the still-live retired page")
		}
		retried := make(chan error, 1)
		go func() { retried <- worker.closeTab("old") }()
		closing = requireBiDiTestCommand(t, fixture.commands)
		if worker.tabs["old"] != use {
			t.Error("retry did not retain the original page ownership")
		}
		fixture.reply(t, closing)
		if err := <-retried; err != nil || worker.tabs["old"] != nil || fixture.live["old"] {
			t.Fatalf("retired retry = %v, uses=%v live=%v", err, worker.tabs, fixture.live)
		}
		if err := worker.closeTab("old"); err != nil {
			t.Fatal(err)
		}
		requireNoBiDiTestCommand(t, fixture.commands)
	})
}

func TestReloadCancellationKeepsPendingResetOwned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newWorkerPageFixture(t)
		worker := fixture.worker
		tab, err := worker.acquireChat(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		worker.releaseChat(tab, true)
		reset := requireBiDiTestCommand(t, fixture.commands)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		reloaded := make(chan error, 1)
		go func() { reloaded <- worker.Reload(ctx) }()
		requireNoBiDiTestCommand(t, fixture.commands)
		cancel()
		synctest.Wait()
		select {
		case err := <-reloaded:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled reload = %v", err)
			}
		default:
			t.Fatal("Reload did not cancel its cleanup wait")
		}
		if worker.tabs["main"].requests != 1 || tab.resetting == nil || fixture.created != 0 {
			t.Fatal("canceled Reload abandoned its reset or opened another page")
		}
		fixture.reply(t, reset)
		fixture.drain(t)
		worker.pending.Wait()
		if tab.busy || tab.resetting != nil || worker.tabs["main"].requests != 0 || len(fixture.live) != 1 {
			t.Fatal("reset failed to finish after Reload canceled its wait")
		}
	})
}

func TestReloadWaitsForRetiredResetClose(t *testing.T) {
	for _, failClose := range []bool{false, true} {
		t.Run(fmt.Sprintf("close-error=%t", failClose), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newWorkerPageFixture(t)
				worker := fixture.worker
				tab, err := worker.acquireChat(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				worker.releaseChat(tab, true)
				reset := requireBiDiTestCommand(t, fixture.commands)
				resetDone := tab.resetting
				opened := make(chan string, 1)
				go func() {
					id, err := worker.createTab(t.Context())
					if err != nil {
						t.Error(err)
					}
					opened <- id
				}()
				fixture.reply(t, requireBiDiTestCommand(t, fixture.commands))
				id := <-opened
				// A reload that began with a live response may already have switched.
				if err := worker.switchTab(id, loadedPage{page: "replacement"}); err != nil {
					t.Fatal(err)
				}
				reloaded := make(chan error, 1)
				go func() { reloaded <- worker.Reload(t.Context()) }()
				fixture.drain(t)
				if len(reloaded) != 0 {
					t.Error("Reload forgot a reset on a retired chat no longer in worker.chats")
				}
				fixture.reply(t, reset)
				var closing bidiTestCommand
				for {
					closing = requireBiDiTestCommand(t, fixture.commands)
					if closing.Method == "browsingContext.close" {
						break
					}
					fixture.reply(t, closing)
				}
				if closing.Params["context"] != "main" || worker.tabs["main"] == nil {
					t.Error("retired reset lost its page before the close acknowledgement")
				}
				select {
				case <-resetDone:
					t.Error("reset completion was published before retired close completed")
				default:
				}
				if failClose {
					fixture.closeFailures["main"] = 1
					fixture.reply(t, closing)
					closing = requireBiDiTestCommand(t, fixture.commands)
					if closing.Method != "browsingContext.close" || closing.Params["context"] != "main" || worker.tabs["main"] == nil {
						t.Fatalf("Reload did not retry the retained page: %+v", closing)
					}
				}
				if len(reloaded) != 0 {
					t.Error("Reload released its extra slot while retired close was blocked")
				}
				fixture.reply(t, closing)
				fixture.drain(t)
				if err := <-reloaded; err != nil {
					t.Fatal(err)
				}
				worker.pending.Wait()
				if len(fixture.live) != 1 || !fixture.live[id] || worker.tabs["main"] != nil {
					t.Fatalf("retired reset was not completely released: live=%v uses=%v", fixture.live, worker.tabs)
				}
			})
		})
	}
}

func TestReloadReportsRetiredCloseFailureBeforeOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newWorkerPageFixture(t)
		worker := fixture.worker
		worker.tabs["old"] = &tabUse{retired: true}
		fixture.live["old"] = true
		fixture.closeFailures["old"] = 1
		reloaded := make(chan error, 1)
		go func() { reloaded <- worker.Reload(t.Context()) }()
		fixture.drain(t)
		if err := <-reloaded; err == nil {
			t.Error("Reload hid the retired close failure from its budget owner")
		}
		if fixture.created != 0 || len(fixture.navigated) != 0 || worker.tabs["old"] == nil || !fixture.live["old"] {
			t.Fatalf("failed cleanup allowed a reload or lost its slot: created=%d navigated=%v uses=%v live=%v", fixture.created, fixture.navigated, worker.tabs, fixture.live)
		}
		go func() { reloaded <- worker.Reload(t.Context()) }()
		fixture.drain(t)
		if err := <-reloaded; err != nil || len(fixture.live) != 1 {
			t.Fatalf("Reload could not retry retained cleanup: %v live=%v", err, fixture.live)
		}
	})
}

func TestReloadKeepsLiveResponseWithoutWaitingForIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newWorkerPageFixture(t)
		worker := fixture.worker
		worker.useTab("main")
		reloaded := make(chan error, 1)
		go func() { reloaded <- worker.Reload(t.Context()) }()
		fixture.drain(t)
		select {
		case err := <-reloaded:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("Reload waited for a live response instead of keeping its old page")
		}
		if len(fixture.live) != 2 || !worker.tabs["main"].retired || worker.tabs["main"].requests != 1 {
			t.Fatal("Reload did not retain the live original page")
		}
		finished := make(chan struct{})
		go func() { worker.finishRequest("main"); close(finished) }()
		fixture.drain(t)
		<-finished
		if len(fixture.live) != 1 || fixture.live["main"] {
			t.Fatal("last old response did not physically close only its own page")
		}
	})
}
