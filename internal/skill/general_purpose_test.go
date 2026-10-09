package skill

import (
	"strings"
	"testing"
)

// Task 632: the general-purpose built-in writer profile and its
// DisableBuiltinNames gate. The gate must hide only the shipped built-in — a
// user-authored skill sharing the name keeps resolving in the off state, which
// is what keeps the flag-off surface byte-identical.

func TestGeneralPurposeBuiltinShipsAsWriterProfile(t *testing.T) {
	store := New(Options{HomeDir: t.TempDir(), ProjectRoot: t.TempDir()})
	sk, ok := store.Read(GeneralPurposeProfileName)
	if !ok {
		t.Fatal("general-purpose built-in is not registered")
	}
	if sk.RunAs != RunSubagent {
		t.Fatalf("runAs = %q, want subagent", sk.RunAs)
	}
	if sk.ReadOnly {
		t.Fatal("general-purpose must be a writer profile, not read-only")
	}
	if sk.Scope != ScopeBuiltin {
		t.Fatalf("scope = %q, want builtin", sk.Scope)
	}
	// Four-segment skeleton from the 631 attribution: identity + completion
	// principle, operating rules, answer structure checklist, scope creep.
	for _, want := range []string{
		"general-purpose subagent",
		"don't gold-plate",
		"use_capability",
		"What you did",
		"What changed",
		"What you verified",
		"Open questions",
		"scope creep",
	} {
		if !strings.Contains(sk.Body, want) {
			t.Fatalf("general-purpose body missing %q\n---\n%s", want, sk.Body)
		}
	}
}

func TestGeneralPurposeIndexLineCarriesSubagentTag(t *testing.T) {
	store := New(Options{HomeDir: t.TempDir(), ProjectRoot: t.TempDir()})
	index := CatalogBlock(store.List())
	if !strings.Contains(index, GeneralPurposeProfileName+" [🧬 subagent]") {
		t.Fatalf("index line missing subagent tag for general-purpose:\n%s", index)
	}
}

func TestDisableBuiltinNamesHidesOnlyTheBuiltIn(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	// A user project skill that happens to share the gated name.
	writeSkill(t, project, ".reasonix/skills/"+GeneralPurposeProfileName+".md",
		"---\nname: "+GeneralPurposeProfileName+"\ndescription: my own writer\n---\nuser body")

	off := New(Options{
		HomeDir:             home,
		ProjectRoot:         project,
		DisableBuiltinNames: []string{GeneralPurposeProfileName},
	})
	if sk, ok := off.Read(GeneralPurposeProfileName); !ok {
		t.Fatal("flag off: a user skill sharing the gated name must still resolve")
	} else if sk.Scope != ScopeProject || sk.Body != "user body" {
		t.Fatalf("flag off: resolved the wrong skill: scope=%s body=%q", sk.Scope, sk.Body)
	}
	if skills := off.List(); hasBuiltinGeneralPurpose(skills) {
		t.Fatal("flag off: the shipped built-in must not appear in the model-visible list")
	}

	on := New(Options{HomeDir: home, ProjectRoot: project})
	sk, ok := on.Read(GeneralPurposeProfileName)
	if !ok {
		t.Fatal("flag on: built-in general-purpose must resolve")
	}
	// Project scope wins over builtin, so the user file still shadows it here.
	if sk.Scope != ScopeProject {
		t.Fatalf("flag on: scope = %q, want project override to win", sk.Scope)
	}
}

func hasBuiltinGeneralPurpose(skills []Skill) bool {
	for _, sk := range skills {
		if sk.Name == GeneralPurposeProfileName && sk.Scope == ScopeBuiltin {
			return true
		}
	}
	return false
}
