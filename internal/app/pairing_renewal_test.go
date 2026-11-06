package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

func TestPairingImportVerifiesIdentityAndKeepsOnlyTokenDigest(t *testing.T) {
	for _, test := range []struct {
		name, label string
		authUser    int
		canceled    bool
		wantError   bool
	}{
		{name: "verified", label: "USER@example.test", authUser: 3},
		{name: "identity inferred", authUser: 3},
		{name: "forged label", label: "forged@example.test", authUser: 3, wantError: true},
		{name: "invalid auth user", authUser: 10, wantError: true},
		{name: "invalid verified state", authUser: 3, wantError: true},
		{name: "canceled", authUser: 3, canceled: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			admin := newAccountTestAdmin(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			state, err := aistudio.ParseCookieImport("SAPISID=upload-fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
			if err != nil {
				t.Fatal(err)
			}
			state.SetAuthExtension(aistudio.AuthExtension{
				Source: aistudio.AuthSource{Browser: "chrome", Email: "forged@example.test"},
				OAuth:  &aistudio.ChromeOAuthMaterial{GaiaID: "forged-gaia", RefreshToken: "oauth-fixture"},
			})
			state.Origins = []aistudio.StorageOrigin{{Origin: "https://aistudio.google.com", LocalStorage: []aistudio.StorageItem{{Name: "fixture", Value: "untrusted-storage"}}}}
			calls := 0
			admin.verifyCookies = func(_ context.Context, state *aistudio.StorageState, proxy string, authUser int) (chromeauth.Verification, error) {
				calls++
				if authUser != 3 || proxy != "http://127.0.0.1:1234" || len(state.Origins) != 0 {
					t.Fatal("pairing verification lost auth_user/proxy or trusted uploaded localStorage")
				}
				if _, exists, _ := state.AuthExtension(); exists {
					t.Fatal("pairing verification trusted uploaded OAuth or identity")
				}
				state.Cookies[0].Value = "verified-fixture"
				if test.name == "invalid verified state" {
					state.Cookies = nil
				}
				if test.canceled {
					cancel()
				}
				return chromeauth.Verification{Email: "user@example.test", ModelCount: 1}, nil
			}
			account, err := admin.ImportAccountState(ctx, api.AccountStateInput{
				Label: test.label, Proxy: "http://127.0.0.1:1234", AuthUser: test.authUser,
				StorageState: state, RenewalToken: "renewal-fixture",
			})
			if (err != nil) != test.wantError || test.authUser < 10 && calls != 1 || test.authUser == 10 && calls != 0 {
				t.Fatalf("pairing import calls=%d error=%v", calls, err)
			}
			stored, err := admin.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if test.wantError {
				if len(stored) != 0 || len(admin.pool.Status()) != 0 {
					t.Fatal("failed pairing import published unverified account")
				}
				return
			}
			if len(stored) != 1 || account.ID != "user@example.test" || len(account.Models) != 0 || admin.service.catalog.(*cookieCatalogStub).refreshCalls != 0 {
				t.Fatal("pairing import lost identity or implicitly started PageCatalog")
			}
			extension, exists, err := stored[0].StorageState.AuthExtension()
			if err != nil || !exists || extension.Source.Email != account.ID || extension.OAuth != nil || len(stored[0].StorageState.Origins) != 0 {
				t.Fatal("server retained uploaded authentication metadata")
			}
			if stored[0].Config.AuthUser != 3 || stored[0].StorageState.Cookies[0].Value != "verified-fixture" {
				t.Fatal("server did not store verified cookies for the selected session")
			}
			record, err := readPairingRenewal(stored[0].Directory)
			if err != nil || record.TokenSHA256 != pairingTokenDigest("renewal-fixture") || !record.RenewedAt.IsZero() {
				t.Fatal("server did not store only the renewal token digest")
			}
			for _, name := range []string{pairingRenewalName, "storage-state.json"} {
				data, err := os.ReadFile(filepath.Join(stored[0].Directory, name))
				if err != nil || strings.Contains(string(data), "renewal-fixture") || strings.Contains(string(data), "oauth-fixture") || strings.Contains(string(data), "untrusted-storage") {
					t.Fatal("persisted server state leaked source secrets")
				}
			}
		})
	}
}

func TestPairingRenewalRequiresTokenAndVerifiedIdentity(t *testing.T) {
	for _, test := range []struct {
		name, token, email string
		canceled, failSave bool
		wantError          bool
	}{
		{name: "verified", token: "renewal-fixture", email: "USER@example.test"},
		{name: "missing token", email: "user@example.test", wantError: true},
		{name: "wrong token", token: "wrong-token", email: "user@example.test", wantError: true},
		{name: "wrong account", token: "renewal-fixture", email: "other@example.test", wantError: true},
		{name: "invalid verified state", token: "renewal-fixture", email: "user@example.test", wantError: true},
		{name: "canceled", token: "renewal-fixture", email: "user@example.test", canceled: true, wantError: true},
		{name: "record failure rolls back cookies", token: "renewal-fixture", email: "user@example.test", failSave: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			admin := newAccountTestAdmin(t)
			state, err := aistudio.ParseCookieImport("SAPISID=old-fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
			if err != nil {
				t.Fatal(err)
			}
			cfg := aistudio.DefaultAccountConfig("user@example.test")
			cfg.AuthUser, cfg.Proxy = 3, "http://127.0.0.1:4321"
			account, err := admin.addAccount(t.Context(), cfg, state, "", "renewal-fixture")
			if err != nil {
				t.Fatal(err)
			}
			stored, _ := admin.pool.Account(account.ID)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			admin.verifyCookies = func(_ context.Context, next *aistudio.StorageState, proxy string, authUser int) (chromeauth.Verification, error) {
				calls++
				if authUser != 3 || proxy != cfg.Proxy {
					t.Fatal("renewal used the wrong account environment")
				}
				next.Cookies[0].Value = "verified-renewal"
				if test.name == "invalid verified state" {
					next.Cookies = nil
				}
				if test.canceled {
					cancel()
				}
				if test.failSave {
					path := filepath.Join(stored.Directory, pairingRenewalName)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				return chromeauth.Verification{Email: test.email}, nil
			}
			result, err := admin.RenewAccountCookies(ctx, account.ID, test.token, state.Cookies)
			if (err != nil) != test.wantError || test.token == "renewal-fixture" && calls != 1 || test.token != "renewal-fixture" && calls != 0 {
				t.Fatalf("renewal calls=%d error=%v", calls, err)
			}
			if test.token != "renewal-fixture" {
				var operation *adminOperationError
				if !errors.As(err, &operation) || operation.code != "pairing_renewal_invalid" {
					t.Fatal("invalid token was not rejected before cookie validation")
				}
			}
			updated, loadErr := aistudio.LoadStorageState(stored.StoragePath)
			want := "verified-renewal"
			if test.wantError {
				want = "old-fixture"
			}
			if loadErr != nil || updated.Cookies[0].Value != want {
				t.Fatal("renewal failure committed partial cookies")
			}
			if !test.wantError && (result.PairingRenewedAt == nil || result.PairingRenewedAt.IsZero()) {
				t.Fatal("successful renewal did not record its timestamp")
			}
			lease, acquireErr := admin.pool.AcquireAccount(t.Context(), account.ID)
			if acquireErr != nil {
				t.Fatal("renewal retained its exclusive account lease")
			}
			_ = lease.Release()
		})
	}
}
