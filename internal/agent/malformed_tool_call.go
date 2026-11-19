package agent

import (
	"fmt"
	"regexp"
	"strings"

	"reasonix/internal/i18n"
)

// tl1707 (external issue #103, v1.38.3-20261115-0841, win11, deepseek-v4.1-flash,
// autopilot + Yolo): the model emitted tool-call markup as PLAIN TEXT — bare
// <bash>/<command> tags with an <arg_key> fragment — instead of going through
// the structured tool_calls channel. The model believed it had called tools;
// the host saw a normal final answer, ended the turn with no tool results to
// continue from, and the unattended run stopped silently. The output itself
// even reported normal progress ("改动统计不完整/未记录检查 3 工具"), so there
// was nothing on screen distinguishing the broken turn from a healthy one.
//
// Detection runs in handleFinalResponse, whose call site only fires for rounds
// with ZERO structured tool calls (run_loop.go `len(calls) == 0`) — that is the
// "no matching tool_calls" half of the judgement. The text half scans for bare
// tool-call tags OUTSIDE fenced code blocks and inline code spans, so an
// ordinary answer that shows the XML format as an example (inside ``` fences or
// backtick spans) never trips the detector. The residual false-positive face —
// prose that mentions the tags bare — costs at most one notice plus one bounded
// recovery ask phrased with an explicit "ignore if you meant no tools" escape;
// the false-negative face is the silent stop this task fixes, which is the
// heavier failure, so the detector deliberately errs wide.
//
// Recovery ladder (same discipline as the provider-rejection ladder, tl1314):
// exactly ONE host-injected re-ask round asking the model to re-send the tool
// call in the structured format; a second malformed round recovers nothing and
// the turn ends with the detection notice already on the transcript — never an
// unbounded loop. Deliberately NOT implemented: tolerant host-side parsing and
// execution of the bare markup. The fragments are provider-internal spill
// (arg_key leftovers, truncated argument shapes); executing a guess parsed out
// of them — under autopilot Yolo, without approval — risks running a wrong or
// truncated command, which costs more than the silent stop it would rescue.
var (
	// malformedToolCallTagRe matches bare open/close tags of the tool-call
	// shape seen in the wild (issue #103): <bash>, <command>, <arg_key>,
	// with optional attributes and either closing form. The tag name must be
	// followed by whitespace, a slash, or the closing bracket, so ordinary
	// words never match (<bashism>, <command-x> aside — that shape is itself
	// absurd in prose). Matching is case-insensitive: spilled markup is not
	// guaranteed to be lowercase.
	malformedToolCallTagRe = regexp.MustCompile(`(?i)</?(bash|command|arg_key)(\s[^<>]*)?/?>`)

	// maxMalformedToolCallRecoveries is the hard ceiling on host-injected
	// re-ask rounds per run: exactly 1. Same bounded-by-construction
	// discipline as maxRejectedRecoveryRounds (tl1314) — the second malformed
	// round cannot inject anything.
	maxMalformedToolCallRecoveries = 1
)

// stripCodeSpansForMalformedScan removes fenced code blocks (``` or ~~~, with
// optional language info string) and inline backtick spans so markup shown as
// an EXAMPLE cannot trigger the detector. Odd-position backtick segments are
// treated as code even when a line has an unmatched backtick (the tail after a
// lone backtick is dropped) — erring toward missed detections on that rare
// shape, because the false-positive direction (a normal answer NAGGED with a
// notice) is the cheaper mistake here.
func stripCodeSpansForMalformedScan(text string) string {
	var out strings.Builder
	inFence := false
	var fenceMarker string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			inFence = true
			fenceMarker = trimmed[:3]
			continue
		}
		if inFence && strings.HasPrefix(trimmed, fenceMarker) {
			inFence = false
			continue
		}
		if inFence {
			continue
		}
		// Inline spans: keep even-index segments (outside code), drop odd ones.
		parts := strings.Split(line, "`")
		for i, seg := range parts {
			if i%2 == 0 {
				out.WriteString(seg)
			}
		}
		out.WriteString("\n")
	}
	return out.String()
}

// detectMalformedToolCallText reports the distinct tool-call tag names found as
// bare markup outside code spans. Empty result = no malformed shape (or only
// code-span examples, which is the normal-answer face).
func detectMalformedToolCallText(text string) []string {
	scannable := stripCodeSpansForMalformedScan(text)
	seen := make(map[string]bool)
	var tags []string
	for _, m := range malformedToolCallTagRe.FindAllStringSubmatch(scannable, -1) {
		name := strings.ToLower(m[1])
		if !seen[name] {
			seen[name] = true
			tags = append(tags, name)
		}
	}
	return tags
}

// malformedToolCallNotice is the transcript line that replaces the silent stop
// (tl1707 phase 1). Deliberately explicit: the model tried to call tools, the
// call never arrived as a structured tool_calls entry, and nothing was executed.
func malformedToolCallNotice() string {
	return i18n.M.MalformedToolCall
}

// malformedToolCallDetail names the matched tags and the provider, mirroring
// the emptyFinalNoticeDetail diagnostic style (facts in the detail, short line
// in the notice text).
func malformedToolCallDetail(tags []string, providerName string) string {
	return fmt.Sprintf("malformed tool-call markup detected outside code spans: %s (provider %s); the tool calls were not executed", strings.Join(tags, ", "), providerName)
}

// malformedToolCallRetryNotice is the Info line for the single bounded
// recovery round, mirroring the provider-rejection stage notices.
func malformedToolCallRetryNotice() string {
	return i18n.M.MalformedToolCallRetry
}

// malformedToolCallStageDetail names the recovery position for the audit
// trail, same shape as providerRejectionStageDetail.
func malformedToolCallStageDetail(stage int) string {
	return fmt.Sprintf("recovery %d/%d: re-ask for structured tool_calls", stage, maxMalformedToolCallRecoveries)
}

// malformedToolCallRetryMessage is the single re-ask instruction (tl1707): the
// model must re-send the intended tool call through the real structured
// channel. It carries an explicit escape ("ignore if you meant no tools") so
// the rare false positive costs one cheap round instead of derailing the task.
func malformedToolCallRetryMessage() string {
	return "Your previous reply contained tool-call markup (like <bash>, <command> or <arg_key> tags) as plain text, so the host did not recognize it as a tool call and nothing was executed. If you intended to run tools, re-send them NOW as real structured tool calls through the tool-call channel; do not write the markup as text again. If you did not intend any tool call, ignore this notice and continue your answer directly."
}
