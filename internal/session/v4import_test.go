package session

// Task 805 tests. Fixtures build a synthetic v4 by-id store with the fork's
// own production API (Create/Append/Flush) and legacy transcripts with the
// fork's own save machinery — everything inside t.TempDir(), nothing touches
// a real %APPDATA% tree and nothing is committed into the repository.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

type v4FixtureSession struct {
	id       string
	messages []provider.Message
	// sourcePath sets manifest.source.path (the MigrateLegacy anchor).
	sourcePath string
}

type v4Fixture struct {
	root     string
	sessions []v4FixtureSession
}

func buildV4Fixture(t *testing.T, fixture v4Fixture) {
	t.Helper()
	persistence := NewFilesystemPersistence(fixture.root)
	ctx := context.Background()
	for _, spec := range fixture.sessions {
		sess, err := persistence.Create(CreateOptions{SessionID: spec.id})
		if err != nil {
			t.Fatalf("create v4 fixture session %s: %v", spec.id, err)
		}
		events := make([]Event, 0, len(spec.messages))
		for _, message := range spec.messages {
			payload, err := json.Marshal(map[string]any{"message": message})
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, Event{Kind: "message/complete", Payload: payload})
		}
		if len(events) > 0 {
			if _, err := sess.Append(ctx, Batch{OperationID: "fixture:" + spec.id, Events: events}); err != nil {
				t.Fatalf("append v4 fixture %s: %v", spec.id, err)
			}
			if _, err := sess.Flush(ctx); err != nil {
				t.Fatalf("flush v4 fixture %s: %v", spec.id, err)
			}
		}
		if spec.sourcePath != "" {
			manifest, err := readManifest(filepath.Join(fixture.root, spec.id, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			manifest.Source = &Source{Path: spec.sourcePath, Version: "legacy"}
			if err := writeManifest(filepath.Join(fixture.root, spec.id, "manifest.json"), manifest); err != nil {
				t.Fatal(err)
			}
		}
		if err := sess.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func fixtureMessages(prefix string, count int) []provider.Message {
	messages := make([]provider.Message, 0, count)
	messages = append(messages, provider.Message{Role: provider.RoleSystem, Content: prefix + " system", ID: prefix + "-sys"})
	for i := 0; i < count-1; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		messages = append(messages, provider.Message{Role: role, Content: prefix + " message " + time.Unix(int64(1700000000+i), 0).UTC().Format("150405") + "#" + itoa(i), ID: prefix + "-m" + itoa(i)})
	}
	return messages
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for ; i > 0; i /= 10 {
		digits = string(rune('0'+i%10)) + digits
	}
	return digits
}

func writeLegacyFixture(t *testing.T, dir, name string, messages []provider.Message) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	sess := agent.NewSession("")
	sess.AddBatch(messages...)
	if err := sess.Save(path); err != nil {
		t.Fatalf("write legacy fixture %s: %v", path, err)
	}
	return path
}

func planFrom(t *testing.T, opts V4ImportOptions) *V4ImportPlan {
	t.Helper()
	plan, err := PlanV4Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func entryByID(t *testing.T, plan *V4ImportPlan, id string) V4PlanEntry {
	t.Helper()
	for _, entry := range plan.Entries {
		if entry.SessionID == id {
			return entry
		}
	}
	t.Fatalf("plan has no entry for %s", id)
	return V4PlanEntry{}
}

// 1) Incremental: the v3 transcript is a strict prefix of the v4 projection,
// the tail is appended, and a re-run plans nothing (idempotent).
func TestV4ImportIncrementalAppendIsIdempotent(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	all := fixtureMessages("inc", 4)
	v3Path := filepath.Join(v3Dir, "20260101-000000.000000000-inc.jsonl")
	// The v4 session id is the deterministic bridge id of the v3 path, so this
	// case also covers the bridge-id mapping anchor.
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{
		{id: bridgeSessionIDForPath(filepath.Clean(v3Path)), messages: all},
	}})
	writeLegacyFixture(t, v3Dir, filepath.Base(v3Path), all[:2])

	opts := V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}}
	plan := planFrom(t, opts)
	entry := entryByID(t, plan, bridgeSessionIDForPath(filepath.Clean(v3Path)))
	if entry.Status != V4ImportAppend || entry.TailCount != 2 || entry.Mapping != "bridge-id" {
		t.Fatalf("plan entry = %s/%d/%s, want append/2/bridge-id (reason %s)", entry.Status, entry.TailCount, entry.Mapping, entry.Reason)
	}
	if plan.Mode != V4ImportModeIncremental {
		t.Fatalf("mode = %s, want incremental", plan.Mode)
	}

	report, err := RunV4Import(context.Background(), V4ImportRunOptions{V4ImportOptions: opts})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || !report.SourceUnmoved || report.AppendCount != 1 || len(report.Backup.Dir) == 0 {
		t.Fatalf("report verified=%v unmoved=%v append=%d backup=%+v", report.Verified, report.SourceUnmoved, report.AppendCount, report.Backup)
	}
	written, err := loadLegacyMessages(v3Path)
	if err != nil || len(written) != 4 {
		t.Fatalf("after run: %d messages, err %v", len(written), err)
	}
	for i := range all {
		if messageDigest(written[i]) != messageDigest(all[i]) {
			t.Fatalf("row %d differs from the v4 projection", i)
		}
	}
	if _, err := agent.LoadSession(v3Path); err != nil {
		t.Fatalf("fork loader cannot read the backfilled transcript: %v", err)
	}

	replan := planFrom(t, opts)
	if got := entryByID(t, replan, bridgeSessionIDForPath(filepath.Clean(v3Path))); got.Status != V4ImportSkip {
		t.Fatalf("re-plan entry = %s (%s), want skip", got.Status, got.Reason)
	}
}

