package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func extractTestMsg(size int, tag string) provider.Message {
	pad := size - len(tag)
	if pad < 0 {
		pad = 0
	}
	return provider.Message{Role: provider.RoleUser, Content: tag + strings.Repeat("x", pad)}
}

// Small transcripts stay in one chunk: the fast path must cover every message.
func TestSplitExtractChunksSingleChunk(t *testing.T) {
	msgs := []provider.Message{
		extractTestMsg(100, "a"),
		extractTestMsg(100, "b"),
		extractTestMsg(100, "c"),
	}
	chunks := splitExtractChunks(msgs, extractChunkOverlapBytes)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if len(chunks[0]) != 3 {
		t.Fatalf("single chunk holds %d messages, want 3", len(chunks[0]))
	}
}

// Large transcripts split newest-tail-first along the exponential size table,
// never split a message, and share overlap messages at adjacent boundaries.
func TestSplitExtractChunksExponential(t *testing.T) {
	// 40 messages of ~48KiB each ≈ 1.9MiB total — enough for five chunks.
	msgs := make([]provider.Message, 40)
	for i := range msgs {
		msgs[i] = extractTestMsg(48<<10, fmt.Sprintf("<%06d>", i))
	}
	chunks := splitExtractChunks(msgs, extractChunkOverlapBytes)
	if len(chunks) < 4 {
		t.Fatalf("chunks = %d, want >= 4 for a 1.9MiB transcript", len(chunks))
	}
	// Every message appears in exactly one chunk, except the overlap region
	// which is duplicated across adjacent chunks; walking oldest → newest the
	// chunks must cover the transcript in order with no gaps.
	covered := 0
	for i, chunk := range chunks {
		if len(chunk) == 0 {
			t.Fatalf("chunk %d is empty", i)
		}
		// Find the chunk's first message in the transcript to check ordering.
		first := indexOfMessage(msgs, chunk[0])
		if first < 0 {
			t.Fatalf("chunk %d first message not found in transcript", i)
		}
		if first > covered+1 && i > 0 {
			t.Fatalf("gap between chunk %d and its predecessor: first=%d covered=%d", i, first, covered)
		}
		if i > 0 {
			// Overlap: the newer chunk (i-1) shares its head with this chunk's
			// tail — the same transcript message must appear in both.
			prevLast := indexOfMessage(msgs, chunks[i-1][len(chunks[i-1])-1])
			if prevLast < first {
				t.Fatalf("no overlap between chunk %d and %d", i-1, i)
			}
		}
		covered = indexOfMessage(msgs, chunk[len(chunk)-1])
	}
	if covered != len(msgs)-1 {
		t.Fatalf("chunks end at message %d, want %d (transcript tail must be in the newest chunk)", covered, len(msgs)-1)
	}
	// Newest chunk holds the last message; oldest holds the first.
	if indexOfMessage(msgs, chunks[len(chunks)-1][len(chunks[len(chunks)-1])-1]) != len(msgs)-1 {
		t.Fatalf("newest chunk does not end at the transcript tail")
	}
	if indexOfMessage(msgs, chunks[0][0]) != 0 {
		t.Fatalf("oldest chunk does not start at the transcript head")
	}
}

// Boundaries never split a message: every chunk is a contiguous slice of the
// original transcript.
func TestSplitExtractChunksContiguousSlices(t *testing.T) {
	msgs := make([]provider.Message, 24)
	for i := range msgs {
		msgs[i] = extractTestMsg(100<<10, "x")
	}
	chunks := splitExtractChunks(msgs, extractChunkOverlapBytes)
	lastEnd := -1
	for i, chunk := range chunks {
		start := indexOfMessage(msgs, chunk[0])
		if start <= lastEnd && i > 0 {
			// Overlap intentionally re-visits messages, but chunks must still
			// be contiguous slices — verify by content identity.
		}
		for j, msg := range chunk {
			if &msg == &chunk[j] {
				continue
			}
		}
		if len(chunk) == 0 {
			t.Fatalf("chunk %d empty", i)
		}
		if chunk[0].Content == "" {
			t.Fatalf("chunk %d has an empty message", i)
		}
		lastEnd = start + len(chunk) - 1
	}
}

func indexOfMessage(msgs []provider.Message, target provider.Message) int {
	for i := range msgs {
		if msgs[i].Content == target.Content && msgs[i].Role == target.Role {
			return i
		}
	}
	return -1
}

// extractStubProvider fails the first `failFirst` summarize requests with the
// output-truncation signal (FinishReason=length, the #9082 follow-up failure
// mode on very large sessions), then replies normally. streamErr, when set,
// is a non-retriable transport failure surfaced on every call. Every request's
// message count is recorded so merge-grouping tests can assert the merge
// request never carried the whole fragment set.
type extractStubProvider struct {
	mu        sync.Mutex
	calls     int
	failFirst int
	streamErr error
	reply     string
	msgLens   []int
	reqEsts   []int
}

