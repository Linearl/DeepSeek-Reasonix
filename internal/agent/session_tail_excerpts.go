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
// A tail window can land entirely on assistant/tool traffic; when the first
// window yields fewer than n excerpts and the file extends past it, one wider
// read retries before giving up with whatever was found.
func SessionTailUserExcerpts(sessionPath string, n int) []string {
	sessionPath = strings.TrimSpace(sessionPath)
	if n <= 0 || sessionPath == "" {
		return nil
	}
	info, err := os.Stat(sessionPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return nil
	}
	msgs, covered := readSessionTailMessages(sessionPath, info.Size(), sessionTailExcerptWindow)
	excerpts := SessionRecentExcerptsFromMessages(msgs, n)
	if len(excerpts) >= n || covered || info.Size() <= sessionTailExcerptWindow {
		return excerpts
	}
	if wider, wideCovered := readSessionTailMessages(sessionPath, info.Size(), sessionTailExcerptMaxWindow); len(wider) > 0 {
		if retry := SessionRecentExcerptsFromMessages(wider, n); len(retry) > len(excerpts) || wideCovered {
			return retry
		}
	}
	return excerpts
}

const (
	// sessionTailExcerptWindow is the first tail read. Three user messages
	// almost always sit within the last 128KB even after long closing
	// assistant answers; the retry window below covers the rest.
	sessionTailExcerptWindow = 128 * 1024
	// sessionTailExcerptMaxWindow bounds the single retry read.
	sessionTailExcerptMaxWindow = 1024 * 1024
	// maxTailExcerptLines caps how many decoded messages one window feeds the
	// excerpt walk, bounding the hover's work regardless of message density.
	maxTailExcerptLines = 64
	// maxTailExcerptBytes skips decoding one oversized line. Excerpts display
	// at 80 runes (truncatePreview); anything longer than this is an embedded
	// payload, not a hoverable question.
	maxTailExcerptBytes = 64 * 1024
)

// readSessionTailMessages decodes the complete lines inside the last window
// bytes of path, oldest first, capped at maxTailExcerptLines messages.
// covered reports whether the window reached the file start (no older lines
// exist), so the caller knows a retry cannot find more.
func readSessionTailMessages(path string, size, window int64) ([]provider.Message, bool) {
	if window > size {
		window = size
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	start := size - window
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
	if n := len(buf); n > 0 && buf[n-1] != '\n' {
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i+1]
		} else {
			return nil, start == 0
		}
	}
	msgs := make([]provider.Message, 0, 8)
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		var line []byte
		if i >= 0 {
			line, buf = buf[:i], buf[i+1:]
		} else {
			line, buf = buf, nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || len(line) > maxTailExcerptBytes {
			continue
		}
		var m provider.Message
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		msgs = append(msgs, m)
		if len(msgs) >= maxTailExcerptLines {
			break
		}
	}
	// The collected window is newest last; the excerpt walk wants oldest
	// first, which is exactly the file order retained above.
	return msgs, start == 0
}
