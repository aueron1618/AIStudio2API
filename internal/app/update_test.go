package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelfUpdaterKeepsForkReleaseSource(t *testing.T) {
	updater := newSelfUpdater("dev", "aistudio2api", nil, nil, nil)
	if updater.releaseURL != "https://github.com/aueron1618/AIStudio2API" ||
		updater.apiURL != "https://api.github.com/repos/aueron1618/AIStudio2API/releases/tags/" {
		t.Fatalf("updater would replace the fork with another distribution: %s, %s", updater.releaseURL, updater.apiURL)
	}
}

const testReleasePath = "/aueron1618/AIStudio2API"
const testTagPath = testReleasePath + "/releases/tag/v1.2.3"
const testReleaseAPI = "/repos" + testReleasePath + "/releases/tags/"

func newTestUpdater(t *testing.T, handler http.HandlerFunc) *selfUpdater {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("release request sent credentials")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	u := newSelfUpdater("v1.2.2", "unused-offline-executable", func() string { return "" }, newTestRequestRegistry(t), nil)
	u.mode, u.releaseURL, u.apiURL = "binary", server.URL+testReleasePath, server.URL+testReleaseAPI
	return u
}

func writeTestAtom(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `<feed xmlns="http://www.w3.org/2005/Atom">
<entry><id>tag:github.com,2008:Repository/42/v1.2.3</id><link href="https://other.invalid%s"/><content>foreign notes</content></entry>
<entry><link rel="alternate" href="http://%s%s"/><updated>2026-01-02T03:04:05Z</updated>
<content type="html"><![CDATA[<p>Fix <b>A &amp; B</b></p><ul><li><a href="https://other.invalid">Details</a></li></ul><script>hidden()</script>]]></content></entry></feed>`, testTagPath, r.Host, testTagPath)
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
			name := base + target.format
			assetPath := testReleasePath + "/releases/download/" + version + "/" + name
			var checks, feeds, metadata, downloads atomic.Int32
			updater := newTestUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "HEAD " + testReleasePath + "/releases/latest":
					checks.Add(1)
					http.Redirect(w, r, testTagPath, http.StatusFound)
				case "GET " + testReleasePath + "/releases.atom":
					feeds.Add(1)
					writeTestAtom(w, r)
				case "GET " + testReleaseAPI + version:
					metadata.Add(1)
					if r.Header.Get("Accept") != "application/vnd.github+json" {
						t.Error("release metadata Accept header missing")
					}
					origin := "http://" + r.Host
					fmt.Fprintf(w, `{"tag_name":%q,"html_url":%q,"assets":[{"name":%q,"browser_download_url":%q,"digest":%q}]}`,
						version, origin+testTagPath, name, origin+assetPath, "sha256:"+hex.EncodeToString(sum[:]))
				case "GET " + assetPath:
					downloads.Add(1)
					if r.Header.Get("Accept") != "application/octet-stream" {
						t.Error("release asset Accept header missing")
					}
					if _, err := w.Write(archive.Bytes()); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected release request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			for _, refresh := range []bool{false, false, true} {
				status, err := updater.UpdateStatus(t.Context(), refresh)
				if err != nil || status.Error != "" || !status.Available || status.Latest != version || status.URL != updater.repoURL()+"/releases/tag/"+version ||
					status.Notes != "Fix A & B\nDetails" || status.PublishedAt.Format(time.RFC3339) != "2026-01-02T03:04:05Z" {
					t.Fatalf("release status = %+v, %v", status, err)
				}
			}
			if checks.Load() != 2 || feeds.Load() != 2 || metadata.Load() != 0 || downloads.Load() != 0 {
				t.Fatalf("requests on status/cache/refresh: page=%d feed=%d api=%d asset=%d", checks.Load(), feeds.Load(), metadata.Load(), downloads.Load())
			}
			downloaded, err := updater.download(t.Context(), updater.release, name)
			if err != nil || !bytes.Equal(downloaded, archive.Bytes()) {
				t.Fatalf("verified download failed: %v", err)
			}
			if metadata.Load() != 1 || downloads.Load() != 1 || updater.progress.Stage != "verify" || updater.progress.Downloaded != int64(archive.Len()) {
				t.Fatalf("verified download: api=%d asset=%d progress=%+v", metadata.Load(), downloads.Load(), updater.progress)
			}
			if target.binary != "" {
				executable, err := extractExecutable(downloaded, base+target.format, target.binary)
				if err != nil || !bytes.Equal(executable, payload) {
					t.Fatalf("release archive layout failed: %v", err)
				}
			}
		})
	}
}

