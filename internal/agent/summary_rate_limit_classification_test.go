package agent

import (
	"testing"
)

// tl1716: the bare "429" probe inside summaryRateLimited was a substring
// match, so any larger number containing "429" — the field case was a
// context-overflow 400 carrying "requested 42953 tokens" — classified as a
// rate limit and parked the summary lane on a 60s wait. The tool-description
// growth in this branch pushed the overflow token count past exactly that
// substring window (42565 → 42953) and the EffectSummaryOverflow test burned
// its 120s deadline on two waits. The fix matches a standalone status number
// only; these pins are red-before/green-after on the field text.
func TestTL1716OverflowNumberIsNotRateLimited(t *testing.T) {
	// The verbatim field shape: status 400 whose message embeds a token count
	// starting with 429.
	overflow := `boot-dense-summary: status 400: {"error":{"message":"This model's maximum context length is 40000 tokens. However, you requested 42953 tokens (34761 in the messages, 8192 in the completion). Please reduce the length of the messages or completion.","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`
	if summaryRateLimited(errString(overflow)) {
		t.Fatal("a context-overflow 400 carrying 42953 must not classify as a rate limit")
	}
	if summaryRateLimited(errString("requested 4290 tokens")) ||
		summaryRateLimited(errString("total 15294 tokens")) {
		t.Fatal("any larger number containing 429 must not classify as a rate limit")
	}
}

// The bare-429 intent must survive the narrowing: standalone status frames in
// shapes httpStatusOf's "status "/"http "/"code " markers do NOT cover still
// classify as a rate limit.
func TestTL1716Standalone429StillRateLimited(t *testing.T) {
	for _, text := range []string{
		`HTTP 429`,
		`gateway answered [429] with an empty body`,
		`{"error":{"code":429,"type":"rate_limit_error"}}`,
		`429 without any other marker`,
		`status: 429, retry later`,
	} {
		if !summaryRateLimited(errString(text)) {
			t.Fatalf("standalone 429 must stay rate-limited: %q", text)
		}
	}
}

func TestTL1716ContainsStatusNumberUnit(t *testing.T) {
	positives := []string{
		"HTTP 429", `"code":429`, "[429]", " 429 ", "429.", "x429x",
	}
	for _, text := range positives {
		if !containsStatusNumber(text, "429") {
			t.Fatalf("standalone number must hit: %q", text)
		}
	}
	negatives := []string{
		"42953", "4290", "15294", "4294295", "42900", "1429",
	}
	for _, text := range negatives {
		if containsStatusNumber(text, "429") {
			t.Fatalf("embedded number must not hit: %q", text)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
