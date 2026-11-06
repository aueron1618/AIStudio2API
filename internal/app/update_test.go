package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSelfUpdaterKeepsForkReleaseSource(t *testing.T) {
	updater := newSelfUpdater("dev", "aistudio2api", nil, nil, nil)
	if updater.releaseURL != "https://api.github.com/repos/aueron1618/AIStudio2API/releases/latest" {
		t.Fatalf("updater would replace the fork with another distribution: %s", updater.releaseURL)
	}
}

func TestSelfUpdaterReleaseDelivery(t *testing.T) {
	for _, target := range []struct {
		name   string
		format string
		binary string
	}{
		{"windows-amd64", ".zip", "aistudio2api.exe"},
		{"linux-amd64", ".tar.gz", "aistudio2api"},
		{"linux-arm64", ".tar.gz", "aistudio2api"},
		{"darwin-amd64", ".tar.gz", "aistudio2api"},
		{"darwin-arm64", ".tar.gz", "aistudio2api"},
		{"darwin-universal", ".zip", "aistudio2api"},
		{"linux-amd64", ".AppImage", ""},
		{"linux-amd64", ".deb", ""},
	} {
		t.Run(target.name+target.format, func(t *testing.T) {
			const version = "v1.2.3"
			base := "aistudio2api-" + version + "-" + target.name
			payload := []byte("offline executable fixture")
			var archive bytes.Buffer
			switch target.format {
			case ".zip":
				writer := zip.NewWriter(&archive)
				names := []string{base + "/start.bat", base + "/" + target.binary}
				if target.name == "darwin-universal" {
					names = []string{"AIStudio2API.app/Contents/MacOS/" + target.binary}
				}
				for _, name := range names {
					entry, err := writer.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					body := payload
					if strings.HasSuffix(name, "/start.bat") {
						body = []byte("@echo off\r\naistudio2api.exe %*\r\n")
					}
					if _, err := entry.Write(body); err != nil {
						t.Fatal(err)
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			case ".tar.gz":
				compressed := gzip.NewWriter(&archive)
				writer := tar.NewWriter(compressed)
				if err := writer.WriteHeader(&tar.Header{Name: base + "/" + target.binary, Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(payload); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := compressed.Close(); err != nil {
					t.Fatal(err)
				}
			default:
				archive.Write(payload)
			}
			sum := sha256.Sum256(archive.Bytes())
			release := githubRelease{
				TagName: version,
				HTMLURL: "https://github.com/aueron1618/AIStudio2API/releases/tag/" + version,
				Assets:  []githubAsset{{Name: base + target.format, Digest: "sha256:" + hex.EncodeToString(sum[:])}},
			}
			var checks atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected request method or credentials: %s", r.Method)
				}
				switch r.URL.Path {
				case "/releases/latest":
					checks.Add(1)
					if r.Header.Get("Accept") != "application/vnd.github+json" {
						t.Error("release metadata Accept header missing")
					}
					if err := json.NewEncoder(w).Encode(release); err != nil {
						t.Error(err)
					}
				case "/asset":
					if r.Header.Get("Accept") != "application/octet-stream" {
						t.Error("release asset Accept header missing")
					}
					if _, err := w.Write(archive.Bytes()); err != nil {
						t.Error(err)
					}
				default:
					http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			release.Assets[0].URL = server.URL + "/asset"
			updater := newSelfUpdater("v1.2.2", "unused-offline-executable", func() string { return "" }, nil, nil)
			updater.releaseURL = server.URL + "/releases/latest"
			for _, refresh := range []bool{false, false, true} {
				status, err := updater.UpdateStatus(t.Context(), refresh)
				if err != nil || !status.Available || status.Latest != version || status.URL != release.HTMLURL {
					t.Fatalf("release status = %+v, %v", status, err)
				}
			}
			if checks.Load() != 2 {
				t.Fatalf("metadata cache/refresh fetched %d times", checks.Load())
			}
			downloaded, err := updater.download(t.Context(), &release, base+target.format)
			if err != nil || !bytes.Equal(downloaded, archive.Bytes()) {
				t.Fatalf("verified download failed: %v", err)
			}
			if target.binary != "" {
				executable, err := extractExecutable(downloaded, base+target.format, target.binary)
				if err != nil || !bytes.Equal(executable, payload) {
					t.Fatalf("release archive layout failed: %v", err)
				}
			}
			for _, digest := range []string{"", "sha256:" + strings.Repeat("0", 64)} {
				invalid := release
				invalid.Assets = []githubAsset{{Name: base + target.format, URL: server.URL + "/asset", Digest: digest}}
				if _, err := updater.download(t.Context(), &invalid, base+target.format); err == nil {
					t.Fatalf("accepted unverified payload with digest %q", digest)
				}
			}
			if _, err := updater.download(t.Context(), &release, "missing-asset"); err == nil {
				t.Fatal("missing release asset was accepted")
			}
			updater.releaseURL = server.URL + "/unavailable"
			status, err := updater.UpdateStatus(t.Context(), true)
			if err != nil || status.Error == "" || status.URL != release.HTMLURL {
				t.Fatalf("failed refresh discarded fork metadata: %+v, %v", status, err)
			}
		})
	}
}
