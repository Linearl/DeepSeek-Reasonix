package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// Task 772: the four conversation-store modes must each survive the full data
// lifecycle — write, read, continue, compaction, rewind, fork recovery, crash
// recovery — and the mode ladder must be round-trippable with zero loss. The
// 2026-09-19 dual-write rollback (13 hours, 13.3 GB mirror) happened because
// none of this was pinned down; these tests are the gate.

// newModeFixture builds one isolated mode sandbox: its own temp root holding
// the legacy transcript directory and the v4 store, plus the bridge boot would
// create for this mode (nil for v3_only — boot skips the bridge entirely).
func newModeFixture(t *testing.T, mode string) (*SessionV4Bridge, string, string) {
	t.Helper()
	t.Setenv("REASONIX_SESSION_STORAGE", mode)
	root := t.TempDir()
	legacyDir := filepath.Join(root, "sessions")
	v4Root := filepath.Join(root, "sessions-v4")
	if !config.SessionV4WritesEnabledForMode(mode) {
		return nil, legacyDir, v4Root
	}
	bridge, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatalf("bridge for %s: %v", mode, err)
	}
	t.Cleanup(func() { _ = bridge.CloseAll(context.Background()) })
	bridge.SetReadsV4(config.SessionV4ReadsEnabledForMode(mode))
	bridge.SetV3Frozen(config.SessionV3FrozenForMode(mode))
	return bridge, legacyDir, v4Root
}

func mustSaveLegacy(t *testing.T, sess *agent.Session, path string) {
	t.Helper()
	if err := sess.Save(path); err != nil {
		t.Fatalf("legacy save %s: %v", path, err)
	}
}

// readTranscript is the mode-aware history read: modes 3/4 go through the v4
// bridge, modes 1/2 fall back to the legacy file exactly like the desktop.
func readTranscript(t *testing.T, bridge *SessionV4Bridge, ctx context.Context, mode, legacyPath string, sess *agent.Session) []provider.Message {
	t.Helper()
	if config.SessionV4ReadsEnabledForMode(mode) {
		msgs, ok := bridge.HistoryMessages(ctx, legacyPath)
		if !ok {
			t.Fatalf("%s: v4 history read missed for %s", mode, legacyPath)
		}
		return msgs
	}
	loaded, err := agent.LoadSession(legacyPath)
	if err != nil {
		t.Fatalf("%s: legacy reload: %v", mode, err)
	}
	return loaded.Snapshot()
}

func sameConversation(a, b []provider.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}

func wantConversation(t *testing.T, got, want []provider.Message, what string) {
	t.Helper()
	if !sameConversation(got, want) {
		t.Fatalf("%s: got %d messages, want %d (%v vs %v)", what, len(got), len(want), roles(got), roles(want))
	}
}

func roles(msgs []provider.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, string(m.Role)+":"+m.Content)
	}
	return out
}

// TestSessionStorageFourModeLifecycle runs the full data lifecycle once per
// mode. Every stage re-asserts the mode contract, so a regression in any
// single cell of the matrix names its mode and stage.
func TestSessionStorageFourModeLifecycle(t *testing.T) {
	for _, mode := range config.SessionStorageModes {
		t.Run(mode, func(t *testing.T) {
			lifecycle := newModeLifecycle(t, mode)
			lifecycle.seed()
			lifecycle.writeMirror()
			lifecycle.readSide()
			lifecycle.continueConversation()
			lifecycle.compaction()
			lifecycle.rewind()
			lifecycle.forkRecovery()
			lifecycle.crashRecovery()
			lifecycle.legacyFileUntouched()
			lifecycle.freezeContract()
		})
	}
}

type modeLifecycle struct {
	t          *testing.T
	mode       string
	bridge     *SessionV4Bridge
	ctx        context.Context
	legacyPath string
	v4Root     string

	sess     *agent.Session
	forkPath string
}

func newModeLifecycle(t *testing.T, mode string) *modeLifecycle {
	bridge, legacyDir, v4Root := newModeFixture(t, mode)
	return &modeLifecycle{
		t:          t,
		mode:       mode,
		bridge:     bridge,
		ctx:        context.Background(),
		legacyPath: filepath.Join(legacyDir, "chat.jsonl"),
		v4Root:     v4Root,
		sess:       agent.NewSession(""),
	}
}

