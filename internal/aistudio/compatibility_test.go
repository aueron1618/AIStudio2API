package aistudio

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestQuotaCooldownForError(t *testing.T) {
	now := time.Date(2026, 10, 19, 18, 17, 20, 0, time.UTC)
	dailyDelay := time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC).Sub(now)
	window := strconv.FormatInt(now.Add(-20*time.Second).Unix(), 10)
	for _, test := range []struct {
		name       string
		message    string
		metadata   map[string]string
		retryDelay time.Duration
		kind       string
		delay      time.Duration
		global     bool
		escalate   bool
	}{
		{name: "generic", message: "Quota exceeded", kind: "短期限额", delay: time.Minute, escalate: true},
		{name: "plan", message: "You have reached your usage limit", kind: "套餐限额", delay: time.Minute, escalate: true},
		{name: "plan with retry delay", message: "You have reached your usage limit", retryDelay: 17 * time.Second, kind: "短期限额", delay: 17 * time.Second},
		{
			name: "minute before plan and daily metadata", message: "You have reached your usage limit; try again tomorrow",
			metadata: map[string]string{"quota_unit": "1/min/{project}/{model}", "quota_limit": "RequestsPerDay", "window_start_time": window},
			kind:     "分钟限额", delay: 40 * time.Second,
		},
		{
			name: "daily unit before minute limit", message: "You have reached your usage limit",
			metadata: map[string]string{"quota_unit": "1/day/{project}/{model}", "quota_limit": "RequestsPerMinute"},
			kind:     "每日限额", delay: dailyDelay,
		},
		{
			name: "limit before metric", message: "Quota exceeded",
			metadata: map[string]string{"quota_limit": "RequestsPerMinute", "quota_metric": "requests_per_day"},
			kind:     "分钟限额", delay: time.Minute,
		},
		{name: "daily message before plan", message: "You have reached your usage limit; try again tomorrow", kind: "每日限额", delay: dailyDelay},
		{name: "minute message before plan", message: "You have reached your usage limit per minute", kind: "分钟限额", delay: time.Minute},
		{
			name: "retry delay before minute window", message: "You have reached your usage limit",
			metadata:   map[string]string{"quota_unit": "1/min/{project}/{model}", "window_start_time": window},
			retryDelay: 17 * time.Second, kind: "分钟限额", delay: 17 * time.Second,
		},
		{
			name: "retry delay before daily global", message: "You have reached your usage limit",
			metadata:   map[string]string{"quota_metric": "requests_per_day_global"},
			retryDelay: 17 * time.Second, kind: "每日限额", delay: 17 * time.Second, global: true,
		},
		{
			name: "project user global", message: "Quota exceeded",
			metadata: map[string]string{"quota_limit": "RequestsPerMinutePerProjectPerUser"},
			kind:     "分钟限额", delay: time.Minute, global: true,
		},
		{
			name: "per model excludes global", message: "Quota exceeded",
			metadata: map[string]string{"quota_metric": "requests_global_per_model"},
			kind:     "短期限额", delay: time.Minute, escalate: true,
		},
		{
			name: "permodel excludes global", message: "Quota exceeded",
			metadata: map[string]string{"quota_limit": "RequestsPerProjectPerUserPerModel"},
			kind:     "短期限额", delay: time.Minute, escalate: true,
		},
		{
			name: "model unit excludes global", message: "Quota exceeded",
			metadata: map[string]string{"quota_metric": "requests_global", "quota_unit": "1/min/{project}/{model}"},
			kind:     "分钟限额", delay: time.Minute,
		},
		{name: "all limits zero", message: "Quota exceeded per minute, limit:0\nQuota exceeded per minute, limit: 0", kind: "无配额", delay: time.Minute},
		{name: "zero before plan", message: "You have reached your usage limit, limit: 0", kind: "无配额", delay: time.Minute},
		{name: "mixed limits", message: "Quota exceeded per minute, limit: 0\nQuota exceeded per minute, limit: 10", kind: "分钟限额", delay: time.Minute},
		{name: "zero keeps retry delay", message: "Quota exceeded per day, limit: 0", retryDelay: 17 * time.Second, kind: "无配额", delay: 17 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", &RPCError{
				StatusCode: http.StatusTooManyRequests, Message: test.message, Metadata: test.metadata, RetryDelay: test.retryDelay,
			})
			cooldown, ok := QuotaCooldownForError(err, now)
			if !ok || cooldown.Kind != test.kind || cooldown.Global != test.global || cooldown.Escalate != test.escalate ||
				!cooldown.Until.Equal(now.Add(test.delay)) || cooldown.Reason != test.kind+": "+err.Error() {
				t.Fatalf("cooldown=%+v ok=%v want=%+v", cooldown, ok, test)
			}
		})
	}
	for _, err := range []error{nil, errors.New("quota exceeded"), &RPCError{StatusCode: http.StatusForbidden, Message: "You have reached your usage limit"}} {
		if cooldown, ok := QuotaCooldownForError(err, now); ok {
			t.Fatalf("non-quota error=%v cooldown=%+v", err, cooldown)
		}
	}
}

