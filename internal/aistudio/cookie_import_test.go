package aistudio

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

const importTestHeader = "SAPISID=test-secret==; __Secure-1PAPISID=one; __Secure-3PAPISID=three"

func TestParseCookieImportFormats(t *testing.T) {
	jsonCookies := `[{"name":"SAPISID","value":"test-secret==","domain":".google.com","expirationDate":4102444800,"sameSite":"no_restriction"},{"name":"__Secure-1PAPISID","value":"one"},{"name":"__Secure-3PAPISID","value":"three","session":true}]`
	netscape := "# Netscape HTTP Cookie File\r\n#HttpOnly_.google.com\tTRUE\t/\tTRUE\t0\tSAPISID\ttest-secret==\r\n.google.com\tTRUE\t/\tTRUE\t4102444800\t__Secure-1PAPISID\tone\n.google.com\tTRUE\t/\tTRUE\t0\t__Secure-3PAPISID\tthree\n.google.com\tTRUE\t/\tTRUE\t0\tempty\t"
	for name, raw := range map[string]string{
		"header":        importTestHeader,
		"prefixed":      "\ufeffCookie: " + importTestHeader,
		"multiline":     strings.ReplaceAll(importTestHeader, "; ", "\r\n"),
		"array":         jsonCookies,
		"storage-state": `{"cookies":` + jsonCookies + `,"origins":[{"origin":"https://aistudio.google.com","localStorage":[{"name":"identity","value":"forged"}]}],"aistudio2api":{"source":{"email":"forged@example.com"},"oauth":{"refresh_token":"untrusted"}}}`,
		"netscape":      netscape,
	} {
		t.Run(name, func(t *testing.T) {
			state, err := ParseCookieImport(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Cookies) != 3 || len(state.Origins) != 0 {
				t.Fatalf("unexpected cookie/origin counts: %d/%d", len(state.Cookies), len(state.Origins))
			}
			if _, exists, _ := state.AuthExtension(); exists {
				t.Fatal("trusted uploaded identity")
			}
			upstream, err := ParseCookieText(raw)
			if err != nil {
				t.Fatal(err)
			}
			localJSON, _ := json.Marshal(state)
			upstreamJSON, _ := json.Marshal(upstream)
			if string(localJSON) != string(upstreamJSON) {
				t.Fatal("upstream and local Cookie entries use different security rules")
			}
			if value, ok := state.CookieValue("SAPISID", aiStudioOrigin, time.Now()); !ok || value != "test-secret==" {
				t.Fatal("lost cookie value")
			}
			if _, err := NewSigner().Sign(state); err != nil {
				t.Fatal(err)
			}
			if name == "netscape" && (!state.Cookies[0].HTTPOnly || state.Cookies[0].Expires != -1) {
				t.Fatal("lost Netscape session/HttpOnly attributes")
			}
			if name == "array" && (state.Cookies[0].Expires != 4102444800 || state.Cookies[0].SameSite != "None" || !state.Cookies[0].Secure) {
				t.Fatal("lost extension metadata")
			}
			path := filepath.Join(t.TempDir(), "storage-state.json")
			if err := WriteStorageState(path, state); err != nil {
				t.Fatal(err)
			}
			reloaded, err := LoadStorageState(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewSigner().Sign(reloaded); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCookieImportScopeAndExpiry(t *testing.T) {
	state, err := ParseCookieImport(importTestHeader)
	if err != nil {
		t.Fatal(err)
	}
	state.Cookies = append(state.Cookies,
		StateCookie{Name: "SAPISID", Value: "other-host", Domain: "accounts.google.com", Path: "/", Expires: -1},
		StateCookie{Name: "other", Value: "foreign", Domain: ".example.com", Path: "/", Expires: -1},
		StateCookie{Name: "other", Value: "foreign", Domain: "evilgoogle.com", Path: "/", Expires: -1},
		StateCookie{Name: "expired", Value: "old", Domain: ".google.com", Path: "/", Expires: 1},
		StateCookie{Name: "partitioned", Value: "old", Domain: ".google.com", Path: "/", Expires: -1, PartitionKey: "https://example.com"},
	)
	raw, _ := json.Marshal(state)
	parsed, err := ParseCookieImport(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Cookies) != 4 {
		t.Fatalf("cookie count = %d", len(parsed.Cookies))
	}
	if value, _ := parsed.CookieValue("SAPISID", aiStudioOrigin, time.Now()); value != "test-secret==" {
		t.Fatal("broadened host-specific cookie")
	}
	parsed, err = ParseCookieImport(importTestHeader + "; OSID=osid; __Host-GAPS=gaps")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Cookies[3].Domain != "aistudio.google.com" || parsed.Cookies[4].Domain != "accounts.google.com" {
		t.Fatal("incorrect header cookie domains")
	}
	parsed, err = ParseCookieImport(importTestHeader + "; SAPISID=test-secret==")
	if err != nil || len(parsed.Cookies) != 3 {
		t.Fatalf("identical duplicate: %v", err)
	}
}

func TestCookieImportRejectsInvalidInputWithoutSecrets(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":            " ",
		"missing-auth":     "SID=test-secret",
		"broken-json":      `[{"name":"test-secret"`,
		"wrong-json-type":  `{"cookies":"test-secret"}`,
		"wrong-value-type": `[{"name":"SAPISID","value":{"secret":"test-secret"}}]`,
		"header-injection": importTestHeader + "; bad=test-secret\x00",
		"invalid-name":     importTestHeader + "; bad name=test-secret",
		"conflict":         importTestHeader + "; SAPISID=test-secret-other",
		"invalid-netscape": ".google.com\tTRUE\t/\tTRUE\tbad\tSAPISID\ttest-secret",
		"too-large":        strings.Repeat("x", MaxCookieImportBytes+1),
		"expired-session":  `{"cookies":[{"name":"SAPISID","value":"test-secret","expires":1,"session":true}]}`,
		"expired":          `{"cookies":[{"name":"SAPISID","value":"test-secret","expires":1}]}`,
		"invalid-path":     `[{"name":"SAPISID","value":"test-secret","path":"bad"}]`,
		"invalid-samesite": `[{"name":"SAPISID","value":"test-secret","sameSite":"bad"}]`,
		"host-prefix":      importTestHeader + "; __Host-test=test-secret\n#HttpOnly_.google.com\tTRUE\t/\tTRUE\t0\t__Host-bad\ttest-secret",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCookieImport(raw)
			if err == nil {
				t.Fatal("accepted invalid cookies")
			}
			if strings.Contains(err.Error(), "test-secret") {
				t.Fatal("error leaks cookie data")
			}
			if _, err := ParseCookieText(raw); err == nil || strings.Contains(err.Error(), "test-secret") {
				t.Fatal("upstream Cookie parser bypassed validation or leaked a secret")
			}
		})
	}
	var cookies strings.Builder
	cookies.WriteString(importTestHeader)
	for i := 0; i < maxImportCookies; i++ {
		fmt.Fprintf(&cookies, "; c%d=v", i)
	}
	if _, err := ParseCookieImport(cookies.String()); err == nil {
		t.Fatal("accepted too many cookies")
	}
}

func TestCookieTextMissingLoginSentinel(t *testing.T) {
	for _, raw := range []string{"[]", "SID=only", `[{"name":"SAPISID","value":"old","expires":1,"session":true}]`} {
		if _, err := ParseCookieText(raw); !errors.Is(err, ErrLoginCookieMissing) {
			t.Fatalf("browser discovery cannot identify signed-out state: %v", err)
		}
	}
}

func TestDecodeCookieLoginResultPreservesCleanupWarning(t *testing.T) {
	state, err := ParseCookieImport(importTestHeader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeNativeLoginResult(camoufoxnative.LoginResult{
		StorageStateJSON: raw, Email: "real@example.com", CloseError: "cleanup warning", VerifiedAt: time.Now(),
	}, "cookie")
	if err != nil || result.CloseError != "cleanup warning" || result.Email != "real@example.com" {
		t.Fatalf("successful cookie export was discarded: %+v, %v", result, err)
	}
	extension, exists, err := result.StorageState.AuthExtension()
	if err != nil || !exists || extension.Source.Browser != "cookie" || extension.Source.Email != result.Email || extension.OAuth != nil {
		t.Fatal("decoded Cookie result has the wrong identity or OAuth material")
	}
}
