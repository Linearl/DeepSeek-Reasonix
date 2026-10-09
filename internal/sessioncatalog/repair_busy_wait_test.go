package sessioncatalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 671: a busy repair outcome is transient writer contention, not damage.
// The row must park in pending with its previous catalog state untouched —
// never deferred, never counted by the deferred/active counters that drive
// the "历史记录稍后重试" notice — and the next wave must converge it.

func TestBusyRepairParksPendingWithoutDeferredNotice(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	catalog, err := Open(ctx, Options{
		Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	record := SessionRecord{
		Path: filepath.Join(dir, "writer.jsonl"), Directory: dir, Scope: "global",
		TurnsState: TurnsUnknown, Health: HealthOK, Preview: "old preview", Turns: 3,
	}
	if _, err := catalog.upsertSessionsWithNotification(ctx, []SessionRecord{record}, nil, "seed", false, upsertDirectoryProjection); err != nil {
		t.Fatal(err)
	}

	var calls int
	catalog.testRepairSessionHook = func(context.Context, string) (agent.SessionListingRepairResult, error) {
		calls++
		return agent.SessionListingRepairResult{}, agent.ErrSessionListingRepairBusy
	}
	catalog.runRepairWave(ctx)
	if calls != 1 {
		t.Fatalf("busy wave repair calls = %d, want 1", calls)
	}

	var state, kind string
	var health Health
	var turnsState TurnsState
	var attempts int
	if err := catalog.db.QueryRowContext(ctx, `SELECT repair_state,repair_error_kind,health,turns_state,repair_attempts
		FROM catalog_sessions WHERE path_key=?`, catalog.pathKey(record.Path)).
		Scan(&state, &kind, &health, &turnsState, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || kind != repairBusyErrorKind || health != HealthOK || turnsState != TurnsUnknown {
		t.Fatalf("busy park = %s/%s/%s/%s, want pending/busy/ok/unknown", state, kind, health, turnsState)
	}
	if attempts != 0 {
		t.Fatalf("busy park grew attempts to %d, want unchanged 0", attempts)
	}

	status := catalog.Status()
	if status.RepairDeferred != 0 {
		t.Fatalf("busy park raised RepairDeferred = %d, want 0", status.RepairDeferred)
	}
	if status.RepairActive != 0 {
		t.Fatalf("busy park raised RepairActive = %d, want 0", status.RepairActive)
	}
	if status.RepairErrorKinds[repairBusyErrorKind] != 1 {
		t.Fatalf("busy diagnostic kind count = %d, want 1", status.RepairErrorKinds[repairBusyErrorKind])
	}

	// A busy row retries on the fixed delay without growing the backoff.
	now = now.Add(repairBusyRetryDelay + time.Second)
	catalog.testRepairSessionHook = func(context.Context, string) (agent.SessionListingRepairResult, error) {
		calls++
		return agent.SessionListingRepairResult{Status: agent.SessionListingRepairApplied, Preview: "new preview", Turns: 4}, nil
	}
	catalog.runRepairWave(ctx)
	if calls != 2 {
		t.Fatalf("post-busy repair calls = %d, want 2", calls)
	}
	if err := catalog.db.QueryRowContext(ctx, `SELECT repair_state,repair_error_kind,health,turns_state
		FROM catalog_sessions WHERE path_key=?`, catalog.pathKey(record.Path)).
		Scan(&state, &kind, &health, &turnsState); err != nil {
		t.Fatal(err)
	}
	if state != "complete" || kind != "" || health != HealthOK || turnsState != TurnsValid {
		t.Fatalf("busy recovery = %s/%s/%s/%s, want complete//ok/valid", state, kind, health, turnsState)
	}
	status = catalog.Status()
	if status.RepairDeferred != 0 || status.RepairActive != 0 {
		t.Fatalf("post-recovery counters deferred/active = %d/%d, want 0/0", status.RepairDeferred, status.RepairActive)
	}
	if len(status.RepairErrorKinds) != 0 {
		t.Fatalf("post-recovery error kinds = %v, want empty", status.RepairErrorKinds)
	}
}

// TestBusyRepairWithHeldSourceLocks exercises the real lock path: a writer
// process holds the session save/file/meta locks while the catalog wave runs,
// so RepairSessionListingProjection itself reports busy. No deferred state and
// no repair notice may appear; after the writer releases, the next wave heals.
func TestBusyRepairWithHeldSourceLocks(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "collab.jsonl")
	saveLineageSession(t, path, "question", "answer")
	// Force a stale listing projection so the reconciled row lands unknown.
	if err := agent.UpdateBranchMeta(path, false, func(meta *agent.BranchMeta) error {
		meta.Revision++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	target := DirectoryTarget{Path: dir, Scope: "global"}
	catalog, err := Open(ctx, Options{
		Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	if err := catalog.ReconcileDirectory(ctx, target); err != nil {
		t.Fatal(err)
	}
	var turnsState TurnsState
	if err := catalog.db.QueryRowContext(ctx, `SELECT turns_state FROM catalog_sessions WHERE path_key=?`,
		catalog.pathKey(path)).Scan(&turnsState); err != nil {
		t.Fatal(err)
	}
	if turnsState != TurnsUnknown {
		t.Fatalf("seeded row turns_state = %s, want unknown", turnsState)
	}

	// The foreground writer holds the generation locks across the wave.
	_, unlockWriter, err := agent.TryLockSessionListingGeneration(path)
	if err != nil {
		t.Fatal(err)
	}
	catalog.runRepairWave(ctx)

	var state, kind string
	var health Health
	if err := catalog.db.QueryRowContext(ctx, `SELECT repair_state,repair_error_kind,health
		FROM catalog_sessions WHERE path_key=?`, catalog.pathKey(path)).Scan(&state, &kind, &health); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || kind != repairBusyErrorKind || health != HealthOK {
		t.Fatalf("busy park under held locks = %s/%s/%s, want pending/busy/ok", state, kind, health)
	}
	status := catalog.Status()
	if status.RepairDeferred != 0 || status.RepairActive != 0 {
		t.Fatalf("held-lock busy counters deferred/active = %d/%d, want 0/0", status.RepairDeferred, status.RepairActive)
	}

	unlockWriter()
	now = now.Add(repairBusyRetryDelay + time.Second)
	catalog.runRepairWave(ctx)
	if err := catalog.db.QueryRowContext(ctx, `SELECT repair_state,repair_error_kind,turns_state
		FROM catalog_sessions WHERE path_key=?`, catalog.pathKey(path)).Scan(&state, &kind, &turnsState); err != nil {
		t.Fatal(err)
	}
	if state != "complete" || turnsState != TurnsValid {
		t.Fatalf("post-release repair = %s/%s/%s, want complete/…/valid", state, kind, turnsState)
	}
}

// TestNonBusyRepairFailureStillDefers keeps the damage path honest: only an
// idle-file parse failure may land in deferred (and raise the notice counts).
func TestNonBusyRepairFailureStillDefers(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	catalog, err := Open(ctx, Options{
		Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	record := SessionRecord{
		Path: filepath.Join(dir, "broken.jsonl"), Directory: dir, Scope: "global",
		TurnsState: TurnsUnknown, Health: HealthOK,
	}
	if _, err := catalog.upsertSessionsWithNotification(ctx, []SessionRecord{record}, nil, "seed", false, upsertDirectoryProjection); err != nil {
		t.Fatal(err)
	}
	catalog.testRepairSessionHook = func(context.Context, string) (agent.SessionListingRepairResult, error) {
		return agent.SessionListingRepairResult{}, errors.New("simulated idle-file parse failure")
	}
	catalog.runRepairWave(ctx)
	var state, kind string
	var health Health
	if err := catalog.db.QueryRowContext(ctx, `SELECT repair_state,repair_error_kind,health
		FROM catalog_sessions WHERE path_key=?`, catalog.pathKey(record.Path)).Scan(&state, &kind, &health); err != nil {
		t.Fatal(err)
	}
	if state != "deferred" || kind != "io" || health != HealthDegraded {
		t.Fatalf("io failure = %s/%s/%s, want deferred/io/degraded", state, kind, health)
	}
	status := catalog.Status()
	if status.RepairDeferred != 1 {
		t.Fatalf("io failure RepairDeferred = %d, want 1", status.RepairDeferred)
	}
}

// TestMigrationV13ReclassifyLegacyBusyDeferred proves the upgrade path: rows
// the pre-671 build parked in deferred with kind=busy (the incident residue)
// return to pending with scan-time health, so the notice clears on reopen.
func TestMigrationV13ReclassifyLegacyBusyDeferred(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "catalog.sqlite")
	catalog, err := Open(ctx, Options{Path: dbPath, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	record := SessionRecord{
		Path: filepath.Join(dir, "legacy.jsonl"), Directory: dir, Scope: "global",
		TurnsState: TurnsUnknown, Health: HealthOK,
	}
	if _, err := catalog.upsertSessionsWithNotification(ctx, []SessionRecord{record}, nil, "seed", false, upsertDirectoryProjection); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().Add(30 * time.Minute).UnixMilli()
	if _, err := catalog.db.ExecContext(ctx, `UPDATE catalog_sessions SET repair_state='deferred',repair_attempts=5,
		repair_retry_at=?,repair_error_kind='busy',health='degraded' WHERE path_key=?`, retryAt, catalog.pathKey(record.Path)); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// Rewind the migration journal so the reopen replays V13 over the legacy row.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=13`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	catalog, err = Open(ctx, Options{Path: dbPath, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	var state, kind string
	var health Health
	var attempts, retry int64
	if err := catalog.db.QueryRowContext(ctx, `SELECT repair_state,repair_error_kind,health,repair_attempts,repair_retry_at
		FROM catalog_sessions WHERE path_key=?`, catalog.pathKey(record.Path)).
		Scan(&state, &kind, &health, &attempts, &retry); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || kind != "" || health != HealthOK || attempts != 0 || retry != 0 {
		t.Fatalf("legacy busy deferred after V13 = %s/%s/%s/%d/%d, want pending//ok/0/0",
			state, kind, health, attempts, retry)
	}
}
