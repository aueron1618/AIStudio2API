package aistudio

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

const antiTruncationBufferLimit = 16 << 20

type antiTruncationTransport struct {
	name          string
	pending       []Event
	bufferedBytes int
	calls         int
	synthetic     bool
	realTools     bool
	finished      bool
	forward       func(Event) error
	emitErr       error
}

// prepareAntiTruncation runs after the genuine tool contract has been prepared.
func prepareAntiTruncation(request GenerateRequest) (GenerateRequest, *antiTruncationTransport, error) {
	var nonce [12]byte
	var name string
	for {
		if _, err := rand.Read(nonce[:]); err != nil {
			return request, nil, fmt.Errorf("anti-truncation tool nonce: %w", err)
		}
		name = "agw_emit_" + hex.EncodeToString(nonce[:])
		if !slices.ContainsFunc(request.Tools.Functions, func(tool FunctionDeclaration) bool { return tool.Name == name }) {
			break
		}
	}
	config := request.Tools.ToolConfig
	if config.Mode == "none" {
		request.Tools = Tools{}
	}
	request.Tools.Functions = append(slices.Clone(request.Tools.Functions), FunctionDeclaration{
		Name:        name,
		Description: "Use this transport tool exactly once for the final user-visible answer. Put the complete answer in content. Never wrap genuine tool calls in it.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`),
	})
	request.Tools.ToolConfig.Mode = "auto"
	request.Tools.ToolConfig.AllowedFunctionNames = nil
	request.Tools.ToolConfig.ParallelCalls = nil
	instruction := fmt.Sprintf("Always use tool `%s` to output your final reply in its `content` argument. Do not output anything outside this tool call.", name)
	if config.Mode == "required" || config.ParallelCalls != nil && !*config.ParallelCalls {
		instruction = "The final-answer transport tool does not fulfill a genuine tool-use requirement and is excluded from any limit on genuine tool calls.\n" + instruction
	}
	if len(request.Contents) > 0 && request.Contents[len(request.Contents)-1].Role == RoleAssistant {
		request.Contents = append(slices.Clone(request.Contents), Content{Role: RoleUser, Parts: []Part{{Text: instruction}}})
		request.antiTruncationControlTail = true
	} else {
		if len(request.System) > 0 {
			instruction = "\n" + instruction
		}
		request.System = appendSystem(request.System, instruction)
	}
	return request, &antiTruncationTransport{name: name}, nil
}

// emitter must precede stop matching, output accounting and genuine tool checks.
func (transport *antiTruncationTransport) emitter(forward func(Event) error) func(Event) error {
	transport.forward = forward
	return func(event Event) (err error) {
		if transport.emitErr != nil {
			return transport.emitErr
		}
		defer func() {
			if err != nil {
				transport.emitErr = err
				transport.pending = nil
				transport.bufferedBytes = 0
			}
		}()
		if event.Kind == EventToolCall && event.FunctionCall != nil && event.FunctionCall.Name == transport.name {
			transport.calls++
			transport.synthetic = true
			if err := transport.flush(true); err != nil {
				return err
			}
			text, err := antiTruncationContent(event.FunctionCall.Arguments)
			if err != nil {
				return err
			}
			event.Kind = EventText
			event.FunctionCall = nil
			event.Text = text
			event.Thought, event.ThoughtSignature = false, ""
			return forward(event)
		}
		switch event.Kind {
		case EventText:
			if transport.synthetic {
				return nil
			}
		case EventToolCall, EventServerToolCall, EventServerToolResponse, EventExecutableCode, EventCodeExecutionResult:
			transport.realTools = true
		case EventFinish:
			transport.finished = true
			if err := transport.flush(transport.synthetic); err != nil {
				return err
			}
			if transport.synthetic && !transport.realTools && strings.EqualFold(event.FinishReason, "tool_calls") {
				event.FinishReason = "STOP"
			}
			return forward(event)
		case EventError:
			if err := transport.flush(true); err != nil {
				return err
			}
			if event.Err != nil {
				return event.Err
			}
			return fmt.Errorf("anti-truncation upstream error without a cause")
		}
		if event.Kind == EventText || len(transport.pending) > 0 {
			buffered := struct {
				Event
				UsageDetails, FrameUsageDetails *UsageDetails
			}{Event: event}
			if event.Usage != nil {
				buffered.UsageDetails = event.Usage.Details
			}
			if event.FrameUsage != nil {
				buffered.FrameUsageDetails = event.FrameUsage.Details
			}
			encoded, err := json.Marshal(buffered)
			if err != nil {
				return fmt.Errorf("anti-truncation event buffer: %w", err)
			}
			// Include per-event overhead so empty events cannot grow the queue indefinitely.
			size := len(encoded) + 1024
			if size > antiTruncationBufferLimit-transport.bufferedBytes {
				return fmt.Errorf("anti-truncation event buffer exceeds %d bytes", antiTruncationBufferLimit)
			}
			transport.pending = append(transport.pending, event)
			transport.bufferedBytes += size
			return nil
		}
		return forward(event)
	}
}

