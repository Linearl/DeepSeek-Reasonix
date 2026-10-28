package control

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/capability"
	"reasonix/internal/event"
	"reasonix/internal/memory"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
	"reasonix/internal/skill"
	"reasonix/internal/tool"
)

func TestTurnContextUsesLiveSkillCatalogAcrossAddEditDelete(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeControlSkill(t, project, ".reasonix/skills/alpha/SKILL.md", "---\ndescription: alpha one\n---\nbody")
	store := skill.New(skill.Options{HomeDir: home, ProjectRoot: project, DisableBuiltins: true})
	sess := agent.NewSession("stable system")
	executor := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Executor: executor, SkillStore: store, Skills: store.List(),
		SessionContextStatic: sessioncontext.Sections{Workspace: "workspace"},
	})

	appendCurrent := func() sessioncontext.Snapshot {
		t.Helper()
		if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
			t.Fatal("expected a replacement context")
		}
		for i := range slices.Backward(sess.Messages) {
			if snapshot, ok := sessioncontext.Parse(sess.Messages[i].Content); ok {
				return snapshot
			}
		}
		t.Fatal("no context found")
		return sessioncontext.Snapshot{}
	}

	first := appendCurrent()
	if !strings.Contains(first.Sections.SkillsCatalog, "alpha one") {
		t.Fatalf("first catalog = %q", first.Sections.SkillsCatalog)
	}
	betaPath := filepath.Join(project, ".reasonix", "skills", "beta", "SKILL.md")
	writeControlSkill(t, project, ".reasonix/skills/beta/SKILL.md", "---\ndescription: beta one\n---\nbody")
	second := appendCurrent()
	if second.Digest == first.Digest || !strings.Contains(second.Sections.SkillsCatalog, "beta one") {
		t.Fatalf("added-skill snapshot = %+v", second)
	}
	writeControlSkill(t, project, ".reasonix/skills/beta/SKILL.md", "---\ndescription: beta edited\n---\nbody")
	third := appendCurrent()
	if third.Digest == second.Digest || !strings.Contains(third.Sections.SkillsCatalog, "beta edited") {
		t.Fatalf("edited-skill snapshot = %+v", third)
	}
	if err := os.Remove(betaPath); err != nil {
		t.Fatal(err)
	}
	fourth := appendCurrent()
	if fourth.Digest == third.Digest || strings.Contains(fourth.Sections.SkillsCatalog, "beta") {
		t.Fatalf("deleted-skill snapshot = %+v", fourth)
	}
	if got := sessionContextCount(sess.Messages); got != 4 {
		t.Fatalf("context count = %d, want one full snapshot per change", got)
	}
	if executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("unchanged live catalog should deduplicate")
	}
}

func TestTurnContextPublishesBackgroundMemoryReplacementWithoutLegacyUpdate(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "user")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	mem := memory.Load(memory.Options{CWD: project, UserDir: userDir})
	sess := agent.NewSession("stable system")
	executor := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: executor, Memory: mem, SessionContextStatic: sessioncontext.Sections{Workspace: "workspace"}})
	if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("initial runtime snapshot was not published")
	}
	if _, err := c.SaveMemory(memory.Memory{
		Name: "currency", Description: "balance currency", Scope: memory.FactScopeGlobal,
		Type: memory.TypeUser, Body: "Use RMB.",
	}); err != nil {
		t.Fatal(err)
	}
	if composed := c.Compose("hello"); strings.Contains(composed, "<memory-update>") {
		t.Fatalf("background save generated legacy update: %q", composed)
	}
	if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("memory change did not publish a context")
	}
	snapshot, ok := latestControlSessionContext(sess.Messages)
	if !ok || !strings.Contains(snapshot.Sections.BackgroundMemory, "currency") || !strings.Contains(snapshot.Sections.BackgroundMemory, "Use RMB.") {
		t.Fatalf("memory snapshot = %+v", snapshot)
	}
	if err := c.ForgetMemory("currency"); err != nil {
		t.Fatal(err)
	}
	if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("forget did not publish a replacement context")
	}
	latest, ok := latestControlSessionContext(sess.Messages)
	if !ok || strings.Contains(latest.Sections.BackgroundMemory, "currency") {
		t.Fatalf("forgotten fact remained in latest snapshot: %+v", latest)
	}
}

