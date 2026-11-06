package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type cookieImportAdmin struct {
	AdminService
	lifecycle    context.Context
	mu           sync.Mutex
	inputs       []CookieImportInput
	browserCalls int
	importCookie func(context.Context, CookieImportInput) (AdminAccount, error)
}

func (admin *cookieImportAdmin) AccountTaskContext() context.Context { return admin.lifecycle }

func (admin *cookieImportAdmin) CookieBrowsers(context.Context) ([]CookieBrowser, error) {
	admin.browserCalls++
	return []CookieBrowser{{Address: "127.0.0.1:9222", Email: "cookie@example.com", Browser: "Chromium", Locale: "zh-CN", Timezone: "Asia/Shanghai"}}, nil
}

func (admin *cookieImportAdmin) ImportCookieAccount(ctx context.Context, input CookieImportInput) (AdminAccount, error) {
	admin.mu.Lock()
	admin.inputs = append(admin.inputs, input)
	admin.mu.Unlock()
	if admin.importCookie != nil {
		return admin.importCookie(ctx, input)
	}
	return AdminAccount{ID: "cookie@example.com", Label: "cookie@example.com"}, nil
}

func (admin *cookieImportAdmin) capturedInputs() []CookieImportInput {
	admin.mu.Lock()
	defer admin.mu.Unlock()
	return append([]CookieImportInput(nil), admin.inputs...)
}

