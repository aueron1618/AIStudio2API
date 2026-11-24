package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type promptMediaTestStore struct {
	PromptStore
	path, mime    string
	opened, saved int
}

func (store *promptMediaTestStore) OpenMedia(_ context.Context, id string) (*os.File, string, error) {
	store.opened++
	if id != "fixture" {
		return nil, "", ErrPromptNotFound
	}
	file, err := os.Open(store.path)
	return file, store.mime, err
}

func (store *promptMediaTestStore) SaveMedia(ctx context.Context, _ string, content io.Reader) (string, error) {
	store.saved++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_, err := io.Copy(io.Discard, content)
	return "fixture", err
}

func TestPromptMediaPermissionsAndRange(t *testing.T) {
	for _, mimeType := range []string{"video/webm", "application/pdf"} {
		t.Run(mimeType, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "media.fixture")
			if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
				t.Fatal(err)
			}
			store := &promptMediaTestStore{path: path, mime: mimeType}
			handler := NewHandler(nil, Config{Prompts: store, APIKey: "generation-key", AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "fixture", AdminSessionPath: filepath.Join(dir, "sessions.json")})
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				endpoint := "/api/prompts/media/fixture"
				if method == http.MethodPost {
					endpoint = "/api/prompts/media"
				}
				response := accountTaskRequest(handler, method, endpoint, "private-media", http.Header{"Authorization": {"Bearer generation-key"}})
				if response.Code != http.StatusUnauthorized || store.opened != 0 || store.saved != 0 {
					t.Fatalf("generation key accessed management media: %d %s", response.Code, response.Body)
				}
			}
			login := accountTaskRequest(handler, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"fixture"}`, nil)
			if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
				t.Fatal("fixture management login failed")
			}
			headers := http.Header{"Cookie": {login.Result().Cookies()[0].String()}, "Range": {"bytes=2-5"}}
			foreign := headers.Clone()
			foreign.Set("Origin", "https://foreign.example")
			if response := accountTaskRequest(handler, http.MethodGet, "/api/prompts/media/fixture", "", foreign); response.Code != http.StatusForbidden || store.opened != 0 {
				t.Fatal("foreign origin accessed stored prompt media")
			}
			response := accountTaskRequest(handler, http.MethodGet, "/api/prompts/media/fixture", "", headers)
			if response.Code != http.StatusPartialContent || response.Header().Get("Content-Range") != "bytes 2-5/10" || response.Body.String() != "2345" || response.Header().Get("Content-Type") != mimeType {
				t.Fatalf("prompt media Range contract changed: %d %v %s", response.Code, response.Header(), response.Body)
			}
			policy := response.Header().Get("Content-Security-Policy")
			if !strings.Contains(policy, "frame-ancestors 'self'") || strings.Contains(policy, "sandbox") != (mimeType != "application/pdf") {
				t.Fatalf("prompt media framing policy changed: %s", policy)
			}
			response = accountTaskRequest(handler, http.MethodPost, "/api/prompts/media", "private-media", headers)
			if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"id":"fixture"}` || store.saved != 1 {
				t.Fatalf("authenticated prompt media upload failed: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestMediaDirectoryDefaultLimitPreservesFiles(t *testing.T) {
	dir := t.TempDir()
	name := fmt.Sprintf("%064x.png", 1)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("existing media"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(nil, Config{MediaDir: dir})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/media/"+name, nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "existing media" {
		t.Fatalf("default media limit removed existing content: %d %s", recorder.Code, recorder.Body)
	}
}

func TestMediaDirectoryRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	store := &mediaStore{dir: dir, limit: 10}
	if err := store.save("existing.png", []byte("keep")); err != nil {
		t.Fatal(err)
	}
	if err := store.save("oversized.png", make([]byte, 11)); err == nil {
		t.Fatal("oversized media was saved and immediately evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "oversized.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized media was written: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "existing.png")); err != nil || string(data) != "keep" {
		t.Fatalf("oversized media evicted existing content: %q, %v", data, err)
	}
}

// TestMediaDirectoryLimit 验证媒体目录超过上限时删除最久未使用的文件：启动与保存新文件时回收，读取媒体与续接读回刷新使用时间
func TestMediaDirectoryLimit(t *testing.T) {
	dir := t.TempDir()
	setUsed := func(name string, hours int) {
		t.Helper()
		used := time.Now().Add(time.Duration(hours) * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, name), used, used); err != nil {
			t.Fatal(err)
		}
	}
	name := func(index int) string { return fmt.Sprintf("%064x.png", index) }
	for index := range 4 {
		name := name(index)
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, 10), 0o644); err != nil {
			t.Fatal(err)
		}
		setUsed(name, index-10)
	}
	handler := NewHandler(nil, Config{MediaDir: dir, MediaLimit: 30})
	assertMedia := func(want ...string) {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, entry := range entries {
			got = append(got, entry.Name())
		}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("媒体目录 = %v，期望 %v", got, want)
		}
	}
	assertMedia(name(1), name(2), name(3))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/media/"+name(1), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /media/%s = %d", name(1), recorder.Code)
	}
	server := &server{config: Config{MediaDir: dir}, media: &mediaStore{dir: dir, limit: 30}}
	output := mediaOutput{base: "http://127.0.0.1:2048", store: server.media}
	first := aistudio.Blob{MIME: "image/png", Data: []byte("first-new!")}
	if err := output.save(first); err != nil {
		t.Fatal(err)
	}
	assertMedia(name(1), name(3), mediaFileName(first))

	setUsed(name(1), -5)
	setUsed(mediaFileName(first), -4)
	link := aistudio.Content{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "![media](http://127.0.0.1:2048/media/" + name(3) + ")"}}}
	if err := server.restoreMediaLinks([]aistudio.Content{link}); err != nil {
		t.Fatal(err)
	}
	second := aistudio.Blob{MIME: "image/png", Data: []byte("second-new")}
	if err := output.save(second); err != nil {
		t.Fatal(err)
	}
	assertMedia(name(3), mediaFileName(first), mediaFileName(second))
}
