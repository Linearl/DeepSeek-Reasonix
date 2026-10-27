package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/memory"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// The tl-1194 guard suite: instruction files (AGENTS.md / REASONIX.md) and
// durable memory must reach the provider-visible system prompt after a
// compaction and a same-tab /new or /clear — the valve the v1.32 merge
// silently dropped along with the original guard tests (3c5e6b5e7). If these
// tests are ever deleted again, rebuildHostState loses its only callers and
// the stale-prompt bug ships invisibly a second time.

// memoryRefreshFixture lays down a workspace with a standing instruction doc
// plus a user config root, so memory.Load discovers both the doc-memory file
// and the auto-memory store (the store's presence is what makes the # Memory
// policy block render into Compose output).
func memoryRefreshFixture(t *testing.T) (ws, userDir string) {
	t.Helper()
	root := t.TempDir()
	ws = filepath.Join(root, "ws")
	userDir = filepath.Join(root, "user")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"),
		[]byte("## Standing instructions\n\nKeep answers terse.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, userDir
}

// countingMemoryReload mirrors the boot memoryReload closure (memory.Compose
// over a fresh disk load) and counts invocations, so tests can tell "the
// rebuild never ran" apart from "the rebuild ran but wrote nothing".
func countingMemoryReload(base, ws, userDir string, calls *atomic.Int64) func() string {
	return func() string {
		calls.Add(1)
		return memory.Compose(base, memory.Load(memory.Options{CWD: ws, UserDir: userDir}))
	}
}

// bulkPinnedFixture installs one pinned project fact so the pinned section of
// the session-context has content, and returns the fact file path for direct
// external edits (acceptance ⑧).
func bulkPinnedFixture(t *testing.T, ws, userDir, body string) string {
	t.Helper()
	store := memory.StoreFor(userDir, ws)
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fact := "---\n" +
		"id: mem-pin-1\n" +
		"revision: 1\n" +
		"created_at: 2026-07-01T00:00:00Z\n" +
		"updated_at: 2026-07-01T00:00:00Z\n" +
		"name: deploy-checklist\n" +
		"title: Deploy checklist\n" +
		"description: Always-on deploy checklist\n" +
		"activation: pinned\n" +
		"metadata:\n" +
		"    type: project\n" +
		"    scope: project\n" +
		"---\n\n" + body + "\n"
	path := filepath.Join(store.Dir, "deploy-checklist.md")
	if err := os.WriteFile(path, []byte(fact), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// addBulkHistory appends a transcript heavy enough that a forced maintenance
// pass performs a real fold (same shape the branch sidecar tests use).
func addBulkHistory(sess *agent.Session) {
	for i := range 30 {
		id := "bulk-" + string(rune('a'+i))
		sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file", Arguments: "{}"}}})
		sess.Add(provider.Message{Role: provider.RoleTool, ToolCallID: id, Name: "read_file", Content: strings.Repeat("line\n", 200)})
	}
}

func bulkHistorySession(system string) *agent.Session {
	sess := agent.NewSession(system)
	addBulkHistory(sess)
	return sess
}

// compactFixture wires the full stack: scripted provider, executor with a
// small window, controller with the boot-mirroring reload closure.
func compactFixture(t *testing.T, ws, userDir, system string, calls *atomic.Int64) (*Controller, *agent.Agent) {
	t.Helper()
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("summary"), textTurn("summary")}}
	exec := agent.New(prov, tool.NewRegistry(), bulkHistorySession(system), agent.Options{
		SessionPath:   filepath.Join(ws, "session.json"),
		ContextWindow: 10_000, CompactRatio: 0.8, RecentKeep: 2,
	}, event.Discard)
	c := New(Options{
		Executor:           exec,
		SessionDir:         ws,
		Label:              "test",
		Memory:             memory.Load(memory.Options{CWD: ws, UserDir: userDir}),
		MemorySystemReload: countingMemoryReload("base prompt", ws, userDir, calls),
	})
	return c, exec
}

// TestRebuildHostStateReplacesLeadingSystem restores the guard 3c5e6b5e7
// deleted: the rebuild must swap the leading system message for the reloader's
// output and leave the transcript untouched.
func TestRebuildHostStateReplacesLeadingSystem(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("base + policies + [oldMemory] + skills")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "history one"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "history two"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)

	var called int
	c := New(Options{
		Executor:   exec,
		SessionDir: dir,
		Label:      "test",
		MemorySystemReload: func() string {
			called++
			return "base + policies + [new memory loaded from disk] + skills"
		},
	})

	c.rebuildHostState()

	if called == 0 {
		t.Fatal("memorySystemReload should be invoked")
	}
	msgs := exec.Session().Snapshot()
	if len(msgs) != 3 {
		t.Fatalf("message count changed: got %d want 3", len(msgs))
	}
	if msgs[0].Role != provider.RoleSystem || !strings.Contains(msgs[0].Content, "new memory loaded from disk") {
		t.Fatalf("system message not refreshed: %+v", msgs[0])
	}
	if msgs[1].Content != "history one" || msgs[2].Content != "history two" {
		t.Fatalf("history was disturbed: %+v", msgs)
	}
}

