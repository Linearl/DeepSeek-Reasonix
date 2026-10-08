package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/skill"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// Task 632: the general-purpose writer profile. The denylist (ask), the
// default-prompt structure clause, the usage line, and the end-to-end writer
// run are all pinned here; the availability switch itself is exercised at the
// skill-store layer (internal/skill) and the boot wiring (internal/boot).

func generalPurposeLookup(store *skill.Store) ProfileLookup {
	return func(name string) (ProfileDefinition, bool) {
		sk, ok := store.Read(name)
		if !ok {
			return ProfileDefinition{}, false
		}
		return ProfileFromSkill(sk), true
	}
}

func TestGeneralPurposeProfileIsNamedBuiltinWriter(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir(), ProjectRoot: t.TempDir()})
	sk, ok := store.Read(skill.GeneralPurposeProfileName)
	if !ok {
		t.Fatal("general-purpose built-in missing from the skill store")
	}
	def := ProfileFromSkill(sk)
	if !def.NamedBuiltin {
		t.Fatal("general-purpose must be flagged as a named built-in")
	}
	if def.ReadOnly {
		t.Fatal("general-purpose must resolve as a writer profile")
	}
	if def.Body == "" {
		t.Fatal("general-purpose body must carry the full system prompt")
	}
}

// The denylist wins over every allow source: a call-level tools whitelist that
// names ask still loses it under the general-purpose profile, while an unnamed
// writer task keeps the exact historical face (ask included).
func TestGeneralPurposeRegistryDenylistStripsAsk(t *testing.T) {
	root := t.TempDir()
	parent := tool.NewRegistry()
	parent.Add(NewAskTool())
	parent.Add(stubWrite{})
	parent.Add(stubBash{})

	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       &mockProvider{name: "sub"},
		ParentRegistry: parent,
		SysPrompt:      "sys",
	}).WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithProfileLookup(generalPurposeLookup(skill.New(skill.Options{HomeDir: t.TempDir(), ProjectRoot: root})))

	spec, err := task.buildTaskSpec(context.Background(), "x", "", skill.GeneralPurposeProfileName, nil, []string{"ask", "write_file"}, 0, "", "", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	names, err := IntersectToolLists(parent, spec.Grant.ProfileTools, spec.Grant.CallTools)
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := task.buildSubagentRegistry(spec, names, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("ask"); ok {
		t.Fatal("general-purpose registry must not expose ask (deny wins over the call allowlist)")
	}
	if _, ok := reg.Get("write_file"); !ok {
		t.Fatal("general-purpose registry lost its writer tool")
	}

	// Unnamed task: unchanged historical behavior — ask stays.
	bare, err := task.buildTaskSpec(context.Background(), "x", "", "", nil, nil, 0, "", "", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	bareReg, _, err := task.buildSubagentRegistry(bare, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bareReg.Get("ask"); !ok {
		t.Fatal("unnamed writer task must keep ask (existing behavior)")
	}
}

// 越权路径：a general-purpose dispatch with explicit write_paths rejects writes
// outside the claim at execution time (path-bound writer enforcement).
func TestGeneralPurposeWriteOutsideClaimBlocked(t *testing.T) {
	root := t.TempDir()
	parent := tool.NewRegistry()
	parent.Add(stubWrite{})

	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       &mockProvider{name: "sub"},
		ParentRegistry: parent,
		SysPrompt:      "sys",
	}).WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithProfileLookup(generalPurposeLookup(skill.New(skill.Options{HomeDir: t.TempDir(), ProjectRoot: root})))

	spec, err := task.buildTaskSpec(context.Background(), "x", "", skill.GeneralPurposeProfileName, []string{"docs/report.md"}, nil, 0, "", "", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := task.buildSubagentRegistry(spec, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	writer, ok := reg.Get("write_file")
	if !ok {
		t.Fatal("write_file missing from path-bound registry")
	}
	if _, err := writer.Execute(context.Background(), json.RawMessage(`{"path":"escape.txt"}`)); err == nil ||
		!strings.Contains(err.Error(), "outside this subagent's declared write_paths") {
		t.Fatalf("out-of-claim write = %v, want write_paths rejection", err)
	}
	if _, err := writer.Execute(context.Background(), json.RawMessage(`{"path":"docs/report.md"}`)); err != nil {
		t.Fatalf("in-claim write rejected: %v", err)
	}
}

func TestFormatSubagentRunResultUsageNote(t *testing.T) {
	store := NewSubagentStore(t.TempDir())
	run, err := store.PrepareFresh(SubagentSpec{Kind: "task", Name: "task", ParentSession: "parent-session", SystemPrompt: "sys"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Release()

	out := FormatSubagentRunResult("all done", run, false, "Subagent usage: 2 tool calls, 2 requests, 61342 tokens, 48s.")
	usageAt := strings.Index(out, "Subagent usage:")
	refAt := strings.Index(out, "To continue this same subagent transcript")
	answerAt := strings.Index(out, "Final answer:")
	if usageAt < 0 || refAt < 0 || answerAt < 0 || !(refAt < usageAt && usageAt < answerAt) {
		t.Fatalf("usage line must sit between the reference guidance and the final answer:\n%s", out)
	}

	// No note: byte-identical historical shape.
	if plain := FormatSubagentRunResult("all done", run, false); strings.Contains(plain, "Subagent usage:") {
		t.Fatalf("plain result must not gain a usage line:\n%s", plain)
	}
	// Ephemeral runs keep the bare-answer contract even with a note.
	if eph := FormatSubagentRunResult("all done", nil, false, "Subagent usage: 1 tool calls, 1 requests, 10 tokens, 1s."); eph != "all done" {
		t.Fatalf("ephemeral result changed shape: %q", eph)
	}
}

func TestSubagentUsageCountersFromEvents(t *testing.T) {
	trk := newSubagentProgressTracker(context.Background(), event.Discard)
	// The tracker owns its merger goroutine when created outside a parent task
	// group; finish closes it (the real run path does this in its defer).
	defer trk.finish(nil, nil)
	sink := trk.wrap()
	sink.Emit(event.Event{Kind: event.ToolDispatch})
	sink.Emit(event.Event{Kind: event.ToolDispatch})
	sink.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 20}})
	sink.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 5, CompletionTokens: 3}})
	summary := trk.usageSummary()
	if summary.ToolCalls != 2 || summary.Requests != 2 || summary.Tokens != 128 {
		t.Fatalf("usage summary = %+v, want 2 calls / 2 requests / 128 tokens", summary)
	}
	line := formatSubagentUsageLine(summary)
	if !strings.Contains(line, "2 tool calls") || !strings.Contains(line, "128 tokens") {
		t.Fatalf("usage line = %q", line)
	}
	if empty := formatSubagentUsageLine(SubagentUsageSummary{}); empty != "" {
		t.Fatalf("zero usage must render no line, got %q", empty)
	}
}

