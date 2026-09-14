package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// graftPrefixOntoLines inserts the given messages at the head of a transcript's
// JSONL lines, after any leading system messages.
//
// It works on lines rather than on decoded messages on purpose: every existing
// line is preserved byte for byte, so the graft can only ever add. Re-encoding
// the whole transcript would rewrite fields this code has no business touching,
// and any difference from the writer's own encoding would show up later as a
// spurious conflict against a transcript that did not actually change.
//
// Leading system messages stay in front because they are the session's identity
// preamble (prompt + memory blocks); a turn inserted before them would read as
// the session starting with a user message it never had.
func graftPrefixOntoLines(lines []string, gap []provider.Message) ([]string, error) {
	if len(gap) == 0 {
		return lines, nil
	}
	encoded := make([]string, 0, len(gap))
	for _, m := range gap {
		b, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("graft: encode message: %w", err)
		}
		encoded = append(encoded, string(b))
	}
	insertAt, err := leadingSystemLineCount(lines)
	if err != nil {
		return nil, err
	}
	merged := make([]string, 0, len(lines)+len(encoded))
	merged = append(merged, lines[:insertAt]...)
	merged = append(merged, encoded...)
	merged = append(merged, lines[insertAt:]...)
	return merged, nil
}

// leadingSystemLineCount counts the leading lines that decode to system-role
// messages.
//
// A line that does not decode is an error rather than a stopping point. The
// transcript's head is where the session's identity preamble lives, so an
// unreadable head means the file cannot be trusted to be what it claims; and
// the two ways to continue are both wrong — inserting ahead of unknown content
// puts turns before the preamble, inserting after it puts them in the middle of
// it. A graft that cannot be placed correctly must not happen at all.
func leadingSystemLineCount(lines []string) (int, error) {
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		var m provider.Message
		if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
			return 0, fmt.Errorf("graft: transcript head is not readable: %w", err)
		}
		if m.Role != provider.RoleSystem {
			break
		}
		count++
	}
	return count, nil
}

// splitTranscriptLines splits a transcript's bytes into lines, tolerating both
// LF and CRLF and a trailing newline, so a graft round-trips a file that ends
// with one without inventing an empty line.
func splitTranscriptLines(raw []byte) []string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// joinTranscriptLines is the inverse, always ending with a single newline so the
// next append lands on a fresh line.
func joinTranscriptLines(lines []string) []byte {
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}