// TestRebuildHostStateNoopWithoutReloaderOrSession restores the second guard
// 3c5e6b5e7 deleted: unwired builds and executor-less controllers are no-ops.
func TestRebuildHostStateNoopWithoutReloaderOrSession(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)

	// No reloader installed → no-op, session untouched.
	c := New(Options{Executor: exec, SessionDir: dir, Label: "test"})
	c.rebuildHostState()
	if got := exec.Session().Snapshot()[0].Content; got != "sys" {
		t.Fatalf("system changed without reloader: %q", got)
	}

	// Reloader present but no executor → no panic.
	c2 := New(Options{SessionDir: dir, Label: "test", MemorySystemReload: func() string { return "x" }})
	c2.executor = nil
	c2.rebuildHostState()
}

// TestCompactRefreshesMemoryRegionFromDisk covers acceptance ①+⑦: a marker
// appended to AGENTS.md after boot reaches the provider-visible leading
// system message through the /compact maintenance exit, and the # Memory
// region appears exactly once.
func TestCompactRefreshesMemoryRegionFromDisk(t *testing.T) {
	ws, userDir := memoryRefreshFixture(t)
	var calls atomic.Int64
	_, exec := compactFixture(t, ws, userDir, "base prompt", &calls)

	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"),
		[]byte("## Standing instructions\n\nKeep answers terse.\n\nZZ-REFRESH-PROBE-COMPACT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := exec.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}

	if calls.Load() == 0 {
		t.Fatal("endMaintenance did not fire the host state rebuild hook")
	}
	msgs := exec.Session().Snapshot()
	if len(msgs) == 0 || msgs[0].Role != provider.RoleSystem {
		t.Fatalf("no leading system message: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "ZZ-REFRESH-PROBE-COMPACT") {
		t.Fatalf("compaction did not refresh the memory region from disk:\n%.400q", msgs[0].Content)
	}
	if got := strings.Count(msgs[0].Content, "# Memory"); got != 1 {
		t.Fatalf("# Memory region count = %d, want 1:\n%.400q", got, msgs[0].Content)
	}
}

// TestCompactRefreshIdempotentWhenMemoryUnchanged covers acceptance ③+⑨: with
// no on-disk change the second maintenance pass leaves the system message
// byte-identical, and a refreshed prompt never invalidates the compaction
// projection (the refresh-then-recompact path must not recompute the fold).
func TestCompactRefreshIdempotentWhenMemoryUnchanged(t *testing.T) {
	ws, userDir := memoryRefreshFixture(t)
	var calls atomic.Int64
	_, exec := compactFixture(t, ws, userDir, "base prompt", &calls)

	if err := exec.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("first CompactNow: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("first pass did not fire the rebuild hook")
	}
	first := exec.Session().Snapshot()
	if st := exec.ContextMaintenanceSnapshot(); !st.ProjectionValid {
		t.Fatal("projection invalid right after the post-compaction refresh")
	}
	version := exec.ContextMaintenanceSnapshot().ProjectionVersion

	// 验收⑨: compacting again after the refresh must report there is nothing
	// worth folding — the refreshed prompt lives outside the fold region, so
	// the projection survived and the second forced pass is blocked instead of
	// recomputing (fold_installed=false, projection untouched).
	if err := exec.CompactNow(context.Background(), ""); err == nil {
		t.Fatal("second forced compaction should be blocked — a refresh may not reopen the fold")
	}
	// The rebuild still fires on the blocked pass's exit (deferred), and with
	// no on-disk change (验收③) the system message stays byte-identical.
	if calls.Load() < 2 {
		t.Fatalf("blocked pass did not run the rebuild (calls=%d)", calls.Load())
	}
	second := exec.Session().Snapshot()
	if len(second) != len(first) {
		t.Fatalf("transcript length changed across an idempotent pass: %d -> %d", len(first), len(second))
	}
	if second[0].Content != first[0].Content {
		t.Fatalf("system message rewritten without a memory change:\nfirst:  %.300q\nsecond: %.300q", first[0].Content, second[0].Content)
	}
	if st := exec.ContextMaintenanceSnapshot(); !st.ProjectionValid || st.ProjectionVersion != version {
		t.Fatalf("projection must survive the refresh and the blocked pass: valid=%v version=%d want %d",
			st.ProjectionValid, st.ProjectionVersion, version)
	}
}

// TestCompactWithExtensionPromptPreservesExtensionSegment covers the blocking
// acceptance ⑥+⑦: when an extension owns the base prompt, the rebuild must
// skip the memory step so the extension segment survives a /compact verbatim,
// and no duplicate # Memory region may appear.
func TestCompactWithExtensionPromptPreservesExtensionSegment(t *testing.T) {
	ws, userDir := memoryRefreshFixture(t)
	var calls atomic.Int64
	c, exec := compactFixture(t, ws, userDir, "boot base that must never leak", &calls)

	extPrompt := "extension base\n\n# Memory\n\n- extension-owned pinned line\n\nZZ-EXTENSION-SEGMENT-MUST-SURVIVE\n"
	c.ApplyExtensionSystemPrompt(extPrompt)
	if got := exec.Session().Snapshot()[0].Content; got != extPrompt {
		t.Fatalf("ApplyExtensionSystemPrompt did not install the extension prompt verbatim: %.200q", got)
	}
	// The extension swap replaced the session; reattach bulk history so the
	// forced pass performs a REAL fold — proving endMaintenance (and thus the
	// rebuild hook) actually ran instead of the test passing vacuously.
	addBulkHistory(exec.Session())

	if err := exec.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if st := exec.ContextMaintenanceSnapshot(); !st.ProjectionValid {
		t.Fatal("fixture did not complete a real fold — the extension-preservation assertion would be vacuous")
	}

	msgs := exec.Session().Snapshot()
	if msgs[0].Content != extPrompt {
		t.Fatalf("extension prompt was not preserved verbatim across compaction:\nwant: %.300q\ngot:  %.300q", extPrompt, msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "ZZ-EXTENSION-SEGMENT-MUST-SURVIVE") {
		t.Fatal("extension segment lost")
	}
	if got := strings.Count(msgs[0].Content, "# Memory"); got != 1 {
		t.Fatalf("# Memory region count = %d, want 1 on the extension-owned prompt", got)
	}
	if strings.Contains(msgs[0].Content, "boot base that must never leak") {
		t.Fatal("boot base leaked into an extension-owned session")
	}
}

// TestNewSessionServesLatestMemory covers acceptance ②: a same-tab /new must
// assemble its leading prompt from the latest on-disk memory, not the boot
// copy — and an extension-owned base must pass through untouched.
func TestNewSessionServesLatestMemory(t *testing.T) {
	ws, userDir := memoryRefreshFixture(t)
	var calls atomic.Int64
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("ok")}}
	path := filepath.Join(ws, "session.json")
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("boot base"), agent.Options{SessionPath: path}, event.Discard)
	c := New(Options{
		Executor:               exec,
		SessionDir:             ws,
		Label:                  "test",
		DisableColdResumePrune: true,
		Memory:                 memory.Load(memory.Options{CWD: ws, UserDir: userDir}),
		MemorySystemReload:     countingMemoryReload("base prompt", ws, userDir, &calls),
	})

	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"),
		[]byte("## Standing instructions\n\nKeep answers terse.\n\nZZ-REFRESH-PROBE-NEW\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	msgs := exec.Session().Snapshot()
	if len(msgs) == 0 || msgs[0].Role != provider.RoleSystem {
		t.Fatalf("fresh session has no leading system message: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "ZZ-REFRESH-PROBE-NEW") {
		t.Fatalf("/new kept serving the boot-time base copy:\n%.400q", msgs[0].Content)
	}
	if got := strings.Count(msgs[0].Content, "# Memory"); got != 1 {
		t.Fatalf("# Memory region count = %d, want 1 after /new", got)
	}

	// Extension-owned base: /new passes it through verbatim, reload unconsulted.
	extPrompt := "extension base ZZ-EXT-BASE-NEW\n"
	c.ApplyExtensionSystemPrompt(extPrompt)
	before := calls.Load()
	if err := c.NewSession(); err != nil {
		t.Fatalf("NewSession after extension: %v", err)
	}
	if got := exec.Session().Snapshot()[0].Content; got != extPrompt {
		t.Fatalf("extension base not preserved across /new: %.200q", got)
	}
	if calls.Load() != before {
		t.Fatal("extension-owned /new must not consult the memory reloader")
	}
}

// TestClearSessionServesLatestMemory covers acceptance ② for /clear: same
// refresh contract as /new (tl-1194 保护条款②).
func TestClearSessionServesLatestMemory(t *testing.T) {
	ws, userDir := memoryRefreshFixture(t)
	var calls atomic.Int64
	path := agent.NewSessionPath(ws, "main")
	sess := agent.NewSession("boot base")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, nil, agent.NewSession("boot base"), agent.Options{SessionPath: path}, event.Discard)
	c := New(Options{
		Executor:               exec,
		SessionDir:             ws,
		Label:                  "test",
		DisableColdResumePrune: true,
		Memory:                 memory.Load(memory.Options{CWD: ws, UserDir: userDir}),
		MemorySystemReload:     countingMemoryReload("base prompt", ws, userDir, &calls),
	})
	c.Resume(sess, path)

	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"),
		[]byte("## Standing instructions\n\nKeep answers terse.\n\nZZ-REFRESH-PROBE-CLEAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	msgs := exec.Session().Snapshot()
	if len(msgs) == 0 || msgs[0].Role != provider.RoleSystem {
		t.Fatalf("cleared session has no leading system message: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "ZZ-REFRESH-PROBE-CLEAR") {
		t.Fatalf("/clear kept serving the boot-time base copy:\n%.400q", msgs[0].Content)
	}
}

// TestRebuildFromDiskReflectsExternalFrontmatterEdit covers acceptance ⑧: an
// externally rewritten pinned fact body reaches the live snapshot through the
// rebuild's step 2 (ReloadFromDisk), so the next session-context reflects it.
func TestRebuildFromDiskReflectsExternalFrontmatterEdit(t *testing.T) {
	ws, userDir := memoryRefreshFixture(t)
	factPath := bulkPinnedFixture(t, ws, userDir, "OLD-PINNED-BODY-EVIDENCE")
	var calls atomic.Int64

	mem := memory.Load(memory.Options{CWD: ws, UserDir: userDir})
	exec := agent.New(nil, nil, agent.NewSession("base prompt"), agent.Options{}, event.Discard)
	c := New(Options{
		Executor:           exec,
		SessionDir:         ws,
		Label:              "test",
		Memory:             mem,
		MemorySystemReload: countingMemoryReload("base prompt", ws, userDir, &calls),
	})
	if got := c.Memory().PinnedGuidance; len(got) != 1 || !strings.Contains(got[0].Body, "OLD-PINNED-BODY-EVIDENCE") {
		t.Fatalf("fixture pinned fact not loaded: %+v", got)
	}

	// External edit: the fact file is rewritten behind the app's back (the
	// memory panel edits files on disk the same way). Frontmatter stays,
	// only the body changes.
	if newPath := bulkPinnedFixture(t, ws, userDir, "NEW-PINNED-BODY-EVIDENCE"); newPath != factPath {
		t.Fatalf("fixture path moved: %q -> %q", factPath, newPath)
	}

	c.rebuildHostState()

	got := c.Memory().PinnedGuidance
	if len(got) != 1 || !strings.Contains(got[0].Body, "NEW-PINNED-BODY-EVIDENCE") {
		t.Fatalf("live snapshot did not reflect the external frontmatter edit: %+v", got)
	}
}

// TestReloadFromDiskNoopWhenMemoryDisabled pins the zero-value discipline: a
// controller built without memory must survive the rebuild's step 2.
func TestReloadFromDiskNoopWhenMemoryDisabled(t *testing.T) {
	m := newMemoryManager(nil)
	m.ReloadFromDisk() // must not panic; current() stays nil
	if m.current() != nil {
		t.Fatal("disabled memory gained a snapshot")
	}
}

// TestFreshSessionBasePromptGating pins the freshSessionBasePrompt contract:
// extension passthrough without consulting the reloader, reloader output when
// wired, boot-copy fallback when nil.
func TestFreshSessionBasePromptGating(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)

	var calls int
	c := New(Options{
		Executor:           exec,
		SessionDir:         dir,
		Label:              "test",
		MemorySystemReload: func() string { calls++; return "reload output ZZ" },
	})
	if got := c.freshSessionBasePrompt(); got != "reload output ZZ" {
		t.Fatalf("wired reloader bypassed: %q", got)
	}

	c.ApplyExtensionSystemPrompt("ext base ZZ")
	if got := c.freshSessionBasePrompt(); got != "ext base ZZ" {
		t.Fatalf("extension base not passed through: %q", got)
	}
	if calls != 1 {
		t.Fatalf("extension-owned base must not consult the reloader: calls=%d", calls)
	}

	// Fresh controller on a clean executor (the one above now carries the
	// extension prompt in its session, which newControllerPromptState would
	// otherwise adopt as the base): nil reloader falls back to the boot copy.
	exec2 := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	c2 := New(Options{Executor: exec2, SessionDir: dir, Label: "test"})
	if got := c2.freshSessionBasePrompt(); got != "sys" {
		t.Fatalf("nil reloader must fall back to the boot copy: %q", got)
	}
}
