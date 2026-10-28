package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// store_EventsLog keeps the test table readable.
func store_EventsLog(sessionPath string) string { return store.SessionEventLog(sessionPath) }

// dagForkedLog writes a linear main chain plus one fork head carrying its own
// message, so the migration has a real DAG to flatten (the fork chain must
// leave the flat log while the selected chain survives entry by entry).
func dagForkedLog(t *testing.T, sessionPath string) []provider.Message {
	t.Helper()
	ids, base := dagLinearLog(t, sessionPath)
	at := base.Add(time.Duration(len(ids)) * time.Second)
	dagAppend(t, sessionPath,
		sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "wip", Kind: HeadKindFork, At: at},
		dagMessageEntry(t, "wip", ids[len(ids)-1], "t2", dagMsg(provider.RoleUser, "fork q", "F1"), at.Add(time.Second)),
	)
	st := dagReplay(t, sessionPath)
	msgs, _ := st.materialize(st.selectedHead())
	return msgs
}

func TestMigrateSessionDAGLogApplyIsLossless(t *testing.T) {
	path := dagTestSession(t)
	before := dagForkedLog(t, path)
	if len(before) == 0 {
		t.Fatal("empty materialized chain")
	}
	report, err := MigrateSessionDAGLog(context.Background(), path, SessionDAGMigrationApply)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if report.Status != SessionDAGMigrated {
		t.Fatalf("status = %q detail=%q", report.Status, report.Detail)
	}
	if !report.RolesMatch || !report.DigestMatch || !report.ReplayedOK {
		t.Fatalf("verify flags roles=%t digest=%t replayed=%t", report.RolesMatch, report.DigestMatch, report.ReplayedOK)
	}
	if report.MessageCount != len(before) {
		t.Fatalf("message count %d, want %d", report.MessageCount, len(before))
	}
	// The probe now classifies the log as native schema-1; the flat load
	// returns exactly the selected chain.
	probe, err := probeSessionEventLog(path)
	if err != nil || !probe.native || probe.dag {
		t.Fatalf("post probe native=%v dag=%v err=%v", probe.native, probe.dag, err)
	}
	hasher := newSessionTranscriptHasher()
	res, err := loadSessionTranscript(context.Background(), path, defaultSessionReplayLimits, hasher)
	if err != nil {
		t.Fatalf("flat load: %v", err)
	}
	if len(res.msgs) != len(before) {
		t.Fatalf("flat load %d messages, want %d", len(res.msgs), len(before))
	}
	for i := range before {
		if res.msgs[i].Role != before[i].Role || res.msgs[i].Content != before[i].Content || res.msgs[i].ID != before[i].ID {
			t.Fatalf("message %d drifted: %+v != %+v", i, res.msgs[i], before[i])
		}
	}
	if res.dag {
		t.Fatal("flat load still reports dag")
	}
	// The original schema-2 bytes survive in the backup sidecar.
	if _, err := os.Stat(report.BackupPath); err != nil {
		t.Fatalf("backup sidecar: %v", err)
	}
}

func TestMigrateSessionDAGLogCheckLeavesDiskUntouched(t *testing.T) {
	path := dagTestSession(t)
	dagForkedLog(t, path)
	before, err := os.ReadFile(store_EventsLog(path))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	report, err := MigrateSessionDAGLog(context.Background(), path, SessionDAGMigrationCheck)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.Status != SessionDAGChecked || report.MessageCount == 0 {
		t.Fatalf("status=%q messages=%d", report.Status, report.MessageCount)
	}
	after, err := os.ReadFile(store_EventsLog(path))
	if err != nil {
		t.Fatalf("re-read log: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("check mode modified the log bytes")
	}
	if _, err := os.Stat(report.LogPath + SessionDAGMigrationBackupSuffix); !os.IsNotExist(err) {
		t.Fatal("check mode wrote a backup sidecar")
	}
	// Check is idempotent: a second run reads the same DAG and reports again.
	report2, err := MigrateSessionDAGLog(context.Background(), path, SessionDAGMigrationCheck)
	if err != nil || report2.Status != SessionDAGChecked {
		t.Fatalf("second check status=%q err=%v", report2.Status, err)
	}
}

func TestMigrateSessionDAGLogSkipsNonDAG(t *testing.T) {
	path := dagTestSession(t)
	report, err := MigrateSessionDAGLog(context.Background(), path, SessionDAGMigrationApply)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if report.Status != SessionDAGSkipNotDAG {
		t.Fatalf("status = %q, want %q", report.Status, SessionDAGSkipNotDAG)
	}
}

func TestMigrateSessionDAGLogSkipsDamaged(t *testing.T) {
	path := dagTestSession(t)
	dagForkedLog(t, path)
	f, err := os.OpenFile(store_EventsLog(path), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// A torn trailing entry: the replay marks damaged, migration must refuse.
	if _, err := f.WriteString(`{"schema_version":2,"type":"message"`); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()
	report, err := MigrateSessionDAGLog(context.Background(), path, SessionDAGMigrationApply)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if report.Status != SessionDAGSkipDamaged || !report.DamagedSource {
		t.Fatalf("status=%q damaged=%v", report.Status, report.DamagedSource)
	}
	probe, err := probeSessionEventLog(path)
	if err != nil || !probe.dag {
		t.Fatalf("damaged log must stay schema-2: dag=%v err=%v", probe.dag, err)
	}
}

func TestMigrateSessionDAGLogSecondRunIsNoOp(t *testing.T) {
	path := dagTestSession(t)
	dagForkedLog(t, path)
	if _, err := MigrateSessionDAGLog(context.Background(), path, SessionDAGMigrationApply); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(store_EventsLog(path))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	report, err := MigrateSessionDAGLog(context.Background(), path, SessionDAGMigrationApply)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if report.Status != SessionDAGSkipNotDAG {
		t.Fatalf("second apply status = %q, want %q", report.Status, SessionDAGSkipNotDAG)
	}
	second, err := os.ReadFile(store_EventsLog(path))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second apply rewrote the flat log")
	}
}
