package app

import "testing"

func TestRequestProtocolChannelPrefixes(t *testing.T) {
	for _, tc := range []struct{ path, protocol string }{
		{"/v1/chat/completions", "openai-chat"},
		{"/v1/responses", "openai-responses"},
		{"/v1/responses/resp_123/input_items", "openai-responses"},
		{"/v1/messages", "anthropic"},
		{"/v1/messages/count_tokens", "anthropic"},
		{"/v1/interactions", "interactions"},
		{"/v1/files", "files"},
		{"/v1/images/generations", "images"},
		{"/v1/audio/speech", "audio"},
		{"/v1/videos", "videos"},
		{"/v1/models", "other"},
	} {
		for _, prefix := range []string{"", "/build"} {
			t.Run(prefix+tc.path, func(t *testing.T) {
				if got := requestProtocol(prefix + tc.path); got != tc.protocol {
					t.Fatalf("protocol=%q want=%q", got, tc.protocol)
				}
			})
		}
	}
	for _, tc := range []struct{ path, protocol string }{
		{"/v1beta/models/gemini:generateContent", "gemini"},
		{"/build/v10/chat/completions", "other"},
		{"/builder/v1/chat/completions", "other"},
	} {
		if got := requestProtocol(tc.path); got != tc.protocol {
			t.Errorf("path=%s protocol=%q want=%q", tc.path, got, tc.protocol)
		}
	}
}
