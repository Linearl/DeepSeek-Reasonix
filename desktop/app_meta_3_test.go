package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

func TestCollectPromptHistoryEntriesLegacyEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"kind":"user.message","text":"hello world"}
{"kind":"user.message","text":"second prompt"}
{"kind":"model.final","content":"response"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", entries[0].Text)
	}
	if entries[1].Text != "second prompt" {
		t.Errorf("expected 'second prompt', got %q", entries[1].Text)
	}
	if entries[0].Turn != 0 || entries[1].Turn != 1 {
		t.Errorf("expected turns 0,1; got %d,%d", entries[0].Turn, entries[1].Turn)
	}
	if entries[0].SessionPath != path {
		t.Errorf("expected session path %q, got %q", path, entries[0].SessionPath)
	}
}

// TestCollectPromptHistoryEntriesEarlyEvent verifies that the migrated legacy event
// format {"type":"user.message","text":"..."} is correctly extracted.
func TestCollectPromptHistoryEntriesEarlyEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user.message","text":"v0 prompt"}
{"type":"model.final","content":"response"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Text != "v0 prompt" {
		t.Errorf("expected 'v0 prompt', got %q", entries[0].Text)
	}
}

// TestCollectPromptHistoryEntriesProviderMessage verifies that the current
// provider.Message format {"role":"user","content":"..."} is correctly extracted.
func TestCollectPromptHistoryEntriesProviderMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello from provider"}
{"role":"assistant","content":"response"}
{"role":"user","content":"another prompt"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Text != "hello from provider" {
		t.Errorf("expected 'hello from provider', got %q", entries[0].Text)
	}
	if entries[1].Text != "another prompt" {
		t.Errorf("expected 'another prompt', got %q", entries[1].Text)
	}
}

// TestCollectPromptHistoryEntriesMixedFormats verifies that both formats in the
// same file are extracted.
func TestCollectPromptHistoryEntriesMixedFormats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"kind":"user.message","text":"legacy prompt"}
{"role":"user","content":"modern prompt"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Text != "legacy prompt" {
		t.Errorf("expected 'legacy prompt', got %q", entries[0].Text)
	}
	if entries[1].Text != "modern prompt" {
		t.Errorf("expected 'modern prompt', got %q", entries[1].Text)
	}
}

