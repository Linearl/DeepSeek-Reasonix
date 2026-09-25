package agent

import (
	"fmt"
	"strings"
)

// Task 286 (upstream #10721): a provider content-safety layer can replace a
// generated answer with one fixed sentence delivered over the NORMAL content
// stream, e.g. "The request was rejected because it was considered high
// risk". handleFinalResponse treated that sentence as the model's visible
// final answer and ended the turn — no recognition, no retry, no notice —
// even when the input was a zero-risk query (measured false positive,
// 2026-09-21). The string never appears in our source (verified against
// upstream desktop-v1.38.12 too): it is provider-side, so it must be matched
// here, not filtered at the provider layer.
//
// Matching is whole-sentence or strict-prefix after trimming — a fixed
// template, not a keyword scan, so a normal answer that merely mentions the
// words cannot be misclassified (the false-positive budget stated in
// #10721).
var knownProviderRejectionTemplates = []string{
	// First measured rejection (task 286 / #10721). Append future provider
	// variants here as they are observed; the set is intentionally a list of
	// complete sentences, never substrings.
	"The request was rejected because it was considered high risk",
}

// maxRejectedTemplateBlocks bounds one rejection cycle: the first hit replays
// once (one bounded retry, distinct from the empty-final budget), the second
// hit ends the run as an error instead of delivering the template as an
// answer. Mirrors maxEmptyFinalBlocks' "attempt then stop" shape at a lower
// ceiling because a repeated rejection is provider-side and replaying more
// cannot change it.
const maxRejectedTemplateBlocks = 2

// isKnownProviderRejection reports whether text is (or begins with) a known
// fixed provider rejection sentence.
func isKnownProviderRejection(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	for _, tpl := range knownProviderRejectionTemplates {
		if trimmed == tpl || strings.HasPrefix(trimmed, tpl) {
			return true
		}
	}
	return false
}

// providerRejectionNotice is the transcript line that replaces silent
// delivery (task 286 phase 1). Deliberately explicit and searchable: the
// model did not answer — the provider's safety layer did.
func providerRejectionNotice() string {
	return "The provider's safety layer replaced the answer with a fixed rejection sentence; Reasonix is replaying the request once instead of delivering it."
}

func providerRejectionDetail(providerName, matched string) string {
	return fmt.Sprintf("provider rejection template matched %q (provider %s)", matched, providerName)
}

// providerRejectionRetryMessage is the host-generated instruction for the
// replay round. It names the cause so the model does not treat the rejection
// as its own failure, and asks for a shape less likely to trip the filter
// (shorter / split), bounded to one replay.
func providerRejectionRetryMessage() string {
	return "The provider rejected the previous reply with a fixed safety sentence before your answer reached the session. This is a provider-side filter, not a failed step. Replay the answer once: keep it shorter, split the response if needed, and avoid quoting blocked wording. If the provider rejects again, end the turn with a brief error summary instead of retrying further."
}
