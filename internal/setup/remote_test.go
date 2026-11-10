package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// TestSetupRemoteOrigin 验证远程地址只接受 HTTPS origin 与本机 HTTP
func TestSetupRemoteOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://example.com":       "https://example.com",
		"https://example.com:8443/": "https://example.com:8443",
		"http://127.0.0.1:2048":     "http://127.0.0.1:2048",
		"http://localhost:2048":     "http://localhost:2048",
		"http://[::1]:2048":         "http://[::1]:2048",
		"http://192.168.1.2:2048":   "",
		"http://example.com":        "",
		"https://example.com/api":   "",
		"https://u:p@example.com":   "",
		"https://example.com?a=1":   "",
		"example.com":               "",
	} {
		got, err := remoteOrigin(raw)
		if want == "" && err == nil || want != "" && (err != nil || got != want) {
			t.Errorf("remoteOrigin(%q) = %q, %v", raw, got, err)
		}
	}
	if _, err := parseSetupFlags([]string{"--token", "abc"}, config.Config{}); err == nil {
		t.Fatal("没有 --remote 时接受了 --token")
	}
}

type setupTransport func(*http.Request) (*http.Response, error)

func (transport setupTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type setupTargetFunc func(context.Context, aistudio.AccountConfig, aistudio.StorageState, string) (string, error)

func (target setupTargetFunc) Save(ctx context.Context, cfg aistudio.AccountConfig, state aistudio.StorageState, directory string) (string, error) {
	return target(ctx, cfg, state, directory)
}

func TestSetupCookieEmailCannotBypassVerification(t *testing.T) {
	previous := verifySetupCookies
	t.Cleanup(func() { verifySetupCookies = previous })
	for _, test := range []struct {
		name, requested string
		canceled        bool
		wantError       bool
	}{
		{name: "infer identity"},
		{name: "matching explicit email", requested: "USER@example.test"},
		{name: "forged explicit email", requested: "forged@example.test", wantError: true},
		{name: "canceled verification", requested: "user@example.test", canceled: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls, saves := 0, 0
			verifySetupCookies = func(_ context.Context, state *aistudio.StorageState, proxy string, authUser int) (chromeauth.Verification, error) {
				calls++
				if authUser != 2 || proxy != "http://127.0.0.1:4321" || len(state.Origins) != 0 {
					t.Fatal("CLI Cookie verification lost session/proxy or trusted localStorage")
				}
				if _, exists, _ := state.AuthExtension(); exists {
					t.Fatal("CLI Cookie verification trusted uploaded identity")
				}
				state.Cookies[0].Value = "verified-fixture"
				if test.canceled {
					cancel()
				}
				return chromeauth.Verification{Email: "user@example.test"}, nil
			}
			state, err := aistudio.ParseCookieImport("SAPISID=fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
			if err != nil {
				t.Fatal(err)
			}
			state.SetAuthExtension(aistudio.AuthExtension{Source: aistudio.AuthSource{Email: "forged@example.test"}, OAuth: &aistudio.ChromeOAuthMaterial{RefreshToken: "oauth-fixture"}})
			state.Origins = []aistudio.StorageOrigin{{Origin: "https://aistudio.google.com", LocalStorage: []aistudio.StorageItem{{Name: "key", Value: "untrusted-fixture"}}}}
			raw, _ := json.Marshal(state)
			options := setupOptions{cookies: string(raw), authUser: 2, egress: "http://127.0.0.1:4321", locale: "en-US", timezone: "UTC"}
			if test.requested != "" {
				options.emails = []string{test.requested}
			}
			target := setupTargetFunc(func(_ context.Context, cfg aistudio.AccountConfig, state aistudio.StorageState, directory string) (string, error) {
				saves++
				if cfg.Label != "user@example.test" || cfg.AuthUser != 2 || state.Cookies[0].Value != "verified-fixture" || directory != "" {
					t.Fatal("CLI Cookie save did not use verified identity and cookies")
				}
				return cfg.Label, nil
			})
			err = importCookies(ctx, target, options)
			if (err != nil) != test.wantError || calls != 1 || test.wantError && saves != 0 || !test.wantError && saves != 1 {
				t.Fatalf("verification=%d saves=%d error=%v", calls, saves, err)
			}
		})
	}
}

func TestRemoteUploadKeepsOAuthOnSource(t *testing.T) {
	for _, responseToken := range []string{"renewal-fixture", ""} {
		t.Run("token="+responseToken, func(t *testing.T) {
			state, err := aistudio.ParseCookieImport("SAPISID=fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
			if err != nil {
				t.Fatal(err)
			}
			extension := aistudio.AuthExtension{
				Source: aistudio.AuthSource{Browser: "chrome", Root: "fixture-root", Profile: "Default", Email: "user@example.test"},
				OAuth:  &aistudio.ChromeOAuthMaterial{GaiaID: "fixture-gaia", RefreshToken: "source-oauth-fixture", WrappedBindingKey: []byte("fixture-key")},
			}
			state.SetAuthExtension(extension)
			state.Origins = []aistudio.StorageOrigin{{Origin: "https://aistudio.google.com", LocalStorage: []aistudio.StorageItem{{Name: "secret", Value: "source-local-fixture"}}}}
			client := &http.Client{Transport: setupTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost || request.URL.String() != "https://fixture.invalid/api/pairing/accounts" || request.Header.Get("Authorization") != "Bearer pairing-fixture" {
					t.Fatal("remote upload lost endpoint or pairing authentication")
				}
				body, err := io.ReadAll(request.Body)
				if err != nil || strings.Contains(string(body), "source-oauth-fixture") || strings.Contains(string(body), "source-local-fixture") || strings.Contains(string(body), "fixture-root") {
					t.Fatal("remote upload transmitted source OAuth or localStorage")
				}
				var input api.AccountStateInput
				if json.Unmarshal(body, &input) != nil || input.AuthUser != 2 || input.Label != "user@example.test" || len(input.StorageState.Cookies) != 3 {
					t.Fatal("remote upload lost verified account input")
				}
				result, _ := json.Marshal(api.PairingAccountResult{Account: api.AdminAccount{ID: input.Label, Label: input.Label}, RenewalToken: responseToken})
				return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(result)))}, nil
			})}
			target := &remoteTarget{client: client, origin: "https://fixture.invalid", token: "pairing-fixture", proxy: "http://127.0.0.1:1234"}
			cfg := aistudio.DefaultAccountConfig("user@example.test")
			cfg.AuthUser = 2
			_, err = target.Save(t.Context(), cfg, state, "")
			if responseToken == "" {
				if err == nil || len(target.renewals) != 0 {
					t.Fatal("remote upload accepted an unusable renewal response")
				}
				return
			}
			if err != nil || len(target.renewals) != 1 {
				t.Fatalf("remote upload = %v", err)
			}
			renewal := target.renewals[0]
			if renewal.Account != cfg.Label || renewal.Token != responseToken || renewal.Source != extension.Source || !reflect.DeepEqual(renewal.OAuth, *extension.OAuth) || renewal.Proxy != target.proxy {
				t.Fatal("source did not retain correct OAuth and renewal token")
			}
			kept, _, _ := state.AuthExtension()
			if !reflect.DeepEqual(kept, extension) {
				t.Fatal("stripping upload mutated the source authentication state")
			}
		})
	}
}

