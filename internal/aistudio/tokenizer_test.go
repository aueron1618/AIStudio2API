package aistudio

import (
	"strings"
	"testing"
	"time"
)

func TestSegmentedTokenizerMatchesWholeEncodingAndIdleRebuild(t *testing.T) {
	values := []string{
		"", "one two three", "  leading and trailing  ", "<b> </b>",
		"literal \u2581 spaces \u2581  next", "tabs\tand\nnewlines \r\nremain",
		"\u4e2d\u6587 \u7b80\u4f53 \u4e2d\u6587", "emoji \U0001f600 and punctuation > </",
		strings.Repeat("repeated long text <b> </b> \u2581 spaces ", 64),
	}
	processor := acquireGeminiTokenizer()
	want := make([]int64, len(values))
	for index, value := range values {
		if value != "" {
			want[index] = int64(len(processor.Encode(value)))
		}
		if got := localTextTokens(value); got != want[index] {
			t.Errorf("segmented count for %q = %d, want %d", value, got, want[index])
		}
	}
	if ReleaseIdleTokenizer(time.Now().Add(time.Second)) {
		t.Error("idle release dropped a tokenizer still in use")
	}
	releaseGeminiTokenizer()
	if !ReleaseIdleTokenizer(time.Now().Add(time.Second)) {
		t.Fatal("idle tokenizer was not released")
	}
	tokenCountCache.Lock()
	clear(tokenCountCache.entries)
	tokenCountCache.order.Init()
	tokenCountCache.Unlock()
	tokenSegments.Lock()
	clear(tokenSegments.current)
	tokenSegments.previous = nil
	tokenSegments.Unlock()
	for index, value := range values {
		if got := localTextTokens(value); got != want[index] {
			t.Errorf("rebuilt count for %q = %d, want %d", value, got, want[index])
		}
	}
	localTokenizer.mu.Lock()
	users := localTokenizer.users
	localTokenizer.mu.Unlock()
	if users != 0 {
		t.Fatalf("tokenizer retained %d active users after counting", users)
	}
}
