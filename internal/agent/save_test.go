package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func TestSaveLoadPreservesLegacyContentAndRawUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	const raw = "fix the bug"
	const rendered = "<reasoning-language>zh</reasoning-language>\n\nfix the bug"
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: rendered, RawContent: raw})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})

	before, err := json.Marshal(provider.ModelMessages(s.Snapshot()))
	if err != nil {
		t.Fatalf("marshal provider messages before save: %v", err)
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	stored := loaded.Snapshot()
	if got := stored[1].Content; got != rendered {
		t.Fatalf("reloaded provider content = %q, want %q", got, rendered)
	}
	if got := stored[1].RawContent; got != raw {
		t.Fatalf("reloaded raw content = %q, want %q", got, raw)
	}
	if stored[1].ProviderContent != "" {
		t.Fatalf("reloaded transitional provider content = %q, want empty", stored[1].ProviderContent)
	}
	after, err := json.Marshal(provider.ModelMessages(stored))
	if err != nil {
		t.Fatalf("marshal provider messages after load: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("provider request bytes changed across save/load:\nbefore: %s\nafter:  %s", before, after)
	}
}


func TestSaveLoadPreservesBoundedToolContentAndCompleteRawContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	full := strings.Repeat("完整工具结果-🧪", 6000)
	bounded, notice := truncateToolOutputFor(full, "read_file", "call-save")
	if notice == "" {
		t.Fatal("fixture did not produce a bounded tool result")
	}
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-save", Name: "read_file", Arguments: `{}`}}})
	s.Add(provider.Message{Role: provider.RoleTool, Name: "read_file", ToolCallID: "call-save", Content: bounded, RawContent: full})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	stored := loaded.Snapshot()[2]
	if stored.Content != bounded || stored.RawContent != full {
		t.Fatalf("tool result changed across save/load: content=%d raw=%d", len(stored.Content), len(stored.RawContent))
	}
	model := provider.ModelMessages(loaded.Snapshot())
	if model[2].Content != bounded || model[2].RawContent != "" {
		t.Fatalf("provider projection after resume is not bounded: content=%d raw=%d", len(model[2].Content), len(model[2].RawContent))
	}
}


// TestSnapshotUpToDateFastPath locks in the #6607 switch-lag fix: a snapshot
// of a session that has not changed since its last save to the same path must
// be a pure in-memory no-op — no serialize, no digest, no disk access. The
// desktop snapshots defensively on every tab/session switch, and on large
// transcripts the redundant full-save work is seconds of UI freeze.
func TestSnapshotUpToDateFastPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("initial SaveSnapshot: %v", err)
	}
	if !s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = false right after a successful save")
	}
	if s.snapshotUpToDate(filepath.Join(t.TempDir(), "other.jsonl")) {
		t.Fatal("snapshotUpToDate = true for a different path")
	}

	// The no-op leaves the disk byte-identical when the log is exactly as the
	// baseline recorded it (任务 831②: the skip now re-verifies the log tail
	// and the ledger identity first — a handful of fixed-size reads, still no
	// serialize, no digest, no full replay).
	logPath := store.SessionEventLog(path)
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("no-op SaveSnapshot: %v", err)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log after no-op save: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("no-op snapshot touched the disk — fast path did not fire")
	}

	// A log that was modified behind the baseline's back (the scribble below)
	// is no longer provably ours, so the fast path must stand down and the
	// full save heals it — a defensive snapshot must never paper over external
	// writes.
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := f.Write([]byte("{torn")); err != nil {
		t.Fatalf("scribble: %v", err)
	}
	f.Close()
	if s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = true with a modified event log behind the baseline")
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("full SaveSnapshot after scribble: %v", err)
	}
	healed, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read healed log: %v", err)
	}
	if string(healed) == string(before)+"{torn" {
		t.Fatal("scribbled log survived the save — the disk re-verification did not stand down")
	}

	// Any transcript change re-arms the full path, which heals the scribble
	// and persists the new turn.
	s.Add(provider.Message{Role: provider.RoleUser, Content: "again"})
	if s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = true after Add")
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after Add: %v", err)
	}
	if !s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = false after the follow-up save")
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.eventLogDamaged {
		t.Fatal("follow-up save left the log damaged")
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "again" {
		t.Fatalf("reloaded tail = %q, want %q", got, "again")
	}
	// 任务 831② semantic change: a load that came back clean AND whose ledger
	// sidecar agrees with the decoded transcript now DOES arm the fast path —
	// the load itself proved the in-memory transcript is exactly what the
	// ledger describes, so the first defensive snapshot after a resume skips
	// instead of re-running the full marshal+digest pass. The stale-ledger
	// concern the old assertion covered (interrupted save: bytes landed, the
	// sidecar did not) is covered narrowly below and by
	// TestSameContentSaveHealsStaleLedgerDigest — those loads still save in
	// full on first write.
	if got := loaded.snapshotNoOpReason(path); got != "clean-load" {
		t.Fatalf("snapshotNoOpReason for a freshly loaded session = %q, want clean-load", got)
	}
	if !loaded.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = false for a clean load with an agreeing ledger")
	}

	// A pending rewrite (compaction/rewind) also disarms the fast path.
	s.Replace(append([]provider.Message(nil), loaded.Messages[:2]...))
	s.IncrementRewrite()
	if s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = true with a pending rewrite")
	}
}


func TestSaveSnapshotBoundsCrossProcessFileLockWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lock, err := tryTakeSessionLockFile(store.SessionLockFile(path))
	if err != nil {
		t.Fatalf("take competing session lock: %v", err)
	}
	defer lock.Unlock()

	prevWait, prevPoll := sessionFileLockWait, sessionFileLockPollInterval
	sessionFileLockWait = 40 * time.Millisecond
	sessionFileLockPollInterval = 5 * time.Millisecond
	defer func() {
		sessionFileLockWait = prevWait
		sessionFileLockPollInterval = prevPoll
	}()

	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "must stay in memory"})
	started := time.Now()
	err = s.SaveSnapshot(path)
	if !errors.Is(err, ErrSessionFileLockHeld) {
		t.Fatalf("SaveSnapshot error = %v, want ErrSessionFileLockHeld", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("SaveSnapshot waited %v for a held cross-process lock; want a bounded failure", elapsed)
	}
	if got := s.Snapshot(); len(got) != 2 || got[1].Content != "must stay in memory" {
		t.Fatalf("failed save changed in-memory transcript: %+v", got)
	}
}


func TestSaveSnapshotSucceedsWhenCrossProcessFileLockReleasesBeforeDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lock, err := tryTakeSessionLockFile(store.SessionLockFile(path))
	if err != nil {
		t.Fatalf("take competing session lock: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		lock.Unlock()
		close(released)
	}()
	t.Cleanup(func() { <-released })

	prevWait, prevPoll := sessionFileLockWait, sessionFileLockPollInterval
	sessionFileLockWait = 500 * time.Millisecond
	sessionFileLockPollInterval = 5 * time.Millisecond
	defer func() {
		sessionFileLockWait = prevWait
		sessionFileLockPollInterval = prevPoll
	}()

	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "save after transient lock"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after transient lock: %v", err)
	}
	select {
	case <-released:
	default:
		t.Fatal("SaveSnapshot returned before the competing lock was released")
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.Snapshot(); len(got) != 2 || got[1].Content != "save after transient lock" {
		t.Fatalf("persisted transcript = %+v", got)
	}
}


