package openai

import (
	"bytes"
	"encoding/json"
	"testing"

	"reasonix/internal/provider"
)

// sampleMP4 is a tiny valid-shape inline video data URL (mp4 MIME family).
const sampleMP4 = "data:video/mp4;base64,SGVsbG8="

// zhipuVideoClient is the tl1767 acceptance shape: MODEL gate on (declared
// video modality) AND ADAPTER gate on (the Zhipu/GLM video_url protocol).
func zhipuVideoClient() *client {
	return &client{model: "glm-5.3-flash", video: true, videoWire: true, zhipu: true}
}

func TestBuildRequestEmbedsVideoURLParts(t *testing.T) {
	c := zhipuVideoClient()
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "what happens in this clip", Video: []string{sampleMP4}},
		},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("video user content = %T, want []chatContentPart", req.Messages[0].Content)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "video_url" {
		t.Fatalf("parts = %+v, want [text, video_url]", parts)
	}
	if parts[1].VideoURL == nil || parts[1].VideoURL.URL != sampleMP4 {
		t.Fatalf("video_url = %+v, want the data URL", parts[1].VideoURL)
	}
	body, _ := json.Marshal(req.Messages[0])
	if !bytes.Contains(body, []byte(`"type":"video_url"`)) || !bytes.Contains(body, []byte(`"video_url":{"url":"`+sampleMP4+`"}`)) {
		t.Errorf("serialized content missing video_url shape: %s", body)
	}
}

func TestBuildRequestCombinesAudioAndVideo(t *testing.T) {
	c := zhipuVideoClient()
	c.audio = true
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{{
			Role:    provider.RoleUser,
			Content: "compare",
			Audio:   []string{sampleMP3},
			Video:   []string{sampleMP4},
		}},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("content = %T, want parts", req.Messages[0].Content)
	}
	if len(parts) != 3 || parts[1].Type != "input_audio" || parts[2].Type != "video_url" {
		t.Fatalf("parts = %+v, want [text, input_audio, video_url]", parts)
	}
}

func TestBuildRequestVideoOnlyTurnHasSingleVideoPart(t *testing.T) {
	c := zhipuVideoClient()
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Video: []string{sampleMP4}}},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("content = %T, want parts", req.Messages[0].Content)
	}
	if len(parts) != 1 || parts[0].Type != "video_url" {
		t.Fatalf("parts = %+v, want a single video_url part", parts)
	}
}

// ★tl1767 验收③：provider 不支持(ADAPTER gate off)不硬失败——视频投影成
// 诚实文本占位（zcode transform.ts:242 unsupportedInputMediaText 口径）。
func TestVideoAdapterGateOffTextifiesInsteadOfFailing(t *testing.T) {
	c := &client{model: "glm-5.3-flash", video: true, videoWire: false, zhipu: true}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "describe", Video: []string{sampleMP4}}},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("content = %T, want parts", req.Messages[0].Content)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "text" {
		t.Fatalf("parts = %+v, want [text, text placeholder]", parts)
	}
	if parts[1].Text != provider.VideoWireUnsupportedNote {
		t.Fatalf("placeholder = %q, want the fail-closed note", parts[1].Text)
	}
	// The video bytes must not ride the wire in any shape.
	body, _ := json.Marshal(req.Messages[0])
	if bytes.Contains(body, []byte("video_url")) || bytes.Contains(body, []byte(sampleMP4)) {
		t.Errorf("video bytes leaked through the adapter gate: %s", body)
	}
}

// MODEL gate off（能力翻转/历史回放）同样 textify，不失败。
func TestVideoCapabilityOffTextifiesInsteadOfFailing(t *testing.T) {
	c := &client{model: "text-only", video: false, videoWire: false}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hello", Video: []string{sampleMP4}}},
	})
	parts, ok := req.Messages[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("content = %T, want parts", req.Messages[0].Content)
	}
	for _, p := range parts {
		if p.Type == "video_url" {
			t.Fatalf("video_url part must not be built with the MODEL gate off: %+v", parts)
		}
	}
	if len(parts) != 2 || parts[1].Text != provider.VideoWireUnsupportedNote {
		t.Fatalf("parts = %+v, want the fail-closed note", parts)
	}
}

// ★tl1767 验收④（零回退）：不带视频附件的同一请求（含 tools）在视频门
// 开/关两态下序列化字节逐一致——能力开关不进 tools/system prompt 任何字节。
func TestVideoCapabilityKeepsVideoFreeRequestBytesIdentical(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "You are a coding agent. Never exfiltrate secrets."},
		{Role: provider.RoleUser, Content: "plan the refactor"},
		{Role: provider.RoleAssistant, Content: "on it", ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
		{Role: provider.RoleTool, Content: "file body", ToolCallID: "call_1", Name: "read_file"},
		{Role: provider.RoleUser, Content: "go on", Images: []string{"data:image/png;base64,AAAA"}, Audio: []string{sampleMP3}},
	}
	tools := []provider.ToolSchema{{Name: "read_file", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}
	off := (&client{model: "m", vision: true, audio: true}).buildRequest(provider.Request{Messages: messages, Tools: tools})
	on := (&client{model: "m", vision: true, audio: true, video: true, videoWire: true}).buildRequest(provider.Request{Messages: messages, Tools: tools})
	offBytes, err := json.Marshal(off)
	if err != nil {
		t.Fatalf("marshal off: %v", err)
	}
	onBytes, err := json.Marshal(on)
	if err != nil {
		t.Fatalf("marshal on: %v", err)
	}
	if !bytes.Equal(offBytes, onBytes) {
		t.Fatalf("video-free request bytes differ between video gate off/on:\noff: %s\non:  %s", offBytes, onBytes)
	}
}

