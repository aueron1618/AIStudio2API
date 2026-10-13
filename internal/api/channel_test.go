package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type channelTestService struct {
	aistudio.Service
	channel aistudio.Channel
}

func (s *channelTestService) Models(context.Context) ([]aistudio.Model, error) {
	return []aistudio.Model{
		{ID: "playground-only", Channels: []string{"playground"}},
		{ID: "build-only", Channels: []string{"build"}},
		{ID: "shared", Channels: []string{"playground", "build"}},
	}, nil
}

func (s *channelTestService) Generate(ctx context.Context, _ aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	s.channel = aistudio.ChannelFromContext(ctx)
	events := make(chan aistudio.Event, 2)
	events <- aistudio.Event{Kind: aistudio.EventText, Text: "ok"}
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "STOP"}
	close(events)
	return events, nil
}

func TestChannelRoutes(t *testing.T) {
	for _, tc := range []struct {
		prefix  string
		channel aistudio.Channel
		only    string
		other   string
	}{
		{"/v1", aistudio.ChannelPlayground, "playground-only", "build-only"},
		{"/build/v1", aistudio.ChannelBuild, "build-only", "playground-only"},
	} {
		t.Run(string(tc.channel), func(t *testing.T) {
			service := &channelTestService{}
			handler := NewHandler(service, Config{APIKey: "secret"})
			do := func(method, path, body, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, tc.prefix+path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			w := do(http.MethodGet, "/models", "", "secret")
			if w.Code != http.StatusOK {
				t.Fatalf("models status=%d body=%s", w.Code, w.Body)
			}
			var list struct {
				Data []aistudio.Model `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Data) != 2 || !strings.Contains(w.Body.String(), tc.only) || strings.Contains(w.Body.String(), tc.other) {
				t.Fatalf("unexpected models: %s", w.Body)
			}
			if w := do(http.MethodGet, "/models/"+tc.other, "", "secret"); w.Code != http.StatusNotFound {
				t.Fatalf("other channel model status=%d body=%s", w.Code, w.Body)
			}
			body := `{"model":"shared","messages":[{"role":"user","content":"hello"}]}`
			w = do(http.MethodPost, "/chat/completions", body, "secret")
			if w.Code != http.StatusOK || service.channel != tc.channel {
				t.Fatalf("generate status=%d channel=%s body=%s", w.Code, service.channel, w.Body)
			}
			if w := do(http.MethodGet, "/models", "", "wrong"); w.Code != http.StatusUnauthorized {
				t.Fatalf("auth status=%d", w.Code)
			}
		})
	}
}