func TestSaveShutdownRecoveryBranchBypassesHeldOriginalFileLock(t *testing.T) {
	dir := schemaOneTempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "persisted"})
	if err := base.SaveSnapshot(path); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	current, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "unsaved shutdown tail"})
	lock, err := tryTakeSessionLockFile(store.SessionLockFile(path))
	if err != nil {
		t.Fatalf("take competing session lock: %v", err)
	}
	defer lock.Unlock()

	prevWait, prevPoll := sessionFileLockWait, sessionFileLockPollInterval
	sessionFileLockWait = 40 * time.Millisecond
	sessionFileLockPollInterval = 5 * time.Millisecond
	defer func() {
		sessionFileLockWait = prevWait
		sessionFileLockPollInterval = prevPoll
	}()

	saveErr := current.SaveSnapshot(path)
	if !errors.Is(saveErr, ErrSessionFileLockHeld) {
		t.Fatalf("SaveSnapshot error = %v, want ErrSessionFileLockHeld", saveErr)
	}
	info, err := current.SaveShutdownRecoveryBranch(RecoveryBranchOptions{
		OriginalPath: path,
		Reason:       "shutdown session file lock timeout",
	})
	if err != nil {
		t.Fatalf("SaveShutdownRecoveryBranch: %v", err)
	}
	if info.Path == path {
		t.Fatalf("shutdown recovery path = original path %q", path)
	}
	if !info.Meta.Recovered || info.Meta.RecoveryReason != "shutdown session file lock timeout" {
		t.Fatalf("shutdown recovery meta = %+v", info.Meta)
	}
	recovered, err := LoadSession(info.Path)
	if err != nil {
		t.Fatalf("load shutdown recovery: %v", err)
	}
	if got := recovered.Snapshot(); len(got) != 3 || got[2].Content != "unsaved shutdown tail" {
		t.Fatalf("shutdown recovery transcript = %+v", got)
	}
	original, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload original session: %v", err)
	}
	if got := original.Snapshot(); len(got) != 2 {
		t.Fatalf("held original transcript changed: %+v", got)
	}
}


// TestRepairedSessionArmsFastPath (#6613 review P2): a session loaded with a
// damaged event log — or carrying a load-time normalization repair — must
// re-arm the snapshot no-op fast path once a successful save persists the
// repair. Before the fix the flags were never cleared, so a repaired session
// paid a full serialize + digest on every defensive snapshot until restart.
func TestRepairedSessionArmsFastPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sessionWithTurns(t, path, 2)

	// Tear the event log so the next load marks it damaged.
	logPath := store.SessionEventLog(path)
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := f.Write([]byte(`{"schema_version":1,"type":"ap`)); err != nil {
		t.Fatalf("write torn tail: %v", err)
	}
	f.Close()

	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !s.eventLogDamaged {
		t.Fatal("test setup: session should load damaged")
	}
	if s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = true for a damaged, unsaved session")
	}

	// The healing save persists the repair; the fast path must arm afterwards.
	s.Add(provider.Message{Role: provider.RoleUser, Content: "heal"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("healing SaveSnapshot: %v", err)
	}
	if !s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = false after the healing save — repaired session never re-arms the fast path")
	}

	// A pending normalization repair also disarms, and a successful save that
	// lands it re-arms.
	s.normalizedDirty = true
	if s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = true with a pending normalization repair")
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot with normalization flag: %v", err)
	}
	if !s.snapshotUpToDate(path) {
		t.Fatal("snapshotUpToDate = false after the save persisted the normalization repair")
	}
}