func (l *modeLifecycle) fatal(stage string, err error) {
	l.t.Fatalf("%s/%s: %v", l.mode, stage, err)
}

// seed writes the v3 transcript every mode starts from. The v3 file is the
// execution truth in all four modes, so it exists even in v3_only.
func (l *modeLifecycle) seed() {
	l.sess.Add(provider.Message{ID: "u1", Role: provider.RoleUser, Content: "hello"})
	l.sess.Add(provider.Message{ID: "a1", Role: provider.RoleAssistant, Content: "hi"})
	mustSaveLegacy(l.t, l.sess, l.legacyPath)
}

// writeMirror checks the write side: mode 1 creates no bridge and writes no
// v4 root; modes 2-4 land the full transcript in the mirror.
func (l *modeLifecycle) writeMirror() {
	t := l.t
	if config.SessionV4WritesEnabledForMode(l.mode) {
		if l.bridge == nil {
			t.Fatalf("%s: mode writes v4 but boot produced no bridge", l.mode)
		}
		// v4_only inherits a store a dual-write stage populated before the
		// freeze; stage that handoff explicitly, then freeze (the real user
		// journey — a frozen mode cannot mint its own mirror).
		if config.SessionV3FrozenForMode(l.mode) {
			l.bridge.SetV3Frozen(false)
		}
		if _, err := l.bridge.ImportLegacy(l.ctx, l.legacyPath); err != nil {
			l.fatal("write/import", err)
		}
		if err := l.bridge.SyncAgentTranscript(l.ctx, l.legacyPath, l.sess.Snapshot()); err != nil {
			l.fatal("write/sync", err)
		}
		if config.SessionV3FrozenForMode(l.mode) {
			l.bridge.SetV3Frozen(true)
		}
		ref, ok := l.bridge.RefForAgentPath(l.legacyPath)
		if !ok {
			t.Fatalf("%s: no v4 session mapped after sync", l.mode)
		}
		msgs, err := l.bridge.Service().Query().History(l.ctx, ref)
		if err != nil {
			l.fatal("write/query", err)
		}
		wantConversation(t, msgs, l.sess.Snapshot(), l.mode+"/mirror-after-seed")
		return
	}
	if l.bridge != nil {
		t.Fatalf("%s: mode does not write v4 but a bridge exists", l.mode)
	}
	if _, err := os.Stat(l.v4Root); !os.IsNotExist(err) {
		t.Fatalf("%s: v4 root exists without a bridge: %v", l.mode, err)
	}
}

// readSide checks which copy history prefers per mode.
func (l *modeLifecycle) readSide() {
	t := l.t
	if l.bridge == nil {
		return
	}
	msgs, ok := l.bridge.HistoryMessages(l.ctx, l.legacyPath)
	if config.SessionV4ReadsEnabledForMode(l.mode) {
		if !ok {
			t.Fatalf("%s: reads should prefer v4 but HistoryMessages missed", l.mode)
		}
		wantConversation(t, msgs, l.sess.Snapshot(), l.mode+"/read-v4")
		return
	}
	if ok {
		t.Fatalf("%s: reads should fall back to v3 but HistoryMessages hit", l.mode)
	}
}

// continueConversation grows the transcript by one turn and re-checks both
// stores — the "continue" column of the matrix.
func (l *modeLifecycle) continueConversation() {
	l.sess.Add(provider.Message{ID: "u2", Role: provider.RoleUser, Content: "again"})
	l.sess.Add(provider.Message{ID: "a2", Role: provider.RoleAssistant, Content: "sure"})
	mustSaveLegacy(l.t, l.sess, l.legacyPath)
	if l.bridge == nil {
		return
	}
	if err := l.bridge.SyncAgentTranscript(l.ctx, l.legacyPath, l.sess.Snapshot()); err != nil {
		l.fatal("continue/sync", err)
	}
	wantConversation(l.t, readTranscript(l.t, l.bridge, l.ctx, l.mode, l.legacyPath, l.sess), l.sess.Snapshot(), l.mode+"/after-continue")
}

