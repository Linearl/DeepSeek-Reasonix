package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Task 243 A2: the turn-scoped dispatch ledger — record and echo only, never
// a refusal (semantic de-duplication is out of scope by ruling).

func TestDispatchLedgerRecordsResetsAndBounds(t *testing.T) {
	a := New(nil, nil, NewSession(""), Options{}, nil)

	if got := a.RecentDispatches(); got != nil {
		t.Fatalf("empty ledger must read nil, got %v", got)
	}
	a.RecordDispatch("sc_a (alpha)")
	a.RecordDispatch("sc_b (beta)")
	// Immediate duplicate of the last target collapses (a double send is what
	// the echo is FOR — it stays visible as one entry, not an ever-growing
	// stack of the same line).
	a.RecordDispatch("sc_b (beta)")
	if got := a.RecentDispatches(); len(got) != 2 || got[0] != "sc_a (alpha)" || got[1] != "sc_b (beta)" {
		t.Fatalf("ledger = %v, want [sc_a, sc_b]", got)
	}
	// Repeats with different targets stay — that is the double dispatch the
	// caller must see.
	a.RecordDispatch("sc_a (alpha)")
	if got := a.RecentDispatches(); len(got) != 3 {
		t.Fatalf("non-adjacent repeat must remain visible, got %v", got)
	}

	// Bounded: cap keeps only the newest entries.
	for i := 0; i < maxDispatchLedger+5; i++ {
		a.RecordDispatch("sc_" + string(rune('a'+i%26)) + "_x")
	}
	if got := a.RecentDispatches(); len(got) > maxDispatchLedger {
		t.Fatalf("ledger must be bounded to %d, got %d", maxDispatchLedger, len(got))
	}

	// Turn reset (beginRunTurn's full struct reassignment) clears the echo.
	a.turn = turnRuntime{}
	if got := a.RecentDispatches(); got != nil {
		t.Fatalf("turn reset must clear the ledger, got %v", got)
	}

	// Nil agent safety (direct unit construction).
	var nilAgent *Agent
	nilAgent.RecordDispatch("x")
	if got := nilAgent.RecentDispatches(); got != nil {
		t.Fatalf("nil agent must stay empty, got %v", got)
	}
}

func TestCollabDispatchEchoAndDirectoryField(t *testing.T) {
	// Nil probe: field omitted entirely (not null).
	out, err := directoryPage(SessionCollabConfig{}, 10, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "dispatchedThisTurn") {
		t.Fatalf("nil probe must omit the echo field, got %s", out)
	}

	// Injected probe: the turn's ledger rides the directory answer.
	cfg := SessionCollabConfig{
		RecentDispatches: func() []string { return []string{"sc_a (alpha)", "sc_b (beta)"} },
	}
	out, err = directoryPage(cfg, 10, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	echo, ok := payload["dispatchedThisTurn"].([]any)
	if !ok || len(echo) != 2 || echo[0] != "sc_a (alpha)" {
		t.Fatalf("dispatchedThisTurn = %v, want the two-entry ledger", payload["dispatchedThisTurn"])
	}
}
