package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type accountLoginFunc func(context.Context, aistudio.IsolatedLoginRequest) (aistudio.IsolatedLoginResult, error)

func (login accountLoginFunc) Login(ctx context.Context, request aistudio.IsolatedLoginRequest) (aistudio.IsolatedLoginResult, error) {
	return login(ctx, request)
}

// TestAuthRefreshWithoutExtension 验证缺少续签扩展的账户从当前 Chrome 导入同一邮箱并写回扩展，重新登录保留扩展
func TestAuthRefreshWithoutExtension(t *testing.T) {
	directory := t.TempDir()
	storage := filepath.Join(directory, "storage-state.json")
	state := `{"cookies":[{"name":"SAPISID","value":"old","domain":".google.com","path":"/","secure":true,"sameSite":"Lax"}],"origins":[]}`
	if err := os.WriteFile(storage, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := aistudio.LoadStorageState(storage)
	if err != nil {
		t.Fatal(err)
	}
	account := &aistudio.Account{
		ID: "user@example.com", Directory: directory, StoragePath: storage,
		RuntimePath: filepath.Join(directory, "runtime-state.json"), StorageState: loaded,
		Config: aistudio.DefaultAccountConfig("user@example.com"),
	}
	pool := aistudio.NewAccountPool([]*aistudio.Account{account}, 1)
	lease, err := pool.AcquireAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	var cookies []aistudio.StateCookie
	for _, name := range []string{"SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID"} {
		cookies = append(cookies, aistudio.StateCookie{Name: name, Value: "chrome", Domain: ".google.com", Path: "/", Expires: -1, Secure: true, SameSite: "Lax"})
	}
	refresher := &authRuntimeRefresher{
		pool: pool, requests: newRequestRegistry(t.Context()),
		chromeHas: func(email string) bool { return email == account.ID },
		importCurrent: func(_ context.Context, source aistudio.AuthSource, _ *aistudio.ChromeOAuthMaterial, _ string) (aistudio.StorageState, error) {
			updated := aistudio.StorageState{Cookies: cookies}
			updated.SetAuthExtension(aistudio.AuthExtension{
				Source: source, OAuth: &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token"},
			})
			return updated, nil
		},
		reset: func(string) error { return nil },
	}
	ctx := aistudio.ContextWithAccountLease(t.Context(), lease)
	if !refresher.Available(ctx) {
		t.Fatal("账户在 Chrome 中登录但被判定为不可续签")
	}
	if err := refresher.Refresh(ctx, lease.AuthGeneration()); err != nil {
		t.Fatal(err)
	}
	stored, err := aistudio.LoadStorageState(storage)
	if err != nil {
		t.Fatal(err)
	}
	extension, exists, err := stored.AuthExtension()
	if err != nil || !exists || extension.OAuth == nil || extension.Source.Email != account.ID || stored.Cookies[0].Value != "chrome" {
		t.Fatalf("续签结果 exists=%t extension=%+v cookies=%+v err=%v", exists, extension, stored.Cookies, err)
	}

	login := aistudio.StorageState{Cookies: cookies}
	if err := keepAuthExtension(&login, stored); err != nil {
		t.Fatal(err)
	}
	if kept, exists, _ := login.AuthExtension(); !exists || kept.OAuth == nil || kept.OAuth.RefreshToken != "token" {
		t.Fatalf("重新登录丢失续签扩展: %+v", kept)
	}
}

func TestKeepAuthExtensionWithNativeSource(t *testing.T) {
	for _, test := range []struct {
		name       string
		email      string
		freshOAuth bool
		oldOAuth   bool
		wantOld    bool
	}{
		{name: "native relogin", email: "user@example.test", oldOAuth: true, wantOld: true},
		{name: "case insensitive email", email: "USER@example.test", oldOAuth: true, wantOld: true},
		{name: "different account", email: "other@example.test", oldOAuth: true},
		{name: "fresh renewal material", email: "user@example.test", oldOAuth: true, freshOAuth: true},
		{name: "no previous renewal material", email: "user@example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := aistudio.AuthExtension{Source: aistudio.AuthSource{Browser: "chrome", Root: "fixture-root", Profile: "Default", Email: "user@example.test"}}
			if test.oldOAuth {
				previous.OAuth = &aistudio.ChromeOAuthMaterial{GaiaID: "fixture-gaia", RefreshToken: "old-fixture"}
			}
			current := aistudio.StorageState{}
			current.SetAuthExtension(previous)
			fresh := aistudio.AuthExtension{Source: aistudio.AuthSource{Browser: "camoufox", Email: test.email}}
			if test.freshOAuth {
				fresh.OAuth = &aistudio.ChromeOAuthMaterial{GaiaID: "fixture-gaia", RefreshToken: "fresh-fixture"}
			}
			next := aistudio.StorageState{Cookies: []aistudio.StateCookie{{Name: "SAPISID", Value: "new-fixture", Domain: ".google.com"}}}
			next.SetAuthExtension(fresh)
			if err := keepAuthExtension(&next, current); err != nil {
				t.Fatal(err)
			}
			kept, exists, err := next.AuthExtension()
			want := fresh
			if test.wantOld {
				want = previous
			}
			if err != nil || !exists || kept.Source != want.Source || (kept.OAuth != nil) != (want.OAuth != nil) || kept.OAuth != nil && kept.OAuth.RefreshToken != want.OAuth.RefreshToken {
				t.Fatal("relogin did not preserve the correct account's renewal source and material")
			}
			if next.Cookies[0].Value != "new-fixture" {
				t.Fatal("preserving renewal material replaced fresh cookies")
			}
		})
	}
}

