package camoufoxnative

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
)

type bidiTestListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
	addr        net.Addr
}

func (listener *bidiTestListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *bidiTestListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (listener *bidiTestListener) Addr() net.Addr { return listener.addr }

type bidiTestCommand struct {
	ID     int            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
	err    error
}

// Use real HTTP/WebSocket framing over net.Pipe so synctest can control both
// scheduling and deadlines without browser access, network I/O or real sleeps.
func newBiDiTestClient(t *testing.T) (*bidiClient, *websocket.Conn, <-chan bidiTestCommand) {
	t.Helper()
	clientPipe, serverPipe := net.Pipe()
	t.Cleanup(func() {
		_ = clientPipe.Close()
		_ = serverPipe.Close()
	})
	listener := &bidiTestListener{
		connections: make(chan net.Conn, 1),
		closed:      make(chan struct{}),
		addr:        serverPipe.LocalAddr(),
	}
	listener.connections <- serverPipe
	accepted := make(chan *websocket.Conn, 1)
	commands := make(chan bidiTestCommand)
	ctx := t.Context()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upgrader := websocket.Upgrader{}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			t.Errorf("upgrade WebSocket: %v", err)
			return
		}
		accepted <- connection
		for {
			var command bidiTestCommand
			command.err = connection.ReadJSON(&command)
			select {
			case commands <- command:
			case <-ctx.Done():
				return
			}
			if command.err != nil {
				return
			}
		}
	}))
	if err := server.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientPipe, nil
		},
	}
	connection, _, err := dialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial WebSocket: %v", err)
	}
	peer := <-accepted
	t.Cleanup(func() {
		_ = connection.Close()
		_ = peer.Close()
	})
	return newBiDiClient(connection), peer, commands
}

func startBiDiTestCommand(client *bidiClient, ctx context.Context, method string) <-chan bidiResponse {
	done := make(chan bidiResponse, 1)
	go func() {
		result, err := client.command(ctx, method, map[string]any{})
		done <- bidiResponse{result: result, err: err}
	}()
	return done
}

func requireBiDiTestResult(t *testing.T, done <-chan bidiResponse, wantErr error) map[string]any {
	t.Helper()
	synctest.Wait()
	select {
	case response := <-done:
		if !errors.Is(response.err, wantErr) {
			t.Fatalf("command error = %v, want %v", response.err, wantErr)
		}
		if wantErr != nil && response.result != nil {
			t.Fatalf("canceled command returned a result: %v", response.result)
		}
		return response.result
	default:
		t.Fatal("command did not return")
		return nil
	}
}

func requireBiDiTestCommand(t *testing.T, commands <-chan bidiTestCommand) bidiTestCommand {
	t.Helper()
	synctest.Wait()
	select {
	case command := <-commands:
		if command.err != nil {
			t.Fatalf("read command: %v", command.err)
		}
		return command
	default:
		t.Fatal("expected a command on the WebSocket")
		return bidiTestCommand{}
	}
}

func requireNoBiDiTestCommand(t *testing.T, commands <-chan bidiTestCommand) {
	t.Helper()
	synctest.Wait()
	select {
	case command := <-commands:
		t.Fatalf("unexpected WebSocket command: %+v", command)
	default:
	}
}

func writeBiDiTestMessage(t *testing.T, peer *websocket.Conn, message map[string]any) {
	t.Helper()
	if err := peer.WriteJSON(message); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func TestBiDiCommandCancellationKeepsConnectionSerial(t *testing.T) {
	for _, test := range []struct {
		name          string
		timeout       bool
		errorResponse bool
	}{
		{name: "cancel"},
		{name: "deadline", timeout: true},
		{name: "canceled-error-response", errorResponse: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, peer, commands := newBiDiTestClient(t)
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				firstDone := startBiDiTestCommand(client, ctx, "test.first")
				first := requireBiDiTestCommand(t, commands)
				if first.ID != 1 || first.Method != "test.first" {
					t.Fatalf("first command = %+v", first)
				}
				if test.timeout {
					<-ctx.Done()
				} else {
					cancel()
				}
				requireBiDiTestResult(t, firstDone, ctx.Err())
				if len(client.commandLock) != 1 {
					t.Fatal("cancellation released the lock before consuming the response")
				}

				waitingCtx, cancelWaiting := context.WithTimeout(t.Context(), time.Second)
				defer cancelWaiting()
				waitingDone := startBiDiTestCommand(client, waitingCtx, "test.canceled-waiter")
				requireNoBiDiTestCommand(t, commands)
				if test.timeout {
					<-waitingCtx.Done()
				} else {
					cancelWaiting()
				}
				requireBiDiTestResult(t, waitingDone, waitingCtx.Err())

				nextDone := startBiDiTestCommand(client, t.Context(), "test.next")
				requireNoBiDiTestCommand(t, commands)
				writeBiDiTestMessage(t, peer, map[string]any{
					"type": "event", "method": "network.responseCompleted",
					"params": map[string]any{
						"request": map[string]any{
							"url":    "https://alkalimakersuite-pa.clients6.google.com" + accessTokenPath,
							"method": http.MethodPost,
						},
						"response": map[string]any{"status": http.StatusOK},
					},
				})
				requireNoBiDiTestCommand(t, commands)
				if client.accessTokenStatus != http.StatusOK {
					t.Fatal("canceled exchange stopped observing network events")
				}
				writeBiDiTestMessage(t, peer, map[string]any{
					"type": "success", "id": first.ID + 100, "result": map[string]any{"value": "unrelated"},
				})
				requireNoBiDiTestCommand(t, commands)

				response := map[string]any{"type": "success", "id": first.ID, "result": map[string]any{"value": "late"}}
				if test.errorResponse {
					response = map[string]any{"type": "error", "id": first.ID, "error": "unknown error", "message": "late failure"}
				}
				writeBiDiTestMessage(t, peer, response)
				next := requireBiDiTestCommand(t, commands)
				if next.ID != first.ID+1 || next.Method != "test.next" {
					t.Fatalf("canceled waiter was sent or command IDs changed: %+v", next)
				}
				writeBiDiTestMessage(t, peer, map[string]any{
					"type": "success", "id": next.ID, "result": map[string]any{"value": "next"},
				})
				if result := requireBiDiTestResult(t, nextDone, nil); result["value"] != "next" {
					t.Fatalf("reused connection returned the wrong response: %v", result)
				}
				if len(client.commandLock) != 0 {
					t.Fatal("completed exchange retained the command lock")
				}
				requireNoBiDiTestCommand(t, commands)
			})
		})
	}
}

func TestBiDiCommandAlreadyCanceledDoesNotSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, peer, commands := newBiDiTestClient(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if result, err := client.command(ctx, "test.canceled", nil); !errors.Is(err, context.Canceled) || result != nil {
			t.Fatalf("already canceled command = %v, %v", result, err)
		}
		if len(client.commandLock) != 0 || client.nextID != 0 {
			t.Fatal("already canceled command retained the lock or allocated an ID")
		}
		requireNoBiDiTestCommand(t, commands)
		done := startBiDiTestCommand(client, t.Context(), "test.next")
		command := requireBiDiTestCommand(t, commands)
		if command.ID != 1 || command.Method != "test.next" {
			t.Fatalf("next command = %+v", command)
		}
		writeBiDiTestMessage(t, peer, map[string]any{"type": "success", "id": command.ID, "result": map[string]any{}})
		requireBiDiTestResult(t, done, nil)
	})
}
