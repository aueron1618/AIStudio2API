package aistudio

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		"storage-state": `{"cookies":` + jsonCookies + `,"origins":[{"origin":"https://evil.example","localStorage":[]}],"aistudio2api":{"source":{"email":"forged@example.com"}}}`,
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
