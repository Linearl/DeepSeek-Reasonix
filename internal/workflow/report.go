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
// time (never trusted from the sub-agent's answer). SourceID is the engine's
// per-producer serial ("<nodeID>#<n>", tl1326 B1): it travels on the typed
// result entries so a downstream reviewer can echo it back, and add/promote
// then merge the rewrite into the ORIGINAL entry instead of appending a
// duplicate. Engine-internal — WorkflowFindingView (the external JSON) does
// not carry it.
type Finding struct {
	Where    string `json:"where"`
	What     string `json:"what"`
	Evidence string `json:"evidence"`
	Status   string `json:"status"`
	Severity string `json:"severity"`
	SourceID string `json:"sourceId,omitempty"`
}

// sameFinding is THE dedup/promotion key, shared by reportBag.add and
// reportBag.promote (tl1326 hard rule: both must resolve the same entry —
// two independent matchers here are how a merged entry silently stops
// promoting). Same engine-issued source serial decides first (the reviewer's
// rewrite merges into the original); otherwise exact (where, what) wording
// equality keeps the pre-B1 idempotent contract ("resumed or double-reported
// item never appears twice") — including the case of two producers minting
// different serials for the identical wording.
func sameFinding(a, b Finding) bool {
	if a.SourceID != "" && a.SourceID == b.SourceID {
		return true
	}
	return a.Where == b.Where && a.What == b.What
}

// WorkflowReport is the run's handoff shape (task 685 point 5): what was
// found and confirmed, what actually decided the result, and — under strict
// semantics — what could NOT be checked (never "what existed but was skipped").
type WorkflowReport struct {
	Conclusion string    `json:"conclusion"`
	Findings   []Finding `json:"findings"`
	Verified   []string  `json:"verified"`
	NotCovered []string  `json:"notCovered"`
}

// reportBag accumulates findings the moment they land — one at a time, under
// a lock — so a run that dies at node twelve still hands back the eleven
// nodes' worth of reported work (SKILL.md §10 "salvage by report": the array
// you accumulate at the end is the array you lose).
//
// rawReported counts every add() call (tl1440): the gap between it and
// len(findings) is exactly how many reports were merged away (same-source
// rewrites, echoed or where-fallback re-keyed). It feeds the report's
// conclusion-counting note so a reader never sees "N findings" next to a
// conclusion that counted fewer, with no explanation of the difference. It
// lives only on the in-memory bag — snapshots persist []Finding, so run.json
// keeps its shape and old runs replay untouched.
type reportBag struct {
	mu          sync.Mutex
	findings    []Finding
	rawReported int
}

// add appends one finding immediately. Idempotent per the shared key
// (sameFinding): a backfill carrying a known source serial merges its
// rewrite into the original entry in place — topologically later wording
// wins, the bag never grows — while an exact (where, what) re-report stays a
// no-op exactly as before tl1326.
func (b *reportBag) add(f Finding) {
	if f.Status != FindingVerified && f.Status != FindingUnconfirmed {
		f.Status = FindingUnconfirmed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rawReported++
	for i := range b.findings {
		have := &b.findings[i]
		if !sameFinding(*have, f) {
			continue
		}
		if f.SourceID != "" && have.SourceID == f.SourceID {
			// Same engine-issued source: the (topologically later) reviewer's
			// rewrite overwrites the original wording/evidence/severity.
			have.What = f.What
			have.Evidence = f.Evidence
			have.Severity = f.Severity
		}
		return
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

// reportedTotal is the raw number of add() calls so far (tl1440): the dedup
// baseline the conclusion's counting note compares len(findings) against.
func (b *reportBag) reportedTotal() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rawReported
}

// promote flips unconfirmed findings matching the shared key (sameFinding)
// to verified once an independent confirmation reproduces them. Unconfirmed
// rows are kept — promotion is a status upgrade in place, never a drop. A
// non-empty note (tl1326 B1 per-item verdict) is folded into the entry's
// evidence so the confirmer's wording reaches the human report.
func (b *reportBag) promote(f Finding) {
	b.promoteNote(f, "")
}

// promoteNote is promote with an optional confirmer note; both go through
// the same key as add (tl1326 hard rule).
func (b *reportBag) promoteNote(f Finding, note string) {
	f.Status = FindingVerified
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.findings {
		if !sameFinding(b.findings[i], f) {
			continue
		}
		b.findings[i].Status = FindingVerified
		if note != "" {
			b.findings[i].Evidence = strings.TrimSpace(b.findings[i].Evidence + "\n[confirm] " + note)
		}
		return
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