func TestSelfUpdaterRejectsUnverifiedAssets(t *testing.T) {
	const tag = "v1.2.3"
	const name = "aistudio2api-v1.2.3-windows-amd64.zip"
	const assetPath = testReleasePath + "/releases/download/" + tag + "/" + name
	const payload = "release archive"
	sum := sha256.Sum256([]byte(payload))
	for _, tc := range []struct {
		name     string
		change   func(*githubRelease)
		download bool
	}{
		{"wrong tag", func(m *githubRelease) { m.TagName = "v1.2.4" }, false},
		{"foreign release page", func(m *githubRelease) { m.HTMLURL = "https://other.invalid" + testTagPath }, false},
		{"draft", func(m *githubRelease) { m.Draft = true }, false},
		{"prerelease", func(m *githubRelease) { m.Prerelease = true }, false},
		{"missing asset", func(m *githubRelease) { m.Assets = nil }, false},
		{"similar asset", func(m *githubRelease) { m.Assets[0].Name += ".sha256" }, false},
		{"duplicate asset", func(m *githubRelease) { m.Assets = append(m.Assets, m.Assets[0]) }, false},
		{"missing URL", func(m *githubRelease) { m.Assets[0].URL = "" }, false},
		{"foreign asset URL", func(m *githubRelease) { m.Assets[0].URL = "https://other.invalid" + assetPath }, false},
		{"wrong asset tag", func(m *githubRelease) { m.Assets[0].URL = strings.Replace(m.Assets[0].URL, tag, "v1.2.4", 1) }, false},
		{"missing digest", func(m *githubRelease) { m.Assets[0].Digest = "" }, false},
		{"wrong algorithm", func(m *githubRelease) { m.Assets[0].Digest = "sha1:" + strings.Repeat("0", 64) }, false},
		{"short digest", func(m *githubRelease) { m.Assets[0].Digest = "sha256:00" }, false},
		{"non-hex digest", func(m *githubRelease) { m.Assets[0].Digest = "sha256:" + strings.Repeat("z", 64) }, false},
		{"metadata unavailable", nil, false},
		{"malformed metadata", nil, false},
		{"metadata redirect", nil, false},
		{"asset redirect", nil, true},
		{"invalid bytes", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests, downloads atomic.Int32
			updater := newTestUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET " + testReleaseAPI + tag:
					requests.Add(1)
					switch tc.name {
					case "metadata unavailable":
						http.NotFound(w, r)
						return
					case "malformed metadata":
						io.WriteString(w, `{"tag_name":`)
						return
					case "metadata redirect":
						http.Redirect(w, r, "/repos/another/project/releases/tags/"+tag, http.StatusFound)
						return
					}
					origin := "http://" + r.Host
					metadata := githubRelease{TagName: tag, HTMLURL: origin + testTagPath, Assets: []githubAsset{
						{Name: name, URL: origin + assetPath, Digest: "sha256:" + hex.EncodeToString(sum[:])},
					}}
					if tc.change != nil {
						tc.change(&metadata)
					}
					if err := json.NewEncoder(w).Encode(metadata); err != nil {
						t.Error(err)
					}
				case "GET " + assetPath:
					downloads.Add(1)
					if tc.name == "asset redirect" {
						http.Redirect(w, r, "/another/project/releases/download/"+tag+"/"+name, http.StatusFound)
						return
					}
					io.WriteString(w, "changed archive")
				default:
					t.Errorf("followed a redirect or used an unexpected path: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			release := &githubRelease{TagName: tag, HTMLURL: updater.repoURL() + "/releases/tag/" + tag}
			archive, err := updater.download(t.Context(), release, name)
			var wantDownloads int32
			if tc.download {
				wantDownloads = 1
			}
			if err == nil || archive != nil || requests.Load() != 1 || downloads.Load() != wantDownloads {
				t.Fatalf("unverified release: error=%v bytes=%d api=%d downloads=%d", err, len(archive), requests.Load(), downloads.Load())
			}
			if tc.name == "invalid bytes" && (updater.progress.Stage != "verify" || !strings.Contains(err.Error(), "SHA-256")) {
				t.Fatalf("unexpected rejection at %q: %v", updater.progress.Stage, err)
			}
		})
	}
}

