package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// tl1691 zero-summary lane. A normal fold replaces the folded region with a
// model-written digest, which costs one full-prefill summary request (the
// 8~12万 in/次 the tl1679 report measured) and secondhand-fies every fact it
// touches. This lane instead installs a deterministic pointer document: a
// content-hash name (fold-<sha6>) plus a folded-work index whose lines are
// canonical addresses the recall tool reads back verbatim. The canonical
// transcript is untouched — the pointer is a projection-side stand-in, not a
// new artifact family (降级搬: 零新工件、零磁盘增长、零迁移脚本).
//
// The lane is a live read at fold time, like the tl1397 ranges lane: a
// mid-session switch-off cannot un-install a pointer that already landed, but
// the next fold goes back through the summary path, and the projection
// validates against the canonical prefix hash either way.

const (
	// The index is a discovery aid, not the whole region: search covers
	// everything, and unbounded index lines would rebuild the fold the lane
	// exists to shed.
	foldPointerIndexMaxLines = 24
	foldPointerLineMaxRunes  = 110
)

// zeroSummaryFoldEnabled resolves the tl1691 lane: an explicit override wins
// (tests, harnesses), otherwise the live config read decides. Under a test
// binary with no override the lane is forced off, so the regression suite
// never flips lanes on the host machine's config file — the same live read is
// what production folds consult.
func (a *Agent) zeroSummaryFoldEnabled() bool {
	if a.zeroSummaryOverride != nil {
		return *a.zeroSummaryOverride
	}
	if flag.Lookup("test.v") != nil {
		return false
	}
	return config.ZeroSummaryCompactLive()
}

// foldProduct produces the fold's replacement document: a digest on the
// summary lane, or a pointer + index on the tl1691 zero-summary lane.
func (a *Agent) foldProduct(ctx context.Context, trigger string, canonical []provider.Message, pointerFrom, covered int, fold []provider.Message, instructions string, sourceTokens int, inputMode string, req foldRequest) (foldSummary, CompactionTelemetry, error) {
	if a.zeroSummaryFoldEnabled() {
		return a.foldToPointer(trigger, canonical, pointerFrom, covered, fold, sourceTokens, inputMode)
	}
	return a.summarizeFold(ctx, trigger, fold, instructions, sourceTokens, inputMode, req)
}

// foldToPointer builds the zero-summary fold product with no provider request.
func (a *Agent) foldToPointer(trigger string, canonical []provider.Message, pointerFrom, covered int, fold []provider.Message, sourceTokens int, inputMode string) (foldSummary, CompactionTelemetry, error) {
	text, pointerID := buildFoldPointerText(canonical, pointerFrom, covered, len(fold))
	res := foldSummary{
		Text: text, Mode: CompactionModePointer, Pointer: pointerID,
		FoldTokens: a.guardedSummaryInputTokens(fold), InputMode: inputMode,
		// Spans 0: no summarizer call was made or needed.
	}
	tele := CompactionTelemetry{
		Trigger: trigger, CacheState: a.CacheState(), Mode: CompactionModePointer,
		SourceTokens: sourceTokens, FoldTokens: res.FoldTokens,
		SummaryInputMode: inputMode,
		// RequestCount 0: the pointer is host-minted — the lane's whole point.
		// The consumer reads 0 as "no provider spend", which is the savings
		// signal this lane exists to produce.
	}
	return res, tele, nil
}

// buildFoldPointerText renders the pointer document and its content-hash name.
// The hash names the folded region's verbatim content, studio's
// fold-<sha6>.md naming without the file: same determinism, zero new
// artifacts — the name is discoverable from the receipt and the pointer
// message, and recomputable from the transcript alone.
func buildFoldPointerText(canonical []provider.Message, from, to, foldLen int) (string, string) {
	id := foldPointerID(canonical, from, to)
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %d earlier messages were folded without a digest (zero-summary compaction). Their verbatim content is still in this session's transcript.\n\nFolded work index:\n", id, foldLen)
	lines := 0
	from, to = clampSpan(len(canonical), from, to)
	for i := from; i < to && lines < foldPointerIndexMaxLines; i++ {
		m := canonical[i]
		if m.LocalOnly {
			continue
		}
		switch m.Role {
		case provider.RoleUser:
			// A prior digest's own text is secondhand; it is folded content
			// for recall's search but not an index line.
			if isCompactionSummary(m) {
				continue
			}
			fmt.Fprintf(&b, "#%d user: %s\n", i, foldPointerHead(UserMessageText(m)))
			lines++
		case provider.RoleAssistant:
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "#%d assistant calls %s: %s\n", i, tc.Name, foldPointerHead(tc.Arguments))
				lines++
				if lines >= foldPointerIndexMaxLines {
					break
				}
			}
		}
	}
	b.WriteString("\nBring any line back with recall(positions=[n]) — it returns the message together with its tool results — or search the folded region with recall(query=\"words\"). One recall budget per compaction; a request past it is refused whole, not truncated.")
	return b.String(), id
}

// foldPointerID names the folded region by the hash of its verbatim render.
func foldPointerID(canonical []provider.Message, from, to int) string {
	from, to = clampSpan(len(canonical), from, to)
	sum := sha256.Sum256([]byte(renderRecallSpan(canonical[from:to])))
	return "fold-" + hex.EncodeToString(sum[:])[:6]
}

// clampSpan bounds [from,to) to the canonical slice.
func clampSpan(n, from, to int) (int, int) {
	if from < 0 {
		from = 0
	}
	if to > n {
		to = n
	}
	if to < from {
		to = from
	}
	return from, to
}

// foldPointerHead is one index line's preview: whitespace-collapsed and cut on
// a rune boundary.
func foldPointerHead(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > foldPointerLineMaxRunes {
		return string(runes[:foldPointerLineMaxRunes]) + "…"
	}
	return s
}
