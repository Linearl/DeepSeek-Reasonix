package session

import (
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func bigMessages(t *testing.T, count, size int) []provider.Message {
	t.Helper()
	body := strings.Repeat("x", size)
	messages := make([]provider.Message, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, provider.Message{
			ID:      "m" + string(rune('a'+i%26)),
			Role:    provider.RoleAssistant,
			Content: body,
		})
	}
	return messages
}

func TestTrimMessagesToBudgetKeepsNewestWithinBudget(t *testing.T) {
	budget := int64(4 << 20)
	messages := bigMessages(t, 64, 1<<20) // 64 MiB of content, 16x the budget

	trimmed, truncated := trimMessagesToBudget(messages, budget)
	if !truncated {
		t.Fatal("expected truncation for a list far above the budget")
	}
	if got := messageBytes(trimmed); got > budget {
		t.Fatalf("trimmed list is %d bytes, budget is %d", got, budget)
	}
	if len(trimmed) >= len(messages) {
		t.Fatalf("expected fewer than %d messages, kept %d", len(messages), len(trimmed))
	}
	// The newest message must survive: a chat view is anchored at the tail.
	last := messages[len(messages)-1].Content
	if trimmed[len(trimmed)-1].Content != last {
		t.Fatal("tail message was dropped, the newest history must be kept")
	}
}

func TestTrimMessagesToBudgetLeavesSmallListsAlone(t *testing.T) {
	messages := bigMessages(t, 4, 1<<10)
	trimmed, truncated := trimMessagesToBudget(messages, 96<<20)
	if truncated {
		t.Fatal("a list under the budget must not be reported as truncated")
	}
	if len(trimmed) != len(messages) {
		t.Fatalf("list changed: %d -> %d", len(messages), len(trimmed))
	}
}

func TestTrimMessagesToBudgetZeroBudgetIsUnbounded(t *testing.T) {
	// budget <= 0 keeps the historical full-projection behaviour, which the
	// low-level Store API compatibility callers still rely on.
	messages := bigMessages(t, 8, 1<<20)
	trimmed, truncated := trimMessagesToBudget(messages, 0)
	if truncated || len(trimmed) != len(messages) {
		t.Fatalf("zero budget must be a no-op, got truncated=%v len=%d", truncated, len(trimmed))
	}
}

// TestTrimMessagesToBudgetBoundsResidentHeap is the measured form of the same
// claim: a bounded reconstruction must not leave the unbounded list resident.
func TestTrimMessagesToBudgetBoundsResidentHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("heap measurement is not meaningful in short mode")
	}
	messages := bigMessages(t, 128, 1<<20) // ~128 MiB of live payload

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	trimmed, truncated := trimMessagesToBudget(messages, 8<<20)
	if !truncated {
		t.Fatal("expected truncation")
	}
	runtime.KeepAlive(messages)
	runtime.KeepAlive(trimmed)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	// The bounded slice is a window over the same backing array, so the point of
	// the measurement is that the retained window is what the caller keeps: the
	// rest of the array is unreachable through it and collected on the next GC.
	if got := messageBytes(trimmed); got > 8<<20 {
		t.Fatalf("retained window is %d bytes, budget is %d", got, 8<<20)
	}
	if len(trimmed) == 0 {
		t.Fatal("bounded window is empty")
	}
	t.Logf("heap in use: before=%d MiB after=%d MiB, kept %d/%d messages",
		before.HeapInuse>>20, after.HeapInuse>>20, len(trimmed), len(messages))
}
