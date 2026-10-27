package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"reasonix/internal/provider"
)

// SessionTailUserExcerpts returns up to n recent noise-filtered user messages
// (newest first), computed on demand from the tail of one session's .jsonl
// display checkpoint. This is the tl-1177b 方案 B read-side path behind the
// session wall card's hover overlay: it reads no sidecar field and runs no
// backfill, so sessions saved before any excerpts sidecar existed answer on
// the first hover.
//
// The .jsonl holds one provider.Message per line, oldest first, and both the
// schema-1 writer and the schema-2 DAG path keep it current (writeSessionMessages
// / appendSessionDisplayReadModel / writeDAGCheckpointCache). A save mid-write
// can leave a torn last line; it is dropped, like any line that fails to
// decode. Lines past maxTailExcerptBytes are skipped without decoding — a lean
// user text never reaches that size, while an embedded base64 image would
// only burn the parse budget.
//
// A tail window can land entirely on assistant/tool traffic (a long closing
// execution); collection therefore stops only after n excerpt-eligible
// messages, and when the first window yields fewer than n the read widens once
// before giving up with whatever was found.
func SessionTailUserExcerpts(sessionPath string, n int) []string {
	sessionPath = strings.TrimSpace(sessionPath)
	if n <= 0 || sessionPath == "" {
		return nil
	}
	info, err := os.Stat(sessionPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return nil
	}
	msgs, covered := readSessionTailMessages(sessionPath, info.Size(), sessionTailExcerptWindow, n)
	excerpts := SessionRecentExcerptsFromMessages(msgs, n)
	if len(excerpts) >= n || covered || info.Size() <= sessionTailExcerptWindow {
		return excerpts
	}
	if wider, _ := readSessionTailMessages(sessionPath, info.Size(), sessionTailExcerptMaxWindow, n); len(wider) > 0 {
		return SessionRecentExcerptsFromMessages(wider, n)
	}
	return excerpts
}

// SessionTailAgentReply returns the closing assistant message of one session's
// transcript tail, shortened for the hover overlay (tl-1188 返修: user excerpts
// alone carry little information value — the reader wants the final answer
// next to them). It reads the same tail window as SessionTailUserExcerpts and
// walks newest-first, so the first displayable assistant prose wins; an empty
// string means no assistant reply sits in the tail window.
func SessionTailAgentReply(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	info, err := os.Stat(sessionPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	msgs, _ := readSessionTailMessages(sessionPath, info.Size(), sessionTailExcerptWindow, recentExcerptCount)
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != provider.RoleAssistant {
			continue
		}
		text := strings.TrimSpace(previewProse(strings.TrimSpace(m.Content)))
		if text == "" {
			continue
		}
		return truncateTailAgentReply(text)
	}
	return ""
}

const tailAgentReplyMaxRunes = 240

// truncateTailAgentReply bounds the reply for the overlay. The hover card is
// an external layered popover since tl-1188, so it can afford a few lines —
// unlike the 80-rune list preview (truncatePreview).
func truncateTailAgentReply(s string) string {
	runes := []rune(s)
	if len(runes) <= tailAgentReplyMaxRunes {
		return s
	}
	return strings.TrimSpace(string(runes[:tailAgentReplyMaxRunes])) + "…"
}

const (
	// sessionTailExcerptWindow is the first tail read. Three user messages
	// almost always sit within the last 128KB even after long closing
	// assistant answers; the retry window below covers the rest.
	sessionTailExcerptWindow = 128 * 1024
	// sessionTailExcerptMaxWindow bounds the single retry read.
	sessionTailExcerptMaxWindow = 1024 * 1024
	// maxTailExcerptBytes skips decoding one oversized line. Excerpts display
	// at 80 runes (truncatePreview); anything longer than this is an embedded
	// payload, not a hoverable question.
	maxTailExcerptBytes = 64 * 1024
)

// readSessionTailMessages decodes complete lines inside the last window bytes
// of path, walking newest first and stopping once n excerpt-eligible messages
// are in hand; the returned slice is oldest first, the order the shared
// excerpt walk expects. covered reports whether the window reached the file
// start (no older lines exist), so the caller knows a retry cannot find more.
func readSessionTailMessages(path string, size int64, window int, n int) ([]provider.Message, bool) {
	if int64(window) > size {
		window = int(size)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	start := size - int64(window)
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, false
	}
	// A mid-window start begins inside a line whose head we never read.
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			return nil, start == 0
		}
	}
	// A tail write in flight leaves a partial final line; drop it.
	if length := len(buf); length > 0 && buf[length-1] != '\n' {
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i+1]
		} else {
			return nil, start == 0
		}
	}
	// Split the window once, then walk the line starts newest first.
	type lineSpan struct{ lo, hi int }
	spans := make([]lineSpan, 0, 32)
	for offset := 0; offset < len(buf); {
		i := bytes.IndexByte(buf[offset:], '\n')
		if i < 0 {
			spans = append(spans, lineSpan{offset, len(buf)})
			break
		}
		spans = append(spans, lineSpan{offset, offset + i})
		offset += i + 1
	}
	parsed := make([]provider.Message, 0, 8)
	eligible := 0
	for i := len(spans) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(buf[spans[i].lo:spans[i].hi])
		if len(line) == 0 || len(line) > maxTailExcerptBytes {
			continue
		}
		var m provider.Message
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		parsed = append(parsed, m)
		if IsUserAuthoredTurnMessage(m) && sessionPreviewNoiseless(m) != "" {
			eligible++
			if eligible >= n {
				break
			}
		}
	}
	// parsed is newest first; reverse to the file order (oldest first) the
	// shared excerpt walk expects.
	for i, j := 0, len(parsed)-1; i < j; i, j = i+1, j-1 {
		parsed[i], parsed[j] = parsed[j], parsed[i]
	}
	return parsed, start == 0
}
