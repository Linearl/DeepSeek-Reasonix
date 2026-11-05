package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// This file is tl1396's prefix-cache red line for the compression chain
// (compact / compress / curate). A compression that silently destroys more
// provider prefix-cache than its scenario allows is a cost defect, not a
// refactor detail: every later turn re-pays the destroyed prefix at miss
// prices. The scorer and the red line below are pure functions so the gate
// (scripts/check-compress-cache-floor.sh → go test) can hold every compression
// change against them without a live provider.

// CompressCacheScenario names the compression shape a red-line check applies
// to. The two scenarios carry different lines (task tl1396's three-state
// design):
//
//   - Full ("全量压缩"): the fold rewrites the whole projection — the summary
//     lands right after the fixed head, so every retained tail byte shifts and
//     the pre-compression prefix is broken by theoretical necessity. The
//     ABSOLUTE line does not apply here (state ① exemption); only the RELATIVE
//     floor does (state ②).
//   - Partial ("部分压缩", reserved for tl1397): small-range incremental folds
//     that keep the head and most of the prefix verbatim. The absolute line
//     applies (state ③): ≥95% prefix-cache hit rate, the billion-context
//     benchmark's cost ≤2% regime.
type CompressCacheScenario string

const (
	// CompressScenarioFull is the current projection-rewriting fold
	// (auto compaction + the explicit compress tool).
	CompressScenarioFull CompressCacheScenario = "full"
	// CompressScenarioPartial is tl1397's planned small-range incremental fold.
	CompressScenarioPartial CompressCacheScenario = "partial"
)

// PartialCompressCacheFloorRatio is the absolute red line for partial
// compression (state ③): a small-range incremental fold must leave ≥95% of the
// next request's tokens on the cached prefix. Reserved for tl1397 — the line
// ships now so tl1397's implementation lands against a pinned contract instead
// of an after-the-fact number.
const PartialCompressCacheFloorRatio = 0.95

// fullCompressionCacheFloorRatio is the scenario coefficient for the relative
// drop judgment (state ②): after a full compression, the prefix-cache hit rate
// must stay at or above hitBefore × this coefficient. The value is the
// conservative lower bound measured over the calibration pool in
// TestFullCompressionCacheCalibration (synthetic sessions driven through the
// real projection builder), rounded DOWN to a 0.05 grid so normal samples sit
// strictly above the line. Calibration data (2026-11-05, tl1396 first run,
// fallbackTokPerChar-independent rune metric):
//
//	samples=240  min ratio=0.2487  (min landed on the tight-head/wide-tail
//	corner: a small fixed head over a heavily compressed body, where the
//	retained head is the only surviving prefix)
//
// so the line is 0.20. Any change to the fold that eats into the head, or
// inflates the projection so the retained prefix shrinks relative to the
// request, breaks the line and turns the gate red.
const fullCompressionCacheFloorRatio = 0.20

// PrefixCacheHitRate estimates the share of `request`'s provider-visible
// tokens a provider prefix cache can still serve, given the bytes the cache
// already holds (`cached`, typically the previous request's prefix).
//
// 口径 (the measurable reading the task pins down): serialize both sides to
// their deterministic provider-visible rune stream — the same field set
// providerVisibleFingerprint covers — and score longest-common-prefix runes
// over the request's total runes. The rune stream stands in for the provider's
// token stream; a hit rate is a ratio of like units, so the substitution does
// not move the ratio, and it keeps the scorer deterministic and
// tokenizer-independent. The metric is conservative: a mid-message cache hit
// after a divergent byte would count fully on a real provider but only
// partially here, so red-line passes are not flattered by the proxy.
//
// Pure: no agent state, no provider calls.
func PrefixCacheHitRate(cached, request []provider.Message) float64 {
	reqRunes := []rune(prefixCacheWireText(modelInputMessages(request)))
	if len(reqRunes) == 0 {
		return 1
	}
	cachedRunes := []rune(prefixCacheWireText(modelInputMessages(cached)))
	return float64(commonPrefixLen(cachedRunes, reqRunes)) / float64(len(reqRunes))
}

