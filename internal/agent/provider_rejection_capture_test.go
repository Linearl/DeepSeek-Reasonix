package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1742 acceptance matrix (each test pins one cell; no SKIPs anywhere):
//
//	M1  rejection #1 arms a pending case (A digest + B + C1 + metadata), nothing on disk yet
//	M2  rewrite round passes  → exactly one case file, outcome=rewrite_passed, A/B/C/D complete
//	M3  rejection #2 extends the same case with round 2 (B2 + C2)
//	M4  degrade round passes  → outcome=degrade_passed, two rounds, D = progress summary
//	M5  ladder exhausted      → outcome=exhausted file still lands, terminal error unchanged
//	M6  capture dir unwritable → ladder runs identically, failure is silent
//	M7  no rejection          → zero files, zero behavior change
//	M8  tool round resets the streak → pending case finalizes as streak_reset_by_toolwork, next streak gets its own file
//
// The pinned rejection template sentence (measured shape from #10721).
const tl1742RejectionText = "The request was rejected because it was considered high risk"

// withCaptureDir points the capture at a fresh temp dir and restores the
// override when the test ends.
func withCaptureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetRejectionCasesDir(dir)
	t.Cleanup(func() { SetRejectionCasesDir("") })
	return dir
}

// readRejectionCases loads every case file in dir.
func readRejectionCases(t *testing.T, dir string) []rejectionCaseFile {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("capture dir unreadable: %v", err)
	}
	var cases []rejectionCaseFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("case file %s unreadable: %v", e.Name(), err)
		}
		var c rejectionCaseFile
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("case file %s not valid JSON: %v", e.Name(), err)
		}
		cases = append(cases, c)
	}
	return cases
}

// M1+M2 (red-to-green main pin): one rejection, the rewrite round delivers a
// real answer — the case file must carry all four segments plus metadata.
func TestRejectionCaptureWritesFullABCDCaseOnRewritePass(t *testing.T) {
	dir := withCaptureDir(t)
	sink := &incompleteReadEventSink{}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: tl1742RejectionText}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "All checks passed: 5 tasks listed."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "list my tasks"); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	cases := readRejectionCases(t, dir)
	if len(cases) != 1 {
		t.Fatalf("case files = %d, want exactly 1 (armed at rejection, flushed on the recovered final)", len(cases))
	}
	c := cases[0]
	if c.SchemaVersion != rejectionCaseSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", c.SchemaVersion, rejectionCaseSchemaVersion)
	}
	if c.Outcome != rejectionCaseOutcomeRewrite {
		t.Fatalf("outcome = %q, want %q", c.Outcome, rejectionCaseOutcomeRewrite)
	}
	if len(c.Rounds) != 1 {
		t.Fatalf("rounds = %d, want 1", len(c.Rounds))
	}
	r := c.Rounds[0]
	// B segment: the rejected reply, verbatim.
	if !strings.Contains(r.RejectedReply, "The request was rejected because") {
		t.Fatalf("rejected_reply missing the B segment: %q", r.RejectedReply)
	}
	// C segment: the stage-1 rewrite instruction the host actually injected.
	if !strings.Contains(r.RewriteInstruction, "rewrite the answer once") {
		t.Fatalf("rewrite_instruction missing the C segment: %q", r.RewriteInstruction)
	}
	if r.Stage != 1 {
		t.Fatalf("round stage = %d, want 1", r.Stage)
	}
	// D segment: the rewritten reply that got through.
	if c.RewrittenReply != "All checks passed: 5 tasks listed." {
		t.Fatalf("rewritten_reply = %q, want the recovered final verbatim", c.RewrittenReply)
	}
	// A segment: the context digest must include the trigger user message.
	if c.Context.MessageCount < 1 {
		t.Fatalf("context_before.message_count = %d, want >= 1", c.Context.MessageCount)
	}
	if !strings.Contains(c.Context.TriggerUserMessage, "list my tasks") {
		t.Fatalf("trigger_user_message = %q, want the real user input", c.Context.TriggerUserMessage)
	}
	if len(c.Context.RecentMessages) == 0 {
		t.Fatal("context_before.recent_messages empty — no A-segment previews captured")
	}
	// Metadata: timestamps parse, provider recorded.
	if _, err := time.Parse(time.RFC3339, c.CreatedAt); err != nil {
		t.Fatalf("created_at %q not RFC3339: %v", c.CreatedAt, err)
	}
	if _, err := time.Parse(time.RFC3339, c.CollectedAt); err != nil {
		t.Fatalf("collected_at %q not RFC3339: %v", c.CollectedAt, err)
	}
	if c.Provider != "p" {
		t.Fatalf("provider = %q, want p", c.Provider)
	}
	if c.SessionRef == "" {
		t.Fatal("session_ref empty — expected a transport-session tag (or \"nosession\")")
	}
}

