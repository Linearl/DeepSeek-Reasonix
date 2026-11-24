package provider

import (
	"encoding/base64"
	"slices"
	"strings"
)

// tl1767 (视频输入接入): the wire-side facts for video input. First phase is
// file attachments only — inline base64 data URLs — mirroring the audio chain
// (task 1008). Like images and audio, video only ever widens the user-turn
// content shape; it must never touch the tools list or the system prompt, so
// capability caches keep prefix bytes stable.
//
// Fail-closed contract (zcode transform.ts:242 口径 unsupportedInputMediaText):
// a provider that cannot carry video never fails the request — the video part
// is projected into a text placeholder so the model is told honestly that the
// bytes are not there. Construction keeps video out of non-video models in the
// first place (the resolver gate), so this note fires only on mid-turn flips
// and protocol gaps.
const (
	// MaxVideoAttachmentBytes is the raw-byte ceiling for one inline video
	// attachment (zhipu video_url: single file <= 50 MB base64-embedded,
	// matching the task-1008 audio ceiling).
	MaxVideoAttachmentBytes = 50 << 20
)

// VideoAttachmentExts lists the file extensions accepted as video input
// attachments (tl1767: five common containers, all re-encoded to base64
// anyway, mirroring the audio extension whitelist).
var VideoAttachmentExts = []string{".mp4", ".mov", ".webm", ".mkv", ".avi"}

// VideoWireUnsupportedNote is the fail-closed text placeholder projected in
// place of a video part when the target protocol cannot carry video bytes.
// Keep it provider-neutral: adapters append it verbatim.
const VideoWireUnsupportedNote = "[A video attachment was part of this turn, but the selected provider protocol cannot carry video input; the video bytes are not in this request. Tell the user the video did not come through and ask them to retry with a video-capable provider/model.]"

// IsVideoAttachmentPath reports whether a path's extension names an accepted
// video input format. Extension-only check on purpose: the bytes are forwarded
// verbatim as base64, exactly like images and audio.
func IsVideoAttachmentPath(path string) bool {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 {
		return false
	}
	ext := strings.ToLower(path[dot:])
	return slices.Contains(VideoAttachmentExts, ext)
}

// IsVideoDataURL reports whether s is a `data:video/...;base64,...` URL.
func IsVideoDataURL(s string) bool {
	_, _, ok := ParseVideoDataURL(s)
	return ok
}

// ParseVideoDataURL splits an inline video data URL into its MIME subtype and
// base64 payload. Accepted MIME family: video/*.
func ParseVideoDataURL(s string) (mimeType string, payload string, ok bool) {
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
	if !strings.HasPrefix(mimeType, "video/") {
		return "", "", false
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return "", "", false
	}
	return mimeType, payload, true
}

// VideoMIMEForPath maps an accepted video file extension onto the MIME type
// the inline data URL carries. Empty for non-video paths.
func VideoMIMEForPath(path string) string {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 {
		return ""
	}
	switch strings.ToLower(path[dot:]) {
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	default:
		return ""
	}
}

// HasVideoInput reports whether any message carries inline video. Video rides
// its own field (like audio) so the two capability gates stay independent.
func HasVideoInput(messages []Message) bool {
	for _, m := range messages {
		if len(m.Video) > 0 {
			return true
		}
	}
	return false
}

// TextifyVideoAttachments is the fail-closed projection (tl1767, zcode
// transform.ts:242 口径): protocols without a video content part (Anthropic,
// OpenAI Responses) strip inline video and append one honest text placeholder
// per video-bearing message instead of failing the request or silently
// dropping the bytes. Messages without video pass through unchanged (same
// slice, no allocation).
func TextifyVideoAttachments(messages []Message) []Message {
	if !HasVideoInput(messages) {
		return messages
	}
	out := make([]Message, len(messages))
	changed := false
	for i, m := range messages {
		if len(m.Video) == 0 {
			out[i] = m
			continue
		}
		changed = true
		m.Video = nil
		if m.Content == "" {
			m.Content = VideoWireUnsupportedNote
		} else {
			m.Content = m.Content + "\n\n" + VideoWireUnsupportedNote
		}
		out[i] = m
	}
	if !changed {
		return messages
	}
	return out
}
