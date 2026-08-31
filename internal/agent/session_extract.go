package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"reasonix/internal/provider"
)

// Chunked session recovery summarizes an over-length transcript in
// exponentially decaying fragments and tree-reduces their digests.
// It powers the #9082 in-place compaction fallback.

const (
	// Chunk byte budgets from newest to oldest (design doc 2026-08-18:
	// 会话引用与压缩机制分析). Chunks older than the listed sizes stay at
	// the last size - coarse folding is fine for ancient history.
	extractChunkNewestBytes = 64 << 10
	extractChunkNextBytes   = 128 << 10
	extractChunkThirdBytes  = 256 << 10
	extractChunkFourthBytes = 512 << 10
	extractChunkOldestBytes = 768 << 10
	// Adjacent chunks share this many bytes near their boundary so a fact
	// spanning the cut is not lost between digests.
	extractChunkOverlapBytes = 16 << 10
	minMergeInputTokens      = 400
)

const extractFragmentInstructionTmpl = `This is fragment %d/%d of an over-length session being recovered after its context exceeded the model window. Compact the preceding fragment into a durable briefing under these exact headings, omitting a heading only if it has no content:

## Standing facts & constraints
## Goal
## Decisions & rationale
## Files & code
## Commands & outcomes
## Errors & fixes
## Pending & next step

Rules: be terse - bullet points and fragments, not prose. Preserve identifiers, paths, and numbers exactly. This fragment sits earlier in the session than newer ones will, so capture everything this fragment establishes even if it may be refined later. Do NOT invent anything not present in the messages; if something is unknown, leave it out rather than guessing. Output only the structured Markdown briefing. Do not call tools. Do not output reasoning.`

const extractMergeInstruction = `The following are sequential fragment briefings of one over-length session, ordered oldest to newest. Merge them into the session's final resume briefing under the same exact headings (## Standing facts & constraints / ## Goal / ## Decisions & rationale / ## Files & code / ## Commands & outcomes / ## Errors & fixes / ## Pending & next step). Later fragments supersede earlier ones - keep the final state of every fact, drop superseded entries, and preserve identifiers, paths, and numbers exactly. Output only the structured Markdown briefing. Do not call tools. Do not output reasoning.`

// extractChunkSizes returns the per-chunk byte budgets from newest to oldest.
func extractChunkSizes() []int {
	return []int{
		extractChunkNewestBytes,
		extractChunkNextBytes,
		extractChunkThirdBytes,
		extractChunkFourthBytes,
		extractChunkOldestBytes,
	}
}

// messageWireBytes approximates the transcript footprint of one message -
// the full local original plus any reasoning payload, which is what a
// summary request would have to carry.
func messageWireBytes(msg provider.Message) int {
	n := len(msg.Content) + len(msg.RawContent) + len(msg.ProviderContent) + len(msg.ReasoningContent)
	for _, img := range msg.Images {
		n += len(img)
	}
	return n
}

// splitExtractChunks splits messages newest-tail-first into chunks following
// the exponential size table, with adjacent chunks sharing `overlap` bytes
// around their boundary. Boundaries never split a message: a chunk always
// starts at a message boundary even when that overshoots the budget. Returns
// chunks oldest-first. A transcript smaller than the newest chunk yields a
// single chunk covering everything.
func splitExtractChunks(msgs []provider.Message, overlap int) [][]provider.Message {
	if len(msgs) == 0 {
		return nil
	}
	sizes := extractChunkSizes()
	type span struct{ lo, hi int }
	var spans []span // newest -> oldest
	end := len(msgs)
	for i := 0; end > 0; i++ {
		size := sizes[min(i, len(sizes)-1)]
		if i > 0 {
			size -= overlap // the shared boundary region is counted by the newer chunk
		}
		lo := end
		acc := 0
		for lo > 0 && acc < size {
			lo--
			acc += messageWireBytes(msgs[lo])
		}
		spans = append(spans, span{lo: lo, hi: end})
		end = lo
	}
	// Widen every older chunk's right edge into its newer neighbor's head so
	// the shared overlap bytes are summarized twice, keeping boundary facts
	// in both digests.
	for j := 1; j < len(spans); j++ {
		hi := spans[j].hi // the older chunk ends where the newer one begins
		acc := 0
		for hi < spans[j-1].hi && acc < overlap {
			acc += messageWireBytes(msgs[hi])
			hi++
		}
		spans[j].hi = hi
	}
	chunks := make([][]provider.Message, 0, len(spans))
	for _, current := range slices.Backward(spans) { // oldest first
		chunks = append(chunks, msgs[current.lo:current.hi])
	}
	return chunks
}

// chunkedFoldSummary summarizes a fold too large for one summarizer request:
// byte-budgeted fragments (exponentially decaying, newest finest), each
// summarized with the resilient half-split retry, and the digests merged via
// tree-reduce. It is the compaction fallback for over-length folds (#9082
// #9572 follow-up): the projection still installs in the same session, so
// work continues in place. progress, when non-nil, reports (chunks
// summarized, total chunks); the total grows when a fragment splits.
func (a *Agent) chunkedFoldSummary(ctx context.Context, fold []provider.Message, instructions string, progress func(done, total int)) (foldSummary, error) {
	if len(fold) == 0 {
		return foldSummary{}, fmt.Errorf("fold is empty")
	}
	chunks := splitExtractChunks(fold, extractChunkOverlapBytes)
	if len(chunks) == 0 {
		return foldSummary{}, fmt.Errorf("fold is empty")
	}
	report := orNoopProgress(progress)
	// Fragments may split in half on summarizer failure (see
	// extractFragmentResilient), so the progress total grows as splits happen.
	var progressMu sync.Mutex
	done, total := 0, len(chunks)
	advance := func(grown bool) {
		progressMu.Lock()
		defer progressMu.Unlock()
		if grown {
			total++
		} else {
			done++
		}
		report(done, total)
	}
	parts := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		fragInstr := fmt.Sprintf(extractFragmentInstructionTmpl, i+1, len(chunks))
		res, err := a.extractFragmentResilient(ctx, chunk, fragInstr, advance)
		if err != nil {
			return foldSummary{}, fmt.Errorf("fragment %d/%d: %w", i+1, len(chunks), err)
		}
		parts = append(parts, res)
	}
	text, err := a.mergeFragments(ctx, parts)
	if err != nil {
		return foldSummary{}, err
	}
	return foldSummary{Text: text, Mode: CompactionModeChunked, Spans: len(chunks)}, nil
}

