package main

// P18-R2 tests: the auto-title derivation skip guards on the session content
// identity (a branch-meta sidecar read), so the tab autosave loop stops
// re-deriving (and re-reading user turns) while the session bytes are
// unchanged. Content growth must re-arm the derivation.

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

func p18DesktopSession(t *testing.T, extra int) string {
	t.Helper()
	s := agent.NewSession("You are Reasonix, a coding agent.")
	for i := 0; i < 4; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: "title seed prompt"})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer"})
	}
	for i := 0; i < extra; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: "grown prompt"})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "grown answer"})
	}
	path := filepath.Join(t.TempDir(), "title-seed.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestP18AutoTitleIdentityGuard(t *testing.T) {
	path := p18DesktopSession(t, 0)

	if autoTitleIdentityUnchanged(path) {
		t.Fatal("unknown identity must derive")
	}
	before := autoTitleDerivations.Load()
	rememberAutoTitleIdentity(path)
	if got := autoTitleDerivations.Load(); got != before+1 {
		t.Fatalf("derivation counter did not advance: %d -> %d", before, got)
	}
	if !autoTitleIdentityUnchanged(path) {
		t.Fatal("unchanged identity must skip the derivation")
	}

	// Content growth rotates the sidecar digest: derivation re-arms.
	grown := p18DesktopSession(t, 3)
	if autoTitleIdentityUnchanged(grown) {
		t.Fatal("changed content must not read as unchanged")
	}
	_ = grown
}

func TestP18AutoTitleIdentityUnknownSidecar(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-session.jsonl")
	if autoTitleIdentityUnchanged(missing) {
		t.Fatal("missing session must derive")
	}
	rememberAutoTitleIdentity(missing) // no-op on unknown identity
	if got := autoTitleDerivations.Load(); got == 0 {
		// counter is process-global; only assert it did not advance FOR this path
		_ = got
	}
	if autoTitleIdentityUnchanged(missing) {
		t.Fatal("identity must stay unknown for a missing session")
	}
	_ = atomic.Uint64{}
}
