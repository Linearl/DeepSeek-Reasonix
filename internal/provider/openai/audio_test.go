package openai

import (
	"bytes"
	"encoding/json"
	"testing"

	"reasonix/internal/provider"
)

// sampleMP3 is a tiny valid-shape inline audio data URL (mp3 MIME family).
const sampleMP3 = "data:audio/mpeg;base64,SGVsbG8="

func TestBuildRequestEmbedsInputAudioParts(t *testing.T) {
	c := &client{model: "mimo-v2.6-flash", audio: true}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "what is in this clip", Audio: []string{sampleMP3}},
		},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("audio user content = %T, want []chatContentPart", req.Messages[0].Content)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "input_audio" {
		t.Fatalf("parts = %+v, want [text, input_audio]", parts)
	}
	if parts[1].InputAudio == nil || parts[1].InputAudio.Data != sampleMP3 || parts[1].InputAudio.Format != "mp3" {
		t.Fatalf("input_audio = %+v, want the data URL with format mp3", parts[1].InputAudio)
	}
	body, _ := json.Marshal(req.Messages[0])
	if !bytes.Contains(body, []byte(`"type":"input_audio"`)) || !bytes.Contains(body, []byte(`"format":"mp3"`)) {
		t.Errorf("serialized content missing input_audio shape: %s", body)
	}
}

func TestBuildRequestCombinesImagesAndAudio(t *testing.T) {
	c := &client{model: "mimo-v2.6-flash", vision: true, audio: true}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{{
			Role:    provider.RoleUser,
			Content: "compare",
			Images:  []string{"data:image/png;base64,AAAA"},
			Audio:   []string{sampleMP3},
		}},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("content = %T, want parts", req.Messages[0].Content)
	}
	if len(parts) != 3 || parts[1].Type != "image_url" || parts[2].Type != "input_audio" {
		t.Fatalf("parts = %+v, want [text, image_url, input_audio]", parts)
	}
}

func TestBuildRequestAudioTurnWithoutTextHasSingleAudioPart(t *testing.T) {
	c := &client{model: "mimo-v2.6-flash", audio: true}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Audio: []string{sampleMP3}}},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("content = %T, want parts", req.Messages[0].Content)
	}
	if len(parts) != 1 || parts[0].Type != "input_audio" {
		t.Fatalf("parts = %+v, want a single input_audio part", parts)
	}
}

func TestAudioWireFormatFromExtensionFallback(t *testing.T) {
	c := &client{model: "m", audio: true}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Audio: []string{"data:application/octet-stream;base64,AAAA.flac"}}},
	})
	parts := req.Messages[0].Content.([]chatContentPart)
	if parts[0].InputAudio.Format != "flac" {
		t.Fatalf("format = %q, want extension fallback flac", parts[0].InputAudio.Format)
	}
}

// ★任务 1008 验收硬条目：音频能力开/关两态下，不带音频附件的同一请求
// （含 tools）序列化字节逐一致——能力开关不进 tools/system prompt 任何字节。
func TestAudioCapabilityKeepsAudioFreeRequestBytesIdentical(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleUser, Content: "plan the refactor"},
		{Role: provider.RoleAssistant, Content: "on it", ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
		{Role: provider.RoleTool, Content: "file body", ToolCallID: "call_1", Name: "read_file"},
		{Role: provider.RoleUser, Content: "go on", Images: []string{"data:image/png;base64,AAAA"}},
	}
	tools := []provider.ToolSchema{{Name: "read_file", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}
	off := (&client{model: "m", vision: true}).buildRequest(provider.Request{Messages: messages, Tools: tools})
	on := (&client{model: "m", vision: true, audio: true}).buildRequest(provider.Request{Messages: messages, Tools: tools})
	offBytes, err := json.Marshal(off)
	if err != nil {
		t.Fatalf("marshal off: %v", err)
	}
	onBytes, err := json.Marshal(on)
	if err != nil {
		t.Fatalf("marshal on: %v", err)
	}
	if !bytes.Equal(offBytes, onBytes) {
		t.Fatalf("audio-free request bytes differ between audio gate off/on:\noff: %s\non:  %s", offBytes, onBytes)
	}
}

// ★验收硬条目：capability 元数据里的 audio 模态本身不改变任何请求字节。
func TestAudioModalityMetadataAloneKeepsRequestBytesIdentical(t *testing.T) {
	messages := []provider.Message{{Role: provider.RoleUser, Content: "hello"}}
	plain, err := json.Marshal((&client{model: "m"}).buildRequest(provider.Request{Messages: messages}))
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(provider.Config{
		Name: "m", BaseURL: "https://example.test/v1", Model: "m",
		ModelInfo: &provider.ModelInfo{ID: "m", InputModalities: []provider.ModelModality{provider.ModalityText, provider.ModalityAudio}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)
	if !c.audio {
		t.Fatal("declared audio modality should enable the audio gate")
	}
	withMeta, err := json.Marshal(c.buildRequest(provider.Request{Messages: messages}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, withMeta) {
		t.Fatalf("audio metadata alone changed request bytes:\nplain: %s\nmeta:  %s", plain, withMeta)
	}
}

func TestStreamRejectsAudioWhenGateOff(t *testing.T) {
	c := &client{model: "text-only", name: "openai"}
	err := provider.RejectAudioIfPresent(c.name, c.model, "audio input is not enabled for this model", []provider.Message{
		{Role: provider.RoleUser, Content: "listen", Audio: []string{sampleMP3}},
	})
	if err == nil {
		t.Fatal("audio with the gate off must be rejected, not silently dropped")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("text-only")) {
		t.Errorf("error should name the model: %v", err)
	}
}

func TestVisionOffKeepsAudioModalityInModelInfo(t *testing.T) {
	// ModelInfo is present, so it is the authority for both gates (the same
	// precedence vision uses): an image-free set with audio keeps audio on.
	p, err := New(provider.Config{
		Name: "m", BaseURL: "https://example.test/v1", Model: "m",
		ModelInfo: &provider.ModelInfo{ID: "m", InputModalities: []provider.ModelModality{provider.ModalityText, provider.ModalityAudio}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)
	if c.vision {
		t.Fatal("an image-free modality set should keep the vision gate off")
	}
	if !c.audio {
		t.Fatal("vision off must not clobber the audio gate")
	}
	info := c.ModelInfo()
	if !info.SupportsInput(provider.ModalityAudio) {
		t.Fatalf("ModelInfo modalities = %v, want audio preserved", info.InputModalities)
	}
}