// compaction replaces the transcript with a smaller summarized set (the
// compaction projection) — the mirror must follow, not keep the stale tail.
func (l *modeLifecycle) compaction() {
	compacted := []provider.Message{
		{ID: "c1", Role: provider.RoleUser, Content: "hello"},
		{ID: "c2", Role: provider.RoleAssistant, Content: "summary of earlier turns"},
	}
	l.sess.Replace(compacted)
	mustSaveLegacy(l.t, l.sess, l.legacyPath)
	if l.bridge == nil {
		return
	}
	if err := l.bridge.SyncAgentTranscript(l.ctx, l.legacyPath, l.sess.Snapshot()); err != nil {
		l.fatal("compaction/sync", err)
	}
	wantConversation(l.t, readTranscript(l.t, l.bridge, l.ctx, l.mode, l.legacyPath, l.sess), compacted, l.mode+"/after-compaction")
}

// rewind shrinks the transcript to a prefix of the conversation (the rewind
// projection) and asserts the mirror does not keep the dropped tail.
func (l *modeLifecycle) rewind() {
	rewound := []provider.Message{
		{ID: "r1", Role: provider.RoleUser, Content: "hello"},
	}
	l.sess.Replace(rewound)
	mustSaveLegacy(l.t, l.sess, l.legacyPath)
	if l.bridge == nil {
		return
	}
	if err := l.bridge.SyncAgentTranscript(l.ctx, l.legacyPath, l.sess.Snapshot()); err != nil {
		l.fatal("rewind/sync", err)
	}
	wantConversation(l.t, readTranscript(l.t, l.bridge, l.ctx, l.mode, l.legacyPath, l.sess), rewound, l.mode+"/after-rewind")
}

// forkRecovery simulates a recovery fork: a second transcript file carrying
// the same conversation gets its own v4 session, and the original mapping
// stays usable — the two paths must never cross-contaminate.
func (l *modeLifecycle) forkRecovery() {
	t := l.t
	l.forkPath = filepath.Join(filepath.Dir(l.legacyPath), "chat.recovered.jsonl")
	data, err := os.ReadFile(l.legacyPath)
	if err != nil {
		l.fatal("fork/read", err)
	}
	if err := os.WriteFile(l.forkPath, data, 0o600); err != nil {
		l.fatal("fork/write", err)
	}
	if l.bridge == nil {
		return
	}
	if !config.SessionV3FrozenForMode(l.mode) {
		if _, err := l.bridge.ImportLegacy(l.ctx, l.forkPath); err != nil {
			l.fatal("fork/import", err)
		}
	}
	// A frozen mode refuses the fork import (mode 4 contract); the sync alone
	// must still give the forked transcript its own maintained v4 session.
	if err := l.bridge.SyncAgentTranscript(l.ctx, l.forkPath, l.sess.Snapshot()); err != nil {
		l.fatal("fork/sync", err)
	}
	originalRef, _ := l.bridge.RefForAgentPath(l.legacyPath)
	forkRef, _ := l.bridge.RefForAgentPath(l.forkPath)
	if originalRef.SessionID == forkRef.SessionID {
		t.Fatalf("%s: forked transcript shares a v4 session with the original", l.mode)
	}
}

// crashRecovery tears the v4 frames file mid-commit (a crash between the
// append and the fsync), reopens the store through a fresh bridge — the boot
// path after a crash — and re-lands the transcript. The store must recover to
// its last durable boundary and the resync must restore exact equality.
func (l *modeLifecycle) crashRecovery() {
	t := l.t
	if l.bridge == nil {
		return
	}
	ref, ok := l.bridge.RefForAgentPath(l.legacyPath)
	if !ok {
		t.Fatalf("%s: no mapping before crash simulation", l.mode)
	}
	frames := filepath.Join(l.v4Root, ref.SessionID, "events.frames")
	data, err := os.ReadFile(frames)
	if err != nil {
		l.fatal("crash/read-frames", err)
	}
	if err := l.bridge.CloseAll(l.ctx); err != nil {
		l.fatal("crash/close", err)
	}
	if err := os.WriteFile(frames, data[:len(data)*3/4], 0o600); err != nil {
		l.fatal("crash/tear", err)
	}
	reborn, err := NewSessionV4Bridge(l.v4Root)
	if err != nil {
		l.fatal("crash/reopen", err)
	}
	t.Cleanup(func() { _ = reborn.CloseAll(l.ctx) })
	reborn.SetReadsV4(config.SessionV4ReadsEnabledForMode(l.mode))
	reborn.SetV3Frozen(config.SessionV3FrozenForMode(l.mode))
	l.bridge = reborn
	if err := reborn.SyncAgentTranscript(l.ctx, l.legacyPath, l.sess.Snapshot()); err != nil {
		l.fatal("crash/resync", err)
	}
	wantConversation(l.t, readTranscript(l.t, reborn, l.ctx, l.mode, l.legacyPath, l.sess), l.sess.Snapshot(), l.mode+"/after-crash")
}