func TestCollectPromptHistoryEntriesReadsEventTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	rfcTime := time.Date(2026, 6, 14, 10, 30, 5, 6_000_000, time.UTC)
	if err := os.WriteFile(path, []byte(`{"kind":"user.message","text":"legacy timed","time":1800000000123}
{"role":"user","content":"modern timed","createdAt":`+strconv.Quote(rfcTime.Format(time.RFC3339Nano))+`}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].At != 1800000000123 {
		t.Errorf("numeric event time = %d, want 1800000000123", entries[0].At)
	}
	if entries[1].At != rfcTime.UnixMilli() {
		t.Errorf("RFC3339 event time = %d, want %d", entries[1].At, rfcTime.UnixMilli())
	}
}

// TestCollectPromptHistoryEntriesUsesDisplayResolver verifies history recall uses
// the user-visible prompt text, not the controller-expanded model input.
func TestCollectPromptHistoryEntriesUsesDisplayResolver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	expanded := "<memory-update>\nSaved memory\n</memory-update>\n\nvisible prompt"
	if err := os.WriteFile(path, []byte(`{"role":"user","content":`+strconv.Quote(expanded)+`}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordSessionDisplay(dir, path, expanded, "visible prompt"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, sessionDisplayResolver(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Text != "visible prompt" {
		t.Errorf("expected visible prompt, got %q", entries[0].Text)
	}
}

func TestCollectPromptHistoryEntriesSkipsSyntheticMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"Plan approved — plan mode is off"}
{"role":"user","content":"real prompt"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Text != "real prompt" {
		t.Errorf("expected real prompt, got %q", entries[0].Text)
	}
}

// TestCollectPromptHistoryEntriesNoUserMessages verifies that a file with only
// assistant/tool messages returns no entries.
func TestCollectPromptHistoryEntriesNoUserMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"kind":"model.final","content":"response"}
{"kind":"tool.result","output":"done"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// TestCollectPromptHistoryEntriesEmptyFile verifies that an empty JSONL file
// returns no entries without error.
func TestCollectPromptHistoryEntriesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectPromptHistoryEntries(path, info, identityPromptDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// TestScanPromptHistoryFromDir verifies that scanPromptHistoryFromDir scans
// multiple JSONL files and returns prompts newest-first.
func TestScanPromptHistoryFromDir(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{"t1": {ID: "t1", Ctrl: nil, WorkspaceRoot: ""}}}
	_ = app

	dir := t.TempDir()
	// Write two session files with different mtimes (sleep to ensure ordering).
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(`{"role":"user","content":"older prompt"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "b.jsonl"), []byte(`{"role":"user","content":"newer prompt"}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := app.scanPromptHistoryFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Newest-first: "newer prompt" should be first.
	if entries[0].Text != "newer prompt" {
		t.Errorf("expected 'newer prompt' first, got %q", entries[0].Text)
	}
	if entries[1].Text != "older prompt" {
		t.Errorf("expected 'older prompt' second, got %q", entries[1].Text)
	}
}

func TestScanPromptHistoryFromDirUsesSessionActivityBeforeEventInterleaving(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	base := time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC)
	early := filepath.Join(dir, "early.jsonl")
	late := filepath.Join(dir, "late.jsonl")

	if err := os.WriteFile(early, fmt.Appendf(nil, `{"role":"user","content":"early first","time":%d}
{"role":"assistant","content":"ok"}
{"role":"user","content":"early second","time":%d}
`, base.UnixMilli(), base.Add(time.Minute).UnixMilli()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(late, fmt.Appendf(nil, `{"role":"user","content":"late newest","time":%d}
`, base.Add(2*time.Minute).UnixMilli()), 0o644); err != nil {
		t.Fatal(err)
	}
	// Invert file mtimes: session activity should keep each session grouped
	// before event timestamps are considered within that session.
	if err := os.Chtimes(early, base.Add(3*time.Hour), base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(late, base.Add(-3*time.Hour), base.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}

	entries, err := app.scanPromptHistoryFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	want := []string{"early second", "early first", "late newest"}
	for i, w := range want {
		if entries[i].Text != w {
			t.Fatalf("entries[%d] = %q, want %q; all=%+v", i, entries[i].Text, w, entries)
		}
	}
}

func TestScanPromptHistoryFromDirUsesBranchMetaActivityFallback(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	base := time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC)
	early := filepath.Join(dir, "early.jsonl")
	late := filepath.Join(dir, "late.jsonl")

	if err := os.WriteFile(early, []byte(`{"role":"user","content":"early first"}
{"role":"assistant","content":"ok"}
{"role":"user","content":"early second"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(late, []byte(`{"role":"user","content":"late newest"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(early, agent.BranchMeta{
		CreatedAt: base,
		UpdatedAt: base.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(late, agent.BranchMeta{
		CreatedAt: base.Add(time.Minute),
		UpdatedAt: base.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	// Invert file mtimes: branch UpdatedAt should be the activity clock.
	if err := os.Chtimes(early, base.Add(3*time.Hour), base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(late, base.Add(-3*time.Hour), base.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}

	entries, err := app.scanPromptHistoryFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	want := []string{"late newest", "early second", "early first"}
	for i, w := range want {
		if entries[i].Text != w {
			t.Fatalf("entries[%d] = %q, want %q; all=%+v", i, entries[i].Text, w, entries)
		}
	}
}

func TestScanPromptHistoryFromDirSkipsEmptyOrderedSessions(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	base := time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC)
	empty := filepath.Join(dir, "empty.jsonl")
	real := filepath.Join(dir, "real.jsonl")

	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte(`{"role":"user","content":"real prompt"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(empty, agent.BranchMeta{
		CreatedAt: base,
		UpdatedAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(real, agent.BranchMeta{
		CreatedAt: base,
		UpdatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := app.scanPromptHistoryFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "real prompt" {
		t.Fatalf("entries = %+v, want only real prompt after skipping empty session", entries)
	}
}

func TestScanPromptHistoryUsesCurrentSessionBeforeCrossSession(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	if err := os.WriteFile(current, []byte(`{"role":"user","content":"current first"}
{"role":"assistant","content":"ok"}
{"role":"user","content":"current second"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"role":"user","content":"other newest"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC)
	if err := agent.SaveBranchMetaPreserveUpdated(current, agent.BranchMeta{
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(other, agent.BranchMeta{
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: current, Label: "test"})
	defer ctrl.Close()
	app.setTestCtrl(ctrl, "")

	result, err := app.ScanPromptHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("expected current-session entries followed by cross-session fallback, got %d: %+v", len(result.Entries), result.Entries)
	}
	want := []string{"current second", "current first", "other newest"}
	for i, w := range want {
		if result.Entries[i].Text != w {
			t.Fatalf("entries[%d] = %q, want %q; all=%+v", i, result.Entries[i].Text, w, result.Entries)
		}
	}
}

func TestScanPromptHistoryPaginatesCurrentSessionBeforeCrossSession(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	var lines []byte
	for i := range 55 {
		lines = append(lines, fmt.Appendf(nil, `{"role":"user","content":"current %d"}
`, i)...)
	}
	if err := os.WriteFile(current, lines, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"role":"user","content":"other newest"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC)
	if err := agent.SaveBranchMetaPreserveUpdated(current, agent.BranchMeta{
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(other, agent.BranchMeta{
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: current, Label: "test"})
	defer ctrl.Close()
	app.setTestCtrl(ctrl, "")

	result, err := app.ScanPromptHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != promptHistoryPageLimit {
		t.Fatalf("expected %d entries, got %d", promptHistoryPageLimit, len(result.Entries))
	}
	if result.Entries[0].Text != "current 54" {
		t.Fatalf("first entry = %q, want current 54", result.Entries[0].Text)
	}
	if result.Entries[len(result.Entries)-1].Text != "current 5" {
		t.Fatalf("last first-page entry = %q, want current 5", result.Entries[len(result.Entries)-1].Text)
	}
	if !result.HasOlder || result.OlderCursor == "" {
		t.Fatalf("first page should expose an older cursor: %+v", result)
	}
	for _, entry := range result.Entries {
		if entry.Text == "other newest" {
			t.Fatalf("cross-session entry appeared before current-session page was exhausted: %+v", result.Entries)
		}
	}

	nextRequest, err := json.Marshal(promptHistoryRequest{Cursor: result.OlderCursor})
	if err != nil {
		t.Fatal(err)
	}
	next, err := app.ScanPromptHistory(string(nextRequest))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"current 4", "current 3", "current 2", "current 1", "current 0", "other newest"}
	if len(next.Entries) != len(want) {
		t.Fatalf("second page entries = %+v, want %d entries", next.Entries, len(want))
	}
	for i, w := range want {
		if next.Entries[i].Text != w {
			t.Fatalf("second page entries[%d] = %q, want %q; all=%+v", i, next.Entries[i].Text, w, next.Entries)
		}
	}
}

func TestScanPromptHistoryFromDirReadsAllEntriesForInternalHelper(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	var lines []byte
	for i := range 250 {
		lines = append(lines, fmt.Appendf(nil, `{"role":"user","content":"prompt %d"}
`, i)...)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.jsonl"), lines, 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := app.scanPromptHistoryFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 250 {
		t.Fatalf("expected 250 entries, got %d", len(entries))
	}
	if entries[0].Text != "prompt 249" {
		t.Errorf("expected newest 'prompt 249' first, got %q", entries[0].Text)
	}
}

// TestScanPromptHistoryFromDirEmpty verifies an empty directory returns nil.
func TestScanPromptHistoryFromDirEmpty(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	entries, err := app.scanPromptHistoryFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// TestScanPromptHistoryCacheHit verifies that ScanPromptHistory returns nil
// on cache hit (nonce matches).
func TestScanPromptHistoryCacheHit(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}}
	result, err := app.ScanPromptHistory("")
	if err != nil {
		t.Fatal(err)
	}
	nonce := result.Nonce
	if nonce == "" {
		t.Error("expected a non-empty nonce on first call")
	}

	// Second call with the same nonce should be a cache hit (nil entries).
	result2, err := app.ScanPromptHistory(nonce)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Entries != nil {
		t.Error("expected nil entries on cache hit")
	}
	if result2.Nonce != nonce {
		t.Errorf("expected nonce %q unchanged, got %q", nonce, result2.Nonce)
	}
}

func TestScanPromptHistoryCacheIsScopedBySessionDir(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	pathA := filepath.Join(dirA, "a.jsonl")
	pathB := filepath.Join(dirB, "b.jsonl")
	if err := os.WriteFile(pathA, []byte(`{"role":"user","content":"workspace A"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte(`{"role":"user","content":"workspace B"}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	ctrlA := control.New(control.Options{SessionDir: dirA, SessionPath: pathA, Label: "test"})
	ctrlB := control.New(control.Options{SessionDir: dirB, SessionPath: pathB, Label: "test"})
	defer ctrlA.Close()
	defer ctrlB.Close()

	app.setTestCtrl(ctrlA, "")
	first, err := app.ScanPromptHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || first.Entries[0].Text != "workspace A" {
		t.Fatalf("first entries = %+v, want workspace A", first.Entries)
	}

	app.setTestCtrl(ctrlB, "")
	second, err := app.ScanPromptHistory(first.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if second.Entries == nil {
		t.Fatal("expected rescan after session dir changes, got cache hit")
	}
	if len(second.Entries) != 1 || second.Entries[0].Text != "workspace B" {
		t.Fatalf("second entries = %+v, want workspace B", second.Entries)
	}
}

func TestScanPromptHistoryCacheIsScopedBySessionPath(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.jsonl")
	pathB := filepath.Join(dir, "b.jsonl")
	if err := os.WriteFile(pathA, []byte(`{"role":"user","content":"session A"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte(`{"role":"user","content":"session B"}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	ctrlA := control.New(control.Options{SessionDir: dir, SessionPath: pathA, Label: "test"})
	ctrlB := control.New(control.Options{SessionDir: dir, SessionPath: pathB, Label: "test"})
	defer ctrlA.Close()
	defer ctrlB.Close()

	app.setTestCtrl(ctrlA, "")
	first, err := app.ScanPromptHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.Entries[0].Text != "session A" || first.Entries[1].Text != "session B" {
		t.Fatalf("first entries = %+v, want session A followed by session B", first.Entries)
	}

	app.setTestCtrl(ctrlB, "")
	second, err := app.ScanPromptHistory(first.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if second.Entries == nil {
		t.Fatal("expected rescan after session path changes, got cache hit")
	}
	if len(second.Entries) != 2 || second.Entries[0].Text != "session B" || second.Entries[1].Text != "session A" {
		t.Fatalf("second entries = %+v, want session B followed by session A", second.Entries)
	}
}
