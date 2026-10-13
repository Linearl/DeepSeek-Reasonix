package agent

import (
	"time"
)

// currentTimeTag is the transient user-turn block carrying the host clock
// (task 664). Long and overnight sessions left the model to infer "now" from
// context history — the drift the user measured at 2+ hours (reported "5 AM"
// at 08:00). Every user turn now carries the authoritative wall clock at its
// head, so a turn that starts after any idle gap re-anchors time perception.
//
// The block rides the user turn head like the other transient blocks: it never
// enters the stable system prompt (a per-turn timestamp there would invalidate
// the cached prefix on every request) and is stripped from previews/titles via
// TransientUserBlockTags. Because each message is written once and never
// rewritten, the growing history stays append-only for prompt-cache purposes —
// only the new tail message changes per request.
const currentTimeTag = "current-time"

// CurrentTimeBlock renders the one-line clock anchor, or "" for a zero time.
// RFC3339 with a numeric zone offset keeps the local wall clock unambiguous;
// the weekday helps scheduling reasoning ("by Friday", "tomorrow morning").
// The short gloss says what the timestamp is, so the model anchors on it
// instead of guessing from older context.
func CurrentTimeBlock(now time.Time) string {
	if now.IsZero() {
		return ""
	}
	return "<" + currentTimeTag + ">" + now.Format("2006-01-02T15:04:00Z07:00 Mon") +
		" — host clock when this turn started</" + currentTimeTag + ">"
}

// WithCurrentTime prefixes content with the transient current-time block,
// unless the turn already starts with one (host reseed must not double-inject,
// mirroring the other With* wrappers).
func WithCurrentTime(content string, now time.Time) string {
	block := CurrentTimeBlock(now)
	if block == "" || hasLeadingInjectedBlock(content, currentTimeTag) {
		return content
	}
	return block + "\n\n" + content
}

// CollabSentTimeText renders one cross-session message's send timestamp in
// the same shape the current-time block uses (RFC3339 with a numeric zone
// offset plus the weekday), so the injected mail body carries the sender's
// wall clock at its head (任务866/1553: storage metadata, the injected text
// and the UI card carried no time at all — three layers, no clock; the user
// could not tell a fresh message from one that waited overnight). Both
// delivery paths — the desktop pump's injection text and the drain tool's
// rendered mail — call this so the format cannot drift. A zero timestamp
// renders "" so malformed records keep their shape instead of printing a
// fake 1970 clock.
func CollabSentTimeText(atMillis int64) string {
	if atMillis <= 0 {
		return ""
	}
	return time.UnixMilli(atMillis).Format("2006-01-02T15:04:00Z07:00 Mon")
}
