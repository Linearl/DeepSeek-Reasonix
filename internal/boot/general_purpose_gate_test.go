package boot

import (
	"slices"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/skill"
)

// Task 632: the general-purpose writer profile is hidden unless
// experimental_general_purpose_subagent is on. featureGatedBuiltinNames feeds
// skill.Options.DisableBuiltinNames, which hides only the shipped built-in —
// a user-authored skill of the same name keeps resolving (asserted in
// internal/skill).
func TestFeatureGatedBuiltinNamesFollowSwitch(t *testing.T) {
	cfg := &config.Config{}
	got := featureGatedBuiltinNames(cfg)
	if !slices.Equal(got, []string{skill.GeneralPurposeProfileName}) {
		t.Fatalf("flag off: gated builtins = %v, want [%s]", got, skill.GeneralPurposeProfileName)
	}

	cfg.Agent.ExperimentalGeneralPurposeSubagent = true
	if got := featureGatedBuiltinNames(cfg); len(got) != 0 {
		t.Fatalf("flag on: gated builtins = %v, want none", got)
	}
}

// The boot-facing store behaves consistently with the gate: with the switch
// off, the built-in is absent from the model-visible list and unreadable, so
// task profile="general-purpose" and run_skill both bounce at resolution.
func TestGeneralPurposeBuiltinGatedInBootStore(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{}
	store := skill.New(skill.Options{
		HomeDir:             home,
		ProjectRoot:         t.TempDir(),
		DisableBuiltinNames: featureGatedBuiltinNames(cfg),
	})
	if _, ok := store.Read(skill.GeneralPurposeProfileName); ok {
		t.Fatal("flag off: general-purpose must not resolve from the boot-shaped store")
	}

	cfg.Agent.ExperimentalGeneralPurposeSubagent = true
	storeOn := skill.New(skill.Options{
		HomeDir:             home,
		ProjectRoot:         t.TempDir(),
		DisableBuiltinNames: featureGatedBuiltinNames(cfg),
	})
	sk, ok := storeOn.Read(skill.GeneralPurposeProfileName)
	if !ok {
		t.Fatal("flag on: general-purpose must resolve from the boot-shaped store")
	}
	if sk.RunAs != skill.RunSubagent || sk.ReadOnly {
		t.Fatalf("flag on: runAs=%q readOnly=%v, want subagent writer", sk.RunAs, sk.ReadOnly)
	}
}
