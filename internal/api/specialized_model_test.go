package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/gorilla/websocket"
)

type specializedModelCall struct {
	kind, model string
	channel     aistudio.Channel
	generated   aistudio.GenerateRequest
	bidi        aistudio.BidiRequest
}

type specializedModelService struct {
	requestErrorTestService
	mu    sync.Mutex
	calls []specializedModelCall
	image []byte
}

func (service *specializedModelService) record(ctx context.Context, call specializedModelCall) {
	service.mu.Lock()
	defer service.mu.Unlock()
	call.channel = aistudio.ChannelFromContext(ctx)
	service.calls = append(service.calls, call)
}

func (service *specializedModelService) snapshot() []specializedModelCall {
	service.mu.Lock()
	defer service.mu.Unlock()
	return slices.Clone(service.calls)
}

func (*specializedModelService) Models(context.Context) ([]aistudio.Model, error) {
	return []aistudio.Model{
		{ID: "test", Methods: []string{"generateContent", "embedContent", "predict", "createDecision"}, Capabilities: map[string]bool{"image_route": true}},
		{ID: "audio", Methods: []string{"transcribe"}, Capabilities: map[string]bool{"transcription_output": true}},
	}, nil
}

func (service *specializedModelService) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	service.record(ctx, specializedModelCall{kind: "generate", model: request.Model, generated: request})
	event := aistudio.Event{Kind: aistudio.EventText, Text: "answer"}
	switch {
	case slices.Contains(request.Config.ResponseModalities, aistudio.ResponseModalityImage):
		event = aistudio.Event{Kind: aistudio.EventMedia, InlineData: &aistudio.Blob{MIME: "image/png", Data: service.image}}
	case slices.Contains(request.Config.ResponseModalities, aistudio.ResponseModalityAudio):
		event = aistudio.Event{Kind: aistudio.EventMedia, InlineData: &aistudio.Blob{MIME: "audio/l16;rate=24000;channels=1", Data: make([]byte, 960)}}
	case request.Config.ResponseMIMEType == "application/json":
		event.Text = `{"segments":["translated"]}`
	}
	return testEventStream(event, aistudio.Event{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{InputTokens: 7, OutputTokens: 1, TotalTokens: 8}},
		aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop", ProviderModel: request.Model}), nil
}

func (service *specializedModelService) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	service.record(ctx, specializedModelCall{kind: "count", model: request.Model})
	return aistudio.TokenCount{InputTokens: 7}, nil
}

func (service *specializedModelService) Embed(ctx context.Context, request aistudio.EmbeddingRequest) (aistudio.EmbeddingResult, error) {
	service.record(ctx, specializedModelCall{kind: "embed", model: request.Model})
	vectors := make([][]float64, len(request.Requests))
	for index := range vectors {
		vectors[index] = []float64{1, 2}
	}
	return aistudio.EmbeddingResult{Embeddings: vectors, TokenCount: 7}, nil
}

func (service *specializedModelService) GenerateImage(ctx context.Context, request aistudio.ImageRequest) ([]aistudio.GeneratedImage, error) {
	service.record(ctx, specializedModelCall{kind: "image", model: request.Model})
	return []aistudio.GeneratedImage{{MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(service.image)}}, nil
}

func (service *specializedModelService) CreateDecision(ctx context.Context, request aistudio.DecisionRequest) (aistudio.DecisionResult, error) {
	service.record(ctx, specializedModelCall{kind: "decision", model: request.Model})
	return aistudio.DecisionResult{InputTokens: 7, TotalTokens: 7}, nil
}

func (service *specializedModelService) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	service.record(ctx, specializedModelCall{kind: "video", model: request.Model})
	return aistudio.VideoOperation{ID: "video-fixture", Model: request.Model, Prompt: request.Prompt, Seconds: "4", Size: "1280x720", CreatedAt: time.Unix(1, 0)}, nil
}

func (service *specializedModelService) Transcribe(ctx context.Context, request aistudio.TranscriptionRequest) (aistudio.TranscriptionResult, error) {
	service.record(ctx, specializedModelCall{kind: "transcribe", model: request.Model})
	if request.Emit != nil {
		if err := request.Emit(aistudio.Event{Kind: aistudio.EventText, Text: "source"}); err != nil {
			return aistudio.TranscriptionResult{}, err
		}
	}
	return aistudio.TranscriptionResult{Text: "source"}, nil
}