// CheckCompressCacheRedline judges one measured compression against its
// scenario's line and returns a named violation, or nil when the compression
// is inside its budget. It is the single decision both the three-state tests
// and the gate script's go-test run read, so the two cannot drift apart.
//
// State ① (full scenario exemption) lives in the full branch: the absolute
// 95% line is deliberately NOT consulted there — a full rewrite breaks the
// prefix by theoretical necessity, and holding it to the partial line would
// flag every well-behaved fold as a defect.
func CheckCompressCacheRedline(scenario CompressCacheScenario, hitBefore, hitAfter float64) error {
	// redLineEpsilon absorbs float noise exactly at the line: a caller whose
	// measurement lands on the boundary must not flip to red on rounding.
	const redLineEpsilon = 1e-9
	switch scenario {
	case CompressScenarioFull:
		if hitAfter+redLineEpsilon < hitBefore*fullCompressionCacheFloorRatio {
			return fmt.Errorf(
				"prefix-cache red line (full scenario, relative): hit %.4f < before %.4f × coefficient %.2f — the fold destroyed more cache than its scenario's necessity allows",
				hitAfter, hitBefore, fullCompressionCacheFloorRatio)
		}
		return nil
	case CompressScenarioPartial:
		if hitAfter+redLineEpsilon < PartialCompressCacheFloorRatio {
			return fmt.Errorf(
				"prefix-cache red line (partial scenario, absolute): hit %.4f < %.2f — small-range folds must keep the cached prefix intact (cost ≤2%% regime)",
				hitAfter, PartialCompressCacheFloorRatio)
		}
		return nil
	default:
		return fmt.Errorf("prefix-cache red line: unknown scenario %q", scenario)
	}
}

// commonPrefixLen returns the length of the shared rune prefix.
func commonPrefixLen(a, b []rune) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// prefixCacheWireText renders messages as the deterministic byte stream a
// provider would cache: role + content + tool calls + the reasoning/search
// surfaces, in providerVisibleFingerprint's field order, with a boundary
// sentinel between messages so a prefix cannot silently bridge two messages.
// LocalOnly and RawContent never ride to a provider, so they never enter the
// stream (modelInputMessages already projects them away; the explicit checks
// below keep the function safe when called with raw storage rows).
func prefixCacheWireText(msgs []provider.Message) string {
	var b strings.Builder
	b.Grow(charsOfMessages(msgs) + 64*len(msgs))
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		b.WriteString("\x00msg\x00")
		b.WriteString(string(m.Role))
		b.WriteByte('\x00')
		b.WriteString(m.Content)
		if m.ReasoningContent != "" {
			b.WriteString("\x00rc\x00")
			b.WriteString(m.ReasoningContent)
		}
		if m.ReasoningID != "" {
			b.WriteString("\x00rid\x00")
			b.WriteString(m.ReasoningID)
		}
		if m.ToolCallID != "" {
			b.WriteString("\x00tid\x00")
			b.WriteString(m.ToolCallID)
		}
		if m.Name != "" {
			b.WriteString("\x00n\x00")
			b.WriteString(m.Name)
		}
		for _, tc := range m.ToolCalls {
			b.WriteString("\x00tc\x00")
			b.WriteString(tc.ID)
			b.WriteByte('\x00')
			b.WriteString(tc.Name)
			b.WriteByte('\x00')
			b.WriteString(tc.Arguments)
		}
		for _, img := range m.Images {
			b.WriteString("\x00img\x00")
			b.WriteString(img)
		}
		for _, au := range m.Audio {
			b.WriteString("\x00aud\x00")
			b.WriteString(au)
		}
		for _, tb := range m.ThinkingBlocks {
			if raw, err := json.Marshal(tb); err == nil {
				b.WriteString("\x00tb\x00")
				b.Write(raw)
			}
		}
		for _, ri := range m.ResponsesItems {
			b.WriteString("\x00ri\x00")
			b.Write(ri)
		}
		for _, ss := range m.ServerSearch {
			if raw, err := json.Marshal(ss); err == nil {
				b.WriteString("\x00ss\x00")
				b.Write(raw)
			}
		}
	}
	return b.String()
}
