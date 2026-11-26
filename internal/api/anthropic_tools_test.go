package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestAnthropicServerToolChoice 核对 tool_choice 指定 server tool 时只开启该工具对应的 Google 工具与它的选项，指定客户端函数时仍只允许该函数
func TestAnthropicServerToolChoice(t *testing.T) {
	const search = `{"type":"web_search_20250305","name":"web_search","max_uses":1}`
	const tools = search + `,{"type":"web_fetch_20250910","name":"web_fetch","allowed_domains":["example.com"]},{"name":"Read","input_schema":{"type":"object"}}`
	for _, test := range []struct {
		name, tools, choice string
		google, functions   []string
		allowed             []string
		search, fetch       bool
	}{
		{"web_search", search, "web_search", []string{"google_search"}, nil, nil, true, false},
		{"web_search with functions", tools, "web_search", []string{"google_search"}, nil, nil, true, false},
		{"web_fetch with functions", tools, "web_fetch", []string{"url_context"}, nil, nil, false, true},
		{"function", tools, "Read", []string{"google_search", "url_context"}, []string{"Read"}, []string{"Read"}, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var request anthropicRequest
			body := `{"model":"m","max_tokens":400,"messages":[{"role":"user","content":"x"}],"tools":[` + test.tools + `],"tool_choice":{"type":"tool","name":"` + test.choice + `"}}`
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Fatal(err)
			}
			got, err := request.toGenerateRequest("id")
			if err != nil {
				t.Fatal(err)
			}
			var functions []string
			for _, declaration := range got.Tools.Functions {
				functions = append(functions, declaration.Name)
			}
			config := got.Tools.ToolConfig
			if config.Mode != "required" || !slices.Equal(config.AllowedFunctionNames, test.allowed) || !slices.Equal(got.Tools.Google, test.google) || !slices.Equal(functions, test.functions) ||
				(got.Tools.GoogleSearch != nil) != test.search || (got.Tools.URLContext != nil) != test.fetch {
				t.Fatalf("tools=%+v functions=%v", got.Tools, functions)
			}
		})
	}
}

func TestAnthropicSearchUsesAdmittedKeySnapshot(t *testing.T) {
	const keys, snippet = "first, second", "signed fixture text"
	source := aistudio.GroundingChunk{Source: "web", URI: "https://source.example", Title: "Fixture", Text: snippet}
	for _, prefix := range []string{"/v1", "/build/v1"} {
		t.Run(prefix, func(t *testing.T) {
			reads := 0
			current := keys
			var history string
			handler := NewHandler(generationTestService{generate: func(_ context.Context, req aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
				history = req.Contents[0].Parts[0].Text
				current = "rotated"
				return testEventStream(aistudio.Event{Kind: aistudio.EventGrounding, Grounding: &aistudio.GroundingMetadata{
					WebSearchQueries: []string{"test"}, Chunks: []aistudio.GroundingChunk{source},
				}}, aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}), nil
			}}, Config{APIKey: "unused", APIKeys: func() (string, map[string][]string) {
				reads++
				return current, nil
			}})
			send := func(key, messages string) []byte {
				t.Helper()
				r := httptest.NewRequest(http.MethodPost, prefix+"/messages", strings.NewReader(`{"model":"test","max_tokens":16,"messages":`+messages+`,"tools":[{"type":"web_search_20250305","name":"web_search"}]}`))
				r.Header.Set("X-API-Key", key)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("status=%d: %s", w.Code, w.Body)
				}
				return w.Body.Bytes()
			}
			var result struct{ Content json.RawMessage }
			if err := json.Unmarshal(send("second", `[{"role":"user","content":"hi"}]`), &result); err != nil {
				t.Fatal(err)
			}
			if reads != 1 || !strings.Contains(string(result.Content), `"encrypted_content"`) || strings.Contains(string(result.Content), snippet) {
				t.Fatalf("search result did not seal with the admission snapshot: reads=%d content=%s", reads, result.Content)
			}
			previous := `[{"role":"assistant","content":` + string(result.Content) + `},{"role":"user","content":"next"}]`
			current = keys
			send("first", previous)
			if reads != 2 || !strings.Contains(history, snippet) {
				t.Fatalf("same service key list could not verify history: reads=%d history=%s", reads, history)
			}
			send("rotated", previous)
			if reads != 3 || strings.Contains(history, snippet) || !strings.Contains(history, source.URI) {
				t.Fatalf("rotated key recovered the old snippet or lost the public URL: reads=%d history=%s", reads, history)
			}
		})
	}
}
