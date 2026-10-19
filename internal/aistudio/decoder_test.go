package aistudio

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type streamErrorReader struct{ err error }

func (reader streamErrorReader) Read([]byte) (int, error) { return 0, reader.err }

func TestDecodeGenerateItemsReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
		frames int
	}{
		{"before-root", "", 0},
		{"before-field", "[", 0},
		{"before-item", "[[", 0},
		{"partial-item", "[[[],[", 1},
		{"after-item", "[[[]", 1},
		{"before-trailer", "[[[]],", 1},
		{"after-root", "[[[]]]", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := 0
			err := decodeGenerateItems(io.MultiReader(strings.NewReader(tc.prefix), streamErrorReader{io.ErrUnexpectedEOF}), func(json.RawMessage) error {
				frames++
				return nil
			})
			var interrupted *StreamInterruptedError
			if !errors.As(err, &interrupted) || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("expected preserved read failure, got %v", err)
			}
			if interrupted.Method != "GenerateContent" || interrupted.Frames != tc.frames || frames != tc.frames {
				t.Fatalf("interrupted=%+v consumed=%d want=%d", interrupted, frames, tc.frames)
			}
			var protocol *ProtocolEvidenceError
			if errors.As(err, &protocol) {
				t.Fatal("network interruption was also classified as a protocol error")
			}
		})
	}
}

func TestDecodeGenerateItemsKeepsProtocolErrors(t *testing.T) {
	for _, data := range []string{"", "[]", "{}", "[[?,[]]]", "[[[],", "[[[]]]{}"} {
		t.Run(data, func(t *testing.T) {
			err := decodeGenerateItems(strings.NewReader(data), func(json.RawMessage) error { return nil })
			var protocol *ProtocolEvidenceError
			var interrupted *StreamInterruptedError
			if !errors.As(err, &protocol) || errors.As(err, &interrupted) {
				t.Fatalf("expected protocol error for %q, got %v", data, err)
			}
		})
	}
	feedback := &PromptFeedbackError{Reason: "safety"}
	if err := decodeGenerateItems(strings.NewReader("[[[]]]"), func(json.RawMessage) error { return feedback }); err != feedback {
		t.Fatalf("consumer error changed: %v", err)
	}
}

func TestDecodeBuildStreamReadFailure(t *testing.T) {
	item, err := json.Marshal([]string{`{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"STOP"}]}`})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		prefix string
		frames int
	}{
		{"before-root", "", 0},
		{"before-item", "[[", 0},
		{"partial-item", "[[" + string(item[:len(item)/2]), 0},
		{"after-finish", "[[" + string(item), 1},
		{"before-root-end", "[[" + string(item) + "]", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []Event
			err := DecodeBuildStream(io.MultiReader(strings.NewReader(tc.prefix), streamErrorReader{io.ErrUnexpectedEOF}), &BuildStreamDecoder{}, func(event Event) error {
				events = append(events, event)
				return nil
			})
			var interrupted *StreamInterruptedError
			if !errors.As(err, &interrupted) || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("expected preserved read failure, got %v", err)
			}
			if interrupted.Method != buildProxyStreamedMethod || interrupted.Frames != tc.frames {
				t.Fatalf("interrupted=%+v want frames=%d", interrupted, tc.frames)
			}
			if tc.frames > 0 && (len(events) != 2 || events[0].Text != "partial" || events[1].Kind != EventFinish) {
				t.Fatalf("events=%+v", events)
			}
		})
	}
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{"[[" + string(item) + "]]", true},
		{"[[" + string(item) + "],[0]]", true},
		{"[[?]]", false},
		{"[[" + string(item), false},
		{"[[" + string(item) + "]", false},
		{"[[" + string(item) + "],[0]", false},
	} {
		err := DecodeBuildStream(strings.NewReader(tc.data), &BuildStreamDecoder{}, func(Event) error { return nil })
		if tc.ok && err != nil {
			t.Fatal(err)
		}
		if !tc.ok {
			var protocol *ProtocolEvidenceError
			if !errors.As(err, &protocol) {
				t.Fatalf("expected protocol error for %q, got %v", tc.data, err)
			}
		}
	}
}

func TestDecodeBuildFinishReason(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"STOP", "stop"},
		{"FINISH_REASON_MAX_TOKENS", "max_tokens"},
		{"SAFETY", "safety"},
		{"MALFORMED_FUNCTION_CALL", "malformed_function_call"},
		{"MISSING_THOUGHT_SIGNATURE", "missing_thought_signature"},
		{"FINISH_REASON_NEW_POLICY", "provider_new_policy"},
	} {
		events, err := (&BuildStreamDecoder{}).Decode([]byte(`{"candidates":[{"finishReason":"` + tc.value + `"}]}`))
		if err != nil || len(events) != 1 || events[0].Kind != EventFinish || events[0].FinishReason != tc.want {
			t.Fatalf("finish=%s events=%+v error=%v", tc.value, events, err)
		}
	}
}

func TestStreamDecodersRemainIncremental(t *testing.T) {
	item, err := json.Marshal([]string{`{"candidates":[{"content":{"parts":[{"text":"first"}]},"finishReason":"STOP"}]}`})
	if err != nil {
		t.Fatal(err)
	}
	for _, build := range []bool{false, true} {
		name, prefix := "playground", "[[[]"
		if build {
			name, prefix = "build", "[["+string(item)
		}
		t.Run(name, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			emitted := make(chan struct{}, 1)
			emit := func() {
				select {
				case emitted <- struct{}{}:
				default:
				}
			}
			done := make(chan error, 1)
			go func() {
				defer reader.Close()
				if build {
					done <- DecodeBuildStream(reader, &BuildStreamDecoder{}, func(Event) error { emit(); return nil })
				} else {
					done <- decodeGenerateItems(reader, func(json.RawMessage) error { emit(); return nil })
				}
			}()
			if _, err := io.WriteString(writer, prefix); err != nil {
				t.Fatal(err)
			}
			select {
			case <-emitted:
			case <-time.After(5 * time.Second):
				t.Fatal("decoder waited for the whole response before emitting")
			}
			if _, err := io.WriteString(writer, "]]"); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("decoder did not finish")
			}
		})
	}
}
