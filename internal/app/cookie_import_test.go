package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/gorilla/websocket"
)

var _ aistudio.CookieLoginDriver = (*lazyLoginDriver)(nil)
var _ aistudio.CookieLoginDriver = (*aistudio.NativeLoginDriver)(nil)

type cookieLoginStub struct {
	aistudio.IsolatedLoginDriver
	request     aistudio.IsolatedLoginRequest
	state       aistudio.StorageState
	result      aistudio.IsolatedLoginResult
	err         error
	calls       int
	fingerprint string
}

func (driver *cookieLoginStub) ImportCookies(_ context.Context, request aistudio.IsolatedLoginRequest, state aistudio.StorageState) (aistudio.IsolatedLoginResult, error) {
	driver.request = request
	driver.state = state
	driver.calls++
	if _, exists, _ := state.AuthExtension(); exists {
		return aistudio.IsolatedLoginResult{}, errors.New("untrusted extension")
	}
	if len(state.Origins) != 0 {
		return aistudio.IsolatedLoginResult{}, errors.New("untrusted localStorage")
	}
	if driver.fingerprint != "" {
		if err := os.WriteFile(filepath.Join(request.Directory, "camoufox-fingerprint.json"), []byte(driver.fingerprint), 0o600); err != nil {
			return aistudio.IsolatedLoginResult{}, err
		}
	}
	return driver.result, driver.err
}

type cookieCatalogStub struct {
	pool *aistudio.AccountPool
}

func (*cookieCatalogStub) CachedModels() []aistudio.Model {
	return []aistudio.Model{{ID: "test-model"}}
}

func (catalog *cookieCatalogStub) RefreshAccountModels(_ context.Context, id string) ([]aistudio.Model, error) {
	models := catalog.CachedModels()
	return models, catalog.pool.SetCatalog(id, aistudio.BenefitTier(0), models)
}

func (catalog *cookieCatalogStub) RefreshAccountState(ctx context.Context, id string, _ bool) (aistudio.AccountRefresh, error) {
	models, err := catalog.RefreshAccountModels(ctx, id)
	return aistudio.AccountRefresh{Models: len(models)}, err
}

