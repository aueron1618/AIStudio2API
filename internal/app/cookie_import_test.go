package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

var _ aistudio.CookieLoginDriver = (*lazyLoginDriver)(nil)
var _ aistudio.CookieLoginDriver = (*aistudio.NativeLoginDriver)(nil)

type cookieLoginStub struct {
	aistudio.IsolatedLoginDriver
	request aistudio.IsolatedLoginRequest
	result  aistudio.IsolatedLoginResult
	err     error
	calls   int
}

func (driver *cookieLoginStub) ImportCookies(_ context.Context, request aistudio.IsolatedLoginRequest, state aistudio.StorageState) (aistudio.IsolatedLoginResult, error) {
	driver.request = request
	driver.calls++
	if _, exists, _ := state.AuthExtension(); exists {
		return aistudio.IsolatedLoginResult{}, errors.New("untrusted extension")
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

func TestAdminCookieImportPublishAndRollback(t *testing.T) {
	for _, failWorker := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "rollback"}[failWorker], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const header = "SAPISID=refreshed-secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three"
			state, err := aistudio.ParseCookieImport(header)
			if err != nil {
				t.Fatal(err)
			}
			driver := &cookieLoginStub{result: aistudio.IsolatedLoginResult{Email: "New@Example.com", StorageState: state}}
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
			if failWorker {
				workers.Close()
			}
			service := &trackedService{
				lifecycle: ctx, catalog: &cookieCatalogStub{pool: pool}, pool: pool, requests: registry,
				workers: workers, timeout: time.Second, modelRetries: make(map[string]struct{}),
			}
			admin := &runtimeAdmin{pool: pool, store: aistudio.NewAccountStore(root), login: driver,
				requests: registry, headers: headers, workers: workers, service: service}
			account, err := admin.ImportCookieAccount(ctx, api.CookieImportInput{Cookies: strings.Replace(header, "refreshed-secret", "original-secret", 1)})
			if failWorker {
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
			if err != nil || account.ID != "new@example.com" || len(account.Models) != 1 {
				t.Fatalf("published account = %+v, error = %v", account, err)
			}
			stored, err := aistudio.LoadStorageState(filepath.Join(root, account.ID, "storage-state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if value, _ := stored.CookieValue("SAPISID", "https://aistudio.google.com", time.Now()); value != "refreshed-secret" {
				t.Fatal("did not persist refreshed browser cookies")
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
			admin := &runtimeAdmin{pool: pool, store: aistudio.NewAccountStore(root), login: driver, requests: newRequestRegistry(ctx), config: cfg}
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