// TestSaveLoadRoundTrip is the contract `reasonix --resume` depends on: a
// session written to disk reloads byte-for-byte, including tool calls and
// reasoning content (which the model wants to keep across resumes for cache
// hits on thinking-mode providers).
func TestSaveLoadRoundTrip(t *testing.T) {
	s := NewSession("you are reasonix")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "find the bug"})
	s.Add(provider.Message{
		Role:             provider.RoleAssistant,
		Content:          "Let me check.",
		ReasoningContent: "I should look at main.go first.",
		ToolCalls: []provider.ToolCall{{
			ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`, ThoughtSignature: "gemini-signed",
		}},
	})
	s.Add(provider.Message{
		Role: provider.RoleTool, Name: "read_file", ToolCallID: "call_1",
		Content: "package main\nfunc main() {}\n",
	})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "It's fine."})

	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got, want := len(loaded.Messages), len(s.Messages); got != want {
		t.Fatalf("message count after round-trip = %d, want %d", got, want)
	}
	for i, m := range s.Messages {
		if loaded.Messages[i].Role != m.Role {
			t.Errorf("message %d role mismatch", i)
		}
		if loaded.Messages[i].Content != m.Content {
			t.Errorf("message %d content mismatch", i)
		}
		if loaded.Messages[i].ReasoningContent != m.ReasoningContent {
			t.Errorf("message %d reasoning mismatch", i)
		}
		if !reflect.DeepEqual(loaded.Messages[i].ToolCalls, m.ToolCalls) {
			t.Errorf("message %d tool_calls mismatch:\n got: %#v\nwant: %#v", i, loaded.Messages[i].ToolCalls, m.ToolCalls)
		}
	}
}


func TestSavePreservesToolContentOnDisk(t *testing.T) {
	secret := "sk-real-secret-value-123456"
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "inspect"})
	s.Add(provider.Message{
		Role:       provider.RoleTool,
		Name:       "bash",
		ToolCallID: "call_1",
		Content:    "DEEPSEEK_API_KEY=" + secret + "\n",
	})

	path := filepath.Join(t.TempDir(), "verbatim.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved session: %v", err)
	}
	if !strings.Contains(string(body), "DEEPSEEK_API_KEY="+secret) {
		t.Fatalf("session did not persist tool content verbatim:\n%s", body)
	}
}


func TestSaveProtectsVerbatimSessionEventLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file ACLs are not represented by Unix permission bits")
	}
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "inspect"})
	s.Add(provider.Message{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_1", Content: "API_KEY=raw-secret"})
	path := filepath.Join(t.TempDir(), "private.jsonl")
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("first SaveSnapshot: %v", err)
	}
	eventPath := store.SessionEventLog(path)
	assertPrivateSessionFile(t, eventPath)

	// Simulate an event log created by a previous release. The next append must
	// tighten the existing inode before writing any new unredacted content.
	if err := os.Chmod(eventPath, 0o644); err != nil {
		t.Fatal(err)
	}
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("append SaveSnapshot: %v", err)
	}
	assertPrivateSessionFile(t, eventPath)
}


func assertPrivateSessionFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("%s mode = %04o, want 0600", path, got)
	}
}


// TestSaveVerbatimRoundTripKeepsSnapshotBaselineStable pins digest consistency
// for byte-preserving transcripts: a loaded transcript re-saves without a
// rewrite or revision bump, and appending afterward remains a plain append.
func TestSaveVerbatimRoundTripKeepsSnapshotBaselineStable(t *testing.T) {
	secret := "sk-real-secret-value-123456"
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "inspect"})
	s.Add(provider.Message{
		Role:       provider.RoleTool,
		Name:       "bash",
		ToolCallID: "call_1",
		Content:    "DEEPSEEK_API_KEY=" + secret + "\n",
	})

	path := filepath.Join(t.TempDir(), "stable.jsonl")
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("first SaveSnapshot: %v", err)
	}
	rev1, _, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("read revision: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("resave of loaded session: %v", err)
	}
	rev2, _, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("read revision after resave: %v", err)
	}
	if rev1 != rev2 {
		t.Fatalf("no-op resave bumped revision %d -> %d", rev1, rev2)
	}

	// A continued conversation still append-saves cleanly on top.
	loaded.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("append snapshot after verbatim round-trip: %v", err)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(reloaded.Messages); got != 4 {
		t.Fatalf("message count after append = %d, want 4", got)
	}
}


func TestSaveLoadLargeMessage(t *testing.T) {
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run it"})
	// A bash result can exceed any line-buffer cap; Save must round-trip it.
	big := strings.Repeat("x", 5*1024*1024)
	s.Add(provider.Message{Role: provider.RoleTool, Name: "bash", ToolCallID: "c1", Content: big})

	path := filepath.Join(t.TempDir(), "big.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession of a session with a >4MiB message: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(loaded.Messages))
	}
	if loaded.Messages[2].Content != big {
		t.Errorf("large content not round-tripped (got %d bytes, want %d)", len(loaded.Messages[2].Content), len(big))
	}
}


func TestSaveSnapshotRejectsStalePrefixOverwrite(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	current.Add(provider.Message{Role: provider.RoleUser, Content: "second"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "two"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	stale := NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := stale.SaveSnapshot(path); !errors.Is(err, ErrSessionSnapshotConflict) {
		t.Fatalf("SaveSnapshot stale prefix err = %v, want ErrSessionSnapshotConflict", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 5 {
		t.Fatalf("message count after stale snapshot = %d, want 5", got)
	}
	if got := loaded.Messages[4].Content; got != "two" {
		t.Fatalf("last message after stale snapshot = %q, want %q", got, "two")
	}
}


func TestSaveSnapshotAllowsAppendFromDiskPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := base.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	next := NewSession("sys")
	next.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	next.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := next.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot append: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 3 {
		t.Fatalf("message count after append snapshot = %d, want 3", got)
	}
}


// TestSaveSnapshotAppendsAcrossInterruptedToolCallTail is the mid-turn autosave
// shape from the field: a snapshot lands between an assistant tool call and its
// still-running result, so the transcript on disk ends with a dangling call
// that LoadSession answers with a fabricated placeholder. The live session then
// records the real result and keeps going. The next snapshot is a pure append
// over the bytes on disk and must land as one — not collide with the
// placeholder, misread the turn as divergence, and fork a recovery branch.
func TestSaveSnapshotAppendsAcrossInterruptedToolCallTail(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run the build"})
	s.Add(provider.Message{
		Role: provider.RoleAssistant, Content: "Running it.",
		ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "bash", Arguments: `{"cmd":"make"}`}},
	})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("mid-turn SaveSnapshot: %v", err)
	}

	s.Add(provider.Message{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_1", Content: "ok"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "Build passed."})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after tool result: %v", err)
	}

	replay, err := replaySessionEventLog(SessionEventLogPath(path))
	if err != nil {
		t.Fatalf("replay event log: %v", err)
	}
	if replay.damaged {
		t.Fatal("event log damaged after appending over an interrupted tool tail")
	}
	if replay.records != 2 {
		t.Fatalf("event log records = %d, want 2 (bootstrap replace + append)", replay.records)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 5 {
		t.Fatalf("message count after append snapshot = %d, want 5", got)
	}
	if got := loaded.Messages[3].Content; got != "ok" {
		t.Fatalf("tool result after round-trip = %q, want %q", got, "ok")
	}
	if loaded.normalizedDirty {
		t.Fatal("transcript still needs repair after appending the real tool result")
	}
}


// TestSaveSnapshotAppendsAcrossPartiallyAnsweredMultiToolCallTail covers the
// same raw-prefix fallback when a multi-call assistant turn already has some
// tool results on disk. LoadSession fabricates placeholders only for the still
// unanswered calls; the live session later appends the real remaining results.
func TestSaveSnapshotAppendsAcrossPartiallyAnsweredMultiToolCallTail(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "inspect and test"})
	s.Add(provider.Message{
		Role: provider.RoleAssistant, Content: "I will run a few checks.",
		ToolCalls: []provider.ToolCall{
			{ID: "call_read", Name: "read_file", Arguments: `{"path":"main.go"}`},
			{ID: "call_test", Name: "bash", Arguments: `{"cmd":"go test ./..."}`},
			{ID: "call_status", Name: "bash", Arguments: `{"cmd":"git status --short"}`},
		},
	})
	s.Add(provider.Message{Role: provider.RoleTool, Name: "read_file", ToolCallID: "call_read", Content: "package main"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("partial multi-tool SaveSnapshot: %v", err)
	}

	loadedPartial, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession partial: %v", err)
	}
	if !loadedPartial.normalizedDirty {
		t.Fatal("partial multi-tool load should need placeholder repair")
	}

	s.Add(provider.Message{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_test", Content: "ok"})
	s.Add(provider.Message{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_status", Content: "clean"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "All checks passed."})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after remaining multi-tool results: %v", err)
	}

	events := readSessionEventsForTest(t, path)
	if len(events) != 2 || events[1].Type != sessionEventTypeAppend {
		t.Fatalf("events after multi-tool append = %+v, want trailing append", events)
	}
	if events[1].MessageIndex != 4 || len(events[1].Messages) != 3 {
		t.Fatalf("multi-tool append event index=%d len=%d, want index 4 len 3", events[1].MessageIndex, len(events[1].Messages))
	}
	replay, err := replaySessionEventLog(SessionEventLogPath(path))
	if err != nil {
		t.Fatalf("replay event log: %v", err)
	}
	if replay.damaged {
		t.Fatal("event log damaged after appending remaining multi-tool results")
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession final: %v", err)
	}
	if got := len(loaded.Messages); got != 7 {
		t.Fatalf("message count after multi-tool append = %d, want 7", got)
	}
	if got := loaded.Messages[4].Content; got != "ok" {
		t.Fatalf("second tool result after round-trip = %q, want ok", got)
	}
	if got := loaded.Messages[5].Content; got != "clean" {
		t.Fatalf("third tool result after round-trip = %q, want clean", got)
	}
	if loaded.normalizedDirty {
		t.Fatal("multi-tool transcript still needs repair after real results landed")
	}
}


// TestSaveSnapshotUnchangedInterruptedToolCallTailIsNoOp covers the turn that
// stays interrupted (cancel, crash recovery with nothing new in memory):
// re-snapshotting the exact bytes on disk must be a no-op, not a stale-prefix
// conflict against the placeholder the load-time repair fabricated.
func TestSaveSnapshotUnchangedInterruptedToolCallTailIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run the build"})
	s.Add(provider.Message{
		Role: provider.RoleAssistant, Content: "Running it.",
		ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "bash", Arguments: `{"cmd":"make"}`}},
	})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("mid-turn SaveSnapshot: %v", err)
	}
	logBefore, err := os.ReadFile(SessionEventLogPath(path))
	if err != nil {
		t.Fatalf("ReadFile event log: %v", err)
	}

	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot unchanged: %v", err)
	}
	logAfter, err := os.ReadFile(SessionEventLogPath(path))
	if err != nil {
		t.Fatalf("ReadFile event log after no-op snapshot: %v", err)
	}
	if string(logBefore) != string(logAfter) {
		t.Fatal("no-op snapshot rewrote the event log")
	}
	if revision, _, err := sessionContentRevision(path); err != nil || revision != 1 {
		t.Fatalf("revision after no-op snapshot = %d (err %v), want 1", revision, err)
	}
}


// TestSaveRewriteOwnedAcrossInterruptedToolCallTail: compaction rewrites the
// in-memory history while the transcript on disk still ends with the dangling
// call a mid-turn snapshot left behind. Ownership is anchored on the raw bytes
// this session wrote; the placeholder fabricated on load must not revoke it.
func TestSaveRewriteOwnedAcrossInterruptedToolCallTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run the build"})
	s.Add(provider.Message{
		Role: provider.RoleAssistant, Content: "Running it.",
		ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "bash", Arguments: `{"cmd":"make"}`}},
	})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("mid-turn SaveSnapshot: %v", err)
	}

	s.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "[compacted] run the build"},
	})
	if err := s.SaveRewrite(path); err != nil {
		t.Fatalf("SaveRewrite after compaction: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 2 {
		t.Fatalf("message count after owned rewrite = %d, want 2", got)
	}
	if got := loaded.Messages[1].Content; got != "[compacted] run the build" {
		t.Fatalf("compacted message after round-trip = %q", got)
	}
}


// TestSaveSnapshotAfterDirtyResumeKeepsEventChainReplayable: a session resumed
// from an interrupted tool tail carries the load-time repair in memory, so its
// transcript is one message longer than what the event log replays. The next
// snapshot must not take the append shortcut with that inflated index — the
// chain-broken append event would be discarded on replay, silently dropping
// the whole new turn from disk. It must fall back to a full rewrite that
// persists the repair and the new turn together.
func TestSaveSnapshotAfterDirtyResumeKeepsEventChainReplayable(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run the build"})
	s.Add(provider.Message{
		Role: provider.RoleAssistant, Content: "Running it.",
		ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "bash", Arguments: `{"cmd":"make"}`}},
	})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("mid-turn SaveSnapshot: %v", err)
	}

	// Crash + reopen: the resume load answers the dangling call with a
	// placeholder, then the user runs another turn.
	resumed, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession resume: %v", err)
	}
	if !resumed.normalizedDirty {
		t.Fatal("resume load should carry a pending repair for the dangling call")
	}
	resumed.Add(provider.Message{Role: provider.RoleUser, Content: "try again"})
	resumed.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := resumed.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after dirty resume: %v", err)
	}

	replay, err := replaySessionEventLog(SessionEventLogPath(path))
	if err != nil {
		t.Fatalf("replay event log: %v", err)
	}
	if replay.damaged {
		t.Fatal("event log chain broken by the post-resume snapshot")
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession reload: %v", err)
	}
	if got := len(loaded.Messages); got != 6 {
		t.Fatalf("message count after post-resume snapshot = %d, want 6", got)
	}
	if got := loaded.Messages[3].ToolCallID; got != "call_1" {
		t.Fatalf("placeholder tool result not persisted; message 3 tool_call_id = %q", got)
	}
	if got := loaded.Messages[5].Content; got != "done" {
		t.Fatalf("new turn after round-trip = %q, want %q", got, "done")
	}
}


// TestSaveSnapshotAfterDirtyResumeWithTruncatedToolArgsPersistsRepair covers a
// same-length load-time repair: truncated tool-call JSON is fixed in memory, but
// appending against the repaired view would leave the broken arguments on disk.
// The snapshot must rewrite so the repair and new turn persist together.
func TestSaveSnapshotAfterDirtyResumeWithTruncatedToolArgsPersistsRepair(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run tests"})
	s.Add(provider.Message{
		Role:      provider.RoleAssistant,
		Content:   "Running tests.",
		ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "bash", Arguments: `{"cmd":"go test ./...`}},
	})
	s.Add(provider.Message{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_1", Content: "ok"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot with truncated args: %v", err)
	}

	resumed, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession resume: %v", err)
	}
	if !resumed.normalizedDirty {
		t.Fatal("resume load should carry a pending repair for truncated tool arguments")
	}
	const repairedArgs = `{"cmd":"go test ./..."}`
	if got := resumed.Messages[2].ToolCalls[0].Arguments; got != repairedArgs {
		t.Fatalf("repaired args on resume = %q, want %q", got, repairedArgs)
	}

	resumed.Add(provider.Message{Role: provider.RoleUser, Content: "summarize"})
	resumed.Add(provider.Message{Role: provider.RoleAssistant, Content: "Tests passed."})
	if err := resumed.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after truncated-args resume: %v", err)
	}

	events := readSessionEventsForTest(t, path)
	if len(events) != 2 || events[1].Type != sessionEventTypeReplace || events[1].Reason != "snapshot" {
		t.Fatalf("events after truncated-args repair = %+v, want trailing snapshot replace", events)
	}
	replay, err := replaySessionEventLog(SessionEventLogPath(path))
	if err != nil {
		t.Fatalf("replay event log: %v", err)
	}
	if replay.damaged {
		t.Fatal("event log damaged after truncated-args repair rewrite")
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession reload: %v", err)
	}
	if loaded.normalizedDirty {
		t.Fatal("truncated-args repair was not persisted")
	}
	if got := loaded.Messages[2].ToolCalls[0].Arguments; got != repairedArgs {
		t.Fatalf("persisted args = %q, want %q", got, repairedArgs)
	}
	if got := loaded.Messages[5].Content; got != "Tests passed." {
		t.Fatalf("new turn after round-trip = %q, want %q", got, "Tests passed.")
	}
}


