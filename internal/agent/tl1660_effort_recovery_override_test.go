package agent

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
)

// tl1660: the recovery-fork guard no longer declines the per-request effort
// override. The 20261117 incident (tab_f7ee41fe: every switch on a recovery
// copy paid a 14-30s rebuild) proved the override and the reanchor semantics
// orthogonal — the override only moves the wire's reasoning depth, and the
// rebuild on a settled recovery copy is a no-op anchor-wise. These nails pin
// the four scenarios the old guard used to conflate.
func TestTL1660R1RecoveryForkEffortOverrideAccepted(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })

	recoveryAgent := func(efforts []string) *Agent {
		a := New(&varyingProvider{efforts: efforts}, nil, NewSession("s"), Options{}, event.Discard)
		path := filepath.Join(t.TempDir(), "session.json")
		if err := SaveBranchMeta(path, BranchMeta{ID: BranchID(path), Recovered: true}); err != nil {
			t.Fatalf("save branch meta: %v", err)
		}
		a.sess.path = path
		return a
	}

	// (a) A vocabulary-listed level on a recovery copy is accepted and arms the
	// per-request channel — the fast path that used to pay a 14-30s rebuild.
	a := recoveryAgent([]string{"low", "max"})
	if !a.SetSessionEffortOverride("max") {
		t.Fatal("recovery-forked session rejected a vocabulary-listed effort override")
	}
	if got := a.effortOverrideForRequest(); got != "max" {
		t.Fatalf("override = %q, want max", got)
	}
	// The accepted switch leaves a trace (paired against the desktop's
	// fast-per-request line) so a recovery-copy switch stays attributable.
	if logs := buf.String(); !strings.Contains(logs, "accepted on recovery fork") {
		t.Fatalf("accepted-on-recovery-fork trace missing in:\n%s", logs)
	}

	// (b) A session without recovery meta keeps the plain accept path — zero
	// behavior change for normal sessions.
	b := New(&varyingProvider{efforts: []string{"low", "max"}}, nil, NewSession("s"), Options{}, event.Discard)
	if !b.SetSessionEffortOverride("max") {
		t.Fatal("plain session rejected a vocabulary-listed effort override")
	}
	if got := b.effortOverrideForRequest(); got != "max" {
		t.Fatalf("override = %q, want max", got)
	}

	// (c) A level outside the vocabulary still declines — the tl1660 relaxation
	// must not bypass the vocabulary gate.
	c := recoveryAgent([]string{"low"})
	if c.SetSessionEffortOverride("max") {
		t.Fatal("vocabulary miss accepted on a recovery-forked session")
	}
	if got := c.effortOverrideForRequest(); got != "" {
		t.Fatalf("override after rejected level = %q, want empty", got)
	}
	if logs := buf.String(); !strings.Contains(logs, "reason=level-not-in-vocabulary") {
		t.Fatalf("level-not-in-vocabulary decline missing in:\n%s", logs)
	}

	// (d) A non-varying provider still declines on a recovery copy.
	d := New(&fakeProvider{}, nil, NewSession("s"), Options{}, event.Discard)
	dpath := filepath.Join(t.TempDir(), "session.json")
	if err := SaveBranchMeta(dpath, BranchMeta{ID: BranchID(dpath), Recovered: true}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	d.sess.path = dpath
	if d.SetSessionEffortOverride("max") {
		t.Fatal("non-varying provider accepted a per-request override on a recovery copy")
	}
	if logs := buf.String(); !strings.Contains(logs, "reason=provider-not-effort-varying") {
		t.Fatalf("provider-not-effort-varying decline missing in:\n%s", logs)
	}

	// (e) Clearing always succeeds — recovery meta or not.
	if !a.SetSessionEffortOverride("") {
		t.Fatal("clearing the override must always succeed")
	}
	if got := a.effortOverrideForRequest(); got != "" {
		t.Fatalf("override after clear = %q, want empty", got)
	}
}