// M3+M4: both rungs rejected, the file-dump fallback delivers through the
// write tool — the case carries two rounds and the degrade outcome.
func TestRejectionCaptureDegradePassCaseHasTwoRounds(t *testing.T) {
	dir := withCaptureDir(t)
	sink := &incompleteReadEventSink{}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: tl1742RejectionText}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: tl1742RejectionText}, {Type: provider.ChunkDone}},
		{toolCallChunk("w1", "write_file", `{"path":"report.md"}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "Saved report.md: 3 sections, 12 steps done."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	reg.Add(fakeWriteFileTool{})
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "write the report"); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	cases := readRejectionCases(t, dir)
	if len(cases) != 1 {
		t.Fatalf("case files = %d, want exactly 1 (one streak, one file)", len(cases))
	}
	c := cases[0]
	if c.Outcome != rejectionCaseOutcomeDegrade {
		t.Fatalf("outcome = %q, want %q", c.Outcome, rejectionCaseOutcomeDegrade)
	}
	if len(c.Rounds) != 2 {
		t.Fatalf("rounds = %d, want 2 (rewrite rejected + degrade rejected)", len(c.Rounds))
	}
	if c.Rounds[1].Stage != 2 {
		t.Fatalf("round 2 stage = %d, want 2", c.Rounds[1].Stage)
	}
	if !strings.Contains(c.Rounds[1].RewriteInstruction, "file-write tool") {
		t.Fatalf("round 2 instruction = %q, want the degrade fallback text", c.Rounds[1].RewriteInstruction)
	}
	if !strings.Contains(c.RewrittenReply, "Saved report.md") {
		t.Fatalf("rewritten_reply = %q, want the post-degrade summary", c.RewrittenReply)
	}
}

// M5: the third rejection caps the ladder and ends the run as an error — the
// case file must still land (outcome=exhausted) so the failed ladder stays
// analyzable.
func TestRejectionCaptureExhaustedStillWritesCase(t *testing.T) {
	dir := withCaptureDir(t)
	sink := &incompleteReadEventSink{}
	prov := &rejectionProvider{}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	err := a.Run(context.Background(), "list my tasks")
	if err == nil || !strings.Contains(err.Error(), "provider rejected") {
		t.Fatalf("err = %v, want the provider-rejection terminal error (ladder behavior must not change)", err)
	}

	cases := readRejectionCases(t, dir)
	if len(cases) != 1 {
		t.Fatalf("case files = %d, want exactly 1", len(cases))
	}
	c := cases[0]
	if c.Outcome != rejectionCaseOutcomeExhausted {
		t.Fatalf("outcome = %q, want %q", c.Outcome, rejectionCaseOutcomeExhausted)
	}
	if len(c.Rounds) != 2 {
		t.Fatalf("rounds = %d, want 2 (the two injected recovery rounds)", len(c.Rounds))
	}
	if c.RewrittenReply != "" {
		t.Fatalf("rewritten_reply = %q, want empty (no round ever got through)", c.RewrittenReply)
	}
}

// M6 (fail-safety pin): the capture directory is unwritable (the override
// path is an existing FILE). The ladder must behave byte-identically: the
// rewrite instruction is injected, the recovered answer is delivered, the run
// succeeds, and the failure never surfaces as an error or a user notice.
func TestRejectionCaptureFailureDoesNotBlockLadder(t *testing.T) {
	dir := t.TempDir()
	occupied := filepath.Join(dir, "occupied")
	if err := os.WriteFile(occupied, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	SetRejectionCasesDir(occupied)
	t.Cleanup(func() { SetRejectionCasesDir("") })

	sink := &incompleteReadEventSink{}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: tl1742RejectionText}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "Recovered answer after the rewrite round."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	// The capture write will fail; the ladder must not notice.
	if err := a.Run(context.Background(), "list my tasks"); err != nil {
		t.Fatalf("run failed although capture failure must be non-blocking: %v", err)
	}
	if !sink.hasCode(event.NoticeCodeProviderRejectionRewrite) {
		t.Fatal("rewrite notice missing — the capture failure disturbed the ladder")
	}
	// The occupied path must still be a plain file: nothing "repaired" it.
	info, err := os.Stat(occupied)
	if err != nil || info.IsDir() {
		t.Fatalf("occupied path changed state: stat err=%v isDir=%v", err, info != nil && info.IsDir())
	}
}

// M7: a clean run with no rejection writes nothing and creates nothing.
func TestNoRejectionWritesNoCase(t *testing.T) {
	dir := withCaptureDir(t)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: "All checks passed."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, &incompleteReadEventSink{})

	if err := a.Run(context.Background(), "check a.go"); err != nil {
		t.Fatalf("normal run failed: %v", err)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Fatalf("capture dir has %d entries after a clean run, want 0", len(entries))
	}
}

// M8: a tool round resets the rejection streak counter while the pending case
// is still armed. The pending case must finalize as streak_reset_by_toolwork,
// and the new streak gets its own case file.
func TestStreakResetByToolWorkFinalizesPendingCase(t *testing.T) {
	dir := withCaptureDir(t)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: tl1742RejectionText}, {Type: provider.ChunkDone}},
		{toolCallChunk("r1", "read_file", `{"path":"a.go"}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: tl1742RejectionText}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "Recovered after the fresh streak."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	reg.Add(fakeReadFileTool{})
	a := New(prov, reg, NewSession("sys"), Options{}, &incompleteReadEventSink{})

	if err := a.Run(context.Background(), "check a.go"); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	cases := readRejectionCases(t, dir)
	if len(cases) != 2 {
		t.Fatalf("case files = %d, want exactly 2 (reset streak + fresh streak)", len(cases))
	}
	var resetCase, freshCase *rejectionCaseFile
	for i := range cases {
		switch cases[i].Outcome {
		case rejectionCaseOutcomeReset:
			resetCase = &cases[i]
		case rejectionCaseOutcomeRewrite:
			freshCase = &cases[i]
		}
	}
	if resetCase == nil {
		t.Fatalf("no streak_reset_by_toolwork case among outcomes %v", outcomes(cases))
	}
	if len(resetCase.Rounds) != 1 {
		t.Fatalf("reset case rounds = %d, want 1 (only the first rejection belongs to it)", len(resetCase.Rounds))
	}
	if resetCase.RewrittenReply != "" {
		t.Fatalf("reset case rewritten_reply = %q, want empty", resetCase.RewrittenReply)
	}
	if freshCase == nil {
		t.Fatalf("no rewrite_passed case among outcomes %v", outcomes(cases))
	}
	if len(freshCase.Rounds) != 1 || !strings.Contains(freshCase.RewrittenReply, "Recovered after the fresh streak") {
		t.Fatalf("fresh case incomplete: rounds=%d reply=%q", len(freshCase.Rounds), freshCase.RewrittenReply)
	}
}

func outcomes(cases []rejectionCaseFile) []string {
	var out []string
	for _, c := range cases {
		out = append(out, c.Outcome)
	}
	return out
}
