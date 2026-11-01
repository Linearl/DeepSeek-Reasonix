package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 286 (upstream #10721): whole-sentence / strict-prefix matching only —
// a fixed template, never a keyword scan, so ordinary prose that merely
// mentions the words stays a normal answer.
func TestIsKnownProviderRejectionMatchesOnlyTheFixedTemplate(t *testing.T) {
	const template = "The request was rejected because it was considered high risk"
	cases := []struct {
		name string
		text string
		want bool
	}{
		{name: "exact sentence", text: template, want: true},
		{name: "trimmed sentence", text: "  \n\t" + template + " \n", want: true},
		{name: "provider suffix after template", text: template + " (request id: 42)", want: true},
		{name: "empty", text: "", want: false},
		{name: "normal answer", text: "All checks passed; the session list is in the report.", want: false},
		{name: "mentions the words in prose", text: "The provider said a request was rejected, but this answer is real.", want: false},
		{name: "shorter fragment is not the template", text: "The request was rejected", want: false},
		{name: "template not at the start", text: "According to policy: " + template, want: false},
	}
	for _, tc := range cases {
		if got := isKnownProviderRejection(tc.text); got != tc.want {
			t.Errorf("%s: isKnownProviderRejection(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

// rejectionProvider replies with the fixed safety sentence on every round —
// the provider-side shape measured in #10721 (normal content stream, real
// generation time, one canned sentence).
type rejectionProvider struct {
	call int
}

func (p *rejectionProvider) Name() string { return "rejection-provider" }

func (p *rejectionProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.call++
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "The request was rejected because it was considered high risk"}
	close(ch)
	return ch, nil
}

// userInstructions returns the content of every host-generated user message
// matching needle, in conversation order.
func userInstructions(t *testing.T, a *Agent, needle string) []string {
	t.Helper()
	var found []string
	for _, m := range a.Session().Snapshot() {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, needle) {
			found = append(found, m.Content)
		}
	}
	return found
}

// Task 1314: the rejection ladder is bounded by construction — rejection #1
// injects the rewrite instruction, rejection #2 injects the degrade fallback
// (content into a file, reply reduced to a progress summary), and rejection
// #3 ends the run as an error. The third rejection must not inject anything:
// no third host instruction, no fourth provider call, never a loop.
func TestProviderRejectionLadderRewriteThenDegradeThenHardCap(t *testing.T) {
	sink := &incompleteReadEventSink{}
	prov := &rejectionProvider{}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	err := a.Run(context.Background(), "list my tasks")
	if err == nil {
		t.Fatal("a third provider rejection must end the run as an error, not as a delivered answer")
	}
	if !strings.Contains(err.Error(), "provider rejected") {
		t.Fatalf("error = %v, want the provider-rejection failure", err)
	}
	// Hard cap: 1 original round + exactly 2 bounded recovery rounds. A 4th
	// call would mean the ladder kept replaying after the cap.
	if prov.call != 3 {
		t.Fatalf("provider calls = %d, want exactly 3 (original + rewrite round + degrade round, cap enforced)", prov.call)
	}
	if !sink.hasCode(event.NoticeCodeProviderRejection) {
		t.Fatal("no provider_rejection notice — the rejection was silent")
	}
	if !sink.hasCode(event.NoticeCodeProviderRejectionRewrite) {
		t.Fatal("no provider_rejection_rewrite notice — the stage-1 rewrite round never fired")
	}
	if !sink.hasCode(event.NoticeCodeProviderRejectionDegrade) {
		t.Fatal("no provider_rejection_degrade notice — the stage-2 degrade round never fired")
	}
	// Exactly two host recovery instructions, in ladder order: rewrite first,
	// degrade second, and nothing after the cap.
	rewrites := userInstructions(t, a, "rewrite the answer once")
	degrades := userInstructions(t, a, "file-write tool")
	if len(rewrites) != 1 || len(degrades) != 1 {
		t.Fatalf("host instructions: %d rewrite, %d degrade, want exactly 1 each", len(rewrites), len(degrades))
	}
	snapshot := a.Session().Snapshot()
	rewriteIdx, degradeIdx := -1, -1
	for i, m := range snapshot {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "rewrite the answer once") {
			rewriteIdx = i
		}
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "file-write tool") {
			degradeIdx = i
		}
	}
	if rewriteIdx == -1 || degradeIdx == -1 || rewriteIdx > degradeIdx {
		t.Fatalf("ladder order broken: rewrite at %d, degrade at %d, want rewrite before degrade", rewriteIdx, degradeIdx)
	}
}

// The ladder stops as soon as a recovery round succeeds: one rejection, the
// rewrite round produces a real answer, and the degrade stage must never arm.
func TestProviderRejectionRewriteRoundRecoversAndLadderStops(t *testing.T) {
	const template = "The request was rejected because it was considered high risk"
	sink := &incompleteReadEventSink{}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: template}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "All checks passed: 5 tasks listed."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "list my tasks"); err != nil {
		t.Fatalf("run after rewrite round failed: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want exactly 2 (original + one rewrite round)", prov.call)
	}
	if !sink.hasCode(event.NoticeCodeProviderRejectionRewrite) {
		t.Fatal("no rewrite notice for the first rejection")
	}
	if sink.hasCode(event.NoticeCodeProviderRejectionDegrade) {
		t.Fatal("degrade notice fired even though the rewrite round recovered — the ladder did not stop")
	}
	if userInstructions(t, a, "file-write tool") != nil {
		t.Fatal("degrade instruction injected although the rewrite round succeeded")
	}
}

