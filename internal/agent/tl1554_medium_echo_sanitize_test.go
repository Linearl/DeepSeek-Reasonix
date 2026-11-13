package agent

import (
	"strings"
	"testing"
)

// tl1554: pins for the vetted medium echo surfaces — the shared echo helper
// and the fleet id/dep heal. The negative assertion is the same as the
// high-risk surfaces: no bare CR byte, no literal `\r` text, in whatever the
// model reads back.

func TestEchoedNameTokenForms(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "clean passthrough", in: "peek", want: "peek"},
		{name: "real CR form", in: "dr\rain", want: "drain"},
		{name: "literal escape form", in: `dr\rain`, want: "drain"},
		{name: "trims space", in: " query\r ", want: "query"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := echoedNameToken(test.in); got != test.want {
				t.Fatalf("echoedNameToken(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
	// Pure-control garbage: fallback returns the raw form (the caller's %q
	// renders it escaped — never raw bytes).
	if got := echoedNameToken("\r\r"); got != "\r\r" {
		t.Fatalf("fallback = %q, want the raw form for the caller's %q render", got, "\r\r")
	}
}

func TestCleanEchoPathStripsRealControlBytesOnly(t *testing.T) {
	if got := cleanEchoPath("docs\r/readme.txt"); got != "docs/readme.txt" {
		t.Fatalf("real CR not stripped: %q", got)
	}
	// Windows backslash-letter sequences are legitimate path content — kept.
	if got := cleanEchoPath(`C:\readme.txt`); got != `C:\readme.txt` {
		t.Fatalf("legitimate literal backslash-r text stripped from a path: %q", got)
	}
}

func TestFleetPlanHealsPollutedIDs(t *testing.T) {
	items := []fleetTaskItem{
		{ID: "ta\rska", DependsOn: []string{}},
		{ID: "taskb", DependsOn: []string{"ta\rska"}}, // literal form must match the healed id
	}
	plan, err := newFleetPlan(items, false)
	if err != nil {
		t.Fatalf("polluted ids/dep must heal and resolve, got %v", err)
	}
	if plan.ids[0] != "taska" || plan.ids[1] != "taskb" {
		t.Fatalf("ids not healed: %v", plan.ids)
	}
	if len(plan.deps[1]) != 1 || plan.deps[1][0] != 0 {
		t.Fatalf("healed dep did not resolve to task a: %v", plan.deps[1])
	}
	// The unresolvable-after-heal case echoes the cleaned form only.
	items = []fleetTaskItem{
		{ID: "taska", DependsOn: []string{"zzz\rq"}},
	}
	_, err = newFleetPlan(items, false)
	if err == nil {
		t.Fatal("unresolvable dep must still be rejected")
	}
	msg := err.Error()
	if strings.ContainsRune(msg, '\r') || strings.Contains(msg, `\r`) {
		t.Fatalf("fleet error echoes pollution: %q", msg)
	}
	if !strings.Contains(msg, "zzzq") {
		t.Fatalf("fleet error must echo the cleaned form: %q", msg)
	}
}
