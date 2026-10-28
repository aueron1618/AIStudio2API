package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

type rootChannelService struct {
	aistudio.Service
}

func TestRunServerWithoutAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	directory := t.TempDir()
	cfg := config.Default()
	cfg.AuthStates = filepath.Join(directory, "accounts")
	cfg.ListenAddr, cfg.PreviewListenAddr = "127.0.0.1:0", "127.0.0.1:0"
	cfg.AutoStart, cfg.LogFile, cfg.AdminAuthEnabled = false, false, false
	cfg.ProxyAPIKey = "isolated-smoke-key"
	manager, err := newRuntimeManager(ctx, filepath.Join(directory, ".env"), cfg, dataConfigOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		_, err := runServer(ctx, cfg, manager, nil, func(address string) { ready <- address })
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("management listeners did not stop")
		}
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	var address string
	select {
	case address = <-ready:
	case err := <-done:
		t.Fatalf("server did not start: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, tc := range []struct {
		path string
		key  bool
		code int
		html bool
	}{
		{"/", false, http.StatusOK, true},
		{"/playground/", false, http.StatusOK, true},
		{"/api/status", false, http.StatusOK, false},
		{"/v1/models", false, http.StatusUnauthorized, false},
		{"/build/v1/models", false, http.StatusUnauthorized, false},
		{"/v1/models", true, http.StatusServiceUnavailable, false},
		{"/build/v1/models", true, http.StatusServiceUnavailable, false},
		{"/build/v1/not-an-endpoint", true, http.StatusNotFound, false},
	} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.key {
			request.Header.Set("Authorization", "Bearer "+cfg.ProxyAPIKey)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		if readErr != nil || response.StatusCode != tc.code || strings.Contains(response.Header.Get("Content-Type"), "text/html") != tc.html {
			t.Fatalf("%s key=%t status=%d type=%s body=%s err=%v", tc.path, tc.key, response.StatusCode, response.Header.Get("Content-Type"), body, readErr)
		}
	}
}

func (*rootChannelService) Models(context.Context) ([]aistudio.Model, error) {
	return []aistudio.Model{
		{ID: "playground-only", Channels: []string{"playground"}},
		{ID: "build-only", Channels: []string{"build"}},
		{ID: "shared", Channels: []string{"playground", "build"}},
	}, nil
}

func (*rootChannelService) Generate(ctx context.Context, _ aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	events := make(chan aistudio.Event, 2)
	events <- aistudio.Event{Kind: aistudio.EventText, Text: string(aistudio.ChannelFromContext(ctx))}
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "STOP"}
	close(events)
	return events, nil
}

// TestRootChannelRoutes 覆盖 runServer 实际使用的外层路由，防止 Build 请求落入管理页面。
func TestRootChannelRoutes(t *testing.T) {
	handler := rootHandler(api.NewHandler(&rootChannelService{}, api.Config{APIKey: "test-key"}))
	for _, tc := range []struct {
		prefix  string
		channel string
		other   string
	}{
		{"/v1", "playground", "build"},
		{"/build/v1", "build", "playground"},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			do := func(method, path, body, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, tc.prefix+path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				if key != "" {
					r.Header.Set("Authorization", "Bearer "+key)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			t.Run("models", func(t *testing.T) {
				w := do(http.MethodGet, "/models", "", "test-key")
				if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
					t.Fatalf("models status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
				}
				var list struct {
					Data []aistudio.Model `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, model := range list.Data {
					ids = append(ids, model.ID)
					if !slices.Equal(model.Channels, []string{tc.channel}) {
						t.Fatalf("model %s channels=%v", model.ID, model.Channels)
					}
				}
				if len(ids) != 2 || !slices.Contains(ids, "shared") || !slices.Contains(ids, tc.channel+"-only") {
					t.Fatalf("unexpected model IDs: %v", ids)
				}
			})
			t.Run("single-model", func(t *testing.T) {
				if w := do(http.MethodGet, "/models/"+tc.channel+"-only", "", "test-key"); w.Code != http.StatusOK {
					t.Fatalf("own model status=%d body=%s", w.Code, w.Body)
				}
				if w := do(http.MethodGet, "/models/"+tc.other+"-only", "", "test-key"); w.Code != http.StatusNotFound {
					t.Fatalf("other model status=%d body=%s", w.Code, w.Body)
				}
			})
			for _, endpoint := range []struct{ path, body string }{
				{"/chat/completions", `{"model":"shared","messages":[{"role":"user","content":"hello"}]}`},
				{"/chat/completions", `{"model":"shared","messages":[{"role":"user","content":"hello"}],"stream":true}`},
				{"/responses", `{"model":"shared","input":"hello","store":false}`},
				{"/messages", `{"model":"shared","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`},
			} {
				t.Run(endpoint.path, func(t *testing.T) {
					w := do(http.MethodPost, endpoint.path, endpoint.body, "test-key")
					wantType := "application/json"
					if strings.Contains(endpoint.body, `"stream":true`) {
						wantType = "text/event-stream"
					}
					if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), wantType) ||
						!strings.Contains(w.Body.String(), `"`+tc.channel+`"`) {
						t.Fatalf("generate status=%d content-type=%q body=%s", w.Code, w.Header().Get("Content-Type"), w.Body)
					}
				})
			}
			t.Run("auth", func(t *testing.T) {
				for _, key := range []string{"", "wrong"} {
					for _, method := range []string{http.MethodGet, http.MethodPost} {
						path, body := "/models", ""
						if method == http.MethodPost {
							path, body = "/chat/completions", `{"model":"shared","messages":[{"role":"user","content":"hello"}]}`
						}
						if w := do(method, path, body, key); w.Code != http.StatusUnauthorized {
							t.Fatalf("%s auth status=%d body=%s", method, w.Code, w.Body)
						}
					}
				}
			})
			t.Run("unknown-endpoint", func(t *testing.T) {
				w := do(http.MethodGet, "/not-an-endpoint", "", "test-key")
				if w.Code != http.StatusNotFound || strings.Contains(w.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("unknown endpoint status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
				}
			})
		})
	}
}