// TestSaveRecoveryBranchNotNeededWhenRawTranscriptCoversSnapshot: the
// recovery-needed check must also judge coverage against the pre-repair bytes.
// Here the stored transcript equals the snapshot exactly, but normalization
// backfills an empty tool-call name on load; that repair must not make the
// disk look like it fails to cover the snapshot and fork a pointless recovery.
func TestSaveRecoveryBranchNotNeededWhenRawTranscriptCoversSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run the build"})
	s.Add(provider.Message{
		Role: provider.RoleAssistant, Content: "Running it.",
		ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "", Arguments: `{"cmd":"make"}`}},
	})
	s.Add(provider.Message{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_1", Content: "ok"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := s.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if !errors.Is(err, ErrSessionRecoveryNotNeeded) {
		t.Fatalf("SaveRecoveryBranch err = %v, want ErrSessionRecoveryNotNeeded", err)
	}
}


func TestSaveSnapshotAppendsWithoutReplacingPrefixFile(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := base.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat before append: %v", err)
	}

	next := NewSession("sys")
	next.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	next.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := next.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot append: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat after append: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("SaveSnapshot replaced the session file; want append-in-place for disk-prefix snapshots")
	}
}


func TestSaveSnapshotAppendsEventLogAndDisplayReadModel(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := base.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}
	checkpointBefore, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat checkpoint before append: %v", err)
	}

	next, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession base: %v", err)
	}
	next.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := next.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot append: %v", err)
	}

	checkpointAfter, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat checkpoint after append: %v", err)
	}
	if !os.SameFile(checkpointBefore, checkpointAfter) {
		t.Fatal("append-only snapshot replaced the display read model")
	}
	checkpoint, err := loadSessionMessagesFromJSONL(path, nil)
	if err != nil {
		t.Fatalf("read display read model: %v", err)
	}
	if len(checkpoint) != 3 || checkpoint[2].Role != provider.RoleAssistant || checkpoint[2].Content != "one" {
		t.Fatalf("display read model = %+v, want appended assistant message", checkpoint)
	}
	events := readSessionEventsForTest(t, path)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want replace + append", len(events))
	}
	if events[0].Type != sessionEventTypeReplace || events[1].Type != sessionEventTypeAppend {
		t.Fatalf("event types = %q, %q; want replace, append", events[0].Type, events[1].Type)
	}
	if events[1].MessageIndex != 2 || len(events[1].Messages) != 1 || events[1].Messages[0].Content != "one" {
		t.Fatalf("append event = %+v, want assistant suffix at index 2", events[1])
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession after append: %v", err)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "one" {
		t.Fatalf("loaded tail = %q, want one", got)
	}
	identity, known, err := SessionContentIdentity(path)
	if err != nil || !known {
		t.Fatalf("SessionContentIdentity = (%+v, %v, %v)", identity, known, err)
	}
	idx, err := LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	if err != nil {
		t.Fatalf("LoadSessionDisplayIndex: %v", err)
	}
	if !ValidateSessionDisplayIndex(idx, identity.Revision, identity.RevisionKnown, identity.Digest, checkpointAfter.Size()) {
		t.Fatalf("display index does not describe appended read model: %+v", idx)
	}
}