// 2) The v4 copy lags behind v3 (the normal mirror state): nothing to do.
func TestV4ImportSkipsWhenV4Lags(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	all := fixtureMessages("lag", 5)
	v3Path := filepath.Join(v3Dir, "20260101-000000.000000000-lag.jsonl")
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{
		{id: "sess-lag", messages: all[:4], sourcePath: v3Path},
	}})
	writeLegacyFixture(t, v3Dir, filepath.Base(v3Path), all)

	plan := planFrom(t, V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}})
	entry := entryByID(t, plan, "sess-lag")
	if entry.Status != V4ImportSkip || !strings.Contains(entry.Reason, "lags") {
		t.Fatalf("entry = %s (%s), want skip with a lag reason", entry.Status, entry.Reason)
	}
}

// 3) The v3 transcript is gone but its identity is recorded (manifest.source):
// a rebuild restores exactly that file.
func TestV4ImportRebuildsDeletedLegacyTranscript(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	all := fixtureMessages("rebuild", 3)
	deleted := filepath.Join(v3Dir, "20260101-000000.000000000-deleted.jsonl")
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{
		{id: "sess-rebuild", messages: all, sourcePath: deleted},
	}})
	// The v3 store exists (another session lives there), so the mode stays
	// incremental — but this one transcript was deleted.
	writeLegacyFixture(t, v3Dir, "20260101-000000.000000000-keeper.jsonl", fixtureMessages("keeper", 2))

	plan := planFrom(t, V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}})
	entry := entryByID(t, plan, "sess-rebuild")
	if entry.Status != V4ImportRebuild || entry.Mapping != "source" || entry.TargetPath != filepath.Clean(deleted) {
		t.Fatalf("entry = %s/%s/%s, want rebuild/source", entry.Status, entry.Mapping, entry.TargetPath)
	}

	report, err := RunV4Import(context.Background(), V4ImportRunOptions{V4ImportOptions: V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.RebuildCount != 1 {
		t.Fatalf("report verified=%v rebuild=%d", report.Verified, report.RebuildCount)
	}
	written, err := loadLegacyMessages(deleted)
	if err != nil || len(written) != 3 {
		t.Fatalf("rebuilt transcript: %d messages, err %v", len(written), err)
	}
	for i := range all {
		if messageDigest(written[i]) != messageDigest(all[i]) {
			t.Fatalf("rebuilt row %d differs", i)
		}
	}
	if _, err := agent.LoadSession(deleted); err != nil {
		t.Fatalf("fork loader cannot read the rebuilt transcript: %v", err)
	}
}

// 4) Diverged copies are conflicts: nothing is written.
func TestV4ImportConflictsOnDivergence(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	divPath := filepath.Join(v3Dir, "20260101-000000.000000000-div.jsonl")
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{
		{id: "sess-div", messages: fixtureMessages("div", 4), sourcePath: divPath},
	}})
	writeLegacyFixture(t, v3Dir, filepath.Base(divPath), fixtureMessages("rewound", 3))

	before, err := loadLegacyMessages(divPath)
	if err != nil {
		t.Fatal(err)
	}
	plan := planFrom(t, V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}})
	entry := entryByID(t, plan, "sess-div")
	if entry.Status != V4ImportConflict {
		t.Fatalf("entry = %s, want conflict", entry.Status)
	}
	report, err := RunV4Import(context.Background(), V4ImportRunOptions{V4ImportOptions: V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ConflictCount != 1 || len(report.TouchedFiles) != 0 || report.Backup.Files != 0 {
		t.Fatalf("conflict run touched something: %+v", report)
	}
	after, err := loadLegacyMessages(divPath)
	if err != nil || len(after) != len(before) {
		t.Fatalf("conflict run changed the transcript: %d -> %d (err %v)", len(before), len(after), err)
	}
}

// 5) An unmapped v4 session is skipped while the v3 store exists, and rebuilt
// under a minted identity when the whole v3 store is gone.
func TestV4ImportUnmappedSkipAndFullRebuild(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{{id: "sess-orphan", messages: fixtureMessages("orphan", 3)}}})

	// Incremental: the store dir exists (even empty), so orphans stay put.
	if err := os.MkdirAll(v3Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := planFrom(t, V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}})
	if entry := entryByID(t, plan, "sess-orphan"); entry.Status != V4ImportSkip {
		t.Fatalf("incremental orphan = %s (%s), want skip", entry.Status, entry.Reason)
	}

	// Full: the store is gone, the orphan is rebuilt into the minted path.
	if err := os.Remove(v3Dir); err != nil {
		t.Fatal(err)
	}
	opts := V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}, RebuildDir: v3Dir}
	plan = planFrom(t, opts)
	if plan.Mode != V4ImportModeFull {
		t.Fatalf("mode = %s, want full", plan.Mode)
	}
	entry := entryByID(t, plan, "sess-orphan")
	if entry.Status != V4ImportRebuild || entry.Mapping != "new" || filepath.Dir(entry.TargetPath) != v3Dir {
		t.Fatalf("full orphan = %s/%s/%s", entry.Status, entry.Mapping, entry.TargetPath)
	}
	report, err := RunV4Import(context.Background(), V4ImportRunOptions{V4ImportOptions: opts})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.RebuildCount != 1 {
		t.Fatalf("full report verified=%v rebuild=%d", report.Verified, report.RebuildCount)
	}
	if _, err := agent.LoadSession(entry.TargetPath); err != nil {
		t.Fatalf("fork loader cannot read the minted rebuild: %v", err)
	}
}