func TestAdminCookieImportPublishAndRollback(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		failWorker bool
		closeError string
	}{
		{"publish", "", false, ""},
		{"explicit-browser", "browser", false, ""},
		{"rollback", "", true, ""},
		{"cleanup-warning", "", false, "cookie-secret cleanup failed"},
		{"http-publish", "http", false, ""},
		{"http-rollback", "http", true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const header = "SAPISID=refreshed-secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three"
			state, err := aistudio.ParseCookieImport(header)
			if err != nil {
				t.Fatal(err)
			}
			driver := &cookieLoginStub{
				result:      aistudio.IsolatedLoginResult{Email: "New@Example.com", StorageState: state, CloseError: test.closeError},
				fingerprint: `{"firefox_version":152,"locale":"zh-CN","timezone":"Asia/Shanghai","config":{"navigator.userAgent":"verified-fingerprint"}}`,
			}
			root := t.TempDir()
			pool := aistudio.NewAccountPool(nil, 1)
			registry := newRequestRegistry(ctx)
			headers, err := newAccountHeaderProvider(nil, "")
			if err != nil {
				t.Fatal(err)
			}
			defer headers.Close()
			workers := newAccountWorkerManager(pool, nil, registry, "", "", time.Second, 0, 1, 1, true)
			defer workers.Close()
			if test.failWorker {
				workers.Close()
			}
			service := &trackedService{
				lifecycle: ctx, catalog: &cookieCatalogStub{pool: pool}, pool: pool, requests: registry,
				workers: workers, timeout: time.Second, modelRetries: make(map[string]struct{}),
			}
			admin := &runtimeAdmin{pool: pool, store: aistudio.NewAccountStore(root), login: driver,
				requests: registry, headers: headers, workers: workers, service: service}
			httpCalls := 0
			admin.verifyCookies = func(ctx context.Context, pending *aistudio.StorageState, proxy string) (chromeauth.Verification, error) {
				httpCalls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("HTTP verification has no deadline")
				}
				if _, exists, _ := pending.AuthExtension(); exists || len(pending.Origins) != 0 {
					t.Fatal("HTTP verification received untrusted identity or localStorage")
				}
				*pending = driver.result.StorageState
				return chromeauth.Verification{Email: driver.result.Email, ModelCount: 1}, nil
			}
			if test.method == "http" {
				admin.login = nil // HTTP 导入不能依赖登录驱动是否存在。
			}
			account, err := admin.ImportCookieAccount(ctx, api.CookieImportInput{
				Cookies: strings.Replace(header, "refreshed-secret", "original-secret", 1),
				Label:   "forged@example.com", Locale: "zh-CN", Timezone: "Asia/Shanghai", Method: test.method,
			})
			if test.method == "http" {
				if httpCalls != 1 || driver.calls != 0 {
					t.Fatal("HTTP import used the browser login path")
				}
			} else if httpCalls != 0 || driver.calls != 1 {
				t.Fatal("browser import unexpectedly used HTTP verification")
			}
			if test.failWorker {
				if err == nil || len(pool.Status()) != 0 || len(headers.accounts) != 0 {
					t.Fatal("failed publication was not rolled back")
				}
				entries, readErr := os.ReadDir(root)
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, entry := range entries {
					// 跨进程租约目录保留，不能删除可能仍被其他进程引用的锁文件。
					if entry.Name() != ".leases" {
						t.Fatalf("failed publication left account directory: %s", entry.Name())
					}
				}
				if accounts, err := admin.store.Load(); err != nil || len(accounts) != 0 {
					t.Fatal("failed publication left a loadable account")
				}
				return
			}
			if err != nil || account.ID != "new@example.com" || len(account.Models) != 1 || account.Locale != "zh-CN" || account.Timezone != "Asia/Shanghai" {
				t.Fatalf("published account = %+v, error = %v", account, err)
			}
			stored, err := aistudio.LoadStorageState(filepath.Join(root, account.ID, "storage-state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if value, _ := stored.CookieValue("SAPISID", "https://aistudio.google.com", time.Now()); value != "refreshed-secret" {
				t.Fatal("did not persist verified refreshed cookies")
			}
			fingerprint, err := os.ReadFile(filepath.Join(root, account.ID, "camoufox-fingerprint.json"))
			if test.method == "http" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("HTTP import created a browser fingerprint")
				}
			} else if err != nil || !strings.Contains(string(fingerprint), "verified-fingerprint") {
				t.Fatal("verified browser fingerprint was not persisted")
			}
			extension, exists, err := stored.AuthExtension()
			if err != nil || !exists || extension.Source.Email != account.ID || extension.Source.Browser != "cookie" || extension.OAuth != nil {
				t.Fatal("incorrect imported identity")
			}
			loaded, err := admin.store.Load()
			if err != nil || len(loaded) != 1 || loaded[0].ID != account.ID {
				t.Fatal("cannot reload imported account")
			}
			if _, err := os.Stat(driver.request.Directory); !os.IsNotExist(err) {
				t.Fatal("temporary directory not cleaned")
			}
			warned := false
			registry.mu.Lock()
			for _, log := range registry.logs {
				warned = warned || log.Level == "WARN"
				if strings.Contains(log.Message, "cookie-secret") {
					t.Error("cleanup warning leaked browser details")
				}
			}
			registry.mu.Unlock()
			if test.closeError != "" && !warned {
				t.Fatal("cleanup failure was silently ignored")
			}
			_, err = admin.ImportCookieAccount(ctx, api.CookieImportInput{Cookies: header, Label: "different@example.com", Method: test.method})
			var conflict interface{ HTTPStatus() int }
			if !errors.As(err, &conflict) || conflict.HTTPStatus() != http.StatusConflict || len(pool.Status()) != 1 {
				t.Fatal("a forged label allowed an existing account to be replaced")
			}
		})
	}
}