// 红线守护：the structure clause lands on the writer default only. The
// read-only default is the load-bearing read-only contract text and stays
// byte-identical.
func TestDefaultPromptStructureClauseScope(t *testing.T) {
	for _, want := range []string{"what you did", "what changed", "what you verified"} {
		if !strings.Contains(DefaultTaskSystemPrompt, want) {
			t.Fatalf("writer default prompt missing structure clause %q:\n%s", want, DefaultTaskSystemPrompt)
		}
	}
	if strings.Contains(DefaultReadOnlyTaskSystemPrompt, "what changed") {
		t.Fatalf("read-only default prompt must stay byte-identical (no structure clause):\n%s", DefaultReadOnlyTaskSystemPrompt)
	}
}

// End-to-end: flag on → real skill-store lookup → writer child edits a file
// within its claim → usage line + reference + final answer return; the file is
// the deliverable on disk.
func TestGeneralPurposeEndToEndWriterRun(t *testing.T) {
	root := t.TempDir()
	store := skill.New(skill.Options{HomeDir: t.TempDir(), ProjectRoot: root})
	if _, ok := store.Read(skill.GeneralPurposeProfileName); !ok {
		t.Fatal("general-purpose built-in missing")
	}

	parent := tool.NewRegistry()
	ws := builtin.Workspace{Dir: root}
	for _, tl := range ws.Tools("write_file", "read_file") {
		parent.Add(tl)
	}

	finalText := []provider.Chunk{{Type: provider.ChunkText, Text: "Did: wrote the report. Changed: out/report.md. Verified: read back. Open questions: none."}, {Type: provider.ChunkDone}}
	sub := &scriptedProvider{name: "sub", turns: [][]provider.Chunk{
		{toolCallChunk("w1", "write_file", `{"path":"out/report.md","content":"# Report"}`),
			{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 20}}, {Type: provider.ChunkDone}},
		{toolCallChunk("r1", "read_file", `{"path":"out/report.md"}`),
			{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 20}}, {Type: provider.ChunkDone}},
		finalText,
		finalText,
	}}

	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       sub,
		ParentRegistry: parent,
		SysPrompt:      DefaultTaskSystemPrompt,
	}).WithTranscripts(NewSubagentStore(t.TempDir()), root, "base-model", "base-effort").
		WithProfileLookup(generalPurposeLookup(store))

	out, err := task.Execute(testTaskContext(),
		json.RawMessage(`{"prompt":"write the report","profile":"general-purpose","write_paths":["out/report.md"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{
		"Subagent reference: sa_",
		"Subagent usage: 2 tool calls",
		"Final answer:",
		"Did: wrote the report",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("result missing %q\n---\n%s", want, out)
		}
	}
	// The profile body is the full system prompt — no default stacking.
	sys := sub.requests[0].Messages[0].Content
	if !strings.Contains(sys, "general-purpose subagent") || !strings.Contains(sys, "scope creep") {
		t.Fatalf("child system prompt is not the general-purpose body:\n%s", sys)
	}
	if strings.Contains(sys, "You are a sub-agent invoked by a parent coding agent to carry out one focused task.") {
		t.Fatalf("child system prompt stacked the bare-task default:\n%s", sys)
	}
	// The deliverable exists on disk, inside the claim.
	data, err := os.ReadFile(filepath.Join(root, "out", "report.md"))
	if err != nil || string(data) != "# Report" {
		t.Fatalf("deliverable = %q, %v", data, err)
	}
}

// Flag off: the built-in is hidden from the store, so the dispatch boundary
// treats "general-purpose" like any unknown profile — the off state is the
// pre-632 surface.
func TestGeneralPurposeGateOffUnknownProfileAtDispatch(t *testing.T) {
	root := t.TempDir()
	store := skill.New(skill.Options{
		HomeDir:             t.TempDir(),
		ProjectRoot:         root,
		DisableBuiltinNames: []string{skill.GeneralPurposeProfileName},
	})
	parent := tool.NewRegistry()
	for _, tl := range (builtin.Workspace{Dir: root}).Tools("write_file") {
		parent.Add(tl)
	}
	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       &scriptedProvider{name: "sub", turns: [][]provider.Chunk{{{Type: provider.ChunkText, Text: "x"}, {Type: provider.ChunkDone}}}},
		ParentRegistry: parent,
		SysPrompt:      "sys",
	}).WithTranscripts(NewSubagentStore(t.TempDir()), root, "base-model", "base-effort").
		WithProfileLookup(generalPurposeLookup(store))

	_, err := task.Execute(withCallContext(context.Background(), "c", event.Discard, nil, false),
		json.RawMessage(`{"prompt":"x","profile":"general-purpose"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Fatalf("flag-off dispatch = %v, want unknown profile rejection", err)
	}
}
