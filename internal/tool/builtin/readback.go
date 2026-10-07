package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// Task 603: readBack — an edit_file/multi_edit call may ask for its result to
// be read back. The tool then renders the edited region plus surrounding
// context (the familiar `   42→…` numbered form) and files the same window as
// a host-side ReadBackObservation, which the agent records as fresh read
// evidence: a same-file follow-up edit passes the write gate against this new
// baseline, while any other change still fails the coverage match.

const (
	// readBackContextLines is the fixed context margin rendered (and filed)
	// above and below the edited region. 任务 603: "相邻区段各 20 行" as a
	// constant — a config knob would widen the provider-visible surface of a
	// default-off experiment for no measured need.
	readBackContextLines = 20
	// maxReadBackWindowLines caps the whole returned (and filed) window. A
	// batch edit spanning more lines keeps its head; coverage beyond the cap
	// degrades safely to the ordinary read requirement instead of widening
	// the tool result without bound.
	maxReadBackWindowLines = 400
	// readBackSpanTruncated marks a window clipped by the line cap.
	readBackSpanTruncated = "…[read-back window truncated; read_file for the rest]…"
)

// splitReadBackLines splits content the way read_file delivers it: one piece
// per line, trailing "\r" stripped (CRLF sources read identically to their LF
// view), and no phantom piece for a trailing newline.
func splitReadBackLines(content string) []string {
	if content == "" {
		return nil
	}
	body := strings.TrimSuffix(content, "\n")
	parts := strings.Split(body, "\n")
	for i, line := range parts {
		parts[i] = strings.TrimSuffix(line, "\r")
	}
	return parts
}

// readBackWindow picks the [start,end) line window (0-based, half-open) to
// render and file for a 1-based inclusive edited span. A deletion (end before
// start) collapses onto its boundary line. Context shrinks at the file edges,
// then the whole window is capped at maxReadBackWindowLines around the span
// head.
func readBackWindow(total, spanStartLine, spanEndLine int) (start, end int, truncated bool) {
	if spanEndLine < spanStartLine {
		spanEndLine = spanStartLine
	}
	spanStart := max(0, spanStartLine-1)
	spanEnd := min(total, spanEndLine)
	if spanEnd < spanStart {
		spanEnd = spanStart
	}
	start = max(0, spanStart-readBackContextLines)
	end = min(total, spanEnd+readBackContextLines)
	if end-start > maxReadBackWindowLines {
		end = start + maxReadBackWindowLines
		truncated = end < min(total, spanEnd+readBackContextLines)
	}
	return start, end, truncated
}

// renderReadBack renders the model-visible read-back block: a header naming
// the shown and edited ranges (spanStart/spanEnd are 1-based inclusive), then
// the numbered window in read_file's `   42→text` form. The observation is
// built from exactly these lines.
func renderReadBack(path string, lines []string, start, end, spanStart, spanEnd int, truncated bool) (string, *tool.ReadBackObservation) {
	if start >= end {
		return "", nil
	}
	trailer := ""
	if truncated {
		trailer = "\n" + readBackSpanTruncated
	}
	var b strings.Builder
	fmt.Fprintf(&b, "read_back %s lines %d-%d (edited lines %d-%d, context ±%d lines)%s\n",
		path, start+1, end, spanStart, spanEnd, readBackContextLines, trailer)
	hashes := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%4d\u2192%s\n", i+1, lines[i])
		sum := sha256.Sum256([]byte(lines[i]))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	return strings.TrimSuffix(b.String(), "\n"), &tool.ReadBackObservation{
		Path:       path,
		StartLine:  start + 1,
		LineHashes: hashes,
	}
}

// withReadBack appends the read-back block to a successful edit result and
// files the observation on the call context. spanStartLine/spanEndLine bound
// the edited region in the NEW content (1-based inclusive; a deletion passes
// end < start and collapses onto its boundary line). snapshot binds the window
// to the exact post-write content version (same derivation as
// editSource.readSnapshot for the route the write took), so the next same-file
// edit's evidence target matches this window — and any other change still
// misses it. An empty content or a degenerate window keeps the result
// untouched.
func withReadBack(ctx context.Context, result, path, content, snapshot string, spanStartLine, spanEndLine int) string {
	lines := splitReadBackLines(content)
	if len(lines) == 0 {
		return result
	}
	if spanStartLine < 1 {
		return result
	}
	if spanEndLine < spanStartLine {
		spanEndLine = spanStartLine
	}
	start, end, truncated := readBackWindow(len(lines), spanStartLine, spanEndLine)
	block, obs := renderReadBack(path, lines, start, end, spanStartLine, spanEndLine, truncated)
	if block == "" || obs == nil {
		return result
	}
	obs.Snapshot = snapshot
	tool.CollectReadBackObservation(ctx, obs)
	if result == "" {
		return block
	}
	return result + "\n" + block
}

// BindReadBack lights the 工具优化 family (task 603) on the edit writers: with
// enabled false — the compile-time and process-cwd default — the tools keep
// their pre-family schemas and behavior byte for byte. The Workspace assembly
// sets the field directly; this binder covers the ConfineWriters rebuild path.
func BindReadBack(tl tool.Tool, enabled bool) tool.Tool {
	if !enabled {
		return tl
	}
	switch t := tl.(type) {
	case editFile:
		t.readBack = true
		return t
	case multiEdit:
		t.readBack = true
		return t
	}
	return tl
}
