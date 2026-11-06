package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// pairingAdmin 记录配对上传的账户
type pairingAdmin struct {
	AdminService
	imported []AccountStateInput
	renewed  int
}

// ImportAccountState 保存上传内容并返回同名账户
func (admin *pairingAdmin) ImportAccountState(_ context.Context, input AccountStateInput) (AdminAccount, error) {
	admin.imported = append(admin.imported, input)
	return AdminAccount{ID: input.Label, Label: input.Label}, nil
}

type invalidPairingRenewal struct{}

func (invalidPairingRenewal) Error() string   { return "Invalid renewal token" }
func (invalidPairingRenewal) HTTPStatus() int { return http.StatusUnauthorized }

func (admin *pairingAdmin) RenewAccountCookies(_ context.Context, accountID, token string, cookies []aistudio.StateCookie) (AdminAccount, error) {
	if token != "renewal-fixture" || accountID != "user@example.test" || len(cookies) != 1 {
		return AdminAccount{}, invalidPairingRenewal{}
	}
	admin.renewed++
	return AdminAccount{ID: accountID, Label: accountID}, nil
}

// TestPairingTokenAccount 验证令牌由管理会话签发与读取、读取得到当前令牌、上传不需要管理会话、新令牌使旧令牌失效
func TestPairingTokenAccount(t *testing.T) {
	admin := &pairingAdmin{}
	handler := NewHandler(nil, Config{Admin: admin, AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "secret"})
	serve := func(method, path, body string, header http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		for key, values := range header {
			request.Header[key] = values
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if response := serve("POST", "/api/pairing", "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("无会话签发令牌 = %d", response.Code)
	}
	if response := serve("GET", "/api/pairing", "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("无会话读取令牌 = %d", response.Code)
	}
	login := serve("POST", "/api/auth/login", `{"username":"admin","password":"secret"}`, nil)
	cookies := login.Result().Cookies()
	if login.Code != http.StatusOK || len(cookies) == 0 {
		t.Fatalf("登录 = %d %s", login.Code, login.Body)
	}
	session := http.Header{"Cookie": {cookies[0].String()}}
	issue := func() string {
		response := serve("POST", "/api/pairing", "", session)
		var token PairingToken
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &token) != nil || token.Token == "" {
			t.Fatalf("签发令牌 = %d %s", response.Code, response.Body)
		}
		if remaining := time.Until(token.ExpiresAt); remaining <= 9*time.Minute || remaining > 10*time.Minute {
			t.Fatalf("令牌有效期 = %s", remaining)
		}
		return token.Token
	}
	active := func() string {
		response := serve("GET", "/api/pairing", "", session)
		var token PairingToken
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &token) != nil {
			t.Fatalf("读取令牌 = %d %s", response.Code, response.Body)
		}
		return token.Token
	}
	upload := func(token string) int {
		header := http.Header{"Content-Type": {"application/json"}}
		if token != "" {
			header.Set("Authorization", "Bearer "+token)
		}
		body := `{"label":"a@example.com","locale":"en-US","timezone":"UTC","storage_state":{"cookies":[],"origins":[]},"fingerprint":{"config":{"a":1}}}`
		response := serve("POST", "/api/pairing/accounts", body, header)
		if response.Code == http.StatusCreated {
			var result PairingAccountResult
			if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.RenewalToken == "" || result.RenewalToken == token || result.RenewalToken != admin.imported[len(admin.imported)-1].RenewalToken {
				t.Fatal("pairing upload did not return its new server-issued account renewal token")
			}
		}
		return response.Code
	}
	if response := serve("GET", "/api/pairing", "", session); response.Code != http.StatusNotFound {
		t.Fatalf("未签发时读取令牌 = %d", response.Code)
	}
	first := issue()
	if current := active(); current != first {
		t.Fatalf("读取令牌 = %q，签发的是 %q", current, first)
	}
	if code := upload(""); code != http.StatusUnauthorized {
		t.Fatalf("无令牌上传 = %d", code)
	}
	if code := upload(first + "x"); code != http.StatusUnauthorized {
		t.Fatalf("错误令牌上传 = %d", code)
	}
	if code := upload(first); code != http.StatusCreated {
		t.Fatalf("令牌上传 = %d", code)
	}
	if code := upload(first); code != http.StatusCreated {
		t.Fatalf("有效期内第二个账户 = %d", code)
	}
	second := issue()
	if current := active(); current != second {
		t.Fatalf("重新签发后读取令牌 = %q，签发的是 %q", current, second)
	}
	if code := upload(first); code != http.StatusUnauthorized {
		t.Fatalf("旧令牌上传 = %d", code)
	}
	if code := upload(second); code != http.StatusCreated {
		t.Fatalf("新令牌上传 = %d", code)
	}
	if len(admin.imported) != 3 || admin.imported[0].Label != "a@example.com" || string(admin.imported[0].Fingerprint) != `{"config":{"a":1}}` {
		t.Fatalf("导入内容 = %+v", admin.imported)
	}
}

func TestPairingRenewalUsesAccountTokenWithoutAdminSession(t *testing.T) {
	admin := &pairingAdmin{}
	handler := NewHandler(nil, Config{Admin: admin, APIKey: "generation-fixture", AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "fixture"})
	const path = "/api/pairing/accounts/user@example.test/cookies"
	const body = `{"cookies":[{"name":"SAPISID","value":"cookie-fixture","domain":".google.com","path":"/","expires":-1}]}`
	for _, token := range []string{"", "generation-fixture", "wrong-token", "renewal-fixture"} {
		header := http.Header{"Authorization": {"Bearer " + token}}
		response := accountTaskRequest(handler, http.MethodPut, path, body, header)
		want := http.StatusUnauthorized
		if token == "renewal-fixture" {
			want = http.StatusOK
		}
		if response.Code != want || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("renewal response = %d %s", response.Code, response.Body)
		}
		if strings.Contains(response.Body.String(), "cookie-fixture") || strings.Contains(response.Body.String(), "renewal-fixture") {
			t.Fatal("renewal response exposed credentials")
		}
	}
	if admin.renewed != 1 {
		t.Fatal("renewal accepted a management/generation credential instead of the account token")
	}
}

// TestPairingTokenExpiry 验证令牌到期后失效且不再作为当前令牌返回
func TestPairingTokenExpiry(t *testing.T) {
	tokens := &pairingTokens{}
	now := time.Unix(1_700_000_000, 0)
	token := tokens.issue(now)
	if !tokens.valid(token.Token, now.Add(pairingTokenLifetime-time.Second)) {
		t.Fatal("到期前令牌无效")
	}
	if tokens.valid(token.Token, now.Add(pairingTokenLifetime)) {
		t.Fatal("到期后令牌仍有效")
	}
	if current, ok := tokens.active(now.Add(pairingTokenLifetime - time.Second)); !ok || current != token {
		t.Fatal("到期前读不到当前令牌")
	}
	if _, ok := tokens.active(now.Add(pairingTokenLifetime)); ok {
		t.Fatal("到期后仍返回当前令牌")
	}
	if (&pairingTokens{}).valid("", now) {
		t.Fatal("未签发时空令牌有效")
	}
}
