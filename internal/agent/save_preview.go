package agent

import (
	"strings"

	"reasonix/internal/provider"
)

// 会话预览域（tl1633 自 save.go 纯移动拆出）：SessionPreview/摘要摘录。

// SessionPreview returns the same preview and user-turn count used by
// ListSessions for one session file.
func SessionPreview(path string) (string, int) {
	return previewSession(path)
}


// SessionPreviewWithError returns the same preview and user-turn count as
// SessionPreview, but preserves read and decode failures for callers that must
// not persist a fallback derived from an unreadable transcript.
func SessionPreviewWithError(path string) (string, int, error) {
	return previewSessionWithError(path)
}


// SessionPreviewFromMessages computes the same preview line and user-turn count
// as previewSession, but from an in-memory message slice. Session.Save writes
// exactly these messages to the .jsonl, so this is byte-for-byte equivalent to
// decoding the file — letting the autosave path persist the counts into the
// sidecar without a disk read.
// sessionPreviewNoiseless extracts the displayable prose of one user turn:
// transient/host blocks, pasted-text chrome and leading @file references are
// the noise rows the preview must not show. Empty output marks a noise-only
// turn (a bare file dump, an empty steer) that carries nothing recognizable —
// the tail-first walk skips it and keeps looking at older turns.
func sessionPreviewNoiseless(msg provider.Message) string {
	text := strings.TrimSpace(StripPasteDisplayLabel(previewProse(UserMessageText(msg))))
	// previewProse deliberately keeps a prompt that is nothing but @file
	// references verbatim ("there is nothing else to show"); for the preview
	// that IS the noise the user asked us to filter (tl 1177), so a remaining
	// @-leading text marks a noise-only turn.
	if strings.HasPrefix(text, "@") {
		return ""
	}
	return text
}


// SessionPreviewFromMessages returns the session preview and the number of
// user-role turns so the picker can show "5 turns · 'help me debug the…'".
//
// tl 1177: the preview is tail-first — it walks from the newest user turn
// backwards and takes the first one with displayable prose, because the latest
// exchange is what tells the user whether this is the session they want (the
// old first-turn preview read stale once a conversation moved on). Noise-only
// turns are skipped; if every turn is noise the preview stays empty rather
// than leaking chrome text.
func SessionPreviewFromMessages(msgs []provider.Message) (string, int) {
	first := ""
	turns := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if !IsUserAuthoredTurnMessage(m) {
			continue
		}
		turns++
		if first == "" {
			if prose := sessionPreviewNoiseless(m); prose != "" {
				first = truncatePreview(prose)
			}
		}
	}
	return first, turns
}


// recentExcerptCount is how many closing user messages the hover excerpts
// carry. tl 1188 raised it from 3 to 8: the excerpts moved from the card's
// in-card overlay (where 3 lines was all a ≥210px cell could show) to an
// external ~630×420 hover layer, which has room for a real scan. The
// sidecar grows by a few hundred bytes per session — still tiny.
const recentExcerptCount = 8


// RecentExcerptCount is the exported hover-excerpt width (tl-1177b): the
// desktop's on-demand tail reader (SessionTailUserExcerpts) shares this
// constant so the sidecar field and the hover computation never drift apart.
const RecentExcerptCount = recentExcerptCount


// SessionRecentExcerptsFromMessages returns up to n recent user messages
// (newest first), noise-filtered the same way as the preview. This is the
// data behind the session wall card's hover overlay: the preview line is one
// sentence, the excerpts let the user scan the actual closing exchanges
// without opening the transcript.
func SessionRecentExcerptsFromMessages(msgs []provider.Message, n int) []string {
	if n <= 0 {
		return nil
	}
	var out []string
	for i := len(msgs) - 1; i >= 0 && len(out) < n; i-- {
		m := msgs[i]
		if !IsUserAuthoredTurnMessage(m) {
			continue
		}
		if prose := sessionPreviewNoiseless(m); prose != "" {
			out = append(out, truncatePreview(prose))
		}
	}
	return out
}


// previewSession returns the session preview (the newest recognizable user
// message, truncated — tl 1177 tail-first) and the number of user-role
// messages so the picker can show "5 turns · 'help me debug the…'".
// Errors are swallowed — a malformed file just shows up with an empty preview.
func previewSession(path string) (string, int) {
	preview, turns, _ := previewSessionWithError(path)
	return preview, turns
}


// previewProse drops the leading @file references a prompt opens with so the
// preview shows what was asked rather than a row of paths. A prompt that is
// nothing but references keeps them — there is nothing else to show.
func previewProse(s string) string {
	rest := strings.TrimLeft(s, " \t")
	for strings.HasPrefix(rest, "@") {
		end := strings.IndexAny(rest, " \t\r\n")
		if end < 0 {
			return s
		}
		next := strings.TrimLeft(rest[end:], " \t")
		if strings.TrimSpace(next) == "" {
			return s
		}
		rest = next
	}
	if rest == "" {
		return s
	}
	return rest
}


// truncatePreview clamps a preview line to 80 runes with an ellipsis, matching
// what the pickers render.
func truncatePreview(s string) string {
	if r := []rune(s); len(r) > 80 {
		return string(r[:77]) + "…"
	}
	return s
}

