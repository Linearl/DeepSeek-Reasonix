package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/claimcheck"
	"reasonix/internal/tool"
)

func init() {
	tool.RegisterBuiltin(claimCheck{})
}

// claimCheck is the agent-native half of claim hygiene (task 222 L1): any
// "does not exist / is missing / is not implemented" claim that will be
// WRITTEN into a deliverable must be produced through this tool (or the
// claim-check CLI), never from a sampled query like `find | head`. The only
// output shape is the structured verdict — there is no truncatable free-text
// stream to misread.
//
// Scope guard: the verdict speaks to file/dir EXISTENCE only. "The file
// exists" is not "the feature works"; do not widen it. Skipped (unreadable)
// entries force uncertain=true instead of a clean answer.
type claimCheck struct{}

func (claimCheck) Name() string { return "claim_check" }

func (claimCheck) Description() string {
	return "Verify an existence claim and return a structured verdict (CONFIRMED/REFUTED with evidence). Use this — or the packaged `reasonix claim-check` CLI — for every negative claim (\"X does not exist / is missing / is not implemented\") that will be written into a deliverable. Sampling queries (find/head/tail) are NOT valid existence evidence: a truncated listing has silently dropped entries before. Scope: existence only — a hit proves presence, never content or behavior. Unreadable entries are counted into `skipped`, and skipped>0 sets uncertain=true (no clean verdict is offered). Pattern matching is case-sensitive on every platform; symlinks and junctions are not followed. Schema: {claim: \"missing\"|\"exists\", path} for an exact probe, or {claim, pattern, bases:[...]} for a repo-wide search. Verdict logic: claim=missing → any hit REFUTES; claim=exists → any hit CONFIRMS; no hit means the opposite."
}

func (claimCheck) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "claim":{"type":"string","enum":["missing","exists"],"description":"The claim to verify: missing = the target does not exist; exists = it does."},
  "path":{"type":"string","description":"Exact file/dir path to probe. Use when you have a concrete path; mutually exclusive with pattern."},
  "pattern":{"type":"string","description":"Base-name glob, case-sensitive (e.g. \"dev.workflow.yaml\", \"*.md\"). Requires at least one base."},
  "bases":{"type":"array","items":{"type":"string"},"description":"Search root directories (repeatable). Required with pattern."}
},"required":["claim"]}`)
}

func (claimCheck) ReadOnly() bool { return true }

func (claimCheck) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Claim   string   `json:"claim"`
		Path    string   `json:"path"`
		Pattern string   `json:"pattern"`
		Bases   []string `json:"bases"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	result, err := claimcheck.Check(p.Claim, p.Path, p.Pattern, p.Bases)
	if err != nil {
		if strings.Contains(fmt.Sprint(err), "provide") || strings.Contains(fmt.Sprint(err), "must be") || strings.Contains(fmt.Sprint(err), "mutually") {
			return "", fmt.Errorf("%w: pass {claim:\"missing\"|\"exists\", path} or {claim, pattern, bases:[...]}", err)
		}
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
