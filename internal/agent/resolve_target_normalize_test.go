package agent

import (
	"errors"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// Task 243 A6 (sub-report 01-④3): topicKey-style normalization plus the
// purpose find-or-reuse key. A hit relays to the existing session (ResolveTarget
// never creates); "PR 1741" and "pr_1741" must address the same conversation.

func TestNormalizeCollabRef(t *testing.T) {
	cases := []struct{ in, want string }{
		{"PR 1741", "pr_1741"},
		{"  pr_1741  ", "pr_1741"},
		{"pr_1741", "pr_1741"},
		{"Sc_ABC-123", "sc_abc_123"},
		{"  ", ""},
		{"A  B??C", "a_b_c"},
	}
	for _, tc := range cases {
		if got := normalizeCollabRef(tc.in); got != tc.want {
			t.Errorf("normalizeCollabRef(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The ruling pair from the task text: both directions address one session.
	if normalizeCollabRef("PR 1741") != normalizeCollabRef("pr_1741") {
		t.Fatal("PR 1741 and pr_1741 must fold to the same key")
	}
}

func TestResolveTargetNormalizesBothSides(t *testing.T) {
	ids := []sessioncollab.Identity{
		{ContactID: "sc_a", TopicID: "tp_1", Title: "pr_1741 修复合并", Purpose: "review upstream"},
		{ContactID: "sc_b", TopicID: "tp_2", Title: "日常杂务", Purpose: "daily chores"},
	}

	// Title key, both directions of the ruling pair.
	got, err := ResolveTarget(ids, "PR 1741 修复合并")
	if err != nil || got.TopicID != "tp_1" {
		t.Fatalf("normalized title forward: got %+v err=%v", got, err)
	}
	got, err = ResolveTarget(ids, "pr_1741")
	if err != nil || got.TopicID != "tp_1" {
		t.Fatalf("normalized title backward: got %+v err=%v", got, err)
	}

	// Topic key normalized too.
	got, err = ResolveTarget(ids, "TP 1")
	if err != nil || got.TopicID != "tp_1" {
		t.Fatalf("normalized topic: got %+v err=%v", got, err)
	}

	// Purpose joins as the find-or-reuse key when title misses.
	got, err = ResolveTarget(ids, "Daily-Chores")
	if err != nil || got.TopicID != "tp_2" {
		t.Fatalf("purpose key: got %+v err=%v", got, err)
	}

	// Exact contact match keeps working (compat).
	got, err = ResolveTarget(ids, "sc_a")
	if err != nil || got.TopicID != "tp_1" {
		t.Fatalf("contact key: got %+v err=%v", got, err)
	}

	// Unknown still reports the actionable not-found error.
	if _, err := ResolveTarget(ids, "no_such_ref"); !errors.Is(err, sessioncollab.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestResolveTargetNormalizationAmbiguityListsCandidates(t *testing.T) {
	// Two sessions whose titles normalize to the same key must surface the
	// multi-match error (never a silent wrong pick).
	ids := []sessioncollab.Identity{
		{ContactID: "sc_a", TopicID: "tp_1", Title: "PR 1741"},
		{ContactID: "sc_b", TopicID: "tp_2", Title: "pr 1741"},
	}
	_, err := ResolveTarget(ids, "pr_1741")
	if err == nil || !strings.Contains(err.Error(), "matches 2 sessions") {
		t.Fatalf("normalization collision must be an ambiguity error, got %v", err)
	}
	if !strings.Contains(err.Error(), "tp_1") || !strings.Contains(err.Error(), "tp_2") {
		t.Fatalf("ambiguity must list both candidates, got %v", err)
	}
}
