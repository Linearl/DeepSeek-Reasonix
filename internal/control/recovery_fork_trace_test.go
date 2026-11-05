package control

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

type forkTraceLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *forkTraceLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *forkTraceLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureForkTraceLog(t *testing.T) *forkTraceLogBuffer {
	t.Helper()
	buf := &forkTraceLogBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// parseSlogTextLine splits one slog TextHandler line into its key=value pairs.
func parseSlogTextLine(line string) map[string]string {
	out := map[string]string{}
	for line != "" {
		line = strings.TrimLeft(line, " ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			break
		}
		key, rest := line[:eq], line[eq+1:]
		var value string
		switch {
		case strings.HasPrefix(rest, `"`):
			quoted, err := strconv.QuotedPrefix(rest)
			if err != nil {
				return out
			}
			value, _ = strconv.Unquote(quoted)
			rest = rest[len(quoted):]
		case strings.IndexByte(rest, ' ') >= 0:
			sp := strings.IndexByte(rest, ' ')
			value, rest = rest[:sp], rest[sp:]
		default:
			value, rest = rest, ""
		}
		out[key] = value
		line = rest
	}
	return out
}

// forkTraceRecords returns every recovery decision line in logs, parsed.
func forkTraceRecords(logs string) []map[string]string {
	var out []map[string]string
	scanner := bufio.NewScanner(strings.NewReader(logs))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		if fields := parseSlogTextLine(scanner.Text()); fields["msg"] == forkTraceLogMessage {
			out = append(out, fields)
		}
	}
	return out
}

func forkTraceDecisions(records []map[string]string, decision string) []map[string]string {
	var out []map[string]string
	for _, rec := range records {
		if rec["fork_decision"] == decision {
			out = append(out, rec)
		}
	}
	return out
}

func onlyForkTraceDecision(t *testing.T, logs *forkTraceLogBuffer, decision string) map[string]string {
	t.Helper()
	matches := forkTraceDecisions(forkTraceRecords(logs.String()), decision)
	if len(matches) != 1 {
		t.Fatalf("%s decision lines = %d, want 1\n%s", decision, len(matches), logs.String())
	}
	if matches[0]["fork_reason"] == forkReasonUnclassified {
		t.Fatalf("a ladder exit left its decision unclassified: %v", matches[0])
	}
	return matches[0]
}

func requireForkTraceFields(t *testing.T, rec map[string]string, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got, ok := rec[key]; !ok || got != value {
			t.Fatalf("%s = %q (present=%v), want %q\nrecord: %v", key, got, ok, value, rec)
		}
	}
}

func requireShortDigest(t *testing.T, rec map[string]string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if value := rec[key]; len(value) != 12 || strings.Trim(value, "0123456789abcdef") != "" {
			t.Fatalf("%s = %q, want a 12-hex digest prefix\nrecord: %v", key, value, rec)
		}
	}
}