func accountTaskRequest(handler http.Handler, method, path, body string, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "127.0.0.1:12345"
	request.Header = headers.Clone()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func waitAccountTask(t *testing.T, handler http.Handler, id string, headers http.Header) AccountTask {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		response := accountTaskRequest(handler, http.MethodGet, "/api/accounts/tasks", "", headers)
		var result struct {
			Tasks []AccountTask `json:"tasks"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("task list = %d %s", response.Code, response.Body)
		}
		for _, task := range result.Tasks {
			if task.ID == id && (task.State == "done" || task.State == "failed") {
				return task
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("account task did not finish")
		case <-tick.C:
		}
	}
}

func TestCookieImportEndpoint(t *testing.T) {
	admin := &cookieImportAdmin{lifecycle: t.Context()}
	handler := NewHandler(nil, Config{Admin: admin, AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "secret", APIKey: "generation-key"})
	serve := func(path, body string, headers http.Header) *httptest.ResponseRecorder {
		return accountTaskRequest(handler, http.MethodPost, path, body, headers)
	}
	const path = "/api/accounts/import/cookies"
	const body = `{"items":[{"email":"forged@example.com","cookies":"SAPISID=cookie-secret","auth_user":2}],"proxy":"http://127.0.0.1:8080","locale":"zh-CN","timezone":"Asia/Shanghai"}`
	for _, headers := range []http.Header{nil, {"Authorization": {"Bearer generation-key"}}} {
		if response := serve(path, body, headers); response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status = %d", response.Code)
		}
	}
	login := serve("/api/auth/login", `{"username":"admin","password":"secret"}`, nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("login = %d", login.Code)
	}
	session := http.Header{"Cookie": {login.Result().Cookies()[0].String()}}
	foreign := session.Clone()
	foreign.Set("Origin", "https://evil.example")
	if response := serve(path, body, foreign); response.Code != http.StatusForbidden {
		t.Fatalf("cross origin = %d", response.Code)
	}
	if len(admin.capturedInputs()) != 0 {
		t.Fatal("unauthorized call reached importer")
	}
	importTask := func(body string) AccountTask {
		t.Helper()
		response := serve(path, body, session)
		var result struct {
			Task AccountTask `json:"task"`
		}
		if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Task.ID == "" {
			t.Fatalf("import = %d %s", response.Code, response.Body)
		}
		if result.Task.State != "running" || strings.Contains(response.Body.String(), "cookie-secret") || strings.Contains(response.Body.String(), "forged@example.com") {
			t.Fatal("202 disclosed unverified identity or did not return a running task")
		}
		task := waitAccountTask(t, handler, result.Task.ID, session)
		if task.State != "done" || task.Items[0].Label != "cookie@example.com" {
			t.Fatalf("completed task = %+v", task)
		}
		return task
	}
	importTask(body)
	inputs := admin.capturedInputs()
	if len(inputs) != 1 || inputs[0].Locale != "zh-CN" || inputs[0].Cookies != "SAPISID=cookie-secret" || inputs[0].Proxy == "" || inputs[0].Method != "browser" || inputs[0].AuthUser != 2 {
		t.Fatal("input not forwarded")
	}
	encodeItems := func(items []cookieImportItem) string {
		data, err := json.Marshal(cookieImportRequest{Items: items})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	large := encodeItems([]cookieImportItem{{Cookies: strings.Repeat("x", aistudio.MaxCookieImportBytes+1)}})
	escapedItem := cookieImportItem{Cookies: strings.Repeat("<", aistudio.MaxCookieImportBytes)}
	tooMany := make([]cookieImportItem, maxAccountTaskItems+1)
	for index := range tooMany {
		tooMany[index].Cookies = "SAPISID=fixture"
	}
	for _, body := range []string{
		`{`, `{"cookies":"old-single-object"}`, `{"items":[]}`, `{"items":[{"cookies":[]}]}`,
		`{"items":[{"cookies":""}]}`, `{"items":[{"cookies":"x","method":true}]}`,
		`{"items":[{"cookies":"x","method":"automatic"}]}`, `{"items":[{"cookies":"x","auth_user":-1}]}`,
		`{"items":[{"cookies":"x"},{"cookies":"x","method":"automatic"}]}`,
		`{"items":[{"cookies":"x","auth_user":10}]}`, `{"items":[{"cookies":"x","auth_user":1.5}]}`,
		`{"items":[{"cookies":"x"}],"method":"http"}`, `{"items":[{"cookies":"x"}]} {}`,
		large, encodeItems(tooMany), encodeItems([]cookieImportItem{escapedItem, escapedItem}), strings.Repeat(" ", maxCookieImportRequestBytes+1),
	} {
		if response := serve(path, body, session); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid request = %d", response.Code)
		}
	}
	if len(admin.capturedInputs()) != 1 {
		t.Fatal("invalid input reached importer")
	}
	importTask(`{"items":[{"cdp":"127.0.0.1:9222","email":"forged@example.com","auth_user":0}],"locale":"ja-JP"}`)
	inputs = admin.capturedInputs()
	if len(inputs) != 2 || inputs[1].CDP != "127.0.0.1:9222" || inputs[1].Locale != "ja-JP" {
		t.Fatal("CDP entry did not reach the same importer")
	}
	if response := serve(path, `{"items":[{"cdp":" "}]}`, session); response.Code != http.StatusBadRequest {
		t.Fatal("blank CDP bypassed required Cookie text")
	}
	// Escaping may expand a valid 1 MiB raw value beyond the raw-text limit.
	importTask(encodeItems([]cookieImportItem{escapedItem}))
	unicodeLarge := encodeItems([]cookieImportItem{{Cookies: strings.Repeat("\u4e2d", aistudio.MaxCookieImportBytes/3+1)}})
	if response := serve(path, unicodeLarge, session); response.Code != http.StatusBadRequest {
		t.Fatal("Cookie limit counted characters instead of UTF-8 bytes")
	}
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/accounts/import/browsers", nil)
		if authenticated {
			request.Header = session.Clone()
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		want := http.StatusUnauthorized
		if authenticated {
			want = http.StatusOK
		}
		if recorder.Code != want {
			t.Fatalf("browser discovery auth=%v status=%d", authenticated, recorder.Code)
		}
	}
	if admin.browserCalls != 1 {
		t.Fatal("unauthenticated browser discovery reached the service")
	}
	for _, method := range []string{"browser", "http"} {
		importTask(encodeItems([]cookieImportItem{{Cookies: "SAPISID=cookie-secret", Method: method}}))
		inputs = admin.capturedInputs()
		if inputs[len(inputs)-1].Method != method {
			t.Fatalf("verification method %s was not forwarded", method)
		}
	}
	importTask(encodeItems(tooMany[:maxAccountTaskItems]))
}