func TestAdminCookieImportValidation(t *testing.T) {
	const header = "SAPISID=cookie-secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three"
	state, err := aistudio.ParseCookieImport(header)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		cookies string
		proxy   string
		email   string
		state   aistudio.StorageState
		err     error
		status  int
		calls   int
	}{
		{"empty", "", "", "", state, nil, http.StatusBadRequest, 0},
		{"proxy", header, "invalid", "", state, nil, http.StatusBadRequest, 0},
		{"expired", header, "", "", state, camoufoxnative.ErrAuthenticationRequired, http.StatusBadRequest, 1},
		{"timeout", header, "", "", state, context.DeadlineExceeded, http.StatusGatewayTimeout, 1},
		{"redacted", header, "", "", state, errors.New("browser failed with cookie-secret"), http.StatusBadGateway, 1},
		{"identity", header, "", "not-an-email", state, nil, http.StatusBadRequest, 1},
		{"invalid-result", header, "", "existing@example.com", aistudio.StorageState{}, nil, http.StatusBadRequest, 1},
		{"duplicate", header, "http://127.0.0.1:4321", "Existing@Example.com", state, nil, http.StatusConflict, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			driver := &cookieLoginStub{result: aistudio.IsolatedLoginResult{Email: test.email, StorageState: test.state}, err: test.err}
			root := t.TempDir()
			pool := aistudio.NewAccountPool([]*aistudio.Account{{ID: "existing@example.com", Config: aistudio.DefaultAccountConfig("existing@example.com")}}, 1)
			cfg := config.Default()
			cfg.Proxy = "http://127.0.0.1:1234"
			admin := &runtimeAdmin{pool: pool, store: aistudio.NewAccountStore(root), login: driver, requests: newRequestRegistry(ctx), config: cfg, globalProxy: cfg.Proxy}
			admin.verifyCookies = func(context.Context, *aistudio.StorageState, string) (chromeauth.Verification, error) {
				t.Fatal("browser import silently fell back to HTTP")
				return chromeauth.Verification{}, nil
			}
			_, err := admin.ImportCookieAccount(ctx, api.CookieImportInput{Cookies: test.cookies, Proxy: test.proxy, Locale: "zh-CN", Timezone: "Asia/Shanghai"})
			var status interface{ HTTPStatus() int }
			if !errors.As(err, &status) || status.HTTPStatus() != test.status {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "cookie-secret") {
				t.Fatal("error leaked cookie")
			}
			if driver.calls != test.calls {
				t.Fatalf("driver calls = %d", driver.calls)
			}
			if driver.calls > 0 {
				wantProxy := cfg.Proxy
				if test.proxy != "" {
					wantProxy = test.proxy
				}
				if driver.request.Proxy != wantProxy || driver.request.Locale != "zh-CN" || driver.request.Timezone != "Asia/Shanghai" {
					t.Fatal("lost environment")
				}
				if _, err := os.Stat(driver.request.Directory); !os.IsNotExist(err) {
					t.Fatal("temporary directory not removed")
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 || len(pool.Status()) != 1 {
				t.Fatal("failed import changed accounts")
			}
			admin.requests.mu.Lock()
			defer admin.requests.mu.Unlock()
			for _, log := range admin.requests.logs {
				if strings.Contains(log.Message, "cookie-secret") {
					t.Fatal("log leaked cookie")
				}
			}
		})
	}
}