func readConflictDiagnostics(t *testing.T, path string) []snapshotConflictDiagnostic {
	t.Helper()
	file, err := os.Open(store.SessionConflictLog(path))
	if err != nil {
		t.Fatalf("open conflict log: %v", err)
	}
	defer file.Close()
	var out []snapshotConflictDiagnostic
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var rec snapshotConflictDiagnostic
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("decode conflict record: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

func lastConflictDiagnostic(t *testing.T, path string) snapshotConflictDiagnostic {
	t.Helper()
	diags := readConflictDiagnostics(t, path)
	if len(diags) == 0 {
		t.Fatal("conflicts.jsonl is empty")
	}
	return diags[len(diags)-1]
}

// TestForkTraceNamesTriggerBothSidesAndPeerOnFork is the tl1443 acceptance
// shape: two controllers on one file, the second one's turn-end save lands a
// revision, the first one's turn-end save diverges and forks. The decision
// line and the conflicts.jsonl record must carry the trigger, both
// rev/digest/msgs triples, the ledger writer and the peer that wrote disk.
func TestForkTraceNamesTriggerBothSidesAndPeerOnFork(t *testing.T) {
	logs := captureForkTraceLog(t)
	dir := t.TempDir()
	ctrlA, _, path := divergedControllers1352(t, dir, "shared.jsonl", event.Discard)

	rec := onlyForkTraceDecision(t, logs, forkDecisionFork)
	requireForkTraceFields(t, rec, map[string]string{
		"fork_reason":        forkReasonDiverged,
		"fork_trigger":       forkTriggerTurnEnd,
		"fork_entry":         forkEntryActivityIfChanged,
		"mode":               "snapshot",
		"session":            "shared",
		"conflict_kind":      string(agent.SessionSnapshotConflictDiverged),
		"src_rev":            "1",
		"disk_rev":           "2",
		"disk_writer":        agent.LedgerWriterSelf,
		"disk_writer_pid":    strconv.Itoa(os.Getpid()),
		"peer_trigger":       forkTriggerTurnEnd,
		"peer_entry":         forkEntryActivityIfChanged,
		"brake_active":       "false",
		"brake_seq":          "0",
		"since_last_fork_ms": "-1",
		"controller_forks":   "1",
		"topic_forks":        "0",
		"fork_quota":         strconv.Itoa(agent.SnapshotForkQuotaPerTopic),
		"existing":           "false",
		"rewrite_pending":    "false",
		"fallthrough":        forkFellTakeoverDeclined,
	})
	requireShortDigest(t, rec, "src_digest", "base_digest", "disk_digest", "ledger_digest")
	if rec["src_digest"] == rec["disk_digest"] || rec["disk_digest"] != rec["ledger_digest"] {
		t.Fatalf("sides must differ and the ledger must describe disk: %v", rec)
	}
	for _, key := range []string{"src_msgs", "disk_msgs", "peer_age_ms", "elapsed_ms"} {
		if _, err := strconv.Atoi(rec[key]); err != nil {
			t.Fatalf("%s = %q, want a number", key, rec[key])
		}
	}
	for _, key := range []string{"caller", "peer_caller"} {
		if !strings.Contains(rec[key], "Controller.snapshotActivityIfChanged@") || !strings.Contains(rec[key], "Controller.finishInFlightTurn@") {
			t.Fatalf("%s = %q, want the turn-end write path", key, rec[key])
		}
	}
	if recovery := agent.BranchID(ctrlA.SessionPath()); rec["recovery"] != recovery {
		t.Fatalf("recovery = %q, want the fork target %q", rec["recovery"], recovery)
	}

	var forkRecord *snapshotConflictDiagnostic
	for _, diag := range readConflictDiagnostics(t, path) {
		if diag.Outcome == "forked_recovery_branch" {
			forkRecord = &diag
		}
	}
	if forkRecord == nil {
		t.Fatal("conflicts.jsonl lacks the fork record")
	}
	if forkRecord.Decision != forkDecisionFork || forkRecord.Reason != forkReasonDiverged ||
		forkRecord.Trigger != forkTriggerTurnEnd || forkRecord.Entry != forkEntryActivityIfChanged ||
		forkRecord.Chain != rec["caller"] || forkRecord.SnapshotDigest != rec["src_digest"] ||
		forkRecord.BaseDigest != rec["base_digest"] || forkRecord.DiskDigest != rec["disk_digest"] ||
		forkRecord.LedgerDigest != rec["ledger_digest"] || forkRecord.DiskWriter != agent.LedgerWriterSelf ||
		forkRecord.PeerTrigger != forkTriggerTurnEnd || forkRecord.SinceLastForkMs != -1 ||
		forkRecord.Fallthrough != forkFellTakeoverDeclined {
		t.Fatalf("fork record lacks the trace: %+v", *forkRecord)
	}
	raw, err := os.ReadFile(store.SessionConflictLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), dir) || strings.Contains(string(raw), filepath.ToSlash(dir)) {
		t.Fatalf("conflict record leaked a local path: %s", raw)
	}
}

// aliveWriterConflict builds a diverged conflict whose second writer is still
// alive (it advanced the disk again), so the rev-lag takeover declines.
func aliveWriterConflict(t *testing.T, name string) (string, *agent.Session, *agent.SessionSnapshotConflictError) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	alive, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	alive.Add(provider.Message{Role: "user", Content: "alive-tail"})
	if err := alive.SaveSnapshot(path); err != nil {
		t.Fatalf("alive writer save: %v", err)
	}
	return path, live, conflict
}

