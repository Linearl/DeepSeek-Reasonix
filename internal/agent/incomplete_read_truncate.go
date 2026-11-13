package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"reasonix/internal/i18n"
)

// truncateReadFileOutput returns a contiguous prefix on a complete rendered
// line when possible. Its recovery cursor is exactly the first unseen byte, so
// paging can reconstruct the original without a gap or duplicate.
func truncateReadFileOutput(s, toolName, toolCallID string) (string, string) {
	return truncateReadFileOutputArchived(s, toolName, toolCallID, "")
}

// truncateReadFileOutputArchived is the archive-aware form (tl1561): the
// marker points at the archived original instead of the retired result_ref
// channel. next_offset stays mechanical — readFileRecoveryOffset parses it.
func truncateReadFileOutputArchived(s, toolName, toolCallID, archivePath string) (string, string) {
	headKeep := maxToolOutputBytes - 1024
	if headKeep < 1024 {
		headKeep = maxToolOutputBytes / 2
	}
	head := snapToRuneBoundary(s, 0, min(headKeep, len(s)))
	if newline := strings.LastIndexByte(head, '\n'); newline >= 1024 {
		head = head[:newline+1]
	}
	for range 4 {
		marker := toolOutputRecoveryMarkerAt(toolName, toolCallID, archivePath, len(s), len(head), len(head))
		if len(head)+len(marker) <= maxToolOutputBytes {
			notice := fmt.Sprintf(i18n.M.ToolOutputTruncatedFmt, len(s)-len(head), len(s))
			return head + marker, notice
		}
		trimTo := len(head) - (len(head) + len(marker) - maxToolOutputBytes)
		if trimTo <= 0 {
			head = ""
			continue
		}
		head = snapToRuneBoundary(head, 0, trimTo)
		if newline := strings.LastIndexByte(head, '\n'); newline >= 0 {
			head = head[:newline+1]
		}
	}
	marker := toolOutputRecoveryMarkerAt(toolName, toolCallID, archivePath, len(s), len(head), len(head))
	if len(marker) > maxToolOutputBytes {
		marker = snapToRuneBoundary(marker, 0, maxToolOutputBytes)
	}
	notice := fmt.Sprintf(i18n.M.ToolOutputTruncatedFmt, len(s)-len(head), len(s))
	return head + marker, notice
}

// snapToRuneBoundary snaps [lo, hi) outward to rune starts and returns that
// slice. It is total over 0 <= lo <= hi <= len(s): lo == len(s) is the empty
// tail — the elision shape where cutChars=1 makes tailChars=0 (truncate.go
// elideOversizedContentsAt, keepHeadTail in agent.go) — and the rune walk must
// not index s[len(s)]. tl1083 found this panic reachable from the chunked
// summary lane once pressure folds could enter it (fragment budget / message
// count below contentElideOversizedDiv); the same shape was already live for
// manual and overflow chunked runs.
func snapToRuneBoundary(s string, lo, hi int) string {
	if hi > len(s) {
		hi = len(s)
	}
	if lo > hi {
		lo = hi
	}
	for lo > 0 && lo < len(s) && !utf8.RuneStart(s[lo]) {
		lo--
	}
	for hi < len(s) && !utf8.RuneStart(s[hi]) {
		hi++
	}
	return s[lo:hi]
}
