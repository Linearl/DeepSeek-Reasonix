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

// The rejection must never be delivered silently: one replay, a searchable
// notice, and a closed error when the provider rejects again — not a final
// answer, not an unbounded loop.
func TestProviderRejectionReplaysOnceThenFailsClosed(t *testing.T) {
	sink := &incompleteReadEventSink{}
	prov := &rejectionProvider{}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	err := a.Run(context.Background(), "list my tasks")
	if err == nil {
		t.Fatal("repeated provider rejection must end the run as an error, not as a delivered answer")
	}
	if !strings.Contains(err.Error(), "provider rejected") {
		t.Fatalf("error = %v, want the provider-rejection failure", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want exactly 2 (one bounded replay, no loop)", prov.call)
	}
	if !sink.hasCode(event.NoticeCodeProviderRejection) {
		t.Fatal("no provider_rejection notice — the rejection was silent")
	}
	// Delivery contract is proven by the error above: the run did NOT end on
	// the canned sentence. The retry instruction reached the model as a
	// host-generated turn message:
	sawReplayInstruction := false
	for _, m := range a.Session().Snapshot() {
		if m.Role == provider.RoleUser && strings.Contains(strings.ToLower(m.Content), "replay the answer once") {
			sawReplayInstruction = true
			break
		}
	}
	if !sawReplayInstruction {
		t.Fatal("no host replay instruction in the session — the bounded retry was never requested")
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
}