func TestLoginAccountPreservesNativeRenewalMaterial(t *testing.T) {
	for _, test := range []struct {
		name, email, oldEmail string
		freshOAuth, canceled  bool
		failConfig            bool
		wantOld, wantError    bool
	}{
		{name: "native relogin", wantOld: true},
		{name: "case insensitive", email: "USER@example.test", wantOld: true},
		{name: "fresh OAuth", freshOAuth: true},
		{name: "different known old email", oldEmail: "other@example.test"},
		{name: "wrong login identity", email: "other@example.test", wantError: true},
		{name: "canceled login", canceled: true, wantError: true},
		{name: "config write failure", failConfig: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			admin := newAccountTestAdmin(t)
			state, err := aistudio.ParseCookieImport("SAPISID=old-fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
			if err != nil {
				t.Fatal(err)
			}
			oldEmail := test.oldEmail
			if oldEmail == "" {
				oldEmail = "user@example.test"
			}
			old := aistudio.AuthExtension{
				Source: aistudio.AuthSource{Browser: "chrome", Root: "fixture-root", Profile: "Default", Email: oldEmail},
				OAuth:  &aistudio.ChromeOAuthMaterial{GaiaID: "old-gaia", RefreshToken: "old-fixture"},
			}
			initial := old
			initial.Source.Email = "user@example.test"
			state.SetAuthExtension(initial)
			cfg := aistudio.DefaultAccountConfig("user@example.test")
			cfg.AuthUser = 2
			created, err := admin.addAccount(t.Context(), cfg, state, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if test.oldEmail != "" {
				lease, err := admin.pool.AcquireAccount(t.Context(), created.ID)
				if err != nil {
					t.Fatal(err)
				}
				state.SetAuthExtension(old)
				err = errors.Join(lease.SaveStorageState(state), lease.Release())
				if err != nil {
					t.Fatal(err)
				}
			}
			storedAccount, err := admin.pool.Account(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			configPath := storedAccount.ConfigPath
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			email := test.email
			if email == "" {
				email = created.ID
			}
			fresh := aistudio.AuthExtension{Source: aistudio.AuthSource{Browser: "camoufox", Email: email}}
			if test.freshOAuth {
				fresh.OAuth = &aistudio.ChromeOAuthMaterial{GaiaID: "fresh-gaia", RefreshToken: "fresh-fixture"}
			}
			calls := 0
			admin.login = accountLoginFunc(func(_ context.Context, request aistudio.IsolatedLoginRequest) (aistudio.IsolatedLoginResult, error) {
				calls++
				if request.AuthUser != 0 {
					t.Fatal("isolated native relogin did not use its new single-account session")
				}
				if test.canceled {
					cancel()
				}
				if test.failConfig {
					storedAccount.ConfigPath = storedAccount.Directory
				}
				next, err := aistudio.ParseCookieImport("SAPISID=new-fixture; __Secure-1PAPISID=new-one; __Secure-3PAPISID=new-three")
				if err != nil {
					t.Fatal(err)
				}
				next.SetAuthExtension(fresh)
				return aistudio.IsolatedLoginResult{Email: email, StorageState: next}, nil
			})
			_, err = admin.LoginAccount(ctx, created.ID, nil)
			storedAccount.ConfigPath = configPath
			if (err != nil) != test.wantError || calls != 1 || test.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("relogin calls=%d error=%v", calls, err)
			}
			loaded, err := admin.store.Load()
			if err != nil || len(loaded) != 1 {
				t.Fatalf("reload = %v", err)
			}
			want := fresh
			if test.wantOld || test.wantError {
				want = old
			}
			kept, exists, err := loaded[0].StorageState.AuthExtension()
			if err != nil || !exists || !reflect.DeepEqual(kept, want) {
				t.Fatal("LoginAccount did not preserve the correct renewal source and OAuth")
			}
			wantCookie, wantAuthUser := "new-fixture", 0
			if test.wantError {
				wantCookie, wantAuthUser = "old-fixture", 2
			}
			if loaded[0].StorageState.Cookies[0].Value != wantCookie || loaded[0].Config.AuthUser != wantAuthUser {
				t.Fatal("relogin saved partial cookies or an incorrect auth_user")
			}
			lease, err := admin.pool.AcquireAccount(t.Context(), created.ID)
			if err != nil {
				t.Fatalf("relogin did not release a usable account lease: %v", err)
			}
			_ = lease.Release()
		})
	}
}
