package workflow

import (
	"fmt"
	"strings"
	"sync"
)

// Finding statuses. A finding that survives a failed confirmation is KEPT and
// labelled unconfirmed — a real issue the confirmer could not reproduce is
// still worth a human's eyes (SKILL.md §10).
const (
	FindingVerified    = "verified"
	FindingUnconfirmed = "unconfirmed"
)

// Finding is one reported observation. Status is engine-assigned at report
// time (never trusted from the sub-agent's answer).
type Finding struct {
	Where    string `json:"where"`
	What     string `json:"what"`
	Evidence string `json:"evidence"`
	Status   string `json:"status"`
	Severity string `json:"severity"`
}

// WorkflowReport is the run's handoff shape (task 685 point 5): what was
// found and confirmed, what actually decided the result, and — under strict
// semantics — what could NOT be checked (never "what existed but was skipped").
type WorkflowReport struct {
	Conclusion string   `json:"conclusion"`
	Findings   []Finding `json:"findings"`
	Verified   []string  `json:"verified"`
	NotCovered []string  `json:"notCovered"`
}

// reportBag accumulates findings the moment they land — one at a time, under
// a lock — so a run that dies at node twelve still hands back the eleven
// nodes' worth of reported work (SKILL.md §10 "salvage by report": the array
// you accumulate at the end is the array you lose).
type reportBag struct {
	mu       sync.Mutex
	findings []Finding
}

// add appends one finding immediately. Idempotent per (where, what) so a
// resumed or double-reported item never appears twice.
func (b *reportBag) add(f Finding) {
	if f.Status != FindingVerified && f.Status != FindingUnconfirmed {
		f.Status = FindingUnconfirmed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, have := range b.findings {
		if have.Where == f.Where && have.What == f.What {
			return
		}
	}
	b.findings = append(b.findings, f)
}

// snapshot copies the findings reported so far.
func (b *reportBag) snapshot() []Finding {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.findings) == 0 {
		return nil
	}
	out := make([]Finding, len(b.findings))
	copy(out, b.findings)
	return out
}

// promote flips unconfirmed findings matching (where, what) to verified once
// an independent confirmation reproduces them. Unconfirmed rows are kept —
// promotion is a status upgrade in place, never a drop.
func (b *reportBag) promote(f Finding) {
	f.Status = FindingVerified
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.findings {
		if b.findings[i].Where == f.Where && b.findings[i].What == f.What {
			b.findings[i].Status = FindingVerified
			return
		}
	}
	b.findings = append(b.findings, f)
}

// buildConclusion assembles the report conclusion: the author's template with
// {{refs}} interpolated, or an engine summary of what ran. It is deliberately
// honest about failure — a dead run says it died.
func buildConclusion(wf *Workflow, tmpl string, ran, total int, failedNode string) string {
	if strings.TrimSpace(tmpl) != "" {
		return tmpl
	}
	if failedNode != "" {
		return fmt.Sprintf("Workflow %q stopped at node %q; %d/%d nodes ran. Reported findings are preserved below.", wf.Name, failedNode, ran, total)
	}
	return fmt.Sprintf("Workflow %q completed: %d/%d nodes ran.", wf.Name, ran, total)
}