func (transport *antiTruncationTransport) flush(discardText bool) error {
	pending := transport.pending
	transport.pending, transport.bufferedBytes = nil, 0
	for _, event := range pending {
		if discardText && event.Kind == EventText {
			continue
		}
		if err := transport.forward(event); err != nil {
			return err
		}
	}
	return nil
}

// complete never releases fallback text for an interrupted or unfinished stream.
func (transport *antiTruncationTransport) complete(err error) error {
	if transport.emitErr != nil {
		if err != nil {
			return err
		}
		return transport.emitErr
	}
	if err == nil && !transport.finished {
		err = fmt.Errorf("anti-truncation stream ended without a finish event: %w", io.ErrUnexpectedEOF)
	}
	if err != nil {
		if flushErr := transport.flush(true); flushErr != nil {
			return flushErr
		}
	}
	return err
}

// antiTruncationContent repairs only JSON structure, never guesses a nested field
// or closes an unfinished string containing a potentially truncated answer.
func antiTruncationContent(raw json.RawMessage) (string, error) {
	if len(raw) > antiTruncationBufferLimit {
		return "", fmt.Errorf("anti-truncation arguments exceed %d bytes", antiTruncationBufferLimit)
	}
	raw = bytes.TrimSpace(raw)
	if bytes.HasPrefix(raw, []byte("```")) {
		line := bytes.IndexByte(raw, '\n')
		if line < 0 {
			return "", fmt.Errorf("anti-truncation arguments have an incomplete code fence")
		}
		fence := strings.TrimSpace(string(raw[:line]))
		if fence != "```" && !strings.EqualFold(fence, "```json") {
			return "", fmt.Errorf("anti-truncation arguments have an unsupported code fence")
		}
		raw = bytes.TrimSpace(raw[line+1:])
		if bytes.HasSuffix(raw, []byte("```")) {
			raw = bytes.TrimSpace(raw[:len(raw)-3])
		}
	}
	if len(raw) == 0 || raw[0] != '{' {
		return "", fmt.Errorf("anti-truncation arguments must be an object with string content")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		repaired := make([]byte, 0, len(raw))
		var closers []byte
		inString, escaped := false, false
		for _, b := range raw {
			if inString {
				switch {
				case escaped:
					escaped = false
				case b == '\\':
					escaped = true
				case b == '"':
					inString = false
				case b == '\n':
					repaired = append(repaired, '\\', 'n')
					continue
				case b == '\r':
					repaired = append(repaired, '\\', 'r')
					continue
				case b == '\t':
					repaired = append(repaired, '\\', 't')
					continue
				}
			} else {
				switch b {
				case '"':
					inString = true
				case '{':
					closers = append(closers, '}')
				case '[':
					closers = append(closers, ']')
				case '}', ']':
					if len(closers) == 0 || closers[len(closers)-1] != b {
						return "", fmt.Errorf("anti-truncation arguments have mismatched delimiters")
					}
					closers = closers[:len(closers)-1]
				}
			}
			repaired = append(repaired, b)
		}
		if inString {
			return "", fmt.Errorf("anti-truncation arguments contain an unfinished string: %w", io.ErrUnexpectedEOF)
		}
		for index := len(closers) - 1; index >= 0; index-- {
			repaired = append(repaired, closers[index])
		}
		if err := json.Unmarshal(repaired, &object); err != nil {
			return "", fmt.Errorf("anti-truncation arguments are invalid JSON: %w", err)
		}
	}
	value := bytes.TrimSpace(object["content"])
	if len(value) == 0 || value[0] != '"' {
		return "", fmt.Errorf("anti-truncation arguments must have top-level string content")
	}
	var content string
	if err := json.Unmarshal(value, &content); err != nil {
		return "", fmt.Errorf("anti-truncation content is invalid: %w", err)
	}
	return content, nil
}
