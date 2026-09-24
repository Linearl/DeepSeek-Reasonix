package main

import (
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// TestMigrationSignatureSurvivesOrdinaryWrites pins task 195's marker rework:
// the directory signature covers what migration/repair decisions read, so an
// ordinary append-only save (transcript tail + sidecar revision bump) must NOT
// invalidate the markers — that invalidation is what re-armed the per-render
// ListSessionOrder scan on every turn. Structural changes still do.
func TestMigrationSignatureSurvivesOrdinaryWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ordinary.jsonl")
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "who are you"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	base, err := sessionDirMigrationSignature(dir)
	if err != nil {
		t.Fatal(err)
	}
	if base == "" {
		t.Fatal("empty base signature")
	}

	// Ordinary turn: transcript tail grows, sidecar size/mtime/revision move.
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "I am the assistant"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	after, err := sessionDirMigrationSignature(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after != base {
		t.Fatalf("ordinary append invalidated the migration signature: base=%.16s after=%.16s", base, after)
	}

	// Assigning a topic is a structural field the migration pass branches on.
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("meta load: ok=%v err=%v", ok, err)
	}
	meta.TopicID = "topic_assigned_1"
	if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
		t.Fatal(err)
	}
	withTopic, err := sessionDirMigrationSignature(dir)
	if err != nil {
		t.Fatal(err)
	}
	if withTopic == after {
		t.Fatal("topic assignment must invalidate the migration signature")
	}

	// A brand-new session file is structural.
	extra := filepath.Join(dir, "extra.jsonl")
	x := agent.NewSession("sys")
	x.Add(provider.Message{Role: provider.RoleUser, Content: "newcomer"})
	if err := x.SaveSnapshot(extra); err != nil {
		t.Fatal(err)
	}
	withNew, err := sessionDirMigrationSignature(dir)
	if err != nil {
		t.Fatal(err)
	}
	if withNew == withTopic {
		t.Fatal("a new session file must invalidate the migration signature")
	}
}
