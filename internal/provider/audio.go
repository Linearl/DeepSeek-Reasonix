package provider

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
)

// Task 1008 (模型音频输入): the wire-side facts for OpenAI-compatible audio
// input. First phase is file attachments only — inline base64 data URLs —
// matching the mimo chat-completions `input_audio` content part. Like images,
// audio only ever widens the user-turn content shape; it must never touch the
// tools list or the system prompt, so capability caches keep prefix bytes
// stable.
const (
	// MaxAudioAttachmentBytes is the raw-byte ceiling for one inline audio
	// attachment (mimo docs: single file <= 50 MB base64-embedded).
	MaxAudioAttachmentBytes = 50 << 20
)

// AudioAttachmentExts lists the file extensions accepted as audio input
// attachments (task 1008: five formats, all re-encoded to base64 anyway).
var AudioAttachmentExts = []string{".mp3", ".wav", ".flac", ".m4a", ".ogg"}

// IsAudioAttachmentPath reports whether a path's extension names an accepted
// audio input format. Extension-only check on purpose: the bytes are forwarded
// verbatim as base64, exactly like images.
func IsAudioAttachmentPath(path string) bool {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 {
		return false
	}
	ext := strings.ToLower(path[dot:])
	return slices.Contains(AudioAttachmentExts, ext)
}

// IsAudioDataURL reports whether s is a `data:audio/...;base64,...` URL (or an
// application/ogg variant, which some tools produce for .ogg files).
func IsAudioDataURL(s string) bool {
	_, _, ok := ParseAudioDataURL(s)
	return ok
}

// ParseAudioDataURL splits an inline audio data URL into its MIME subtype and
// base64 payload. Accepted MIME families: audio/* plus application/ogg.
func ParseAudioDataURL(s string) (mimeType string, payload string, ok bool) {
	const prefix = "data:"
	const marker = ";base64,"
	if !strings.HasPrefix(s, prefix) {
		return "", "", false
	}
	i := strings.Index(s, marker)
	if i <= len(prefix) {
		return "", "", false
	}
	mimeType = strings.ToLower(s[len(prefix):i])
	payload = s[i+len(marker):]
	if !strings.HasPrefix(mimeType, "audio/") && mimeType != "application/ogg" {
		return "", "", false
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return "", "", false
	}
	return mimeType, payload, true
}

// AudioWireFormat maps a data-URL MIME type onto the OpenAI input_audio format
// vocabulary (mp3/wav/flac/m4a/ogg). The empty string means the MIME type has
// no documented format name; callers then fall back to the file extension.
func AudioWireFormat(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave":
		return "wav"
	case "audio/flac", "audio/x-flac":
		return "flac"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return "m4a"
	case "audio/ogg", "application/ogg", "audio/vorbis":
		return "ogg"
	default:
		return ""
	}
}

// AudioMIMEForPath maps an accepted audio file extension onto the MIME type
// the inline data URL carries. Empty for non-audio paths.
func AudioMIMEForPath(path string) string {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 {
		return ""
	}
	switch strings.ToLower(path[dot:]) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	case ".ogg":
		return "audio/ogg"
	default:
		return ""
	}
}

// HasAudioInput reports whether any message carries inline audio. Images ride
// the Images field; audio has its own field so the two capability gates stay
// independent (a vision override must never clobber audio, and vice versa).
func HasAudioInput(messages []Message) bool {
	for _, m := range messages {
		if len(m.Audio) > 0 {
			return true
		}
	}
	return false
}

// RejectAudioIfPresent is the construction-layer interception (task 1008): a
// caller built a request whose messages carry audio while the target model is
// not audio-enabled. Fail loudly instead of silently dropping the bytes — a
// silent drop would send the model hunting for content that is not there.
// enablement names what would accept the audio, so the error states the next
// step.
func RejectAudioIfPresent(caller, model, enablement string, messages []Message) error {
	if !HasAudioInput(messages) {
		return nil
	}
	return fmt.Errorf("%s: model %q received audio attachment(s) but audio input is not available for it (%s); enable audio input in the model's capability settings or remove the audio attachments", caller, model, enablement)
}