func sessionContextCount(messages []provider.Message) int {
	count := 0
	for _, message := range messages {
		if _, ok := sessioncontext.Parse(message.Content); ok && message.Origin == provider.MessageOriginHost {
			count++
		}
	}
	return count
}

// Task 969 acceptance ①: the standing L1 directory rides every real turn as a
// session-context section, and ③④: unchanged content appends nothing (the
// block is reused, keeping the provider prefix cache warm) while a memory-only
// change re-sends only the memory section — the standing catalog bytes are not
// repeated.
func TestTurnContextStandingDirectoryRidesTurnsAndDeltasMemoryOnly(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "user")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	mem := memory.Load(memory.Options{CWD: project, UserDir: userDir})
	sess := agent.NewSession("stable system")
	executor := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	catalogBlock := "```\n- skill:review — Review code\n- tool:gh_pr_list\n```"
	c := New(Options{
		Executor: executor, Memory: mem,
		SessionContextStatic: sessioncontext.Sections{Workspace: "workspace"},
		CapabilityDirectory: func() (string, capability.DirectoryStats) {
			return catalogBlock, capability.DirectoryStats{Bytes: len(catalogBlock), Rows: 2}
		},
	})

	if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("initial snapshot with standing directory was not published")
	}
	first, ok := latestControlSessionContext(sess.Messages)
	if !ok || first.Sections.CapabilityCatalog != catalogBlock {
		t.Fatalf("standing directory missing from first snapshot: %+v", first)
	}
	// ③ unchanged content reuses the same block: no new append.
	if executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("unchanged snapshot appended a duplicate block")
	}
	// ④ memory-only change re-sends only the memory section.
	if _, err := c.SaveMemory(memory.Memory{
		Name: "currency", Description: "balance currency", Scope: memory.FactScopeGlobal,
		Type: memory.TypeUser, Body: "Use RMB.",
	}); err != nil {
		t.Fatal(err)
	}
	if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("memory change did not publish a context")
	}
	delta, ok := latestControlSessionContext(sess.Messages)
	if !ok || delta.Sections.CapabilityCatalog != "" || !strings.Contains(delta.Sections.BackgroundMemory, "Use RMB.") {
		t.Fatalf("memory-only delta resent the directory block: %+v", delta)
	}
	// The merged view still resolves the standing catalog from the first
	// snapshot, so the next unchanged turn deduplicates again.
	if executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("post-delta unchanged turn appended a duplicate block")
	}
	// A catalog change re-sends only the catalog section.
	catalogBlock = "```\n- skill:review — Review code\n- tool:gh_pr_list\n- tool:gh_issue_triage\n```"
	if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("catalog change did not publish a context")
	}
	catalogDelta, ok := latestControlSessionContext(sess.Messages)
	if !ok || catalogDelta.Sections.CapabilityCatalog == "" || catalogDelta.Sections.BackgroundMemory != "" {
		t.Fatalf("catalog delta carried extra sections: %+v", catalogDelta)
	}
}

