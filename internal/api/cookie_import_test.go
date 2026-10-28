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

type cookieImportAdmin struct {
	AdminService
	inputs       []CookieImportInput
	browserCalls int
}

func (admin *cookieImportAdmin) CookieBrowsers(context.Context) ([]CookieBrowser, error) {
	admin.browserCalls++
	return []CookieBrowser{{Address: "127.0.0.1:9222", Email: "cookie@example.com", Browser: "Chromium", Locale: "zh-CN", Timezone: "Asia/Shanghai"}}, nil
}

func (admin *cookieImportAdmin) ImportCookieAccount(_ context.Context, input CookieImportInput) (AdminAccount, error) {
	admin.inputs = append(admin.inputs, input)
	return AdminAccount{ID: "cookie@example.com", Label: "cookie@example.com"}, nil
}

func TestCookieImportEndpoint(t *testing.T) {
	admin := &cookieImportAdmin{}
	handler := NewHandler(nil, Config{Admin: admin, AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "secret", APIKey: "generation-key"})
	serve := func(path, body string, headers http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		for key, values := range headers {
			request.Header[key] = values
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	const path = "/api/accounts/import/cookies"
	const body = `{"cookies":"SAPISID=cookie-secret","proxy":"http://127.0.0.1:8080","locale":"zh-CN","timezone":"Asia/Shanghai"}`
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
	if len(admin.inputs) != 0 {
		t.Fatal("unauthorized call reached importer")
	}
	response := serve(path, body, session)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "cookie-secret") {
		t.Fatalf("import = %d %s", response.Code, response.Body)
	}
	if len(admin.inputs) != 1 || admin.inputs[0].Locale != "zh-CN" || admin.inputs[0].Cookies != "SAPISID=cookie-secret" || admin.inputs[0].Proxy == "" {
		t.Fatal("input not forwarded")
	}
	large, _ := json.Marshal(CookieImportInput{Cookies: strings.Repeat("x", aistudio.MaxCookieImportBytes+1)})
	for _, body := range []string{`{`, `{"cookies":[]}`, `{"cookies":""}`, `{"cookies":"x"} {}`, string(large), strings.Repeat(" ", 8*aistudio.MaxCookieImportBytes+1)} {
		if response := serve(path, body, session); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid request = %d", response.Code)
		}
	}
	if len(admin.inputs) != 1 {
		t.Fatal("invalid input reached importer")
	}
	response = serve(path, `{"cdp":"127.0.0.1:9222","label":"claimed@example.com","locale":"ja-JP"}`, session)
	if response.Code != http.StatusCreated || len(admin.inputs) != 2 || admin.inputs[1].CDP != "127.0.0.1:9222" || admin.inputs[1].Locale != "ja-JP" {
		t.Fatalf("CDP entry did not reach the same importer: %d %s", response.Code, response.Body)
	}
	if response := serve(path, `{"cdp":" "}`, session); response.Code != http.StatusBadRequest {
		t.Fatal("blank CDP bypassed required Cookie text")
	}
	// Escaping may expand a valid 1 MiB raw value beyond the raw-text limit.
	escaped, _ := json.Marshal(CookieImportInput{Cookies: strings.Repeat("<", aistudio.MaxCookieImportBytes)})
	if response := serve(path, string(escaped), session); response.Code != http.StatusCreated {
		t.Fatalf("valid escaped envelope rejected: %d", response.Code)
	}
	unicodeLarge, _ := json.Marshal(CookieImportInput{Cookies: strings.Repeat("\u4e2d", aistudio.MaxCookieImportBytes/3+1)})
	if response := serve(path, string(unicodeLarge), session); response.Code != http.StatusBadRequest {
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
}
