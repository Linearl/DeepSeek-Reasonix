// tl1733 Block1 acceptance tests at the Set level: BackgroundDataBlock's
// injected index goes through the slim pipeline (E1), the lab switch is the
// emergency bypass (E2), and legacy directly-constructed Sets keep their
// byte-for-byte behavior.
package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// E1: a Load-built Set injects the slimmed index — bounded lines, no file
// references — while IndexFrom stays untouched.
func TestSlimOffInjectsSlimmedIndex(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	store := StoreFor(root, workDir) // the exact store Load will discover
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-proj", Name: "deploy-region", Title: "Deploy region",
		Description: "This project deploys to eu-central-1", Type: TypeProject,
		Scope: FactScopeProject, Body: "eu-central-1",
	})
	set := Load(Options{CWD: workDir, UserDir: root})
	block := set.BackgroundDataBlock()

	if !strings.Contains(block, "### Background memory index") {
		t.Fatalf("background index section missing:\n%s", block)
	}
	if strings.Contains(block, "](project/deploy-region.md)") {
		t.Fatalf("injected section must use the slim line form:\n%s", block)
	}
	if !strings.Contains(block, "- Deploy region — [project/project] This project deploys to eu-central-1") {
		t.Fatalf("injected line not in slim form:\n%s", block)
	}
	// Source of truth untouched.
	if !strings.Contains(set.Index, "](project/deploy-region.md)") {
		t.Fatalf("Set.Index must keep the full-pool form:\n%s", set.Index)
	}
}

// E2: the lab switch is the emergency bypass — the background index section
// stops injecting entirely while pinned guidance keeps its channel and the
// on-demand layer stays complete.
func TestSlimOnStopsBackgroundIndex(t *testing.T) {
	SetMemoryIndexSlimMode(true)
	t.Cleanup(func() { SetMemoryIndexSlimMode(false) })

	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	store := StoreFor(root, workDir)
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-proj", Name: "deploy-region", Title: "Deploy region",
		Description: "This project deploys to eu-central-1", Type: TypeProject,
		Scope: FactScopeProject, Body: "eu-central-1",
	})
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-pin", Name: "pinned-rule", Title: "Pinned rule",
		Description: "always injected", Type: TypeFeedback, Scope: FactScopeProject,
		Activation: ActivationPinned, Body: "always injected body",
	})
	set := Load(Options{CWD: workDir, UserDir: root})
	block := set.BackgroundDataBlock()

	if strings.Contains(block, "### Background memory index") || strings.Contains(block, "Deploy region") {
		t.Fatalf("bypass on: background index must stop injecting:\n%s", block)
	}
	if !strings.Contains(block, "Pinned rule") {
		t.Fatalf("bypass on: pinned guidance must keep its channel:\n%s", block)
	}
	// On-demand layer stays complete: search finds the stopped fact.
	hits, err := searchMemories(context.Background(), store, "deploy-region", "", "", 8)
	if err != nil || len(hits) != 1 || hits[0].Memory.ID != "mem-proj" {
		t.Fatalf("bypass on: search must stay complete (hits=%v err=%v)", hits, err)
	}
}

// Legacy compatibility: a directly-constructed Set (no Load-time pool) injects
// its pre-rendered Index verbatim — the pre-tl1733 behavior, byte for byte.
func TestLegacySetKeepsVerbatimIndex(t *testing.T) {
	set := &Set{Index: "- [Fact](project/fact.md) — [project/project] desc\n"}
	block := set.BackgroundDataBlock()
	if !strings.Contains(block, "](project/fact.md)") {
		t.Fatalf("legacy Set must keep its verbatim index:\n%s", block)
	}
}

// E2+empty-pool edge: the switch on with no pinned guidance renders an empty
// block (the section's absence must not resurrect the old framing).
func TestSlimOnEmptyBlockWithoutPinned(t *testing.T) {
	SetMemoryIndexSlimMode(true)
	t.Cleanup(func() { SetMemoryIndexSlimMode(false) })

	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	store := StoreFor(root, workDir)
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-proj", Name: "deploy-region", Title: "Deploy region",
		Description: "d", Type: TypeProject, Scope: FactScopeProject, Body: "b",
	})
	set := Load(Options{CWD: workDir, UserDir: root})
	if block := set.BackgroundDataBlock(); block != "" {
		t.Fatalf("bypass on with no pinned guidance must render nothing:\n%s", block)
	}
}
