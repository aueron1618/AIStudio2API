package aistudio

import (
	"net/http"
	"testing"
)

func TestBrowserCORSContentTypeClassification(t *testing.T) {
	for _, test := range []struct {
		values []string
		unsafe bool
	}{
		{nil, true},
		{[]string{"text/plain;charset=UTF-8"}, false},
		{[]string{"application/json"}, true},
	} {
		got := corsRequestHeaders(http.Header{"Content-Type": test.values})
		if (len(got) == 1 && got[0] == "content-type") != test.unsafe {
			t.Fatalf("Content-Type=%v unsafe=%v", test.values, got)
		}
	}
}