func TestSaveRewriteAppendsReplaceEventAndRefreshesCheckpoint(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	base.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := base.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession base: %v", err)
	}
	loaded.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "rewound"},
	})
	if err := loaded.SaveRewrite(path); err != nil {
		t.Fatalf("SaveRewrite: %v", err)
	}
	// Rewrites refresh the compatibility checkpoint so direct .jsonl readers
	// and older binaries stay bounded-stale instead of frozen at first save.
	anchor, err := loadSessionMessagesFromJSONL(path, nil)
	if err != nil {
		t.Fatalf("read checkpoint after rewrite: %v", err)
	}
	if len(anchor) != 2 || anchor[1].Content != "rewound" {
		t.Fatalf("checkpoint after rewrite = %+v, want refreshed rewound transcript", anchor)
	}
	events := readSessionEventsForTest(t, path)
	if len(events) != 2 || events[1].Type != sessionEventTypeReplace || events[1].Reason != "rewrite" {
		t.Fatalf("events after rewrite = %+v, want trailing rewrite replace", events)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession after rewrite: %v", err)
	}
	if len(reloaded.Messages) != 2 || reloaded.Messages[1].Content != "rewound" {
		t.Fatalf("replayed rewrite messages = %+v", reloaded.Messages)
	}
}


func TestSaveSnapshotMigratesLegacyJSONLToEventLog(t *testing.T) {
	path := schemaOneSessionPath(t, "legacy.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"system","content":"sys"}`+"\n"+`{"role":"user","content":"legacy"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy jsonl: %v", err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession legacy: %v", err)
	}
	loaded.Add(provider.Message{Role: provider.RoleAssistant, Content: "migrated"})
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot legacy append: %v", err)
	}
	events := readSessionEventsForTest(t, path)
	if len(events) != 1 || events[0].Type != sessionEventTypeReplace {
		t.Fatalf("legacy migration events = %+v, want one replace seed", events)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession migrated: %v", err)
	}
	if got := reloaded.Messages[len(reloaded.Messages)-1].Content; got != "migrated" {
		t.Fatalf("migrated tail = %q, want migrated", got)
	}
	if _, err := os.Stat(SessionEventIndexPath(path)); err != nil {
		t.Fatalf("event index missing: %v", err)
	}
}


func TestSaveSnapshotAllowsAppendAfterSystemPromptRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	base := NewSession("old sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := base.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	next := NewSession("new sys")
	next.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	next.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := next.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after system refresh: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 3 {
		t.Fatalf("message count after system refresh append = %d, want 3", got)
	}
	if got := loaded.Messages[0].Content; got != "new sys" {
		t.Fatalf("system prompt after refresh = %q, want %q", got, "new sys")
	}
}


func TestSaveSnapshotRecordsRevisionAndMetaUpdatesPreserveIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := base.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta base ok=%v err=%v", ok, err)
	}
	if meta.Revision != 1 || meta.ContentDigest == "" || meta.WriterID == "" {
		t.Fatalf("base persistence meta = %+v, want revision/digest/writer", meta)
	}

	if err := UpdateSessionMeta(path, "model-a", "first", 1, true); err != nil {
		t.Fatalf("UpdateSessionMeta: %v", err)
	}
	refreshed, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta refreshed ok=%v err=%v", ok, err)
	}
	if refreshed.Revision != meta.Revision || refreshed.ContentDigest != meta.ContentDigest || refreshed.WriterID != meta.WriterID {
		t.Fatalf("listing meta update changed persistence fields: before=%+v after=%+v", meta, refreshed)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	loaded.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot append: %v", err)
	}
	advanced, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta advanced ok=%v err=%v", ok, err)
	}
	if advanced.Revision != refreshed.Revision+1 {
		t.Fatalf("revision after append = %d, want %d", advanced.Revision, refreshed.Revision+1)
	}
	if advanced.ContentDigest == refreshed.ContentDigest {
		t.Fatalf("content digest did not change after append: %q", advanced.ContentDigest)
	}
}


func TestSaveSnapshotSameContentSkipsRevisionBump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}
	before, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta base ok=%v err=%v", ok, err)
	}

	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot same content: %v", err)
	}
	after, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta after no-op ok=%v err=%v", ok, err)
	}
	if after.Revision != before.Revision || after.ContentDigest != before.ContentDigest || after.WriterID != before.WriterID {
		t.Fatalf("same-content snapshot changed persistence meta: before=%+v after=%+v", before, after)
	}

	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot append after no-op: %v", err)
	}
	advanced, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta advanced ok=%v err=%v", ok, err)
	}
	if advanced.Revision != before.Revision+1 {
		t.Fatalf("revision after append = %d, want %d", advanced.Revision, before.Revision+1)
	}
}


func TestSaveSnapshotSameContentByOtherRuntimeKeepsClonedBaselineWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	resumed, ok := loaded.CloneWithMessagesIfCompatible(loaded.Snapshot())
	if !ok {
		t.Fatal("expected compatible clone")
	}

	// Another runtime autosaves the identical transcript (e.g. a shutdown
	// snapshot of an idle tab). It must not bump the revision, or the resumed
	// clone's baseline goes stale and its next append is misread as a
	// stale-runtime conflict.
	other, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession other: %v", err)
	}
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot other same content: %v", err)
	}

	resumed.Add(provider.Message{Role: provider.RoleUser, Content: "next"})
	if err := resumed.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot append after same-content autosave elsewhere: %v", err)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession appended: %v", err)
	}
	if got := reloaded.Messages[len(reloaded.Messages)-1].Content; got != "next" {
		t.Fatalf("tail after append = %q, want next", got)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*-recovery-*.jsonl")); err != nil || len(matches) != 0 {
		t.Fatalf("recovery branches after append = %v err=%v, want none", matches, err)
	}
}


func TestSaveSnapshotAllowsExactAppendFromStaleRevisionBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}
	staleBaseline := s.persistState(path)

	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot prefix append: %v", err)
	}
	prefixMeta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta prefix ok=%v err=%v", ok, err)
	}
	if prefixMeta.Revision == staleBaseline.revision {
		t.Fatalf("prefix revision did not advance: %d", prefixMeta.Revision)
	}

	s.Add(provider.Message{Role: provider.RoleUser, Content: "two"})
	s.setPersistedBaseline(path, staleBaseline.digest, staleBaseline.version, staleBaseline.revision, true, true, 0, nil)
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot exact append from stale revision baseline: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession appended: %v", err)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "two" {
		t.Fatalf("tail after stale-baseline append = %q, want two", got)
	}
	advancedMeta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta advanced ok=%v err=%v", ok, err)
	}
	if advancedMeta.Revision != prefixMeta.Revision+1 {
		t.Fatalf("revision after stale-baseline append = %d, want %d", advancedMeta.Revision, prefixMeta.Revision+1)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*-recovery-*.jsonl")); err != nil || len(matches) != 0 {
		t.Fatalf("recovery branches after stale-baseline append = %v err=%v, want none", matches, err)
	}
}


