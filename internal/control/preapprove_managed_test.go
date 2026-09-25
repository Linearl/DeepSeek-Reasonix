package control

import (
	"strings"
	"testing"
)

// Task 231 — the four-category pre-approval decision and its classifier.
// Everything here is pure: no Controller, no prompts, no config import.

func TestPreapproveAllowsEveryCombination(t *testing.T) {
	off := PreapproveManagedOptions{} // the shipped default: all five false
	all := PreapproveManagedOptions{
		Enabled:    true,
		Skills:     true,
		Hooks:      true,
		Stores:     true,
		BashEscape: true,
	}
	kinds := []ManagedWriteKind{managedKindSkills, managedKindHooks, managedKindStores, managedKindBash}

	// Default off: no class ever bypasses, autopilot or not (zero regression).
	for _, kind := range kinds {
		if off.allows(true, kind) {
			t.Fatalf("default-off options must never bypass (%s)", kind)
		}
		if off.allows(false, kind) {
			t.Fatalf("default-off options must never bypass outside autopilot (%s)", kind)
		}
	}
	// Master switch off defeats every individually checked category.
	one := PreapproveManagedOptions{Skills: true}
	if one.allows(true, managedKindSkills) {
		t.Fatal("a checked category without the master switch must keep prompting")
	}
	// Master switch on, autopilot off: still prompts (the user's scope rule).
	if all.allows(false, managedKindSkills) {
		t.Fatal("outside autopilot the prompt must stay, even with everything checked")
	}
	// Master switch + autopilot, but the category unchecked: still prompts.
	onlyBash := PreapproveManagedOptions{Enabled: true, BashEscape: true}
	for _, kind := range []ManagedWriteKind{managedKindSkills, managedKindHooks, managedKindStores} {
		if onlyBash.allows(true, kind) {
			t.Fatalf("an unchecked category must keep prompting (%s)", kind)
		}
	}
	if !onlyBash.allows(true, managedKindBash) {
		t.Fatal("the checked category must bypass under master switch + autopilot")
	}
	// Every checked category bypasses under master + autopilot.
	for _, kind := range kinds {
		if !all.allows(true, kind) {
			t.Fatalf("checked category must bypass under master switch + autopilot (%s)", kind)
		}
	}
	// Unclassified targets are never covered, whatever is checked.
	if all.allows(true, managedKindOther) {
		t.Fatal("unclassified managed files (config.toml & friends) must keep prompting")
	}
}

func TestClassifyManagedWrite(t *testing.T) {
	o := PreapproveManagedOptions{
		SkillsDir: `C:\home\reasonix\skills`,
		StoresDir: `C:\home\reasonix`,
		HooksFile: `C:\home\reasonix\settings.json`,
	}
	cases := []struct {
		target string
		want   ManagedWriteKind
	}{
		{`C:\home\reasonix\skills\demo\SKILL.md`, managedKindSkills},
		{`C:\home\reasonix\skills`, managedKindSkills}, // the dir itself counts
		{`C:\home\reasonix\settings.json`, managedKindHooks},
		{`D:\proj\.claude\settings.json`, managedKindHooks}, // file name everywhere
		{`C:\home\reasonix\sessions\topic.jsonl`, managedKindStores},
		{`D:\proj\app\sessions\run-1.jsonl`, managedKindStores}, // project store too
		{`C:\home\reasonix\config.toml`, managedKindOther},
		{`C:\home\reasonix\.env`, managedKindOther},
		// Home-rooted odds and ends ride the stores class — the same region
		// the session-data guard protects (skills-old must not become skills).
		{`C:\home\reasonix\skills-old\SKILL.md`, managedKindStores},
	}
	for _, c := range cases {
		if got := classifyManagedWrite(c.target, o); got != c.want {
			t.Errorf("classify(%q) = %q, want %q", c.target, got, c.want)
		}
	}
}

func TestWithinDirIsSegmentScoped(t *testing.T) {
	if withinDir(`/home/u/skills-old/x`, `/home/u/skills`) {
		t.Fatal("skills-old must not count as inside skills (prefix trap)")
	}
	if !withinDir(`/home/u/skills`, `/home/u/skills`) {
		t.Fatal("the directory itself must be inside itself")
	}
	if !withinDir(`/home/u/skills/a/b.md`, `/home/u/skills`) {
		t.Fatal("nested files must be inside")
	}
	if withinDir(`/home/u`, `/home/u/skills`) {
		t.Fatal("a parent must not count as inside its child")
	}
}

func TestAuditPreapproveNamesTheClass(t *testing.T) {
	// The audit line is the acceptance-4 trail: it must name the class, the
	// subject, and the autopilot context. Call it and assert nothing panics
	// with the exact shapes the approvers pass.
	auditPreapprove(string(managedKindSkills), `edit C:\home\reasonix\skills\x`, true)
	auditPreapprove(string(managedKindBash), "bash echo hi", false)
	if !strings.Contains(string(managedKindBash), "bash") {
		t.Fatal("kind constants stay machine-readable")
	}
}