// TestForkTraceBrakeWindowAdoption pins the brake rung's line: inside the
// window the decision is adopt/fork_brake with the window position and the
// declined takeover named as the fallthrough.
func TestForkTraceBrakeWindowAdoption(t *testing.T) {
	logs := captureForkTraceLog(t)
	path, live, conflict := aliveWriterConflict(t, "brake.jsonl")
	c := newTakeoverLadderController(t, path, live)
	c.mu.Lock()
	c.lastSnapshotForkAt = time.Now().Add(-10 * time.Second)
	c.forkObs.forks = 1
	c.mu.Unlock()

	if _, outcome, err := c.recoverSnapshotConflict(path, conflict, false); err != nil || outcome != conflictAdoptedDisk {
		t.Fatalf("brake outcome = %v err = %v, want conflictAdoptedDisk", outcome, err)
	}
	rec := onlyForkTraceDecision(t, logs, forkDecisionAdopt)
	requireForkTraceFields(t, rec, map[string]string{
		"fork_reason":      forkReasonBrake,
		"fork_entry":       forkEntryDirect,
		"brake_active":     "true",
		"brake_seq":        "1",
		"controller_forks": "1",
		"fallthrough":      forkFellTakeoverDeclined,
		"peer_trigger":     forkTriggerOther,
	})
	if !strings.Contains(rec["caller"], "TestForkTraceBrakeWindowAdoption") {
		t.Fatalf("caller = %q, want the test frame that drove the ladder", rec["caller"])
	}
	if !strings.Contains(rec["peer_caller"], "aliveWriterConflict") {
		t.Fatalf("peer_caller = %q, want the alive writer's frame", rec["peer_caller"])
	}
	since, _ := strconv.Atoi(rec["since_last_fork_ms"])
	remaining, _ := strconv.Atoi(rec["brake_remaining_ms"])
	if since < 10_000 || remaining <= 0 || since+remaining < int(snapshotForkBrakeWindow.Milliseconds())-1000 {
		t.Fatalf("brake window position since=%d remaining=%d, want ~10s in with the rest of the window left", since, remaining)
	}
	last := lastConflictDiagnostic(t, path)
	if last.Outcome != "adopted_disk_within_fork_brake" || last.Decision != forkDecisionAdopt ||
		last.Reason != forkReasonBrake || !last.BrakeActive || last.BrakeSeq != 1 || last.BrakeRemainingMs <= 0 {
		t.Fatalf("brake record = %+v", last)
	}
}

func TestForkTraceStalePrefixAdoption(t *testing.T) {
	logs := captureForkTraceLog(t)
	path := filepath.Join(t.TempDir(), "stale.jsonl")
	live := agent.NewSession("sys")
	live.Add(provider.Message{Role: "user", Content: "m1"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: "user", Content: "m2"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	live.Replace(append([]provider.Message(nil), live.Snapshot()[:2]...))
	saveErr := live.SaveRewrite(path)
	if kind, ok := agent.SnapshotConflictKind(saveErr); !ok || kind != agent.SessionSnapshotConflictStalePrefix {
		t.Fatalf("fixture err = %v, want stale-prefix conflict", saveErr)
	}
	c := newTakeoverLadderController(t, path, live)
	if _, outcome, err := c.recoverSnapshotConflict(path, saveErr, true); err != nil || outcome != conflictAdoptedDisk {
		t.Fatalf("stale outcome = %v err = %v", outcome, err)
	}
	rec := onlyForkTraceDecision(t, logs, forkDecisionAdopt)
	requireForkTraceFields(t, rec, map[string]string{
		"fork_reason":   forkReasonStalePrefix,
		"mode":          "rewrite",
		"conflict_kind": string(agent.SessionSnapshotConflictStalePrefix),
		"brake_active":  "false",
	})
	if _, present := rec["fallthrough"]; present {
		t.Fatalf("stale-prefix adoption must not report a fallthrough: %v", rec)
	}
}

func TestForkTraceQuotaAdoption(t *testing.T) {
	logs := captureForkTraceLog(t)
	path, live, conflict := aliveWriterConflict(t, "quota.jsonl")
	for _, lane := range []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
		cp := agent.NewSession("sys")
		cp.Add(provider.Message{Role: provider.RoleUser, Content: "old copy " + lane})
		if err := cp.Save(filepath.Join(filepath.Dir(path), "quota-recovery-"+lane+".jsonl")); err != nil {
			t.Fatal(err)
		}
	}
	c := newTakeoverLadderController(t, path, live)
	if _, outcome, err := c.recoverSnapshotConflict(path, conflict, false); err != nil || outcome != conflictAdoptedDisk {
		t.Fatalf("quota outcome = %v err = %v", outcome, err)
	}
	requireForkTraceFields(t, onlyForkTraceDecision(t, logs, forkDecisionAdopt), map[string]string{
		"fork_reason": forkReasonQuota,
		"topic_forks": strconv.Itoa(agent.SnapshotForkQuotaPerTopic),
		"fallthrough": forkFellTakeoverDeclined,
	})
}

func TestForkTraceRevLagTakeoverIsRewrite(t *testing.T) {
	logs := captureForkTraceLog(t)
	path := filepath.Join(t.TempDir(), "takeover.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	c := newTakeoverLadderController(t, path, live)
	if _, outcome, err := c.recoverSnapshotConflict(path, conflict, false); err != nil || outcome != conflictTookOver {
		t.Fatalf("takeover outcome = %v err = %v", outcome, err)
	}
	requireForkTraceFields(t, onlyForkTraceDecision(t, logs, forkDecisionRewrite), map[string]string{
		"fork_reason":  forkReasonRevLagTakeover,
		"disk_writer":  agent.LedgerWriterSelf,
		"peer_trigger": forkTriggerOther,
	})
	last := lastConflictDiagnostic(t, path)
	if last.Outcome != "diverged_takeover_committed" || last.Decision != forkDecisionRewrite || last.Reason != forkReasonRevLagTakeover {
		t.Fatalf("takeover record = %+v", last)
	}
}
