package aistudio

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestPageDocumentResourcesAndIcons(t *testing.T) {
	document := parsePageDocument([]byte(`<base href="https://static.example.test/assets/"><link rel="stylesheet" href="site.css"><link rel="preload" as="script" href="app.js"><script src="app.js"></script><script src="boot.js"></script><script async src="ignored.js"></script><noscript><link rel="stylesheet" href="fallback.css"></noscript><link rel="icon" href="small.png" sizes="16x16"><link rel="icon" href="large.png" sizes="128x128">`), "https://aistudio.google.com/prompts/new_chat")
	if len(document.resources) != 3 || document.resources[0] != (pageResource{url: "https://static.example.test/assets/site.css", kind: pageStylesheet}) || document.resources[1].kind != pagePreloadScript || document.resources[2].url != "https://static.example.test/assets/boot.js" {
		t.Fatalf("document resources = %+v", document.resources)
	}
	rich, tab := selectPageIcons(document.icons, 16)
	if rich != "https://static.example.test/assets/large.png" || tab != "https://static.example.test/assets/small.png" {
		t.Fatalf("page icons = %q, %q", rich, tab)
	}
	headers := subresourceHeaders("https://static.example.test/app.js", "*/*", "script", "u=1")
	if headers.Get("Sec-Fetch-Site") != "cross-site" || headers.Get("Sec-Fetch-Storage-Access") != "none" || headers.Get("Sec-Fetch-Dest") != "script" || headers.Get("Priority") != "u=1" {
		t.Fatalf("resource headers = %v", headers)
	}
}

func TestPageCacheRevalidationAndNoStore(t *testing.T) {
	cache, err := loadPageCache(filepath.Join(t.TempDir(), "storage-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Cache-Control": {"max-age=60"}, "Date": {now.Format(http.TimeFormat)}, "Age": {"10"}, "Etag": {`"old"`},
	}}
	if err := cache.store("https://fixture.test/app.js", response, now); err != nil {
		t.Fatal(err)
	}
	if _, fresh := cache.lookup("https://fixture.test/app.js", now.Add(49*time.Second)); !fresh {
		t.Fatal("fresh cached resource was not reused")
	}
	if _, fresh := cache.lookup("https://fixture.test/app.js", now.Add(50*time.Second)); fresh {
		t.Fatal("Age was not included in cache freshness")
	}
	response.StatusCode = http.StatusNotModified
	response.Header = http.Header{"Etag": {`"new"`}, "Date": {now.Add(time.Minute).Format(http.TimeFormat)}}
	if err := cache.store("https://fixture.test/app.js", response, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if entry, fresh := cache.lookup("https://fixture.test/app.js", now.Add(61*time.Second)); !fresh || entry.ETag != `"new"` {
		t.Fatalf("304 did not update validators/freshness: %+v, %v", entry, fresh)
	}
	response.Header.Set("Cache-Control", "no-store")
	if err := cache.store("https://fixture.test/app.js", response, now); err != nil {
		t.Fatal(err)
	}
	if _, present := cache.entries["https://fixture.test/app.js"]; present {
		t.Fatal("no-store resource was retained")
	}
	for _, value := range []string{"no-cache,max-age=60", "no-store,max-age=60", "max-age=-1", "max-age=9223372036854775807"} {
		if _, fresh := cacheMaxAge(value); fresh {
			t.Fatalf("unsafe cache lifetime accepted: %q", value)
		}
	}
}