func TestRemoteRenewalPersistenceAndCookiePush(t *testing.T) {
	previousPath, previousRefresh := renewalsPath, refreshRemoteCookies
	t.Cleanup(func() { renewalsPath, refreshRemoteCookies = previousPath, previousRefresh })
	renewalsPath = filepath.Join(t.TempDir(), "runtime", "remote-renewals.json")
	renewal := remoteRenewal{
		Origin: "https://fixture.invalid", Account: "user@example.test", Token: "renewal-fixture",
		Source: aistudio.AuthSource{Browser: "chrome", Root: "fixture-root", Email: "user@example.test"},
		OAuth:  aistudio.ChromeOAuthMaterial{GaiaID: "fixture-gaia", RefreshToken: "source-oauth-fixture"},
	}
	if err := saveRenewals([]remoteRenewal{renewal}); err != nil {
		t.Fatal(err)
	}
	renewal.Token = "rotated-renewal-fixture"
	if err := saveRenewals([]remoteRenewal{renewal}); err != nil {
		t.Fatal(err)
	}
	stored, err := originRenewals(renewal.Origin)
	if err != nil || !reflect.DeepEqual(stored, []remoteRenewal{renewal}) {
		t.Fatal("source renewal replacement did not persist")
	}
	entries, err := os.ReadDir(filepath.Dir(renewalsPath))
	if err != nil || len(entries) != 1 {
		t.Fatal("atomic renewal save left temporary secret files")
	}
	refreshRemoteCookies = func(_ context.Context, source aistudio.AuthSource, oauth aistudio.ChromeOAuthMaterial, _ string) ([]aistudio.StateCookie, *aistudio.BoundSession, error) {
		if source != renewal.Source || !reflect.DeepEqual(oauth, renewal.OAuth) {
			t.Fatal("renewal did not use source Chrome OAuth")
		}
		state, err := aistudio.ParseCookieImport("SAPISID=refreshed-fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
		return state.Cookies, &aistudio.BoundSession{ID: "source-session"}, err
	}
	calls := 0
	client := &http.Client{Transport: setupTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPut || request.URL.Path != "/api/pairing/accounts/user@example.test/cookies" || request.Header.Get("Authorization") != "Bearer rotated-renewal-fixture" {
			t.Fatal("renewal push lost account path or token")
		}
		body, _ := io.ReadAll(request.Body)
		var input api.PairingRenewalInput
		if json.Unmarshal(body, &input) != nil || len(input.Cookies) != 3 || input.Cookies[0].Value != "refreshed-fixture" || strings.Contains(string(body), "source-oauth-fixture") {
			t.Fatal("renewal did not send only fresh cookies")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	if err := pushRenewal(t.Context(), client, stored[0]); err != nil || calls != 1 {
		t.Fatalf("renewal push calls=%d error=%v", calls, err)
	}
	refreshRemoteCookies = func(context.Context, aistudio.AuthSource, aistudio.ChromeOAuthMaterial, string) ([]aistudio.StateCookie, *aistudio.BoundSession, error) {
		return nil, nil, context.Canceled
	}
	if err := pushRenewal(t.Context(), client, stored[0]); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("canceled cookie refresh still made a remote request")
	}
}

// TestPairingRenewalUploadWithoutBoundSession 验证 OAuth 续签材料与 DBSC 会话登记不随 Cookie 上传
func TestPairingRenewalUploadWithoutBoundSession(t *testing.T) {
	var uploaded aistudio.AuthExtension
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input api.AccountStateInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		uploaded, _, _ = input.StorageState.AuthExtension()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(api.PairingAccountResult{Account: api.AdminAccount{ID: "user@example.com", Label: "user@example.com"}, RenewalToken: "renewal"})
	}))
	defer server.Close()
	state, err := aistudio.ParseCookieImport("SAPISID=fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
	if err != nil {
		t.Fatal(err)
	}
	state.SetAuthExtension(aistudio.AuthExtension{
		Source: aistudio.AuthSource{Browser: "chrome", Profile: "Default", Email: "user@example.com"},
		OAuth:  &aistudio.ChromeOAuthMaterial{GaiaID: "gaia", RefreshToken: "token", WrappedBindingKey: []byte("key")},
		Bound:  &aistudio.BoundSession{ID: "sidts_session", RefreshURL: "https://accounts.google.com/RotateBoundCookies", Credentials: []string{"__Secure-1PSIDRTS"}},
	})
	target := &remoteTarget{client: server.Client(), origin: server.URL, token: "pairing"}
	if _, err := target.Save(t.Context(), aistudio.DefaultAccountConfig("user@example.com"), state, ""); err != nil {
		t.Fatal(err)
	}
	if uploaded.OAuth != nil || uploaded.Bound != nil || uploaded.Source.Browser != "" {
		t.Fatalf("上传的认证扩展 %+v", uploaded)
	}
	if len(target.renewals) != 1 || target.renewals[0].OAuth.RefreshToken != "token" {
		t.Fatalf("本机续签材料 %+v", target.renewals)
	}
	if original, _, err := state.AuthExtension(); err != nil || original.Bound == nil || original.Bound.ID != "sidts_session" {
		t.Fatal("upload changed the source DBSC session")
	}
}