// extractFragmentResilient summarizes one extract fragment, splitting it in
// half and extracting each half when the fragment cannot be summarized whole:
// a very large fragment makes the summarizer output run into the provider's
// output-token limit (the same failure that blocks in-place compaction on
// over-length sessions), and on small-window models the fragment itself can
// overflow the input window. Both are fixed by smaller fragments, so the
// halves are extracted and their digests merged; depth is bounded by the
// message count. report(true) grows the progress total (one fragment became
// two); report(false) marks one leaf fragment summarized.
func (a *Agent) extractFragmentResilient(ctx context.Context, chunk []provider.Message, instructions string, report func(grown bool)) (string, error) {
	res, err := a.foldToSummary(ctx, chunk, instructions)
	if err == nil {
		return strings.TrimSpace(res.Text), nil
	}
	retriable := errors.Is(err, errSummaryOutputTruncated) || errors.Is(err, ErrCompactionRequired)
	if !retriable || len(chunk) < 2 {
		return "", err
	}
	report(true)
	mid := len(chunk) / 2
	left, err := a.extractFragmentResilient(ctx, chunk[:mid], instructions, report)
	if err != nil {
		return "", err
	}
	right, err := a.extractFragmentResilient(ctx, chunk[mid:], instructions, report)
	if err != nil {
		return "", err
	}
	merged, err := a.mergeFragments(ctx, []string{left, right})
	if err != nil {
		return "", fmt.Errorf("merge split fragments: %w", err)
	}
	return merged, nil
}

// mergeInputBudget is the merge-request input ceiling in tokens: half of the
// safe summarizer input, leaving room for the merge instruction and the
// digest output inside the same request. An unknown window disables the
// pre-splitting (the request fails into the provider's own error then).
func (a *Agent) mergeInputBudget() int {
	window := a.effectiveContextWindow()
	if window <= 0 {
		return math.MaxInt
	}
	return max(minMergeInputTokens, (window-a.summaryOutputBudget()-protocolReserveTokens)/2)
}

// mergeGroup merges one group of fragment briefings. A group that cannot be
// summarized whole (output truncation, input overflow) splits in half and
// recurses — the briefings are already in hand, so the merge must not fail
// with them discarded (#9082 follow-up).
func (a *Agent) mergeGroup(ctx context.Context, group []string) (string, error) {
	merged, err := a.foldToSummary(ctx, mergeDigestMessages(group), extractMergeInstruction)
	if err == nil {
		return strings.TrimSpace(merged.Text), nil
	}
	retriable := errors.Is(err, errSummaryOutputTruncated) || errors.Is(err, ErrCompactionRequired)
	if !retriable || len(group) < 2 {
		return "", err
	}
	mid := len(group) / 2
	left, err := a.mergeGroup(ctx, group[:mid])
	if err != nil {
		return "", err
	}
	right, err := a.mergeGroup(ctx, group[mid:])
	if err != nil {
		return "", err
	}
	return a.mergeGroup(ctx, []string{left, right})
}

// mergeFragments merges fragment briefings into one final briefing. When the
// whole set would overflow the merge request (many fragments, or a small
// provider window), it is merged pairwise first — tree-reduce, so the merge
// never fails with every fragment briefing already in hand.
func (a *Agent) mergeFragments(ctx context.Context, parts []string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("no fragment briefings to merge")
	}
	parts = append([]string(nil), parts...)
	for len(parts) > 1 && estimateMessagesTokens(mergeDigestMessages(parts)) > a.mergeInputBudget() {
		var next []string
		for i := 0; i < len(parts); i += 2 {
			group := parts[i:min(i+2, len(parts))]
			if len(group) == 1 {
				// Odd tail: carried into the next round unchanged.
				next = append(next, group[0])
				continue
			}
			merged, err := a.mergeGroup(ctx, group)
			if err != nil {
				return "", err
			}
			next = append(next, merged)
		}
		if len(next) >= len(parts) {
			break // defensive: cannot shrink further; try the plain merge
		}
		parts = next
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	merged, err := a.mergeGroup(ctx, parts)
	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	return merged, nil
}

// mergeDigestMessages builds the merge request body: one user message per
// fragment briefing, oldest first, so the summarizer sees the timeline.
func mergeDigestMessages(parts []string) []provider.Message {
	msgs := make([]provider.Message, 0, len(parts)+1)
	msgs = append(msgs, provider.Message{
		Role:    provider.RoleUser,
		Content: fmt.Sprintf("Session fragment briefings (%d fragments, oldest to newest):", len(parts)),
	})
	for i, part := range parts {
		msgs = append(msgs, provider.Message{
			Role:    provider.RoleUser,
			Content: fmt.Sprintf("<fragment index=%d>\n%s\n</fragment>", i+1, part),
		})
	}
	return msgs
}

func orNoopProgress(progress func(done, total int)) func(done, total int) {
	if progress != nil {
		return progress
	}
	return func(done, total int) {}
}