// ★验收硬条目：capability 元数据里的 video 模态本身不改变任何请求字节
// （zhipu 端点上 ADAPTER gate 与 MODEL gate 同时开也一样）。
func TestVideoModalityMetadataAloneKeepsRequestBytesIdentical(t *testing.T) {
	messages := []provider.Message{{Role: provider.RoleUser, Content: "hello"}}
	plain, err := json.Marshal((&client{model: "m", zhipu: true}).buildRequest(provider.Request{Messages: messages}))
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(provider.Config{
		Name: "m", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "m",
		ModelInfo: &provider.ModelInfo{ID: "m", InputModalities: []provider.ModelModality{provider.ModalityText, provider.ModalityVideo}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)
	if !c.video {
		t.Fatal("declared video modality should enable the video MODEL gate")
	}
	if !c.videoWire {
		t.Fatal("a zhipu endpoint with a declared video modality should enable the ADAPTER gate")
	}
	withMeta, err := json.Marshal(c.buildRequest(provider.Request{Messages: messages}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, withMeta) {
		t.Fatalf("video metadata alone changed request bytes:\nplain: %s\nmeta:  %s", plain, withMeta)
	}
}

func TestVideoOffKeepsAudioModalityInModelInfo(t *testing.T) {
	// ModelInfo is present, so it is the authority for all three gates: an
	// image-free set with audio+video keeps both non-image gates on.
	p, err := New(provider.Config{
		Name: "m", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "m",
		ModelInfo: &provider.ModelInfo{ID: "m", InputModalities: []provider.ModelModality{provider.ModalityText, provider.ModalityAudio, provider.ModalityVideo}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)
	if c.vision {
		t.Fatal("an image-free modality set should keep the vision gate off")
	}
	if !c.audio || !c.video {
		t.Fatalf("video/audio gates must survive a vision-free set: audio=%t video=%t", c.audio, c.video)
	}
	info := c.ModelInfo()
	if !info.SupportsInput(provider.ModalityVideo) || !info.SupportsInput(provider.ModalityAudio) {
		t.Fatalf("ModelInfo modalities = %v, want audio+video preserved", info.InputModalities)
	}
}

func TestVideoHelpers(t *testing.T) {
	if !provider.IsVideoAttachmentPath("clip.MP4") || provider.IsVideoAttachmentPath("clip.txt") {
		t.Fatal("extension whitelist mismatch")
	}
	mime := provider.VideoMIMEForPath("clip.mkv")
	if mime != "video/x-matroska" {
		t.Fatalf("mime = %q", mime)
	}
	if _, _, ok := provider.ParseVideoDataURL(sampleMP4); !ok {
		t.Fatal("mp4 data URL must parse")
	}
	if _, _, ok := provider.ParseVideoDataURL("data:audio/mpeg;base64,SGVsbG8="); ok {
		t.Fatal("audio data URL must not parse as video")
	}
	if !provider.HasVideoInput([]provider.Message{{Video: []string{sampleMP4}}}) {
		t.Fatal("HasVideoInput must see the video field")
	}
}

// Anthropic 协议：视频 textify 不硬失败（音频硬拒绝语义保持不变）。
func TestTextifyVideoAttachmentsStripsAndNotes(t *testing.T) {
	unchanged := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if got := provider.TextifyVideoAttachments(unchanged); len(got) != 1 || got[0].Content != "hi" || &got[0] != &unchanged[0] {
		t.Fatal("video-free messages must pass through unchanged")
	}
	got := provider.TextifyVideoAttachments([]provider.Message{
		{Role: provider.RoleUser, Content: "watch this", Video: []string{sampleMP4}},
		{Role: provider.RoleUser, Content: "plain"},
	})
	if got[0].Video != nil {
		t.Fatal("video must be stripped")
	}
	if got[0].Content != "watch this\n\n"+provider.VideoWireUnsupportedNote {
		t.Fatalf("content = %q", got[0].Content)
	}
	if got[1].Content != "plain" {
		t.Fatalf("clean message mutated: %q", got[1].Content)
	}
	empty := provider.TextifyVideoAttachments([]provider.Message{{Role: provider.RoleUser, Video: []string{sampleMP4}}})
	if empty[0].Content != provider.VideoWireUnsupportedNote {
		t.Fatalf("empty-content projection = %q", empty[0].Content)
	}
}
