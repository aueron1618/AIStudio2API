package chromeauth

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type verificationTransport func(*http.Request) (*http.Response, error)

func (transport verificationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestVerifyHTTPIdentityAndRotatedCookies(t *testing.T) {
	state, err := aistudio.ParseCookieImport("SAPISID=original-secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: verificationTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		headers := make(http.Header)
		var body string
		switch request.URL.String() {
		case aiStudioChatURL:
			if request.Method != http.MethodGet || request.Header.Get("User-Agent") != "verification-test" || !strings.Contains(request.Header.Get("Cookie"), "SAPISID=original-secret") {
				t.Fatal("page verification lost its request headers")
			}
			body = `<html>untrusted@example.com<script>window.WIZ_global_data={"oPEP7c":"Real\u0040Example.com"};</script></html>`
			headers.Set("Set-Cookie", "SAPISID=rotated-secret; Domain=.google.com; Path=/; Secure")
		case aistudio.MakerSuiteRPCBase + "ListModels":
			if request.Method != http.MethodPost || !strings.Contains(request.Header.Get("Cookie"), "SAPISID=rotated-secret") {
				t.Fatal("model verification did not use the page's rotated cookies")
			}
			if !strings.HasPrefix(request.Header.Get("Authorization"), "SAPISIDHASH ") || request.Header.Get("X-Goog-Authuser") != "0" || request.Header.Get("Origin") != "https://aistudio.google.com" {
				t.Fatal("model verification lost authentication headers")
			}
			input, err := io.ReadAll(request.Body)
			if err != nil || string(input) != "[]" {
				t.Fatal("unexpected model verification payload")
			}
			body = `[[["models/test",null,null,"Test model",null,1024,256,["generateContent"]]]]`
			headers.Set("Content-Type", aistudio.JSONProtobufContentType)
			headers.Set("Set-Cookie", "__Secure-1PAPISID=renewed-secret; Domain=.google.com; Path=/; Secure")
		default:
			t.Fatalf("unexpected verification URL: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	email, err := verifyChatPage(t.Context(), client, &state, "verification-test")
	if err != nil || email != "Real@Example.com" {
		t.Fatalf("verified identity = %q, error = %v", email, err)
	}
	models, err := verifyModels(t.Context(), client, &state, http.Header{"X-Goog-Authuser": {"0"}})
	if err != nil || models != 1 || calls != 2 {
		t.Fatalf("verified models = %d, calls = %d, error = %v", models, calls, err)
	}
	if value, _ := state.CookieValue("__Secure-1PAPISID", aiStudioChatURL, time.Now()); value != "renewed-secret" {
		t.Fatal("model verification did not retain rotated cookies")
	}
}

func TestVerifyHTTPRejectsMissingIdentity(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		signedOut bool
	}{
		{"redirect", http.StatusFound, "", true},
		{"forbidden", http.StatusForbidden, "", false},
		{"missing", http.StatusOK, "other@example.com", false},
		{"invalid-field", http.StatusOK, `{"oPEP7c":123}`, false},
		{"invalid-email", http.StatusOK, `{"oPEP7c":"not-an-email"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := aistudio.StorageState{}
			calls := 0
			client := &http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
				Transport: verificationTransport(func(request *http.Request) (*http.Response, error) {
					calls++
					if calls > 1 {
						t.Fatal("verification followed an authentication redirect")
					}
					return &http.Response{
						StatusCode: test.status, Request: request, Body: io.NopCloser(strings.NewReader(test.body)),
						Header: http.Header{"Location": {"https://accounts.google.com/"}},
					}, nil
				}),
			}
			email, err := verifyChatPage(t.Context(), client, &state, "verification-test")
			if err == nil || email != "" || errors.Is(err, ErrSignedOut) != test.signedOut || calls != 1 {
				t.Fatalf("invalid identity accepted: email=%q, error=%v", email, err)
			}
		})
	}
}

func TestVerifyHTTPRejectsInvalidModelResponses(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{"unauthorized", http.StatusUnauthorized, aistudio.JSONProtobufContentType, "[]"},
		{"html", http.StatusOK, "text/html", "<html>sign in</html>"},
		{"malformed", http.StatusOK, aistudio.JSONProtobufContentType, "{"},
		{"empty", http.StatusOK, aistudio.JSONProtobufContentType, "[[]]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := aistudio.ParseCookieImport("SAPISID=secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: verificationTransport(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status, Request: request, Body: io.NopCloser(strings.NewReader(test.body)),
					Header: http.Header{"Content-Type": {test.contentType}},
				}, nil
			})}
			if models, err := verifyModels(t.Context(), client, &state, make(http.Header)); err == nil || models != 0 {
				t.Fatalf("invalid model verification succeeded: models=%d, error=%v", models, err)
			}
		})
	}
}