// The degrade fallback is a real exit, not a dead end: after the second
// rejection the model can dump the full content into a workspace file through
// the ordinary write tool and deliver a short progress summary as the final
// answer — and the summary round starts a fresh streak (tl-286 semantics).
func TestProviderRejectionDegradeRoundRecoversViaFileDump(t *testing.T) {
	const template = "The request was rejected because it was considered high risk"
	sink := &incompleteReadEventSink{}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: template}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: template}, {Type: provider.ChunkDone}},
		{toolCallChunk("w1", "write_file", `{"path":"report.md"}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "Saved report.md: 3 sections, 12 steps done."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	reg.Add(fakeWriteFileTool{})
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "write the report"); err != nil {
		t.Fatalf("run after degrade round failed: %v", err)
	}
	if prov.call != 4 {
		t.Fatalf("provider calls = %d, want exactly 4 (2 rejections + file-dump tool round + summary round)", prov.call)
	}
	if !sink.hasCode(event.NoticeCodeProviderRejectionDegrade) {
		t.Fatal("no degrade notice — the fallback strategy was never requested")
	}
	// The degrade instruction must precede the file-dump tool round it asks for.
	snapshot := a.Session().Snapshot()
	degradeIdx, writeIdx := -1, -1
	for i, m := range snapshot {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "file-write tool") {
			degradeIdx = i
		}
		if m.Role == provider.RoleTool && m.Name == "write_file" {
			writeIdx = i
		}
	}
	if degradeIdx == -1 || writeIdx == -1 || degradeIdx > writeIdx {
		t.Fatalf("degrade flow broken: degrade instruction at %d, write_file result at %d, want instruction first", degradeIdx, writeIdx)
	}
}

// A normal answer that only resembles the topic must pass through untouched:
// no notice, no replay, clean turn end.
func TestNormalAnswerIsNotTreatedAsRejection(t *testing.T) {
	sink := &incompleteReadEventSink{}
	final := []provider.Chunk{{Type: provider.ChunkText, Text: "All checks passed."}, {Type: provider.ChunkDone}}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("w", "read_file", `{"path":"a.go"}`), {Type: provider.ChunkDone}},
		final,
	}}
	reg := tool.NewRegistry()
	reg.Add(fakeReadFileTool{})
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "check a.go"); err != nil {
		t.Fatalf("normal run failed: %v", err)
	}
	if sink.hasCode(event.NoticeCodeProviderRejection) {
		t.Fatal("normal answer fired the provider-rejection notice")
	}
	if sink.hasCode(event.NoticeCodeProviderRejectionRewrite) || sink.hasCode(event.NoticeCodeProviderRejectionDegrade) {
		t.Fatal("normal answer fired a rejection-ladder notice")
	}
}