func TestSaveSnapshotAllowsCompatibleSystemAppendFromStaleRevisionBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys v1")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}
	staleBaseline := s.persistState(path)

	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot prefix append: %v", err)
	}
	prefixMeta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta prefix ok=%v err=%v", ok, err)
	}

	// A resume swapped the system prompt, then the turn appended a message —
	// while the persistence baseline still points at the first save.
	msgs := s.Snapshot()
	msgs[0] = provider.Message{Role: provider.RoleSystem, Content: "sys v2"}
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "two"})
	s.Replace(msgs)
	s.setPersistedBaseline(path, staleBaseline.digest, staleBaseline.version, staleBaseline.revision, true, true, 0, nil)
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot compatible-system append from stale baseline: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession appended: %v", err)
	}
	if got := loaded.Messages[0].Content; got != "sys v2" {
		t.Fatalf("system after compatible append = %q, want sys v2", got)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "two" {
		t.Fatalf("tail after compatible append = %q, want two", got)
	}
	advancedMeta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta advanced ok=%v err=%v", ok, err)
	}
	if advancedMeta.Revision != prefixMeta.Revision+1 {
		t.Fatalf("revision after compatible append = %d, want %d", advancedMeta.Revision, prefixMeta.Revision+1)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*-recovery-*.jsonl")); err != nil || len(matches) != 0 {
		t.Fatalf("recovery branches after compatible append = %v err=%v, want none", matches, err)
	}
}


func TestSaveSnapshotRefusesStaleBaselineAppendOverRewoundTranscript(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot extend: %v", err)
	}

	// Another runtime rewinds the transcript below this session's baseline
	// (e.g. a cancelled turn truncated the partial assistant reply).
	other, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession other: %v", err)
	}
	other.Replace(other.Snapshot()[:2])
	if err := other.SaveRewrite(path); err != nil {
		t.Fatalf("SaveRewrite rewind: %v", err)
	}

	// Appending from the stale baseline would resurrect the rewound suffix.
	s.Add(provider.Message{Role: provider.RoleUser, Content: "two"})
	if err := s.SaveSnapshot(path); !errors.Is(err, ErrSessionSnapshotConflict) {
		t.Fatalf("SaveSnapshot over rewound transcript err = %v, want ErrSessionSnapshotConflict", err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession after refused append: %v", err)
	}
	if got := len(loaded.Messages); got != 2 {
		t.Fatalf("messages after refused append = %d, want rewound 2", got)
	}
}


func TestSaveRewriteAllowsOwnedRewriteAfterLedgerReset(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "partial turn"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot base: %v", err)
	}
	// The meta sidecar (revision ledger) is lost — e.g. swept by a cleanup
	// that deleted session-adjacent files. The transcript itself is intact.
	if err := os.Remove(BranchMetaPath(path)); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}

	// A cancelled turn strips the partial reply and flushes via SaveRewrite.
	msgs := s.Snapshot()
	s.Replace(msgs[:len(msgs)-1])
	if err := s.SaveRewrite(path); err != nil {
		t.Fatalf("SaveRewrite after ledger reset: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession truncated: %v", err)
	}
	if got := len(loaded.Messages); got != 2 {
		t.Fatalf("messages after owned rewrite = %d, want 2", got)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "first" {
		t.Fatalf("tail after owned rewrite = %q, want first", got)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*-recovery-*.jsonl")); err != nil || len(matches) != 0 {
		t.Fatalf("recovery branches after owned rewrite = %v err=%v, want none", matches, err)
	}
}


func TestSaveSnapshotStillPersistsNormalizedRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	mal := NewSession("sys")
	mal.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	// Unanswered tool call: LoadSession backfills a placeholder result, so the
	// loaded history digests equal to itself while the on-disk bytes differ.
	mal.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "tool-1", Name: "read_file", Arguments: "{}"}}})
	mal.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := mal.Save(path); err != nil {
		t.Fatalf("Save malformed: %v", err)
	}
	base, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta base ok=%v err=%v", ok, err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !loaded.normalizedDirty {
		t.Fatal("fixture did not trigger a load-time repair; adjust the malformed history")
	}
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot repaired history: %v", err)
	}
	repaired, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta repaired ok=%v err=%v", ok, err)
	}
	if repaired.Revision != base.Revision+1 {
		t.Fatalf("revision after repair save = %d, want %d", repaired.Revision, base.Revision+1)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession repaired: %v", err)
	}
	if reloaded.normalizedDirty {
		t.Fatal("repair did not persist: reloaded session is still normalized-dirty")
	}

	// With the repair on disk, the same snapshot is now a true no-op.
	if err := reloaded.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot post-repair: %v", err)
	}
	final, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta final ok=%v err=%v", ok, err)
	}
	if final.Revision != repaired.Revision {
		t.Fatalf("post-repair no-op bumped revision: %d, want %d", final.Revision, repaired.Revision)
	}
}


func TestSaveSnapshotRejectsStalePrefixAfterSystemPromptRefresh(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	current := NewSession("new sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	current.Add(provider.Message{Role: provider.RoleUser, Content: "second"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	stale := NewSession("old sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := stale.SaveSnapshot(path); !errors.Is(err, ErrSessionSnapshotConflict) {
		t.Fatalf("SaveSnapshot stale after system refresh err = %v, want ErrSessionSnapshotConflict", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 4 {
		t.Fatalf("message count after stale system refresh snapshot = %d, want 4", got)
	}
	if got := loaded.Messages[3].Content; got != "second" {
		t.Fatalf("last message after stale system refresh snapshot = %q, want %q", got, "second")
	}
}


func TestSaveRewriteAllowsRewriteOverSameContentForeignStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	base.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := base.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	stale, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession stale: %v", err)
	}
	// Another runtime healed the ledger over identical content: the revision
	// advanced under a foreign writer id, but the recorded digest still
	// describes the exact bytes this session loaded and owns.
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	meta.Revision++
	meta.WriterID = "other-writer"
	if err := SaveBranchMetaPreserveUpdated(path, meta); err != nil {
		t.Fatalf("bump revision: %v", err)
	}

	stale.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "summarized first"},
	})
	if err := stale.SaveRewrite(path); err != nil {
		t.Fatalf("SaveRewrite over same-content stamp: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "summarized first" {
		t.Fatalf("tail after owned rewrite = %q, want summarized first", got)
	}
	advanced, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta advanced ok=%v err=%v", ok, err)
	}
	if advanced.Revision != meta.Revision+1 {
		t.Fatalf("revision after rewrite = %d, want %d", advanced.Revision, meta.Revision+1)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*-recovery-*.jsonl")); err != nil || len(matches) != 0 {
		t.Fatalf("recovery branches after owned rewrite = %v err=%v, want none", matches, err)
	}
}


func TestSaveRewriteRejectsForeignStampForUnattributedBytes(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	base.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := base.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	stale, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession stale: %v", err)
	}
	// A foreign stamp whose digest disagrees with the on-disk transcript is
	// the aftermath of a save whose bytes and record split — the transcript
	// cannot be attributed, so the rewrite must fall to the conflict path.
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	meta.Revision++
	meta.WriterID = "other-writer"
	meta.ContentDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := SaveBranchMetaPreserveUpdated(path, meta); err != nil {
		t.Fatalf("stamp foreign digest: %v", err)
	}

	stale.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "summarized first"},
	})
	err = stale.SaveRewrite(path)
	if !errors.Is(err, ErrSessionSnapshotConflict) {
		t.Fatalf("SaveRewrite unattributed stamp err = %v, want ErrSessionSnapshotConflict", err)
	}
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict.Kind != SessionSnapshotConflictDiverged {
		t.Fatalf("conflict = %+v, want diverged revision conflict", conflict)
	}
	if conflict.BaseRevision != meta.Revision-1 || conflict.DiskRevision != meta.Revision {
		t.Fatalf("conflict revisions = base %d disk %d, want %d/%d",
			conflict.BaseRevision, conflict.DiskRevision, meta.Revision-1, meta.Revision)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "one" {
		t.Fatalf("tail after rejected rewrite = %q, want one", got)
	}
}


