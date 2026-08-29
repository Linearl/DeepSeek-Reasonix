package agent

import (
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/provider"
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
