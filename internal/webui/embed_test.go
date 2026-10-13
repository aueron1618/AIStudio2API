package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildAPIReservedPaths(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/build/v1", "/build/v1/", "/build/v1/models", "/build/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			if !reservedPath(path) {
				t.Fatalf("API path is not reserved: %s", path)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("API path fell back to SPA: status=%d", w.Code)
			}
		})
	}
	for _, path := range []string{"/build", "/build/v10", "/builder/v1"} {
		if reservedPath(path) {
			t.Errorf("non-API path was reserved: %s", path)
		}
	}
}