func (service *specializedModelService) OpenBidi(ctx context.Context, request aistudio.BidiRequest) (*aistudio.BidiSession, error) {
	service.record(ctx, specializedModelCall{kind: "bidi", model: request.Model, bidi: request})
	return nil, fmt.Errorf("offline bidi fixture")
}

func TestSpecializedModelAliases(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"embed", "gemini-embed", "gemini-batch", "image", "edit", "imagen", "speech", "speech-wav", "transcribe", "transcribe-stream", "translate", "translate-audio", "video", "gemini-video", "cache", "prefill", "decision"} {
		for _, mode := range []string{"normal", "alias", "disabled"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				service := &specializedModelService{image: picture.Bytes()}
				admin := &modelLogAdmin{}
				handler := NewHandler(service, Config{Admin: admin, AntiTruncation: func() bool { return mode != "disabled" }})
				base := "test"
				if kind == "translate-audio" {
					base = "audio"
				}
				model := base
				if mode != "normal" {
					model = "models/" + antiTruncationModelPrefix + base
				}
				path, contentType, expectedKind := "", "application/json", "generate"
				body := map[string]any{"model": model}
				switch kind {
				case "embed":
					path, expectedKind, body["input"] = "/build/v1/embeddings", "embed", "hello"
				case "gemini-embed", "gemini-batch":
					path, expectedKind = "/v1beta/models/"+url.PathEscape(strings.TrimPrefix(model, "models/")), "embed"
					content := map[string]any{"parts": []any{map[string]any{"text": "hello"}}}
					body = map[string]any{"content": content}
					if kind == "gemini-batch" {
						path += ":batchEmbedContents"
						body = map[string]any{"requests": []any{map[string]any{"model": model, "content": content}, map[string]any{"model": "models/" + base, "content": content}}}
					} else {
						path += ":embedContent"
					}
				case "image", "edit":
					path, body["prompt"] = "/v1/images/generations", "hello"
					if kind == "edit" {
						path = "/build/v1/images/edits"
					}
				case "imagen", "gemini-video":
					path = "/v1beta/models/" + url.PathEscape(strings.TrimPrefix(model, "models/"))
					body = map[string]any{"instances": []any{map[string]any{"prompt": "hello"}}}
					if kind == "imagen" {
						path, expectedKind = path+":predict", "image"
					} else {
						path, expectedKind = path+":predictLongRunning", "video"
					}
				case "speech", "speech-wav":
					path, body["input"] = "/v1/audio/speech", "hello"
					if kind == "speech-wav" {
						body["response_format"] = "wav"
					}
				case "transcribe", "transcribe-stream", "translate", "translate-audio":
					path, expectedKind = "/v1/audio/transcriptions", "transcribe"
					if kind == "transcribe-stream" {
						body["stream"] = "true"
					}
					if strings.HasPrefix(kind, "translate") {
						path = "/v1/audio/translations"
						if kind == "translate" {
							expectedKind = "generate"
						}
					}
				case "video":
					path, expectedKind, body["prompt"] = "/build/v1/videos", "video", "hello"
				case "cache":
					path, expectedKind = "/v1beta/cachedContents", "count"
					body["contents"] = []any{map[string]any{"parts": []any{map[string]any{"text": "hello"}}}}
				case "prefill":
					path, body["max_tokens"] = "/v1/messages", 0
					body["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
				case "decision":
					path, expectedKind, body["input"] = "/v1beta/decisions", "decision", "hello"
					body["questions"] = map[string]any{"q": map[string]any{"type": "noul", "instructions": "Is this useful?"}}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "edit" || strings.HasPrefix(kind, "transcribe") || strings.HasPrefix(kind, "translate") {
					var buffer bytes.Buffer
					form := multipart.NewWriter(&buffer)
					for key, value := range body {
						if err := form.WriteField(key, fmt.Sprint(value)); err != nil {
							t.Fatal(err)
						}
					}
					field, filename, data := "file", "sample.wav", pcmWAV([]byte{0, 0, 1, 0}, 24000, 1)
					if kind == "edit" {
						field, filename, data = "image", "sample.png", picture.Bytes()
					}
					file, err := form.CreateFormFile(field, filename)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := file.Write(data); err != nil {
						t.Fatal(err)
					}
					if err := form.Close(); err != nil {
						t.Fatal(err)
					}
					raw, contentType = buffer.Bytes(), form.FormDataContentType()
				}
				request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
				request.Header.Set("Content-Type", contentType)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				calls := service.snapshot()
				if mode == "disabled" {
					if response.Code != http.StatusNotFound || len(calls) != 0 {
						t.Fatalf("disabled alias reached upstream: status=%d calls=%+v body=%s", response.Code, calls, response.Body)
					}
					return
				}
				if response.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", response.Code, response.Body)
				}
				channel := aistudio.ChannelPlayground
				if strings.HasPrefix(path, "/build/") {
					channel = aistudio.ChannelBuild
				} else if strings.HasPrefix(path, "/v1beta/") {
					channel = ""
				}
				found := false
				for _, call := range calls {
					if strings.Contains(call.model, antiTruncationModelPrefix) || call.generated.AntiTruncation || call.channel != channel {
						t.Fatalf("specialized request lost its native shape or route: %+v", call)
					}
					if call.kind == expectedKind {
						found = true
						if call.model != base {
							t.Fatalf("model=%q want=%q", call.model, base)
						}
					}
				}
				if !found {
					t.Fatalf("no %s request observed: %+v", expectedKind, calls)
				}
				wantRequested := model
				if strings.HasPrefix(path, "/v1beta/models/") {
					wantRequested = strings.TrimPrefix(model, "models/")
				}
				if admin.entry.RequestedModel != wantRequested {
					t.Fatalf("original model audit lost: got=%q want=%q", admin.entry.RequestedModel, wantRequested)
				}
				if kind == "speech" && response.Header().Get("Content-Type") != "audio/mpeg" || kind == "speech-wav" && !bytes.HasPrefix(response.Body.Bytes(), []byte("RIFF")) {
					t.Fatalf("speech default/explicit format changed: headers=%v", response.Header())
				}
				if kind == "embed" || kind == "video" || kind == "cache" || kind == "prefill" {
					var payload map[string]any
					if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					wantModel := base
					if kind == "cache" {
						wantModel = "models/" + base
					}
					if payload["model"] != wantModel {
						t.Fatalf("normal model response changed: %v", payload["model"])
					}
				}
			})
		}
	}
}

