package builtincontent_test

import (
	"testing"

	"reasonix/internal/skill/builtincontent"
)

// Task 116: the MiMo-derived playbooks ship INSIDE the binary. A user must not
// have to install anything for them to show up in the skills catalog, and a
// missing description would render as a blank catalog line.
func TestShippedPlaybooksAreEmbedded(t *testing.T) {
	items, err := builtincontent.All()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]builtincontent.SkillMarkdown{}
	for _, item := range items {
		byName[item.Name] = item
	}
	for _, want := range []string{
		"reasonix-guide",
		"reasonix-fork-guide",
		"deep-research",
		"data-analytics",
		"memory-search",
		"gh-issue-submit",
		"github-issue-triage",
	} {
		sk, ok := byName[want]
		if !ok {
			t.Fatalf("embedded skill %q is missing; have %v", want, byName)
		}
		if sk.Description == "" {
			t.Fatalf("embedded skill %q has no description", want)
		}
		if sk.RunAs != "inline" {
			t.Fatalf("embedded skill %q runAs = %q, want inline", want, sk.RunAs)
		}
	}
}