func TestSelfUpdaterRejectsForeignReleaseChecks(t *testing.T) {
	for _, location := range []string{
		"https://other.invalid" + testTagPath,
		"/Mag1cFall/AIStudio2API/releases/tag/v1.2.3",
		testTagPath + "/another", testTagPath + "?source=foreign", testTagPath + "#fragment",
		testTagPath + "%2Fpath", testTagPath + "-rc.1", testTagPath + "+build", "atom redirect",
	} {
		t.Run(location, func(t *testing.T) {
			u := newTestUpdater(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case testReleasePath + "/releases/latest":
					target := location
					if target == "atom redirect" {
						target = testTagPath
					}
					http.Redirect(w, r, target, http.StatusFound)
				case testReleasePath + "/releases.atom":
					if location != "atom redirect" {
						t.Error("invalid tag reached Atom")
					}
					http.Redirect(w, r, "/other/project/releases.atom", http.StatusFound)
				default:
					t.Errorf("followed foreign release: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			status, err := u.UpdateStatus(t.Context(), false)
			if err != nil || status.Error == "" || status.Available || status.Latest != "" || status.URL != u.repoURL()+"/releases/latest" {
				t.Fatalf("release provenance: %+v, %v", status, err)
			}
		})
	}
}

func TestSelfUpdaterConcurrentChecks(t *testing.T) {
	const readers = 8
	var checks atomic.Int32
	started, proceed := make(chan struct{}), make(chan struct{})
	u := newTestUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testReleasePath + "/releases/latest":
			if checks.Add(1) == 1 {
				close(started)
			}
			select {
			case <-proceed:
			case <-r.Context().Done():
				return
			}
			http.Redirect(w, r, testTagPath, http.StatusFound)
		case testReleasePath + "/releases.atom":
			writeTestAtom(w, r)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	results := make(chan error, readers)
	for range readers {
		go func() {
			status, err := u.UpdateStatus(t.Context(), false)
			if err == nil && (!status.Available || status.Error != "") {
				err = fmt.Errorf("incomplete shared check: %+v", status)
			}
			results <- err
		}()
	}
	<-started
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := u.UpdateStatus(canceled, true)
	close(proceed)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled waiter: %v", err)
	}
	for range readers {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if checks.Load() != 1 {
		t.Fatalf("concurrent readers made %d checks", checks.Load())
	}
}

func TestSelfUpdaterCacheAndRateLimit(t *testing.T) {
	var heads, pages, feeds atomic.Int32
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now := start
	u := newTestUpdater(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "HEAD " + testReleasePath + "/releases/latest":
			heads.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "GET " + testReleasePath + "/releases/latest":
			pages.Add(1)
			http.Redirect(w, r, testTagPath, http.StatusFound)
		case "GET " + testReleasePath + "/releases.atom":
			switch feeds.Add(1) {
			case 2:
				w.Header().Set("Retry-After", "90")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(start.Add(3*time.Hour).Unix(), 10))
				http.Error(w, "private rate-limit details", http.StatusTooManyRequests)
			case 3:
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(start.Add(2*time.Hour).Unix(), 10))
				http.Error(w, "private rate-limit details", http.StatusForbidden)
			case 4:
				http.Error(w, "private ordinary failure", http.StatusServiceUnavailable)
			default:
				writeTestAtom(w, r)
			}
		default:
			t.Errorf("unexpected check path: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	u.now = func() time.Time { return now }
	for _, step := range []struct {
		age      time.Duration
		count    int32
		kind     string
		retryAge time.Duration
	}{
		{0, 1, "", 0},
		{time.Hour - time.Second, 1, "", 0},
		{time.Hour, 2, "rate_limited", time.Hour + 90*time.Second},
		{time.Hour + 89*time.Second, 2, "rate_limited", time.Hour + 90*time.Second},
		{time.Hour + 90*time.Second, 3, "rate_limited", 2 * time.Hour},
		{2*time.Hour - time.Second, 3, "rate_limited", 2 * time.Hour},
		{2 * time.Hour, 4, "", 2*time.Hour + 5*time.Minute},
		{2*time.Hour + 5*time.Minute, 5, "", 0},
	} {
		now = start.Add(step.age)
		status, err := u.UpdateStatus(t.Context(), false)
		var retryAt time.Time
		if step.retryAge != 0 {
			retryAt = start.Add(step.retryAge)
		}
		if err != nil || status.Latest != "v1.2.3" || status.URL != u.repoURL()+"/releases/tag/v1.2.3" || !status.Available ||
			status.Notes != "Fix A & B\nDetails" || (status.Error != "") != (step.retryAge != 0) || strings.Contains(status.Error, "private") ||
			status.ErrorKind != step.kind || !status.RetryAt.Equal(retryAt) || heads.Load() != step.count || pages.Load() != step.count || feeds.Load() != step.count {
			t.Fatalf("at %v: %+v, %v; heads=%d pages=%d feeds=%d", step.age, status, err, heads.Load(), pages.Load(), feeds.Load())
		}
	}
}
