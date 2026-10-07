package builtin

import (
	"fmt"
	"strconv"
	"strings"
)

// Line-range variant of edit_file (task 207). It lets a model replace or
// delete a block by line numbers instead of re-emitting the whole old text,
// with two guards against the classic line-number failure mode (SWE-agent,
// NeurIPS 2024 ACI): a required source_token binds the edit to the exact
// version the model read, and optional anchor_head/anchor_tail prefixes
// catch drifted line numbers before the wrong block is touched.

// anchorPrefixMatch compares an anchor against a line's content prefix,
// insensitive to leading/trailing whitespace and the line terminator:
// indentation is formatting, not identity, so it must not trigger a drift
// rejection — but any content mismatch does.
func anchorPrefixMatch(line, anchor string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.TrimRight(line, "\r")), strings.TrimSpace(anchor))
}

// parseLineRange parses the "278-292" (1-based, inclusive) line_range value.
func parseLineRange(raw string) (start, end int, err error) {
	parts := strings.SplitN(raw, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(`invalid line_range %q: use "start-end" (1-based, inclusive, e.g. "278-292")`, raw)
	}
	start, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || start < 1 {
		return 0, 0, fmt.Errorf(`invalid line_range %q: start must be a positive line number`, raw)
	}
	end, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || end < start {
		return 0, 0, fmt.Errorf(`invalid line_range %q: end must be a line number >= start`, raw)
	}
	return start, end, nil
}

// applyLineRangeEdit replaces the 1-based inclusive line range in content
// with replacement (empty = delete the range) and returns the updated
// content plus the number of replacement lines inserted. anchorHead and
// anchorTail, when non-empty, must prefix-match the first/last line of the
// range (terminator trimmed) — a mismatch is a drift rejection, never a
// fuzzy fix, and error text names only the fact and the recovery, never the
// line contents.
func applyLineRangeEdit(content, lineRange, anchorHead, anchorTail, replacement string) (updated string, inserted int, err error) {
	start, end, err := parseLineRange(lineRange)
	if err != nil {
		return "", 0, err
	}
	lines := strings.Split(content, "\n")
	total := len(lines)
	if total > 0 && lines[total-1] == "" {
		total-- // trailing newline: the virtual element is not a line
	}
	if end > total {
		return "", 0, fmt.Errorf("line_range %q exceeds the file: it currently has %d lines; re-read it and retry", lineRange, total)
	}
	if anchorHead != "" && !anchorPrefixMatch(lines[start-1], anchorHead) {
		return "", 0, fmt.Errorf("line range drift: line %d does not start with the expected anchor_head; line numbers have drifted since your read — re-read the file and retry with fresh line numbers", start)
	}
	if anchorTail != "" && !anchorPrefixMatch(lines[end-1], anchorTail) {
		return "", 0, fmt.Errorf("line range drift: line %d does not start with the expected anchor_tail; line numbers have drifted since your read — re-read the file and retry with fresh line numbers", end)
	}
	crlf := strings.HasSuffix(lines[start-1], "\r")
	replLines := []string{}
	if replacement != "" {
		replLines = strings.Split(strings.TrimSuffix(replacement, "\n"), "\n")
		if crlf {
			for i, line := range replLines {
				if !strings.HasSuffix(line, "\r") {
					replLines[i] = line + "\r"
				}
			}
		}
	}
	// 容量提示只取单一 len（无加减算术，32 位下不可溢出，消除
	// go/allocation-size-overflow）；实际长度由 append 摊销兜底，行为不变。
	out := make([]string, 0, len(lines))
	out = append(out, lines[:start-1]...)
	out = append(out, replLines...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), len(replLines), nil
}

// lineRangeSnippet renders the edited window of the updated content: the
// replaced (or deleted) position with a few context lines on each side, so a
// successful variant edit does not cost an extra confirmation read. The
// window is capped to keep the receipt bounded on large replacements.
func lineRangeSnippet(updated string, start, inserted int) string {
	const (
		contextLines = 3
		maxShown     = 40
	)
	lines := strings.Split(updated, "\n")
	total := len(lines)
	if total > 0 && lines[total-1] == "" {
		total--
	}
	lo := max(1, start-contextLines)
	hi := min(total, start+inserted+contextLines-1)
	if hi-lo+1 > maxShown {
		hi = lo + maxShown - 1
	}
	var b strings.Builder
	for i := lo; i <= hi; i++ {
		fmt.Fprintf(&b, "%6d | %s\n", i, strings.TrimRight(lines[i-1], "\r"))
	}
	return strings.TrimRight(b.String(), "\n")
}