// 6) A failure mid-run rolls every touched file back (boundary 3): the
// appended transcript is back to its pre-run rows and the failed rebuild
// leaves nothing behind.
func TestV4ImportFailureRollsBack(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{
		{id: "sess-ok", messages: fixtureMessages("rb-ok", 4)},
		{id: "sess-bad", messages: fixtureMessages("rb-bad", 3)},
	}})
	v3Path := writeLegacyFixture(t, v3Dir, "20260101-000000.000000000-ok.jsonl", fixtureMessages("rb-ok", 2))
	// The failed rebuild targets a path whose parent is a regular file.
	blocker := filepath.Join(v3Dir, "blocked.jsonl")
	if err := os.WriteFile(blocker, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	// Map sess-bad onto <v3Dir>/blocked.jsonl/… via the manifest source anchor.
	raw, err := os.ReadFile(filepath.Join(v4Root, "sess-bad", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["source"] = map[string]any{"path": filepath.Join(blocker, "inside.jsonl"), "version": "legacy"}
	fixed, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v4Root, "sess-bad", "manifest.json"), fixed, 0o600); err != nil {
		t.Fatal(err)
	}

	opts := V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}}
	_, runErr := RunV4Import(context.Background(), V4ImportRunOptions{V4ImportOptions: opts})
	if runErr == nil {
		t.Fatal("run should have failed on the blocked rebuild")
	}
	written, err := loadLegacyMessages(v3Path)
	if err != nil || len(written) != 2 {
		t.Fatalf("rollback left %d rows (want 2), err %v", len(written), err)
	}
	if _, err := os.Stat(filepath.Join(blocker, "inside.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("failed rebuild left artifacts: %v", err)
	}
}

// 7) The v4 source tree is byte-identical after a run (boundary 5), and the
// fingerprint check refuses a source that moved under the run.
func TestV4ImportSourceFingerprintUnchanged(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	all := fixtureMessages("fp", 4)
	fpPath := filepath.Join(v3Dir, "20260101-000000.000000000-fp.jsonl")
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{
		{id: "sess-fp", messages: all, sourcePath: fpPath},
	}})
	writeLegacyFixture(t, v3Dir, filepath.Base(fpPath), all[:2])

	before, _, _, err := SourceTreeFingerprint(context.Background(), v4Root)
	if err != nil {
		t.Fatal(err)
	}
	report, err := RunV4Import(context.Background(), V4ImportRunOptions{V4ImportOptions: V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}}})
	if err != nil {
		t.Fatal(err)
	}
	after, _, _, err := SourceTreeFingerprint(context.Background(), v4Root)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || !report.SourceUnmoved || report.SourceBefore != before {
		t.Fatalf("source moved: %s -> %s (report unmoved=%v)", before, after, report.SourceUnmoved)
	}
}

// 8) A damaged v3 transcript is a conflict, not a rebuild: the backfill never
// overwrites a file it cannot read.
func TestV4ImportDamagedLegacyIsConflict(t *testing.T) {
	base := t.TempDir()
	v4Root := filepath.Join(base, "sessions-v4")
	v3Dir := filepath.Join(base, "sessions")
	damaged := filepath.Join(v3Dir, "20260101-000000.000000000-dmg.jsonl")
	buildV4Fixture(t, v4Fixture{root: v4Root, sessions: []v4FixtureSession{
		{id: "sess-dmg", messages: fixtureMessages("dmg", 3), sourcePath: damaged},
	}})
	if err := os.MkdirAll(v3Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(damaged, []byte("{\"role\":\"user\",\"content\":}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := planFrom(t, V4ImportOptions{V4Root: v4Root, V3Dirs: []string{v3Dir}})
	entry := entryByID(t, plan, "sess-dmg")
	if entry.Status != V4ImportConflict || !strings.Contains(entry.Reason, "decode") {
		t.Fatalf("entry = %s (%s), want conflict with a decode reason", entry.Status, entry.Reason)
	}
}