func TestSaveSnapshotAllowsOwnedNonPrefixRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	s.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "summarized first"},
	})
	// The persisted digest, revision, and ledger digest still describe the
	// exact bytes this Session wrote, so a non-prefix snapshot may safely use
	// the full-rewrite path without creating a recovery branch.
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot owned non-prefix rewrite: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 2 {
		t.Fatalf("message count after accepted snapshot rewrite = %d, want 2", got)
	}
	if got := loaded.Messages[1].Content; got != "summarized first" {
		t.Fatalf("rewritten content = %q, want %q", got, "summarized first")
	}
}


func TestSaveSnapshotRejectsInterruptedForeignWriteAtSameRevision(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	base := NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "base"})
	if err := base.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	stale, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession stale: %v", err)
	}
	revision, _, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("sessionContentRevision: %v", err)
	}

	foreignMessages := append(stale.Snapshot(),
		provider.Message{Role: provider.RoleAssistant, Content: "foreign writer tail"})
	foreignDigest, err := digestSessionMessages(foreignMessages)
	if err != nil {
		t.Fatalf("digest foreign messages: %v", err)
	}
	if err := appendSessionReplaceEvent(path, foreignMessages, foreignDigest, revision, "snapshot"); err != nil {
		t.Fatalf("append interrupted foreign event: %v", err)
	}
	if err := writeSessionMessages(path, foreignMessages); err != nil {
		t.Fatalf("write interrupted foreign checkpoint: %v", err)
	}
	// Simulate a crash before recordSessionContentRevision: the transcript and
	// event log changed, but the revision still equals stale's baseline.

	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "stale writer tail"})
	err = stale.SaveSnapshot(path)
	if !errors.Is(err, ErrSessionSnapshotConflict) {
		t.Fatalf("SaveSnapshot err = %v, want ErrSessionSnapshotConflict", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession final: %v", err)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Content; got != "foreign writer tail" {
		t.Fatalf("foreign tail after rejected snapshot = %q, want preserved", got)
	}
}


func TestSaveRewriteAllowsOwnedNonPrefixRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	s.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "summarized first"},
	})
	if err := s.SaveRewrite(path); err != nil {
		t.Fatalf("SaveRewrite owned rewrite: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 2 {
		t.Fatalf("message count after rewrite = %d, want 2", got)
	}
	if got := loaded.Messages[1].Content; got != "summarized first" {
		t.Fatalf("rewritten content = %q, want %q", got, "summarized first")
	}
}


func TestCloneWithMessagesPreservesRewriteBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("old sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "tool-1", Name: "read_file", Arguments: "{}"}}})
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "tool-1", Name: "read_file", Content: strings.Repeat("detail ", 100)})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	msgs := loaded.Snapshot()
	msgs[0].Content = "new sys"
	msgs[3].Content = "[elided tool result]"
	resumed := loaded.CloneWithMessages(msgs)
	if err := resumed.SaveRewrite(path); err != nil {
		t.Fatalf("SaveRewrite cloned resume rewrite: %v", err)
	}

	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession rewritten: %v", err)
	}
	if got := reloaded.Messages[0].Content; got != "new sys" {
		t.Fatalf("system prompt after rewrite = %q, want new sys", got)
	}
	if got := reloaded.Messages[3].Content; got != "[elided tool result]" {
		t.Fatalf("tool result after rewrite = %q, want elided", got)
	}
}


func TestCloneWithMessagesIfCompatibleRejectsHistoryChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("old sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	systemOnly := loaded.Snapshot()
	systemOnly[0].Content = "new sys"
	if _, ok := loaded.CloneWithMessagesIfCompatible(systemOnly); !ok {
		t.Fatal("system-only change should be compatible")
	}

	changed := loaded.Snapshot()
	changed[2].Content = "rewritten assistant"
	if _, ok := loaded.CloneWithMessagesIfCompatible(changed); ok {
		t.Fatal("non-system history change should not preserve baseline")
	}
}


func TestSaveRewriteRejectsStalePrefixOverwrite(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	current.Add(provider.Message{Role: provider.RoleUser, Content: "second"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	stale := NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := stale.SaveRewrite(path); !errors.Is(err, ErrSessionSnapshotConflict) {
		t.Fatalf("SaveRewrite stale err = %v, want ErrSessionSnapshotConflict", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := len(loaded.Messages); got != 4 {
		t.Fatalf("message count after stale rewrite = %d, want 4", got)
	}
	if got := loaded.Messages[3].Content; got != "second" {
		t.Fatalf("last message after stale rewrite = %q, want %q", got, "second")
	}
}


func TestSaveRecoveryBranchPersistsDivergedSnapshot(t *testing.T) {
	path := schemaOneSessionPath(t, "session.jsonl")
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	current.Add(provider.Message{Role: provider.RoleUser, Content: "disk second"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	stale := NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "local second"})
	if err := stale.SaveSnapshot(path); !errors.Is(err, ErrSessionSnapshotConflict) {
		t.Fatalf("SaveSnapshot stale err = %v, want ErrSessionSnapshotConflict", err)
	}

	info, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if err != nil {
		t.Fatalf("SaveRecoveryBranch: %v", err)
	}
	if info.Path == "" || info.Path == path {
		t.Fatalf("recovery path = %q, want distinct path", info.Path)
	}
	// tl 1177 moved the preview to tail-first (newest user turn), so the
	// recovery branch of the diverged local snapshot reports "local second",
	// not the old head-first "first". The disk-authority pins below stay
	// untouched: the stale save was rejected, the original tail keeps the
	// on-disk "disk second" (memory residue never won), and the recovery
	// branch persists exactly the diverged local snapshot.
	if info.Turns != 2 || info.Preview != "local second" {
		t.Fatalf("recovery preview/turns = %q/%d, want local second/2", info.Preview, info.Turns)
	}
	recovered, err := LoadSession(info.Path)
	if err != nil {
		t.Fatalf("LoadSession recovery: %v", err)
	}
	if got := recovered.Messages[len(recovered.Messages)-1].Content; got != "local second" {
		t.Fatalf("recovery tail = %q, want local second", got)
	}
	original, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession original: %v", err)
	}
	if got := original.Messages[len(original.Messages)-1].Content; got != "disk second" {
		t.Fatalf("original tail = %q, want disk second", got)
	}
	meta, ok, err := LoadBranchMeta(info.Path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta recovery ok=%v err=%v", ok, err)
	}
	if !meta.Recovered || meta.ParentID != BranchID(path) || meta.Name != RecoveryBranchDefaultName {
		t.Fatalf("recovery meta = %+v, want recovered parent/name", meta)
	}
	if meta.RecoveryDigest == "" || meta.SchemaVersion != BranchMetaCountsVersion {
		t.Fatalf("recovery digest/schema = %q/%d", meta.RecoveryDigest, meta.SchemaVersion)
	}
	if meta.Revision != 1 || meta.ContentDigest != meta.RecoveryDigest || meta.WriterID == "" {
		t.Fatalf("recovery persistence meta = %+v, want revision/content digest/writer", meta)
	}
}


func TestSaveSnapshotRefusesToRecreateExternallyRemovedBaseline(t *testing.T) {
	dir := schemaOneTempDir(t)
	path := filepath.Join(dir, "removed.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "keep me"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range append([]string{path}, store.SessionSidecarFiles(path)...) {
		if err := os.RemoveAll(artifact); err != nil {
			t.Fatal(err)
		}
	}
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "still in memory"})
	if err := s.SaveSnapshot(path); !errors.Is(err, ErrSessionExternallyRemoved) {
		t.Fatalf("SaveSnapshot error = %v, want ErrSessionExternallyRemoved", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("removed path was silently recreated: %v", err)
	}
}


func TestSaveRecoveryBranchSkipsPureStalePrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	current.Add(provider.Message{Role: provider.RoleUser, Content: "disk second"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	stale := NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	if _, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path}); !errors.Is(err, ErrSessionRecoveryNotNeeded) {
		t.Fatalf("SaveRecoveryBranch stale prefix err = %v, want ErrSessionRecoveryNotNeeded", err)
	}
}


func divergedSessionPair(t *testing.T, dir, name string) (string, *Session) {
	t.Helper()
	path := filepath.Join(dir, name)
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	current.Add(provider.Message{Role: provider.RoleUser, Content: "disk second"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}
	stale := NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "local " + name})
	return path, stale
}


