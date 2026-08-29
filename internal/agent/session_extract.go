package agent

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// #9082 - extract structured highlights from an over-length/unusable session
// into a fresh conversation. The canonical transcript is summarized in
// exponentially decaying chunks (newest fragment first, so recent work keeps
// the finest granularity), each summarized with exactly one provider call,
// and the partial digests are merged into a single compaction-summary that a
// fresh session can adopt as its resume briefing.

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
	for j := len(spans) - 1; j >= 0; j-- { // oldest first
		chunks = append(chunks, msgs[spans[j].lo:spans[j].hi])
	}
	return chunks
}

// ExtractHighlights summarizes the canonical transcript into one
// compaction-summary without touching the live projection or session state.
// progress, when non-nil, reports (chunks summarized, total chunks).
func (a *Agent) ExtractHighlights(ctx context.Context, progress func(done, total int)) (string, error) {
	sess := a.Session()
	if sess == nil {
		return "", fmt.Errorf("session transcript is empty")
	}
	msgs := sess.Snapshot()
	if len(msgs) == 0 {
		return "", fmt.Errorf("session transcript is empty")
	}
	chunks := splitExtractChunks(msgs, extractChunkOverlapBytes)
	if len(chunks) == 0 {
		return "", fmt.Errorf("session transcript is empty")
	}
	report := orNoopProgress(progress)
	if len(chunks) == 1 {
		// Fast path: a single fragment is exactly one compaction request -
		// reuse the canonical template verbatim so the output is directly
		// comparable to a normal compaction summary.
		res, err := a.foldToSummary(ctx, chunks[0], compactionInstruction)
		if err != nil {
			return "", err
		}
		report(1, 1)
		return res.Text, nil
	}
	parts := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		instructions := fmt.Sprintf(extractFragmentInstructionTmpl, i+1, len(chunks))
		res, err := a.foldToSummary(ctx, chunk, instructions)
		if err != nil {
			return "", fmt.Errorf("fragment %d/%d: %w", i+1, len(chunks), err)
		}
		report(i+1, len(chunks))
		parts = append(parts, strings.TrimSpace(res.Text))
	}
	merged, err := a.foldToSummary(ctx, mergeDigestMessages(parts), extractMergeInstruction)
	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	return merged.Text, nil
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
			Content: fmt.Sprintf("<fragment index=%q>\n%s\n</fragment>", i+1, part),
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