func TestQuotaRetryDelaySources(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name  string
		body  string
		delay time.Duration
	}{
		{
			name:  "RetryInfo before message and header",
			body:  `[8,"You have reached your usage limit. Please retry in 30s.",[["type.googleapis.com/google.rpc.RetryInfo",[["7",500000000]]]]]`,
			delay: 7500 * time.Millisecond,
		},
		{name: "message before header", body: `[8,"You have reached your usage limit. Please retry in 2.5s."]`, delay: 2500 * time.Millisecond},
		{name: "header", body: `[8,"You have reached your usage limit"]`, delay: 90 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateRPCResponse("GenerateContent", &RPCResponse{
				StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"90"}},
				Body: io.NopCloser(strings.NewReader(test.body)),
			})
			var rpcError *RPCError
			if !errors.As(err, &rpcError) || rpcError.RetryDelay != test.delay {
				t.Fatalf("err=%v want retry delay=%s", err, test.delay)
			}
			cooldown, ok := QuotaCooldownForError(err, now)
			if !ok || cooldown.Escalate || !cooldown.Until.Equal(now.Add(test.delay)) {
				t.Fatalf("explicit delay ignored: cooldown=%+v ok=%v", cooldown, ok)
			}
		})
	}
}

// TestAttachYouTubeMediaPreservesText 验证普通文本与非空白字符在附加 YouTube 媒体时不被截断为空
func TestAttachYouTubeMediaPreservesText(t *testing.T) {
	content := Content{
		Role: RoleUser,
		Parts: []Part{
			{Text: "  normal text with space  "},
			{Text: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
	}
	attached := attachYouTubeMedia(content)
	if len(attached.Parts) != 2 {
		t.Fatalf("expected 2 parts (1 text + 1 external media), got %d: %#v", len(attached.Parts), attached.Parts)
	}
	if attached.Parts[0].Text != "  normal text with space  " {
		t.Fatalf("unexpected text: %q", attached.Parts[0].Text)
	}
	if attached.Parts[1].ExternalMedia == nil {
		t.Fatalf("expected external media part")
	}
	for _, text := range []string{"  https://example.com/article  ", "  https://youtube.com/playlist?list=abc  "} {
		got := attachYouTubeMedia(Content{Role: RoleUser, Parts: []Part{{Text: text}}})
		if len(got.Parts) != 1 || got.Parts[0].Text != text {
			t.Errorf("ordinary text changed: %#v", got)
		}
	}
}

// TestAttachYouTubeMediaVideoIDs 验证文本中只有 11 位视频 ID 的链接转为视频，链接在汉字与全角标点处结束，其余链接保留为文本
func TestAttachYouTubeMediaVideoIDs(t *testing.T) {
	for _, test := range []struct{ text, media, rest string }{
		{"用一句话总结https://youtu.be/jNQXAC9IVRw这个视频", "https://www.youtube.com/watch?v=jNQXAC9IVRw", "用一句话总结这个视频"},
		{"看看 https://www.youtube.com/watch?v=jNQXAC9IVRw，讲了什么", "https://www.youtube.com/watch?v=jNQXAC9IVRw", "看看 ，讲了什么"},
		{"Embed players with `https://www.youtube.com/embed/${videoId}`.", "", "Embed players with `https://www.youtube.com/embed/${videoId}`."},
		{"Template: https://www.youtube.com/watch?v=VIDEO_ID", "", "Template: https://www.youtube.com/watch?v=VIDEO_ID"},
		{"See https://youtu.be/abc", "", "See https://youtu.be/abc"},
	} {
		got := attachYouTubeMedia(Content{Role: RoleUser, Parts: []Part{{Text: test.text}}})
		var media, rest string
		for _, part := range got.Parts {
			if part.ExternalMedia != nil {
				media = part.ExternalMedia.URL
			}
			rest += part.Text
		}
		if media != test.media || rest != test.rest {
			t.Errorf("%q: media=%q text=%q", test.text, media, rest)
		}
	}
}
