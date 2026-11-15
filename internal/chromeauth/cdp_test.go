package chromeauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestReadCDPBrowserPreservesEnvironmentAndCookieBoundary(t *testing.T) {
	for _, page := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-page", true: "page-environment"}[page], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				upgrader := websocket.Upgrader{}
				connection, err := upgrader.Upgrade(writer, request, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer connection.Close()
				methods := []string{"Storage.getCookies", "Target.getTargets"}
				if page {
					methods = append(methods, "Target.attachToTarget", "Runtime.evaluate")
				}
				for _, method := range methods {
					var command struct {
						ID        int            `json:"id"`
						Method    string         `json:"method"`
						Params    map[string]any `json:"params"`
						SessionID string         `json:"sessionId"`
					}
					if err := connection.ReadJSON(&command); err != nil {
						t.Error(err)
						return
					}
					if command.Method != method {
						t.Errorf("CDP method = %s, want %s", command.Method, method)
						return
					}
					var result any
					switch method {
					case "Storage.getCookies":
						result = map[string]any{"cookies": []any{
							map[string]any{"name": "SAPISID", "value": "fixture", "domain": ".google.com"},
							map[string]any{"name": "partitioned", "value": "excluded", "partitionKey": map[string]any{"topLevelSite": "https://example.com"}},
						}}
					case "Target.getTargets":
						targets := []any{map[string]any{"type": "service_worker", "targetId": "worker"}}
						if page {
							targets = append(targets, map[string]any{"type": "page", "targetId": "page"})
						}
						result = map[string]any{"targetInfos": targets}
					case "Target.attachToTarget":
						if command.Params["targetId"] != "page" || command.Params["flatten"] != true {
							t.Error("environment discovery did not attach to a page")
						}
						result = map[string]any{"sessionId": "page-session"}
					case "Runtime.evaluate":
						if command.SessionID != "page-session" || command.Params["returnByValue"] != true || command.Params["expression"] != "[navigator.language, Intl.DateTimeFormat().resolvedOptions().timeZone]" {
							t.Error("environment discovery lost its attached realm")
						}
						result = map[string]any{"result": map[string]any{"value": []string{"ja-JP", "Asia/Tokyo"}}}
					}
					if err := connection.WriteJSON(map[string]any{"id": command.ID, "result": result}); err != nil {
						t.Error(err)
						return
					}
				}
			}))
			defer server.Close()
			browser, err := ReadCDPBrowser(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"))
			if err != nil {
				t.Fatal(err)
			}
			if page && (browser.Locale != "ja-JP" || browser.Timezone != "Asia/Tokyo") || !page && (browser.Locale != "" || browser.Timezone != "") {
				t.Fatalf("browser environment = %+v", browser)
			}
			var cookies []map[string]any
			if err := json.Unmarshal([]byte(browser.Cookies), &cookies); err != nil || len(cookies) != 1 || cookies[0]["name"] != "SAPISID" {
				t.Fatalf("CDP cookie boundary changed: %s, %v", browser.Cookies, err)
			}
		})
	}
}
