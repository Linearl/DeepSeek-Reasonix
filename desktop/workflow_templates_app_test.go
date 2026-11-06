package main

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/workflow"
)

// tl1454: the template bridges stay thin forwards — the run binds to the
// active tab's session (no tab ⇒ a plain next step, never a guess), and the
// tl1454 run additions project onto the wire only when present (omitempty:
// a pre-tl1454 run's wire keeps its exact JSON shape).
func TestWorkflowTemplateRunNeedsAnActiveSession(t *testing.T) {
	a := &App{tabs: map[string]*WorkspaceTab{}}
	if _, err := a.WorkflowTemplateRun("audit"); err == nil || !strings.Contains(err.Error(), "open a session tab first") {
		t.Fatalf("run without an active tab must name the next step: %v", err)
	}
	if _, err := a.WorkflowTemplateRunPreview("audit"); err == nil {
		t.Fatal("preview without an active tab must fail the same way")
	}
}

func TestWorkflowTemplateRunWireProjection(t *testing.T) {
	var bare workflowRunWire
	applyTemplateRunWire(&bare, workflow.RunTemplateView{})
	raw, _ := json.Marshal(bare)
	for _, key := range []string{"version", "origin", "budget", "usedTokens", "usedRequests", "failure"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("an empty template view must add no %q key: %s", key, raw)
		}
	}

	var wire workflowRunWire
	applyTemplateRunWire(&wire, workflow.RunTemplateView{
		Origin:     workflow.RunOriginPanel,
		Version:    "v·0123456789ab",
		Budget:     &workflow.Budget{Tokens: 1000, Wall: "30m"},
		UsedTokens: 1400,
		Failure:    &workflow.RunFailure{NodeID: "spend-b", Reason: workflow.ReasonBudget, Detail: "workflow budget exceeded"},
	})
	if wire.Origin != "panel" || wire.Version != "v·0123456789ab" || wire.UsedTokens != 1400 {
		t.Fatalf("template view not projected: %+v", wire)
	}
	if wire.Budget == nil || wire.Budget.Tokens != 1000 || wire.Budget.WallMs != 30*60*1000 {
		t.Fatalf("budget not projected: %+v", wire.Budget)
	}
	if wire.Failure == nil || wire.Failure.NodeID != "spend-b" || wire.Failure.Reason != "budget" {
		t.Fatalf("failure not projected: %+v", wire.Failure)
	}
}

func TestWorkflowTemplateHistoryNeverNil(t *testing.T) {
	a := &App{}
	if got := a.WorkflowTemplateHistory("no-such-template-tl1454"); got == nil {
		t.Fatal("history must marshal as [] not null")
	}
}
