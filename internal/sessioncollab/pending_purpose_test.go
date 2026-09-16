package sessioncollab

import "testing"

// Task 144: a session created by a secretary has no transcript yet, so its
// purpose has to wait for the file path. The store must survive that gap and
// clear only the entry it applied.
func TestPendingPurposeStoreLifecycle(t *testing.T) {
	store := NewPendingPurposeStore(t.TempDir())
	if err := store.Set("topic_a", "frontend expert"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("topic_b", "backend expert"); err != nil {
		t.Fatal(err)
	}
	got := store.List()
	if got["topic_a"] != "frontend expert" || got["topic_b"] != "backend expert" {
		t.Fatalf("list: %+v", got)
	}
	if err := store.Clear("topic_a"); err != nil {
		t.Fatal(err)
	}
	after := store.List()
	if _, ok := after["topic_a"]; ok {
		t.Fatalf("cleared entry came back: %+v", after)
	}
	if after["topic_b"] != "backend expert" {
		t.Fatalf("clearing one entry dropped another: %+v", after)
	}
	// Clearing an absent entry is a no-op, so a retry after a crash is safe.
	if err := store.Clear("topic_a"); err != nil {
		t.Fatalf("clear of absent entry must be a no-op: %v", err)
	}
}

func TestPendingPurposeStoreRejectsEmptyInputs(t *testing.T) {
	store := NewPendingPurposeStore(t.TempDir())
	if err := store.Set("", "x"); err == nil {
		t.Fatal("empty topic id must be rejected")
	}
	if err := store.Set("topic", "  "); err == nil {
		t.Fatal("empty purpose must be rejected")
	}
}
