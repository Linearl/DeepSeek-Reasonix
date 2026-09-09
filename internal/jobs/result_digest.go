package jobs

import "strings"

// resultDigestLimit caps the completion-note digest; runes keep the cut
// UTF-8-safe for CJK output.
const resultDigestLimit = 400

// resultDigestOf extracts a short leading excerpt of a finished job's result
// for the completion note. Only successful jobs carry a digest — failures
// already surface their error text.
func resultDigestOf(st Status, result string) string {
	if st != Done || strings.TrimSpace(result) == "" {
		return ""
	}
	trimmed := strings.TrimSpace(result)
	runes := []rune(trimmed)
	if len(runes) > resultDigestLimit {
		trimmed = string(runes[:resultDigestLimit]) + "…"
	}
	// Collapse newlines so the note stays a single readable line.
	return strings.ReplaceAll(trimmed, "\n", " ")
}

// resultDigest returns the completion-note digest for a finished job. The
// terminal status is passed in because the job's own status is published only
// after recordCompletion has queued the note (fork Phase 1b, #9522).
func (j *Job) resultDigest(st Status) string {
	if j == nil {
		return ""
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return resultDigestOf(st, j.result)
}