func TestLiveModelAliasScope(t *testing.T) {
	for _, path := range []string{"/v1/live", "/build/v1/live", "/v1/robotics/stream"} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/enabled=%t", path, enabled), func(t *testing.T) {
				service := &specializedModelService{}
				server := httptest.NewServer(NewHandler(service, Config{AntiTruncation: func() bool { return enabled }}))
				defer server.Close()
				connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
				if err := connection.WriteJSON(map[string]any{"type": "setup", "model": "models/" + antiTruncationModelPrefix + "test", "input_modalities": []string{"text"}, "output_modalities": []string{"text"}}); err != nil {
					t.Fatal(err)
				}
				var response bidiServerMessage
				if err := connection.ReadJSON(&response); err != nil {
					t.Fatal(err)
				}
				calls := service.snapshot()
				if !enabled {
					if len(calls) != 0 || response.Type != "error" {
						t.Fatalf("disabled live alias was accepted: %+v", calls)
					}
					return
				}
				if len(calls) != 1 || calls[0].model != "test" || strings.Contains(calls[0].bidi.ModelAccessScope, antiTruncationModelPrefix) {
					t.Fatalf("live model or access scope contains the selector: %+v", calls)
				}
				wantChannel := aistudio.ChannelPlayground
				if strings.HasPrefix(path, "/build/") {
					wantChannel = aistudio.ChannelBuild
				}
				if calls[0].channel != wantChannel {
					t.Fatalf("live alias escaped URL channel: %q", calls[0].channel)
				}
			})
		}
	}
}