func stampRecoveryMeta(t *testing.T, path string, depth int) {
	t.Helper()
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	meta.Recovered = true
	meta.RecoveryDepth = depth
	if err := SaveBranchMeta(path, meta); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
}


func TestSaveRecoveryBranchDedupesByDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "disk"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	stale := NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "local"})
	first, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if err != nil {
		t.Fatalf("first SaveRecoveryBranch: %v", err)
	}
	second, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if err != nil {
		t.Fatalf("second SaveRecoveryBranch: %v", err)
	}
	if second.Path != first.Path || !second.Existing {
		t.Fatalf("second recovery = %+v, want existing same path %q", second, first.Path)
	}
}


func TestSaveRecoveryBranchCompactsLongParentFilename(t *testing.T) {
	dir := t.TempDir()
	parentID := strings.Repeat("longparent-", 22)
	path := filepath.Join(dir, parentID+".jsonl")
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "disk"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	stale := NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "local"})
	info, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if err != nil {
		t.Fatalf("SaveRecoveryBranch: %v", err)
	}
	base := filepath.Base(info.Path)
	if len(base) > 140 {
		t.Fatalf("recovery basename length = %d (%q), want bounded", len(base), base)
	}
	for _, suffix := range []string{".lock", ".lease.lock", ".lease.json", ".meta"} {
		if len(base+suffix) > 255 {
			t.Fatalf("recovery sidecar basename %q length = %d, want <= 255", base+suffix, len(base+suffix))
		}
	}
	meta, ok, err := LoadBranchMeta(info.Path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta recovery ok=%v err=%v", ok, err)
	}
	if meta.ParentID != BranchID(path) {
		t.Fatalf("recovery parent = %q, want original branch id", meta.ParentID)
	}
}


func TestSaveRecoveryBranchDoesNotCascadeRecoveryFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	current := NewSession("sys")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "disk"})
	if err := current.Save(path); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	local := NewSession("sys")
	local.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	local.Add(provider.Message{Role: provider.RoleAssistant, Content: "local"})
	first, err := local.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if err != nil {
		t.Fatalf("first SaveRecoveryBranch: %v", err)
	}

	recoveryDisk, err := LoadSession(first.Path)
	if err != nil {
		t.Fatalf("LoadSession recovery: %v", err)
	}
	recoveryDisk.Add(provider.Message{Role: provider.RoleUser, Content: "disk follow-up"})
	if err := recoveryDisk.SaveSnapshot(first.Path); err != nil {
		t.Fatalf("SaveSnapshot recovery disk: %v", err)
	}
	recoveryLocal := NewSession("sys")
	recoveryLocal.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	recoveryLocal.Add(provider.Message{Role: provider.RoleAssistant, Content: "local"})
	recoveryLocal.Add(provider.Message{Role: provider.RoleUser, Content: "local follow-up"})

	second, err := recoveryLocal.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: first.Path})
	if err != nil {
		t.Fatalf("second SaveRecoveryBranch: %v", err)
	}
	base := filepath.Base(second.Path)
	if count := strings.Count(base, "-recovery-"); count != 1 {
		t.Fatalf("recovery basename = %q, contains %d recovery markers, want 1", base, count)
	}
	if len(base) > 140 {
		t.Fatalf("recovery basename length = %d (%q), want bounded", len(base), base)
	}
}


// TestSaveSnapshotSameRevisionAllowsOwnedNonPrefixAppend reproduces the scenario from
// #6948: a recovery branch whose snapshot saves systematically diverged because
// checkSnapshotWrite's byte-level prefix comparison failed on messages carrying
// local-only metadata (LocalOnly + interrupted_turn) that survived JSON round-trip
// with subtle differences. The persisted digest proves that the disk still holds
// this Session's baseline, so the save proceeds as a full rewrite instead of
// forking another recovery branch.
func TestSaveSnapshotSameRevisionAllowsOwnedNonPrefixAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Simulate a mid-turn snapshot that captured an interrupted tool turn.
	// The LocalOnly + interrupted_turn metadata is the byte-level difference
	// that makes the normalized disk view diverge from the in-memory snapshot.
	s0 := NewSession("sys")
	s0.Add(provider.Message{Role: provider.RoleUser, Content: "edit file"})
	s0.Add(provider.Message{Role: provider.RoleAssistant, Content: "ok", ToolCalls: []provider.ToolCall{
		{ID: "call_1", Name: "edit", Arguments: `{"file":"f","old":"a","new":"b"}`},
	}})
	s0.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call_1", Name: "edit",
		Content: "edited f (+1 -1)", WorkDurationMs: 1234})
	// Simulate interrupted turn: mid-turn snapshot with LocalOnly recovery placeholder.
	s0.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call_2", Name: "bash",
		LocalOnly: true, WorkDurationMs: 0,
		InterruptedTurn: &provider.InterruptedTurnRecovery{
			Pending: true, CompletedTools: []provider.InterruptedToolSummary{
				{ID: "call_1", Name: "edit", Added: 1, Removed: 1},
			},
		},
	})
	if err := s0.Save(path); err != nil {
		t.Fatalf("Save base: %v", err)
	}

	// Simulate recovery: load the saved transcript into a new session, then
	// add more messages. Every subsequent SaveSnapshot must succeed without a
	// diverged conflict while the persisted digest still proves ownership.
	for turn := range 5 {
		s, err := LoadSession(path)
		if err != nil {
			t.Fatalf("LoadSession turn %d: %v", turn, err)
		}
		s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("msg %d", turn)})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("reply %d", turn)})
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("SaveSnapshot turn %d: %v", turn, err)
		}
	}

	// Final transcript must contain all turns.
	final, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession final: %v", err)
	}
	got := len(final.Messages)
	// base: sys + user + asst(tc) + tool + LocalOnly = 5, + 5*2 = 15
	if got < 10 {
		t.Fatalf("final message count = %d, want >= 10", got)
	}
}


func TestContinueSessionPathReusesPriorFile(t *testing.T) {
	prev := filepath.Join("sessions", "20260602-120000.000000000-deepseek.jsonl")
	if got := ContinueSessionPath(prev, "sessions", "other-model"); got != prev {
		t.Fatalf("carried conversation should keep its file %q, got %q", prev, got)
	}
}


func TestContinueSessionPathMintsFreshWhenNoPrior(t *testing.T) {
	dir := t.TempDir()
	got := ContinueSessionPath("", dir, "deepseek")
	if filepath.Dir(got) != dir || !strings.HasSuffix(got, ".jsonl") {
		t.Fatalf("fresh path = %q, want a .jsonl under %q", got, dir)
	}
}


func TestContinueSessionPathNoPersistence(t *testing.T) {
	if got := ContinueSessionPath("", "", "deepseek"); got != "" {
		t.Fatalf("no session dir should disable persistence, got %q", got)
	}
}


func readSessionEventsForTest(t *testing.T, path string) []sessionEventRecord {
	t.Helper()
	f, err := os.Open(SessionEventLogPath(path))
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var out []sessionEventRecord
	for {
		var rec sessionEventRecord
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode event log: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