// legacyFileUntouched pins the zero-loss floor: no mode, no stage, ever
// breaks the v3 transcript — it stays loadable and equal to the live session.
func (l *modeLifecycle) legacyFileUntouched() {
	loaded, err := agent.LoadSession(l.legacyPath)
	if err != nil {
		l.fatal("v3-untouched/reload", err)
	}
	wantConversation(l.t, loaded.Snapshot(), l.sess.Snapshot(), l.mode+"/legacy-authoritative")
}

// freezeContract pins the mode-4 extra: new legacy imports are refused, the
// refusal never touches the v3 files, and reads keep working off v4.
func (l *modeLifecycle) freezeContract() {
	t := l.t
	if !config.SessionV3FrozenForMode(l.mode) {
		return
	}
	before, err := os.ReadFile(l.legacyPath)
	if err != nil {
		l.fatal("freeze/read-before", err)
	}
	if _, err := l.bridge.ImportLegacy(l.ctx, l.legacyPath); !errors.Is(err, ErrLegacyReadOnly) {
		t.Fatalf("%s: re-import under v4_only = %v, want ErrLegacyReadOnly", l.mode, err)
	}
	if _, err := l.bridge.ContinueLegacy(l.ctx, l.legacyPath, ""); !errors.Is(err, ErrLegacyReadOnly) {
		t.Fatalf("%s: continue-legacy under v4_only = %v, want ErrLegacyReadOnly", l.mode, err)
	}
	after, err := os.ReadFile(l.legacyPath)
	if err != nil {
		l.fatal("freeze/read-after", err)
	}
	if string(before) != string(after) {
		t.Fatalf("%s: refused imports rewrote the frozen legacy transcript", l.mode)
	}
	// The read side keeps working: v4 is the maintained copy.
	wantConversation(t, readTranscript(l.t, l.bridge, l.ctx, l.mode, l.legacyPath, l.sess), l.sess.Snapshot(), l.mode+"/frozen-read")
}

// TestSessionV4BridgeSyncAppendsTailNotFullReplace is the regression gate for
// the 13-hour rollback (task 772 archaeology): growing a transcript through
// repeated saves must cost O(final size) of mirror bytes, not
// O(saves x transcript size). The pre-fix bridge re-appended the full
// transcript on every sync and grew the real-world mirror to 13.3 GB against a
// 43.5 MB legacy store (~300x).
func TestSessionV4BridgeSyncAppendsTailNotFullReplace(t *testing.T) {
	bridge, legacyDir, v4Root := newModeFixture(t, config.SessionStorageDualWriteReadV4)
	ctx := context.Background()
	legacyPath := filepath.Join(legacyDir, "chat.jsonl")

	sess := agent.NewSession("")
	body := strings.Repeat("payload-", 128) // ~1 KB per message
	appendTurn := func(i int) {
		sess.Add(provider.Message{ID: fmt.Sprintf("u%02d", i), Role: provider.RoleUser, Content: body})
		sess.Add(provider.Message{ID: fmt.Sprintf("a%02d", i), Role: provider.RoleAssistant, Content: body})
		mustSaveLegacy(t, sess, legacyPath)
		if err := bridge.SyncAgentTranscript(ctx, legacyPath, sess.Snapshot()); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}
	for i := 1; i <= 12; i++ {
		appendTurn(i)
	}

	ref, ok := bridge.RefForAgentPath(legacyPath)
	if !ok {
		t.Fatal("no v4 session mapped")
	}
	mirrorBytes := dirSize(t, filepath.Join(v4Root, ref.SessionID))
	contentBytes := int64(len(body) * 24) // 24 messages of the same body
	// One full projection of the final transcript plus framing overhead must
	// bound the mirror. The pre-fix bridge crossed 6x here and grows linearly
	// with the save count.
	if mirrorBytes > 2*contentBytes+4096 {
		t.Fatalf("write amplification is back: mirror %d bytes for %d bytes of transcript (pre-fix full-replace regression)", mirrorBytes, contentBytes)
	}
	msgs, err := bridge.Service().Query().History(ctx, ref)
	if err != nil {
		t.Fatalf("final history: %v", err)
	}
	if len(msgs) != 24 {
		t.Fatalf("final history has %d messages, want 24", len(msgs))
	}
}