// Task 969 双态/门控: the directory follows the model-facing skill discovery
// gate — disabled discovery publishes snapshots without the catalog section,
// and a nil renderer keeps the section empty.
func TestTurnContextDirectoryHonorsDiscoveryGate(t *testing.T) {
	sess := agent.NewSession("stable system")
	executor := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Executor:                       executor,
		DisableImplicitSkillInvocation: true,
		SessionContextStatic:           sessioncontext.Sections{Workspace: "workspace"},
		CapabilityDirectory: func() (string, capability.DirectoryStats) {
			return "```\n- skill:review — Review code\n```", capability.DirectoryStats{Bytes: 34, Rows: 1}
		},
	})
	if !executor.AppendTurnContext(c.withTurnContext(context.Background(), true)) {
		t.Fatal("expected an initial snapshot")
	}
	snapshot, ok := latestControlSessionContext(sess.Messages)
	if !ok || snapshot.Sections.CapabilityCatalog != "" || snapshot.Sections.SkillsCatalog != "" {
		t.Fatalf("disabled discovery leaked the directory: %+v", snapshot)
	}

	nilSess := agent.NewSession("stable system")
	nilExecutor := agent.New(nil, tool.NewRegistry(), nilSess, agent.Options{}, event.Discard)
	gated := New(Options{
		Executor:             nilExecutor,
		SessionContextStatic: sessioncontext.Sections{Workspace: "workspace"},
		CapabilityDirectory:  nil,
	})
	if !nilExecutor.AppendTurnContext(gated.withTurnContext(context.Background(), true)) {
		t.Fatal("expected a snapshot with nil directory renderer")
	}
	empty, ok := latestControlSessionContext(nilSess.Messages)
	if !ok || empty.Sections.CapabilityCatalog != "" {
		t.Fatalf("nil renderer produced a catalog section: %+v", empty)
	}
}

func latestControlSessionContext(messages []provider.Message) (sessioncontext.Snapshot, bool) {
	for i := range slices.Backward(messages) {
		if snapshot, ok := sessioncontext.Parse(messages[i].Content); ok && messages[i].Origin == provider.MessageOriginHost {
			return snapshot, true
		}
	}
	return sessioncontext.Snapshot{}, false
}

// Task 1224 phase 2 acceptance: the over-warning-line log warn fires once per
// climb past the warn thresholds while the block still renders in full — the
// phase-1 hard cap and its truncation warning are retired, so this latch is
// the only log-side signal left.
func TestTurnContextDirectoryPressureWarnsOncePerClimb(t *testing.T) {
	sess := agent.NewSession("stable system")
	executor := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	var stats capability.DirectoryStats
	c := New(Options{
		Executor:             executor,
		SessionContextStatic: sessioncontext.Sections{Workspace: "workspace"},
		CapabilityDirectory: func() (string, capability.DirectoryStats) {
			return "```\n- skill:a\n```", stats
		},
	})
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })

	// Climb 1: one over-line warn, rows fully visible.
	stats = capability.DirectoryStats{Bytes: 70 * 1024, Rows: 620, NearCap: true}
	c.withTurnContext(context.Background(), true)
	if got := strings.Count(logs.String(), "over the warning line"); got != 1 {
		t.Fatalf("over-line climb 1 logged %d warnings, want 1:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), "bytes=71680") || !strings.Contains(logs.String(), "warn_bytes=65536") {
		t.Fatalf("over-line warning missing the readings:\n%s", logs.String())
	}
	// Steady over-line state: no re-warn every turn.
	c.withTurnContext(context.Background(), true)
	if got := strings.Count(logs.String(), "over the warning line"); got != 1 {
		t.Fatalf("steady over-line state re-warned (%d warnings), want 1", got)
	}
	// Back under the thresholds: the latch resets silently.
	stats = capability.DirectoryStats{Bytes: 40, Rows: 1}
	c.withTurnContext(context.Background(), true)
	if got := strings.Count(logs.String(), "over the warning line"); got != 1 {
		t.Fatalf("recovery state logged a warning (%d total), want 1", got)
	}
	// Climb 2 after recovery: warns again.
	stats = capability.DirectoryStats{Bytes: 70 * 1024, Rows: 620, NearCap: true}
	c.withTurnContext(context.Background(), true)
	if got := strings.Count(logs.String(), "over the warning line"); got != 2 {
		t.Fatalf("over-line climb 2 logged %d cumulative warnings, want 2:\n%s", got, logs.String())
	}
	// The retired truncation machinery must stay retired: no cap/drop wording
	// anywhere in the warning stream.
	for _, retired := range []string{"truncated at budget cap", "cap_bytes", "dropped="} {
		if strings.Contains(logs.String(), retired) {
			t.Fatalf("retired truncation warning wording %q reappeared:\n%s", retired, logs.String())
		}
	}
}
