package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1762 pins the read_file → edit_file source_token contract end to end:
// the citation the host stamps onto a read_file result
// (agent appendReceiptCitation: "\n[source_token <receiptID>]") must be the
// very token the ledger filed the read's observation under, so citing it
// verbatim turns the edit_file evidence gate green. A token the model cannot
// read from the result (or invents) must stay red — that is the deadlock
// shape this contract exists to prevent.

var sourceTokenCitationRe = regexp.MustCompile(`\[source_token ([^\]\s]+)\]`)

func sourceTokenFromResult(result string) string {
	match := sourceTokenCitationRe.FindStringSubmatch(result)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func newSourceTokenGateAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("read_file builtin is not registered")
	}
	edit, ok := tool.LookupBuiltin("edit_file")
	if !ok {
		t.Fatal("edit_file builtin is not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(read)
	reg.Add(edit)
	a := New(&userInputCaptureProvider{}, reg, NewSession("system"), Options{}, event.Discard)
	// The production run loop enables the read coordinator shadow on every
	// turn (run_loop newReadShadowState); a direct executeBatch harness must
	// mirror that state or it exercises the legacy owner by accident.
	a.turn.readShadow = newReadShadowState(true)
	a.task.ledger = evidence.NewLedger()
	return a, path
}

// TestSourceTokenCitationResolvesInLedger pins consistency (验收 1): the token
// printed on the model-visible read result is the token the ledger filed the
// observation under. If the printed value and the ledger token ever drift, the
// model cites a handle the host never issued and the gate deadlocks.
func TestSourceTokenCitationResolvesInLedger(t *testing.T) {
	a, path := newSourceTokenGateAgent(t)
	read, err := json.Marshal(map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	results := relaxedEvidenceRound(t, a, []provider.ToolCall{
		{ID: "r1", Name: "read_file", Arguments: string(read)},
	})
	token := sourceTokenFromResult(results[0])
	if token == "" {
		t.Fatalf("read_file result carries no source_token citation: %q", results[0])
	}
	observations := a.task.ledger.SourceToken(token)
	if len(observations) == 0 {
		t.Fatalf("printed source_token %q names no ledger observation (contract break: the model would be rejected with source_token_unknown)", token)
	}
	if filepath.Clean(observations[0].Path) != filepath.Clean(path) {
		t.Fatalf("citation resolves to %s, want the read path %s", observations[0].Path, path)
	}
	if len(observations[0].LineHashes) != 3 {
		t.Fatalf("citation observation covers %d lines, want the full 3-line window", len(observations[0].LineHashes))
	}
}

// TestSourceTokenPaysForLineRangeEditNextRound is the end-to-end pin (验收 2):
// read in one provider round, cite the printed token on a line_range edit in
// the next — the gate must pass and the edit must apply. The fabricated-token
// counterpart stays blocked: a token the host never issued proves nothing.
func TestSourceTokenPaysForLineRangeEditNextRound(t *testing.T) {
	a, path := newSourceTokenGateAgent(t)
	read, err := json.Marshal(map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	results := relaxedEvidenceRound(t, a, []provider.ToolCall{
		{ID: "r1", Name: "read_file", Arguments: string(read)},
	})
	token := sourceTokenFromResult(results[0])
	if token == "" {
		t.Fatalf("fixture: no source_token citation on the read result: %q", results[0])
	}

	editWith := func(token string) []provider.ToolCall {
		args, err := json.Marshal(map[string]any{
			"path": path, "line_range": "2-2", "new_string": "changed",
			"anchor_head": "beta", "source_token": token,
		})
		if err != nil {
			t.Fatal(err)
		}
		return []provider.ToolCall{{ID: "e1", Name: "edit_file", Arguments: string(args)}}
	}

	// The deadlock shape: a token the model composed itself is rejected once
	// with the concrete recovery, exactly as a model without access to the
	// printed citation would fail.
	fabricated := relaxedEvidenceRound(t, a, editWith("r_made_up"))
	if !strings.Contains(fabricated[0], "does not name a read") {
		t.Fatalf("a fabricated token must be rejected with source_token_unknown: %q", fabricated[0])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\nbeta\ngamma\n" {
		t.Fatalf("a rejected edit must leave the file untouched, file = %q", content)
	}

	// The printed token turns the gate green.
	results = relaxedEvidenceRound(t, a, editWith(token))
	if strings.Contains(results[0], "blocked") {
		t.Fatalf("the printed source_token must satisfy the line_range evidence gate: %q", results[0])
	}
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\nchanged\ngamma\n" {
		t.Fatalf("the cited edit must have applied, file = %q", content)
	}
}

// TestPagedReadEnvelopeKeepsPagingStateUnderCitation pins the agent-level face
// of the tl1762 trailer fix: a paged read whose result ends with the paging
// trailer plus the host citation must still deliver HasMore/NextCursor in the
// stored envelope — not a fabricated EOF. Before ParseReadTrailer learned to
// strip the citation line, every paged-but-under-budget read lost its paging
// state here (HasMore=false, no cursor, wrong SourceEnd).
func TestPagedReadEnvelopeKeepsPagingStateUnderCitation(t *testing.T) {
	a, path := newSourceTokenGateAgent(t)
	full := filepath.Join(filepath.Dir(path), "paged.txt")
	var body strings.Builder
	for line := 1; line <= 8; line++ {
		body.WriteString(strings.Repeat("x", 8))
		body.WriteString("\n")
	}
	if err := os.WriteFile(full, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := json.Marshal(map[string]any{"path": full, "offset": 0, "limit": 4})
	if err != nil {
		t.Fatal(err)
	}
	results := relaxedEvidenceRound(t, a, []provider.ToolCall{
		{ID: "r1", Name: "read_file", Arguments: string(read)},
	})
	if !strings.Contains(results[0], "PARTIAL view") || !strings.Contains(results[0], "[source_token ") {
		t.Fatalf("fixture: paged result must carry both the paging trailer and the citation: %q", results[0])
	}
	var envelope tool.ReadResultEnvelope
	found := false
	for _, m := range a.Session().Snapshot() {
		if m.Role == provider.RoleTool && m.ToolCallID == "r1" && len(m.ReadResult) > 0 {
			if err := json.Unmarshal(m.ReadResult, &envelope); err != nil {
				t.Fatalf("stored ReadResult is not an envelope: %v", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no read envelope was stored for the paged read")
	}
	if !envelope.HasMore || envelope.EOF {
		t.Fatalf("paged read under a citation lost its paging state: HasMore=%v EOF=%v", envelope.HasMore, envelope.EOF)
	}
	if envelope.NextCursor == "" {
		t.Fatal("paged read under a citation produced no continuation cursor")
	}
	if envelope.SourceEnd != nil {
		t.Fatalf("a partial window must not claim a source end: %d", *envelope.SourceEnd)
	}
}