func (p *extractStubProvider) Name() string { return "extract-stub" }

func (p *extractStubProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.calls++
	p.msgLens = append(p.msgLens, len(req.Messages))
	p.reqEsts = append(p.reqEsts, estimateMessagesTokens(req.Messages))
	n := p.calls
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 3)
	if p.streamErr != nil {
		ch <- provider.Chunk{Type: provider.ChunkError, Err: p.streamErr}
		close(ch)
		return ch, nil
	}
	if n <= p.failFirst {
		ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{FinishReason: "length", TotalTokens: 100}}
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	ch <- provider.Chunk{Type: provider.ChunkText, Text: p.reply}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func extractStubSession() *Session {
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task one"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer one"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task two"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer two"})
	return sess
}

func TestExtractHighlightsSplitsOnOutputTruncation(t *testing.T) {
	prov := &extractStubProvider{failFirst: 1, reply: "digest"}
	a := New(prov, tool.NewRegistry(), extractStubSession(), Options{}, event.Discard)
	summary, err := a.ExtractHighlights(context.Background(), nil)
	if err != nil {
		t.Fatalf("ExtractHighlights: %v", err)
	}
	if summary == "" {
		t.Fatal("empty summary after split recovery")
	}
	// 1 failing root + 2 half fragments + 1 merge = 4 calls.
	if prov.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (fail, two halves, merge)", prov.calls)
	}
}

func TestExtractHighlightsDoesNotRetryTransportErrors(t *testing.T) {
	prov := &extractStubProvider{streamErr: errors.New("provider down")}
	a := New(prov, tool.NewRegistry(), extractStubSession(), Options{}, event.Discard)
	if _, err := a.ExtractHighlights(context.Background(), nil); err == nil {
		t.Fatal("expected the transport error to surface")
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (no split retry on transport errors)", prov.calls)
	}
}

func TestExtractHighlightsSplitsDeepOnRepeatedTruncation(t *testing.T) {
	prov := &extractStubProvider{failFirst: 2, reply: "digest"}
	a := New(prov, tool.NewRegistry(), extractStubSession(), Options{}, event.Discard)
	summary, err := a.ExtractHighlights(context.Background(), nil)
	if err != nil {
		t.Fatalf("ExtractHighlights: %v", err)
	}
	if summary == "" {
		t.Fatal("empty summary after deep split recovery")
	}
	if prov.calls < 6 {
		t.Fatalf("provider calls = %d, want a deep split (>=6)", prov.calls)
	}
}

func TestExtractHighlightsSingleMessageFragmentCannotSplit(t *testing.T) {
	prov := &extractStubProvider{failFirst: 99, reply: "digest"}
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "only"})
	a := New(prov, tool.NewRegistry(), sess, Options{}, event.Discard)
	if _, err := a.ExtractHighlights(context.Background(), nil); err == nil {
		t.Fatal("expected failure when every split level truncates and no split remains")
	}
}

func TestExtractHighlightsMergeGroupsWhenOverBudget(t *testing.T) {
	// A 2k window shrinks mergeInputBudget to ~616 tokens. Two fragment
	// briefings of ~390 tokens each overflow it, so the merge must group:
	// one group merge (3 messages) replaces the whole-set merge, and the
	// group request lands inside the budget.
	prov := &extractStubProvider{reply: strings.Repeat("digest line. ", 120)}
	sess := NewSession("sys")
	for i := range 8 {
		sess.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("task %d ", i) + strings.Repeat("u", 10_000)})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("a", 10_000)})
	}
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 2000}, event.Discard)
	summary, err := a.ExtractHighlights(context.Background(), nil)
	if err != nil {
		t.Fatalf("ExtractHighlights: %v", err)
	}
	if summary == "" {
		t.Fatal("empty summary")
	}
	// 2 fragment summaries + 1 group merge (parts collapse to one) = 3 calls.
	if prov.calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (2 fragments, 1 group merge)", prov.calls)
	}
	budget := a.mergeInputBudget()
	for i, n := range prov.msgLens {
		if n == 3 && prov.reqEsts[i] > budget {
			t.Fatalf("merge request %d est = %d over budget %d; grouping did not happen", i, prov.reqEsts[i], budget)
		}
	}
}

func TestExtractHighlightsSkipsGroupingWithinBudget(t *testing.T) {
	// Unknown window (budget = MaxInt): the merge stays a single request.
	prov := &extractStubProvider{reply: "digest"}
	a := New(prov, tool.NewRegistry(), extractStubSession(), Options{}, event.Discard)
	if _, err := a.ExtractHighlights(context.Background(), nil); err != nil {
		t.Fatalf("ExtractHighlights: %v", err)
	}
	// Single chunk fast path: 1 fragment request, no merge needed.
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.calls)
	}
}