func TestAdminHTTPCookieImportValidation(t *testing.T) {
	const header = "SAPISID=cookie-secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three"
	for _, test := range []struct {
		name         string
		proxy        string
		email        string
		err          error
		invalidState bool
		waitDeadline bool
		status       int
	}{
		{name: "signed-out", err: chromeauth.ErrSignedOut, status: http.StatusBadRequest},
		{name: "missing-cookie", err: camoufoxnative.ErrAuthenticationRequired, status: http.StatusBadRequest},
		{name: "deadline", waitDeadline: true, status: http.StatusGatewayTimeout},
		{name: "canceled", err: context.Canceled, status: http.StatusGatewayTimeout},
		{name: "redacted", err: errors.New("HTTP response with cookie-secret"), status: http.StatusBadGateway},
		{name: "identity", email: "not-an-email", status: http.StatusBadRequest},
		{name: "invalid-result", email: "new@example.com", invalidState: true, status: http.StatusBadRequest},
		{name: "duplicate-global-proxy", email: "Existing@Example.com", status: http.StatusConflict},
		{name: "duplicate-account-proxy", email: "Existing@Example.com", proxy: "http://127.0.0.1:4321", status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			root := t.TempDir()
			pool := aistudio.NewAccountPool([]*aistudio.Account{{ID: "existing@example.com", Config: aistudio.DefaultAccountConfig("existing@example.com")}}, 1)
			cfg := config.Default()
			cfg.InitTimeout = time.Minute
			if test.waitDeadline {
				cfg.InitTimeout = time.Millisecond
			}
			admin := &runtimeAdmin{pool: pool, store: aistudio.NewAccountStore(root), requests: newRequestRegistry(ctx), config: cfg, globalProxy: "http://127.0.0.1:1234"}
			calls := 0
			admin.verifyCookies = func(ctx context.Context, pending *aistudio.StorageState, proxy string) (chromeauth.Verification, error) {
				calls++
				wantProxy := admin.globalProxy
				if test.proxy != "" {
					wantProxy = test.proxy
				}
				if proxy != wantProxy {
					t.Fatal("HTTP verification lost the effective proxy")
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > cfg.InitTimeout {
					t.Fatal("HTTP verification lost the configured timeout")
				}
				pending.Cookies[0].Value = "rotated-secret"
				if test.invalidState {
					*pending = aistudio.StorageState{}
				}
				if test.waitDeadline {
					<-ctx.Done()
				}
				return chromeauth.Verification{Email: test.email, ModelCount: 1}, test.err
			}
			_, err := admin.ImportCookieAccount(ctx, api.CookieImportInput{Cookies: header, Method: " HTTP ", Label: "forged@example.com", Proxy: test.proxy})
			var operation *adminOperationError
			if !errors.As(err, &operation) || operation.status != test.status || calls != 1 {
				t.Fatalf("HTTP verification error = %v, calls = %d", err, calls)
			}
			if test.status == http.StatusBadGateway && operation.code != "cookie_import_http_failed" {
				t.Fatalf("HTTP verification error code = %s", operation.code)
			}
			if strings.Contains(err.Error(), "cookie-secret") || strings.Contains(err.Error(), "rotated-secret") {
				t.Fatal("HTTP verification error leaked credentials")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 || len(pool.Status()) != 1 {
				t.Fatal("failed HTTP verification published an account or pending cookie rotation")
			}
			admin.requests.mu.Lock()
			defer admin.requests.mu.Unlock()
			for _, log := range admin.requests.logs {
				if strings.Contains(log.Message, "cookie-secret") || strings.Contains(log.Message, "rotated-secret") {
					t.Fatal("HTTP verification log leaked credentials")
				}
			}
		})
	}
}

func TestAdminCookieImportRejectsUnknownMethod(t *testing.T) {
	driver := &cookieLoginStub{}
	admin := &runtimeAdmin{login: driver}
	_, err := admin.ImportCookieAccount(t.Context(), api.CookieImportInput{Method: "none", CDP: "127.0.0.1:1"})
	var operation *adminOperationError
	if !errors.As(err, &operation) || operation.status != http.StatusBadRequest || driver.calls != 0 {
		t.Fatalf("unknown verification method was not rejected before reading credentials: %v", err)
	}
}

func cookieCDPServer(t *testing.T, state aistudio.StorageState) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			var command struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := conn.ReadJSON(&command); err != nil {
				return
			}
			var result any
			switch command.Method {
			case "Storage.getCookies":
				result = state
			case "Target.getTargets":
				result = map[string]any{"targetInfos": []any{map[string]any{"targetId": "page", "type": "page"}}}
			case "Target.attachToTarget":
				result = map[string]string{"sessionId": "session"}
			case "Runtime.evaluate":
				result = map[string]any{"result": map[string]any{"value": []string{"ja-JP", "Asia/Tokyo"}}}
			default:
				t.Errorf("unexpected CDP method %q", command.Method)
				return
			}
			if err := conn.WriteJSON(map[string]any{"id": command.ID, "result": result}); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestAdminCookieCDPUsesSameVerification(t *testing.T) {
	state, err := aistudio.ParseCookieImport("SAPISID=cdp-secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
	if err != nil {
		t.Fatal(err)
	}
	state.Cookies = append(state.Cookies, aistudio.StateCookie{Name: "foreign", Value: "not-google", Domain: ".example.com", Path: "/", Expires: -1})
	address := cookieCDPServer(t, state)
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "browser-environment", true: "explicit-environment"}[override], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			driver := &cookieLoginStub{result: aistudio.IsolatedLoginResult{Email: "existing@example.com", StorageState: state}}
			pool := aistudio.NewAccountPool([]*aistudio.Account{{ID: "existing@example.com", Config: aistudio.DefaultAccountConfig("existing@example.com")}}, 1)
			admin := &runtimeAdmin{pool: pool, login: driver, requests: newRequestRegistry(ctx)}
			input := api.CookieImportInput{CDP: address, Label: "forged@example.com"}
			if override {
				input.Locale, input.Timezone = "zh-CN", "Asia/Shanghai"
			}
			_, err := admin.ImportCookieAccount(ctx, input)
			var conflict interface{ HTTPStatus() int }
			if !errors.As(err, &conflict) || conflict.HTTPStatus() != http.StatusConflict || driver.calls != 1 {
				t.Fatalf("CDP bypassed verified identity or duplicate protection: %v", err)
			}
			wantLocale, wantTimezone := "ja-JP", "Asia/Tokyo"
			if override {
				wantLocale, wantTimezone = input.Locale, input.Timezone
			}
			if driver.request.Locale != wantLocale || driver.request.Timezone != wantTimezone || len(driver.state.Cookies) != 3 {
				t.Fatal("CDP environment or Google-domain filtering was lost")
			}
			if _, err := os.Stat(driver.request.Directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("CDP validation left its temporary profile")
			}
		})
	}
}

func TestAdminCookieCDPSizeLimit(t *testing.T) {
	state := aistudio.StorageState{Cookies: []aistudio.StateCookie{{Name: "SAPISID", Value: strings.Repeat("x", aistudio.MaxCookieImportBytes), Domain: ".google.com", Path: "/", Expires: -1}}}
	encoded, _ := json.Marshal(state)
	if len(encoded) <= aistudio.MaxCookieImportBytes {
		t.Fatal("test requires an oversized CDP export")
	}
	driver := &cookieLoginStub{}
	admin := &runtimeAdmin{login: driver}
	_, err := admin.ImportCookieAccount(t.Context(), api.CookieImportInput{CDP: cookieCDPServer(t, state)})
	var invalid interface{ HTTPStatus() int }
	if !errors.As(err, &invalid) || invalid.HTTPStatus() != http.StatusBadRequest || driver.calls != 0 {
		t.Fatal("CDP export bypassed the Cookie size limit")
	}
}
