package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/retrieval"
)

// writeTriggerFact plants a fact whose frontmatter carries a triggers field, so
// the trigger lane has something to match against.
func writeTriggerFact(t *testing.T, dir, name, title, body, triggers string) string {
	t.Helper()
	content := fmt.Sprintf(`---
id: mem-%s
revision: 1
created_at: "2026-01-01T00:00:00.0000000Z"
updated_at: "2026-01-01T00:00:00.0000000Z"
name: %s
title: %s
description: trigger lane fixture
triggers: %s
metadata:
  type: project
  scope: project
  activation: relevant
  volatility: stable
---

%s
`, name, name, title, triggers, body)
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fact %s: %v", name, err)
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	return path
}

func TestTriggerPhrasesSplitting(t *testing.T) {
	cases := []struct {
		field string
		want  int
	}{
		{"proxy 代理端口", 2},
		{"proxy,代理端口", 2},
		{"proxy;代理端口", 2},
		{"proxy，代理端口", 2},
		{"proxy 、 代理端口", 2},
		{"   ", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := len(triggerPhrases(c.field)); got != c.want {
			t.Fatalf("triggerPhrases(%q) = %d phrases, want %d", c.field, got, c.want)
		}
	}
}

// TestTriggerHitCrossWording is the heart of acceptance criterion 3: a fact
// written in English is found by a Chinese query because the fact declared the
// Chinese wording for itself.
func TestTriggerHitCrossWording(t *testing.T) {
	q := func(s string) []string {
		terms, err := retrieval.QueryTerms(s)
		if err != nil {
			t.Fatalf("QueryTerms(%q): %v", s, err)
		}
		return terms
	}

	// Fact declares a Chinese alias; the body is English only.
	triggers := "proxy 代理端口 网络代理"
	if !triggerHit(triggers, q("代理端口")) {
		t.Fatalf("a declared alias must fire on the Chinese query")
	}
	// The English half of the same declaration also fires.
	if !triggerHit(triggers, q("proxy")) {
		t.Fatalf("the English declaration must fire on the English query")
	}
	// A query sharing nothing keeps the lane silent.
	if triggerHit(triggers, q("配色方案")) {
		t.Fatalf("an unrelated query must not fire the trigger lane")
	}
	// No triggers at all never fires (pre-existing memories untouched).
	if triggerHit("", q("代理端口")) {
		t.Fatalf("a fact without triggers must never fire")
	}
}

// TestTriggerHitRejectsGenericPhrase keeps the lane from firing on one common
// bigram, which would make every turn recall the same fact.
func TestTriggerHitRejectsGenericPhrase(t *testing.T) {
	terms, err := retrieval.QueryTerms("记忆召回不准")
	if err != nil {
		t.Fatal(err)
	}
	// One bigram alias is below the specificity floor.
	if triggerHit("召回", terms) {
		t.Fatalf("a single-bigram alias must not fire")
	}
	// A long enough phrase that is fully covered does fire.
	if !triggerHit("记忆召回", terms) {
		t.Fatalf("a multi-bigram alias that is fully covered must fire")
	}
}

// TestTriggerLaneEndToEnd is acceptance criterion 3 through the real search
// path: the fact's body never says 代理端口, yet the query finds it.
func TestTriggerLaneEndToEnd(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	writeTriggerFact(t, dir, "relay-config", "relay configuration",
		"The relay listens on port 40808 and forwards to the upstream.", "代理端口 网络代理")
	writeTestFact(t, dir, "unrelated", "unrelated fact",
		"A note about colour schemes and typography.", time.Time{})

	hits, err := searchMemories(context.Background(), store, "代理端口", "", "", 8)
	if err != nil {
		t.Fatalf("searchMemories: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly the trigger hit, got %d hits: %+v", len(hits), hits)
	}
	if hits[0].Memory.Name != "relay-config" {
		t.Fatalf("wrong fact recalled: %s", hits[0].Memory.Name)
	}
	if !hits[0].Triggered {
		t.Fatalf("the hit must be attributed to the trigger lane")
	}
}

// TestTriggersBackwardCompatible is acceptance criterion 5: a memory without a
// triggers field behaves exactly as it did before this change.
func TestTriggersBackwardCompatible(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	writeTestFact(t, dir, "plain", "plain fact",
		"Resident index probe OTTER-CANYON-77.", time.Time{})

	m, ok := loadMemory(filepath.Join(dir, "plain.md"))
	if !ok {
		t.Fatalf("plain fact must load")
	}
	if m.Triggers != "" {
		t.Fatalf("a fact without a triggers field must load with empty triggers, got %q", m.Triggers)
	}

	hits, err := searchMemories(context.Background(), store, "OTTER-CANYON-77", "", "", 8)
	if err != nil {
		t.Fatalf("searchMemories: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("lexical search must still work without triggers, got %d hits", len(hits))
	}
	if hits[0].Triggered {
		t.Fatalf("a fact without triggers must not be attributed to the trigger lane")
	}
}

// TestTriggersRoundTripThroughFrontmatter pins that the field survives a save
// and reload — otherwise the lane would only work for hand-written files.
func TestTriggersRoundTripThroughFrontmatter(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	if _, err := store.Save(Memory{
		Name:        "roundtrip",
		Description: "round trip probe",
		Triggers:    "代理端口 proxy",
		Body:        "body",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, ok := loadMemory(filepath.Join(dir, "roundtrip.md"))
	if !ok {
		t.Fatalf("reload failed")
	}
	if loaded.Triggers != "代理端口 proxy" {
		t.Fatalf("triggers did not survive the round trip: %q", loaded.Triggers)
	}
}
