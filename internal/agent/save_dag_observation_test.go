package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl-1220 观测计数的验收回归。红线：纯观测、默认零行为影响——
// 计数①只允许在 nil_cache + >32MiB 的整本重放组合上移动，快路径（extended）
// 与小日志都不得移动；计数②只读分类，归档态会话普查后必须仍是归档态。

// TestNilCacheLargeLogReplayCounter pins counter ①: the save-chain full
// replay census fires exactly once for a cold (graph-cache-empty) graph
// acquisition whose replayed log exceeds the 32MiB first-paint threshold, and
// never for warm (extended) acquisitions or small logs. It drives
// dagStateForSave directly over a hand-appended DAG log — the observation
// point itself — instead of the full save pipeline, whose extra 33MiB passes
// would dominate the runtime without adding coverage.
func TestNilCacheLargeLogReplayCounter(t *testing.T) {
	t.Cleanup(func() {
		SetSessionGraphCacheCapacity(sessionGraphCacheCapacityDefault)
		resetGraphCacheForTest()
	})
	base := time.Date(2026, 1, 8, 10, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	big := strings.Repeat("tl1220-nilcache-payload-", 1<<20) // ~24MiB
	entries = append(entries,
		dagMessageEntry(t, SessionMainHead, "", "t1", dagMsg(provider.RoleUser, big, "q1"), base),
		dagMessageEntry(t, SessionMainHead, "q1", "t1", dagMsg(provider.RoleAssistant, big, "a1"), base),
	)
	path := dagTestSession(t)
	dagAppend(t, path, entries...)
	if info, err := os.Stat(store.SessionEventLog(path)); err != nil || info.Size() <= sessionTranscriptTailThresholdBytes {
		t.Fatalf("fixture log must exceed the 32MiB threshold: size=%d err=%v", info.Size(), err)
	}
	s := NewSession("sys") // s.head.state is nil: the process cache is the only graph source

	// Cold start over a >32MiB log: nil_cache + full replay — count exactly 1.
	resetGraphCacheForTest()
	before := SessionDAGNilCacheLargeReplays()
	if _, err := s.dagStateForSave(context.Background(), path, base); err != nil {
		t.Fatal(err)
	}
	if got := SessionDAGNilCacheLargeReplays() - before; got != 1 {
		t.Fatalf("cold nil_cache large replay moved the counter by %d, want 1", got)
	}

	// Warm acquisition (the first call re-populated the process cache):
	// extended, no count.
	if _, err := s.dagStateForSave(context.Background(), path, base); err != nil {
		t.Fatal(err)
	}
	if got := SessionDAGNilCacheLargeReplays() - before; got != 1 {
		t.Fatalf("warm acquisition moved the counter by %d beyond the cold hit, want 0", got-1)
	}

	// A small log never counts, even cold: rebuild under the threshold.
	smallPath := dagTestSession(t)
	smallEntries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	smallEntries = append(smallEntries,
		dagMessageEntry(t, SessionMainHead, "", "t1", dagMsg(provider.RoleUser, "q", "sq1"), base),
	)
	dagAppend(t, smallPath, smallEntries...)
	sSmall := NewSession("sys")
	resetGraphCacheForTest()
	if _, err := sSmall.dagStateForSave(context.Background(), smallPath, base); err != nil {
		t.Fatal(err)
	}
	if got := SessionDAGNilCacheLargeReplays() - before; got != 1 {
		t.Fatalf("cold nil_cache small-log replay moved the counter by %d, want 0", got-1)
	}
}

// TestCensusStoredDAGSessions pins counter ②: the census counts each session
// exactly once per store (plaintext winning over a leftover archive), and the
// archived DAG session it classifies stays archived — the census never
// materializes anything.
func TestCensusStoredDAGSessions(t *testing.T) {
	dir := t.TempDir()

	// Legacy DAG session (plain schema-2 events sidecar).
	t.Setenv(SessionLogSchemaEnv, "v2")
	dagPath := filepath.Join(dir, "dag1.jsonl")
	dagSavedSession(t, dagPath, "d1", "d2")

	// Archived-only DAG session: build a second DAG log, archive its events
	// sidecar by hand (same self-describing shape CompressSessionEventLogZst
	// writes), then remove the plaintext so only the archive remains.
	archPath := filepath.Join(dir, "dag2.jsonl")
	dagSavedSession(t, archPath, "a1", "a2")
	archiveEventsSidecar(t, archPath)

	// Schema-1 native session (build default writes flat).
	t.Setenv(SessionLogSchemaEnv, "")
	flatPath := filepath.Join(dir, "flat1.jsonl")
	dagSavedSession(t, flatPath, "f1")

	// Foreign head at the events path: present but not one of ours.
	junkPath := filepath.Join(dir, "junk.jsonl")
	if err := os.WriteFile(store.SessionEventLog(junkPath), []byte("not a schema we own\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A transcript with no events sidecar at all: not an event-log session,
	// so it must not appear in any count.
	if err := os.WriteFile(filepath.Join(dir, "bare.jsonl"), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	census := CensusStoredDAGSessions([]string{dir})
	if census.ScannedDirs != 1 || census.DirErrors != 0 {
		t.Fatalf("scanned=%d errors=%d, want 1/0", census.ScannedDirs, census.DirErrors)
	}
	if census.EventLogSessions != 4 {
		t.Fatalf("event-log sessions = %d, want 4 (dag1, dag2 archived, flat1, junk)", census.EventLogSessions)
	}
	if census.DAGSessions != 2 {
		t.Fatalf("dag sessions = %d, want 2 (dag1 plain + dag2 archived)", census.DAGSessions)
	}
	if census.NativeSessions != 1 {
		t.Fatalf("native sessions = %d, want 1 (flat1)", census.NativeSessions)
	}
	if census.OtherSessions != 1 {
		t.Fatalf("other sessions = %d, want 1 (junk)", census.OtherSessions)
	}
	if census.ArchivedOnly != 1 || census.DAGArchivedOnly != 1 {
		t.Fatalf("archived-only = %d/%d, want 1/1", census.ArchivedOnly, census.DAGArchivedOnly)
	}

	// Zero-touch proof: the archived sidecar must still be archive-only after
	// the census walked it.
	plain := store.SessionEventLog(archPath)
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("census materialized the archived events log at %s", plain)
	}

	// A missing directory is a counted dir error, not a crash.
	missing := CensusStoredDAGSessions([]string{filepath.Join(dir, "nope")})
	if missing.ScannedDirs != 0 || missing.DirErrors != 1 {
		t.Fatalf("missing dir: scanned=%d errors=%d, want 0/1", missing.ScannedDirs, missing.DirErrors)
	}
}

// archiveEventsSidecar compresses the session's plaintext events sidecar into
// the canonical archive shape (header line + payload) and removes the
// plaintext, bypassing the 4MiB compress floor on purpose — the census must
// classify small archives exactly like production ones.
func archiveEventsSidecar(t *testing.T, sessionPath string) {
	t.Helper()
	plain := store.SessionEventLog(sessionPath)
	blob, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	zst := sessionEventLogZstPathFor(sessionPath)
	f, err := os.Create(zst)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(sessionEventZstHeader{
		SchemaVersion: sessionEventZstSchemaVersion,
		Kind:          sessionEventZstKind,
		PlainBytes:    int64(len(blob)),
		CompressedAt:  time.Now().UTC(),
		Reason:        "tl1220-census-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(append(header, '\n')); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(blob); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
}

// TestClassifySessionEventLogHeadMatchesRuntimeProbe pins that the census
// classifier and the runtime probe cannot disagree about probe.dag: for every
// fixture shape, classifySessionEventLogHead's DAG verdict equals
// probeSessionEventLog's probe.dag.
func TestClassifySessionEventLogHeadMatchesRuntimeProbe(t *testing.T) {
	build := func(t *testing.T, env string) string {
		t.Setenv(SessionLogSchemaEnv, env)
		path := dagTestSession(t)
		dagSavedSession(t, path, "q1", "a1")
		return path
	}
	dagPath := build(t, "v2")
	t.Setenv(SessionLogSchemaEnv, "")
	flatPath := build(t, "")

	for _, path := range []string{dagPath, flatPath} {
		cls, size := classifySessionEventLogHead(path)
		probe, err := probeSessionEventLog(path)
		if err != nil {
			t.Fatal(err)
		}
		if want := probe.dag; (cls == eventLogClassDAG) != want {
			t.Fatalf("%s: census dag=%v (%d bytes) != probe.dag=%v", filepath.Base(path), cls == eventLogClassDAG, size, want)
		}
		if size != probe.size {
			t.Fatalf("%s: census size=%d != probe size=%d", filepath.Base(path), size, probe.size)
		}
	}
}