func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("dir size %s: %v", dir, err)
	}
	return total
}

// TestSessionStorageModeSwitchRoundtrip walks the whole ladder the settings UI
// exposes — v3_only -> dual_write_read_v3 -> dual_write_read_v4 -> v4_only ->
// back down to v3_only — with one persistent store pair and a bridge restart
// per restart-scoped hop (the settings view flags those). Zero loss is
// asserted against the same conversation at every step, and the audit trail
// must record every hop with its pre-switch inventory.
func TestSessionStorageModeSwitchRoundtrip(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	t.Setenv("REASONIX_SESSION_STORAGE", "")

	// The store pair lives at the real config-derived locations so the audit
	// inventory counts the actual files, exactly like the settings UI does.
	legacyPath := filepath.Join(config.SessionDir(), "chat.jsonl")
	v4Root := config.SessionStoreDir()
	ctx := context.Background()

	bridgeFor := func(t *testing.T, mode string) *SessionV4Bridge {
		if !config.SessionV4WritesEnabledForMode(mode) {
			return nil
		}
		bridge, err := NewSessionV4Bridge(v4Root)
		if err != nil {
			t.Fatalf("bridge %s: %v", mode, err)
		}
		t.Cleanup(func() { _ = bridge.CloseAll(ctx) })
		bridge.SetReadsV4(config.SessionV4ReadsEnabledForMode(mode))
		bridge.SetV3Frozen(config.SessionV3FrozenForMode(mode))
		return bridge
	}
	// switch mirrors desktop.setSessionStorageMode: validate -> persist (the
	// config file itself is exercised by the config tests) -> audit with the
	// pre-switch inventory.
	switchMode := func(t *testing.T, from, to string) {
		if err := config.ValidateSessionStorageTransition(from, to, config.SessionStorageHistoryModes()); err != nil {
			t.Fatalf("transition %s -> %s refused: %v", from, to, err)
		}
		snapshot := config.SessionStorageSnapshot()
		if err := config.AppendSessionStorageChange(config.SessionStorageChange{
			From: from, To: to, Source: "settings-ui-test",
			Restart: config.SessionStorageNeedsRestart(from, to),
			V3Count: snapshot.V3Count, V3Bytes: snapshot.V3Bytes,
			V4Count: snapshot.V4Count, V4Bytes: snapshot.V4Bytes,
		}); err != nil {
			t.Fatalf("audit %s -> %s: %v", from, to, err)
		}
	}
	check := func(t *testing.T, mode string, sess *agent.Session, stage string) {
		bridge := bridgeFor(t, mode)
		if bridge == nil {
			loaded, err := agent.LoadSession(legacyPath)
			if err != nil {
				t.Fatalf("%s/%s: legacy reload: %v", mode, stage, err)
			}
			wantConversation(t, loaded.Snapshot(), sess.Snapshot(), mode+"/"+stage)
			return
		}
		if !config.SessionV3FrozenForMode(mode) {
			if _, err := bridge.ImportLegacy(ctx, legacyPath); err != nil {
				t.Fatalf("%s/%s: re-import: %v", mode, stage, err)
			}
		}
		if err := bridge.SyncAgentTranscript(ctx, legacyPath, sess.Snapshot()); err != nil {
			t.Fatalf("%s/%s: sync: %v", mode, stage, err)
		}
		wantConversation(t, readTranscript(t, bridge, ctx, mode, legacyPath, sess), sess.Snapshot(), mode+"/"+stage)
	}

	mode := config.SessionStorageV3Only
	sess := agent.NewSession("")
	sess.Add(provider.Message{ID: "u1", Role: provider.RoleUser, Content: "hello"})
	sess.Add(provider.Message{ID: "a1", Role: provider.RoleAssistant, Content: "hi"})
	mustSaveLegacy(t, sess, legacyPath)
	check(t, mode, sess, "seed")

	// v3_only -> dual_write_read_v3: mirror starts, reads still come from v3.
	next := config.SessionStorageDualWriteReadV3
	switchMode(t, mode, next)
	mode = next
	check(t, mode, sess, "first-mirror")

	// dual_write_read_v3 -> dual_write_read_v4: the read side flips; the two
	// dual modes are the only live-switchable pair, so this hop reuses one
	// bridge and flips the flag exactly like applySessionStorageReadSide.
	if config.SessionStorageNeedsRestart(mode, config.SessionStorageDualWriteReadV4) {
		t.Fatal("dual->dual should not need a restart")
	}
	bridge := bridgeFor(t, mode)
	sess.Add(provider.Message{ID: "u2", Role: provider.RoleUser, Content: "grow"})
	sess.Add(provider.Message{ID: "a2", Role: provider.RoleAssistant, Content: "more"})
	mustSaveLegacy(t, sess, legacyPath)
	if err := bridge.SyncAgentTranscript(ctx, legacyPath, sess.Snapshot()); err != nil {
		t.Fatalf("dual live sync: %v", err)
	}
	bridge.SetReadsV4(true)
	if _, ok := bridge.HistoryMessages(ctx, legacyPath); !ok {
		t.Fatal("dual live switch: reads should prefer v4 after SetReadsV4(true)")
	}
	wantConversation(t, func() []provider.Message { m, _ := bridge.HistoryMessages(ctx, legacyPath); return m }(), sess.Snapshot(), "dual/live-read")
	if err := bridge.CloseAll(ctx); err != nil {
		t.Fatalf("close live bridge: %v", err)
	}
	next = config.SessionStorageDualWriteReadV4
	switchMode(t, mode, next)
	mode = next

	// dual_write_read_v4 -> v4_only: freeze; reads keep working, new imports
	// are refused, but the maintained v4 copy still accepts continued turns.
	next = config.SessionStorageV4Only
	switchMode(t, mode, next)
	mode = next
	check(t, mode, sess, "frozen")
	frozenBridge := bridgeFor(t, mode)
	sess.Add(provider.Message{ID: "u3", Role: provider.RoleUser, Content: "still going"})
	sess.Add(provider.Message{ID: "a3", Role: provider.RoleAssistant, Content: "yes"})
	mustSaveLegacy(t, sess, legacyPath)
	if err := frozenBridge.SyncAgentTranscript(ctx, legacyPath, sess.Snapshot()); err != nil {
		t.Fatalf("v4_only continue: %v", err)
	}
	wantConversation(t, readTranscript(t, frozenBridge, ctx, mode, legacyPath, sess), sess.Snapshot(), "v4_only/continued-under-freeze")

	// v4_only -> dual_write_read_v4 -> v3_only: the full rollback ladder. The
	// v3 file is the execution truth in every mode, so the way back is free.
	next = config.SessionStorageDualWriteReadV4
	switchMode(t, mode, next)
	mode = next
	check(t, mode, sess, "unfrozen")
	next = config.SessionStorageDualWriteReadV3
	switchMode(t, mode, next)
	mode = next
	check(t, mode, sess, "reads-back-to-v3")
	next = config.SessionStorageV3Only
	switchMode(t, mode, next)
	mode = next
	check(t, mode, sess, "back-on-legacy")

	// The audit trail saw every hop, oldest first, with inventories attached.
	changes, err := config.ReadSessionStorageChanges()
	if err != nil {
		t.Fatalf("read audit trail: %v", err)
	}
	if len(changes) != 6 {
		t.Fatalf("audit trail has %d entries, want 6", len(changes))
	}
	for i, change := range changes {
		if change.Source != "settings-ui-test" {
			t.Fatalf("audit entry %d source = %q", i, change.Source)
		}
		if change.V3Count == 0 || change.V3Bytes == 0 {
			t.Fatalf("audit entry %d lost the pre-switch v3 inventory", i)
		}
	}
	if modes := config.SessionStorageHistoryModes(); len(modes) != 12 {
		t.Fatalf("history modes = %d entries, want 12 (from+to per hop)", len(modes))
	}
}
