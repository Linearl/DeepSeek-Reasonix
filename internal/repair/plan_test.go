package repair

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/fileutil"
)

func repairMutationTestKey(path string) string {
	return canonicalRepairPath(path)
}

func TestCanonicalRepairPathUsesFilesystemCaseSemantics(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "Project")
	lower := filepath.Join(root, "project")

	original := repairPathCaseInsensitive
	t.Cleanup(func() { repairPathCaseInsensitive = original })

	repairPathCaseInsensitive = func(string) bool { return false }
	if canonicalRepairPath(upper) == canonicalRepairPath(lower) {
		t.Fatal("case-sensitive filesystem identities were conflated")
	}

	repairPathCaseInsensitive = func(string) bool { return true }
	if canonicalRepairPath(upper) != canonicalRepairPath(lower) {
		t.Fatal("case-insensitive filesystem aliases did not converge")
	}
}

func TestApplyRepairPlanPreservesUncooperativeWriteAfterRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(tabs, []byte("confirmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "tabs", Actions: []RepairPlanAction{{Type: "rebuild_derived_state", Target: "tabs", Reason: "malformed"}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	originalHook := repairMutationAfterRename
	repairMutationAfterRename = func(path string) {
		if path == tabs {
			if err := os.WriteFile(path, []byte("new-after-rename"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { repairMutationAfterRename = originalHook })
	result, err := ApplyRepairPlan(plan, ApplyPlanOptions{ExpectedPreviewID: RepairPlanPreviewID(plan, preview)})
	if err == nil {
		t.Fatal("uncooperative post-rename write was accepted")
	}
	if len(result.Applied) != 0 {
		t.Fatalf("applied = %v, want no successful action", result.Applied)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "new-after-rename" {
		t.Fatalf("post-rename writer was overwritten: %q, %v", got, err)
	}
	quarantines, err := filepath.Glob(tabs + ".reasonix-rebuild-*")
	if err != nil || len(quarantines) != 1 {
		t.Fatalf("confirmed state backup = %v, %v", quarantines, err)
	}
	if got, err := os.ReadFile(quarantines[0]); err != nil || string(got) != "confirmed" {
		t.Fatalf("confirmed state backup = %q, %v", got, err)
	}
	// The moved node must still be undoable: concurrent rewrite is retained as redo.
	repairMutationAfterRename = originalHook
	if _, err := UndoLastRepair(); err != nil {
		t.Fatalf("undo after concurrent recreate: %v", err)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "confirmed" {
		t.Fatalf("undo restored = %q, %v, want confirmed quarantine", got, err)
	}
	redos, err := filepath.Glob(tabs + ".reasonix-redo-*")
	if err != nil || len(redos) != 1 {
		t.Fatalf("redo copies = %v, %v", redos, err)
	}
	if got, err := os.ReadFile(redos[0]); err != nil || string(got) != "new-after-rename" {
		t.Fatalf("redo retained concurrent write = %q, %v", got, err)
	}
}

func TestApplyRepairPlanRestoresCurrentConfirmedSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	if err := os.WriteFile(global, []byte("default_model = \"known-good\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordHealthyConfig("v1"); err != nil {
		t.Fatal(err)
	}
	snapshots, err := ListConfigSnapshots()
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	current := []byte("default_model = \"current\"\n")
	if err := os.WriteFile(global, current, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "snapshot", Actions: []RepairPlanAction{{Type: "restore_snapshot", SnapshotID: snapshots[0].ID, Reason: "known good"}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyRepairPlan(plan, ApplyPlanOptions{ExpectedPreviewID: RepairPlanPreviewID(plan, preview)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || !strings.Contains(result.Applied[0], "restored config snapshot") {
		t.Fatalf("applied = %v", result.Applied)
	}
	if got, err := os.ReadFile(global); err != nil || string(got) != "default_model = \"known-good\"\n" {
		t.Fatalf("restored config = %q, %v", got, err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(global); err != nil || string(got) != string(current) {
		t.Fatalf("undo restored = %q, %v", got, err)
	}
}

func TestApplyRepairPlanReportsConfirmedLastKnownGoodRestoreFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	if err := os.WriteFile(global, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lastKnownGood := lastKnownGoodConfigPath()
	if err := os.MkdirAll(filepath.Dir(lastKnownGood), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lastKnownGood, []byte("[also-broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := RepairPlan{SchemaVersion: 1, Summary: "config", Actions: []RepairPlanAction{{Type: "repair_config", Scope: "global", Reason: "invalid"}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyRepairPlan(plan, ApplyPlanOptions{ExpectedPreviewID: RepairPlanPreviewID(plan, preview)})
	if err == nil || !strings.Contains(err.Error(), "restore confirmed last-known-good config") {
		t.Fatalf("error = %v, want confirmed restore failure", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("applied = %v, want failed action omitted", result.Applied)
	}
	if _, statErr := os.Stat(global); !os.IsNotExist(statErr) {
		t.Fatalf("global config unexpectedly restored: %v", statErr)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(global); err != nil || string(got) != "[broken\n" {
		t.Fatalf("undo restored = %q, %v", got, err)
	}
}

func TestApplyRepairPlanMultiActionUndoRevertsWholePlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(global, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tabs, []byte("bad-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "config + tabs", Actions: []RepairPlanAction{
		{Type: "repair_config", Scope: "global", Reason: "bad toml"},
		{Type: "rebuild_derived_state", Target: "tabs", Reason: "bad tabs"},
	}}
	if _, err := ApplyRepairPlan(plan, ApplyPlanOptions{Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tabs); !os.IsNotExist(err) {
		t.Fatalf("tabs not quarantined: %v", err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(global)
	if err != nil || string(got) != "[broken\n" {
		t.Fatalf("global config not restored by plan-level undo: %q, %v", got, err)
	}
	got, err = os.ReadFile(tabs)
	if err != nil || string(got) != "bad-tabs" {
		t.Fatalf("derived state not restored by plan-level undo: %q, %v", got, err)
	}
}

func TestApplyRepairPlanPersistsWholePrefixBeforeReturningFromAction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(global, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tabs, []byte("bad-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "crash-safe plan", Actions: []RepairPlanAction{
		{Type: "repair_config", Scope: "global", Reason: "bad toml"},
		{Type: "rebuild_derived_state", Target: "tabs", Reason: "bad tabs"},
	}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}

	originalHook := repairTransactionAfterPersist
	repairTransactionAfterPersist = func(tx *RepairTransaction) {
		if len(tx.Changes) > 0 && tx.Changes[len(tx.Changes)-1].Scope == "derived:tabs" {
			panic("simulated crash after action transaction persist")
		}
	}
	panicked := false
	func() {
		defer func() {
			panicked = recover() != nil
		}()
		_, _ = ApplyRepairPlan(plan, ApplyPlanOptions{
			ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
		})
	}()
	repairTransactionAfterPersist = originalHook
	if !panicked {
		t.Fatal("simulated post-persist crash did not run")
	}

	tx, err := ReadLastRepair()
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Changes) != 2 ||
		tx.Changes[0].Scope != "global" ||
		tx.Changes[1].Scope != "derived:tabs" {
		t.Fatalf("durable repair prefix = %+v", tx.Changes)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(global); err != nil || string(got) != "[broken\n" {
		t.Fatalf("global config not restored after crash: %q, %v", got, err)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "bad-tabs" {
		t.Fatalf("derived state not restored after crash: %q, %v", got, err)
	}
}

func TestApplyRepairPlanPersistsMissingSnapshotTargetBeforeCreate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(global, []byte("default_model = \"known-good\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordHealthyConfig("v1"); err != nil {
		t.Fatal(err)
	}
	snapshots, err := ListConfigSnapshots()
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	if err := os.Remove(global); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tabs, []byte("bad-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "crash-safe create", Actions: []RepairPlanAction{
		{Type: "rebuild_derived_state", Target: "tabs", Reason: "bad tabs"},
		{Type: "restore_snapshot", SnapshotID: snapshots[0].ID, Reason: "known good"},
	}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}

	originalHook := repairSnapshotAfterCreate
	t.Cleanup(func() { repairSnapshotAfterCreate = originalHook })
	repairSnapshotAfterCreate = func(path string) {
		if path == global {
			panic("simulated crash after snapshot create")
		}
	}
	panicked := false
	func() {
		defer func() {
			panicked = recover() != nil
		}()
		_, _ = ApplyRepairPlan(plan, ApplyPlanOptions{
			ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
		})
	}()
	repairSnapshotAfterCreate = originalHook
	if !panicked {
		t.Fatal("simulated post-create crash did not run")
	}

	tx, err := ReadLastRepair()
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Changes) != 1 || tx.Changes[0].Scope != "derived:tabs" {
		t.Fatalf("committed repair prefix = %+v", tx.Changes)
	}
	pendingBytes, err := os.ReadFile(pendingRepairTransactionPath())
	if err != nil {
		t.Fatal(err)
	}
	var pending RepairTransaction
	if err := json.Unmarshal(pendingBytes, &pending); err != nil {
		t.Fatal(err)
	}
	if len(pending.Changes) != 2 ||
		pending.Changes[0].Scope != "derived:tabs" ||
		!pending.Changes[1].RemoveOnUndo ||
		!pending.Changes[1].Prepared ||
		pending.Changes[1].TargetPath != global {
		t.Fatalf("prepared repair prefix = %+v", pending.Changes)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	tx, err = ReadLastRepair()
	if err != nil || len(tx.Changes) != 2 || !tx.Undone {
		t.Fatalf("reconciled repair prefix = %+v, %v", tx, err)
	}
	if _, err := os.Lstat(global); !os.IsNotExist(err) {
		t.Fatalf("created config remained after crash recovery: %v", err)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "bad-tabs" {
		t.Fatalf("derived state not restored after crash: %q, %v", got, err)
	}
}

func TestApplyRepairPlanPreparedIntentSurvivesCrashBeforeRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	tabs := filepath.Join(home, "desktop-tabs.json")
	projects := filepath.Join(home, "desktop-projects.json")
	if err := os.WriteFile(projects, []byte("previous-repair"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RebuildDerivedState("projects"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tabs, []byte("confirmed-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "prepared rename", Actions: []RepairPlanAction{{
		Type: "rebuild_derived_state", Target: "tabs", Reason: "bad tabs",
	}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	originalHook := repairMutationAfterPrepare
	t.Cleanup(func() { repairMutationAfterPrepare = originalHook })
	repairMutationAfterPrepare = func(path string) {
		if path == tabs {
			panic("simulated crash before rename")
		}
	}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_, _ = ApplyRepairPlan(plan, ApplyPlanOptions{
			ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
		})
	}()
	repairMutationAfterPrepare = originalHook
	if !panicked {
		t.Fatal("simulated pre-rename crash did not run")
	}
	tx, err := ReadLastRepair()
	if err != nil || len(tx.Changes) != 1 ||
		tx.Changes[0].Scope != "derived:projects" ||
		tx.Changes[0].Prepared {
		t.Fatalf("previous last repair was overwritten = %+v, %v", tx, err)
	}
	if _, err := os.Stat(pendingRepairTransactionPath()); err != nil {
		t.Fatalf("prepared journal is missing: %v", err)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "confirmed-tabs" {
		t.Fatalf("pre-rename target changed: %q, %v", got, err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "confirmed-tabs" {
		t.Fatalf("prepared no-op undo changed target: %q, %v", got, err)
	}
	if got, err := os.ReadFile(projects); err != nil || string(got) != "previous-repair" {
		t.Fatalf("previous last repair was not undone: %q, %v", got, err)
	}
	if _, err := os.Stat(pendingRepairTransactionPath()); !os.IsNotExist(err) {
		t.Fatalf("no-op prepared journal survived reconciliation: %v", err)
	}
}

func TestApplyRepairPlanPreparedIntentSurvivesCrashAfterRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(global, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tabs, []byte("confirmed-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "prepared prefix", Actions: []RepairPlanAction{
		{Type: "repair_config", Scope: "global", Reason: "bad config"},
		{Type: "rebuild_derived_state", Target: "tabs", Reason: "bad tabs"},
	}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	originalHook := repairMutationAfterRename
	t.Cleanup(func() { repairMutationAfterRename = originalHook })
	repairMutationAfterRename = func(path string) {
		if path == tabs {
			panic("simulated crash after rename")
		}
	}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_, _ = ApplyRepairPlan(plan, ApplyPlanOptions{
			ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
		})
	}()
	repairMutationAfterRename = originalHook
	if !panicked {
		t.Fatal("simulated post-rename crash did not run")
	}
	tx, err := ReadLastRepair()
	if err != nil || len(tx.Changes) != 1 || tx.Changes[0].Scope != "global" {
		t.Fatalf("committed repair prefix = %+v, %v", tx, err)
	}
	if _, err := os.Stat(pendingRepairTransactionPath()); err != nil {
		t.Fatalf("post-rename prepared journal is missing: %v", err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	tx, err = ReadLastRepair()
	if err != nil || len(tx.Changes) != 2 || tx.Changes[1].Prepared || !tx.Undone {
		t.Fatalf("reconciled repair prefix = %+v, %v", tx, err)
	}
	if got, err := os.ReadFile(global); err != nil || string(got) != "[broken\n" {
		t.Fatalf("global prefix not restored: %q, %v", got, err)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "confirmed-tabs" {
		t.Fatalf("prepared renamed target not restored: %q, %v", got, err)
	}
}

func TestApplyRepairPlanRetainsDurableRenameWhenPendingCleanupFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(tabs, []byte("confirmed-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "durable cleanup boundary", Actions: []RepairPlanAction{{
		Type: "rebuild_derived_state", Target: "tabs", Reason: "bad tabs",
	}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}

	originalHook := repairPendingAfterMove
	t.Cleanup(func() { repairPendingAfterMove = originalHook })
	injected := false
	var hookErr error
	repairPendingAfterMove = func(path, cleanup string) {
		if injected {
			return
		}
		injected = true
		var b []byte
		b, hookErr = os.ReadFile(cleanup)
		if hookErr == nil {
			hookErr = os.WriteFile(path, b, 0o600)
		}
	}
	_, err = ApplyRepairPlan(plan, ApplyPlanOptions{
		ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
	})
	repairPendingAfterMove = originalHook
	if hookErr != nil {
		t.Fatalf("inject duplicate pending journal: %v", hookErr)
	}
	if err == nil || !strings.Contains(err.Error(), "cleanup pending journal") {
		t.Fatalf("apply error = %v, want durable cleanup failure", err)
	}
	if _, err := os.Lstat(tabs); !os.IsNotExist(err) {
		t.Fatalf("durable rename was compensated: %v", err)
	}
	tx, err := ReadLastRepair()
	if err != nil || len(tx.Changes) != 1 || tx.Changes[0].Prepared {
		t.Fatalf("durable undo state = %+v, %v", tx, err)
	}
	if _, err := os.Stat(pendingRepairTransactionPath()); err != nil {
		t.Fatalf("pending journal needed for cleanup retry is missing: %v", err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "confirmed-tabs" {
		t.Fatalf("undo after cleanup retry = %q, %v", got, err)
	}
}

func TestPendingRepairCleanupNeverDeletesConcurrentReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(tabs, []byte("confirmed-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "pending ownership", Actions: []RepairPlanAction{{
		Type: "rebuild_derived_state", Target: "tabs", Reason: "bad tabs",
	}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}

	replacement := []byte("{\"foreign\":true}\n")
	originalHook := repairPendingAfterMove
	t.Cleanup(func() { repairPendingAfterMove = originalHook })
	injected := false
	repairPendingAfterMove = func(_, cleanup string) {
		if !injected {
			injected = true
			if err := os.WriteFile(cleanup, replacement, 0o600); err != nil {
				panic(err)
			}
		}
	}
	_, err = ApplyRepairPlan(plan, ApplyPlanOptions{
		ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
	})
	repairPendingAfterMove = originalHook
	if err == nil || !strings.Contains(err.Error(), "changed before cleanup") {
		t.Fatalf("apply error = %v, want pending ownership failure", err)
	}
	got, readErr := os.ReadFile(pendingRepairTransactionPath())
	if readErr != nil || string(got) != string(replacement) {
		t.Fatalf("concurrent pending replacement = %q, %v", got, readErr)
	}
	if _, err := UndoLastRepair(); err == nil ||
		!strings.Contains(err.Error(), "reconcile pending mutation") {
		t.Fatalf("undo with foreign pending journal = %v", err)
	}
	if got, err := os.ReadFile(pendingRepairTransactionPath()); err != nil ||
		string(got) != string(replacement) {
		t.Fatalf("foreign pending journal was consumed: %q, %v", got, err)
	}
	if _, err := os.Lstat(tabs); !os.IsNotExist(err) {
		t.Fatalf("failed-closed undo changed repaired target: %v", err)
	}
}

func TestReconcilePreparedRepairRechecksSourceAfterTargetLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	tabs := filepath.Join(home, "desktop-tabs.json")
	if err := os.WriteFile(tabs, []byte("confirmed-tabs"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx := newRepairTransaction(time.Now())
	tx.Changes = append(tx.Changes, preparedRepairChangeForPrevious(
		"derived:tabs",
		tabs,
		tabs+".reasonix-rebuild-20260729T000000Z",
	))
	if err := persistPreparedRepairTransaction(tx); err != nil {
		t.Fatal(err)
	}

	originalHook := repairMutationBeforeLock
	t.Cleanup(func() { repairMutationBeforeLock = originalHook })
	targetKey := repairMutationTestKey(tabs)
	injected := false
	repairMutationBeforeLock = func(paths []string) {
		if injected {
			return
		}
		for _, path := range paths {
			if path == targetKey {
				injected = true
				if err := os.WriteFile(tabs, []byte("drifted-tabs"), 0o600); err != nil {
					panic(err)
				}
			}
		}
	}
	if _, err := UndoLastRepair(); err == nil ||
		!strings.Contains(err.Error(), "prepared state is not present") {
		t.Fatalf("reconcile drift error = %v", err)
	}
	repairMutationBeforeLock = originalHook
	if got, err := os.ReadFile(tabs); err != nil || string(got) != "drifted-tabs" {
		t.Fatalf("reconcile changed drifted source: %q, %v", got, err)
	}
	if _, err := os.Stat(pendingRepairTransactionPath()); err != nil {
		t.Fatalf("ambiguous prepared journal was removed: %v", err)
	}
}

func TestUndoPreparedSnapshotCreatePreservesConcurrentReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	snapshot := []byte("default_model = \"known-good\"\n")
	if err := os.WriteFile(global, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordHealthyConfig("v1"); err != nil {
		t.Fatal(err)
	}
	snapshots, err := ListConfigSnapshots()
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	if err := os.Remove(global); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "snapshot create", Actions: []RepairPlanAction{{
		Type: "restore_snapshot", SnapshotID: snapshots[0].ID, Reason: "known good",
	}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	originalHook := repairSnapshotAfterCreate
	t.Cleanup(func() { repairSnapshotAfterCreate = originalHook })
	repairSnapshotAfterCreate = func(string) { panic("simulated crash after create") }
	func() {
		defer func() { _ = recover() }()
		_, _ = ApplyRepairPlan(plan, ApplyPlanOptions{
			ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
		})
	}()
	repairSnapshotAfterCreate = originalHook
	concurrent := []byte("default_model = \"concurrent\"\n")
	if err := os.WriteFile(global, concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(global); err != nil || string(got) != string(concurrent) {
		t.Fatalf("undo consumed concurrent replacement: %q, %v", got, err)
	}
}

func TestSnapshotCreateDoesNotClaimConcurrentReplacementBeforeOwnershipCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	global := filepath.Join(home, "config.toml")
	snapshot := []byte("default_model = \"known-good\"\n")
	if err := os.WriteFile(global, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordHealthyConfig("v1"); err != nil {
		t.Fatal(err)
	}
	snapshots, err := ListConfigSnapshots()
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	if err := os.Remove(global); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{SchemaVersion: 1, Summary: "snapshot create", Actions: []RepairPlanAction{{
		Type: "restore_snapshot", SnapshotID: snapshots[0].ID, Reason: "known good",
	}}}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	concurrent := []byte("default_model = \"concurrent\"\n")
	originalHook := repairSnapshotAfterCreate
	t.Cleanup(func() { repairSnapshotAfterCreate = originalHook })
	repairSnapshotAfterCreate = func(path string) {
		if err := fileutil.AtomicWriteFile(path, concurrent, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = ApplyRepairPlan(plan, ApplyPlanOptions{
		ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
	})
	repairSnapshotAfterCreate = originalHook
	if err == nil || !strings.Contains(err.Error(), "published create ownership changed") {
		t.Fatalf("concurrent replacement error = %v", err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(global); err != nil || string(got) != string(concurrent) {
		t.Fatalf("undo consumed concurrent replacement: %q, %v", got, err)
	}
}

func TestRepairMutationLockConvergesSymlinkAliases(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real", "project")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(base, "link")
	if err := os.MkdirAll(linkParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(linkParent, "project")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(realDir, "reasonix.toml")
	aliasFile := filepath.Join(linkDir, "reasonix.toml")
	if err := os.WriteFile(realFile, []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if canonicalRepairPath(realFile) != canonicalRepairPath(aliasFile) {
		t.Fatalf("symlink aliases diverged: real=%q alias=%q", canonicalRepairPath(realFile), canonicalRepairPath(aliasFile))
	}

	holder, err := lockRepairMutations(realFile)
	if err != nil {
		t.Fatal(err)
	}
	reached := make(chan struct{})
	originalHook := repairMutationBeforeLock
	repairMutationBeforeLock = func(paths []string) {
		if len(paths) == 1 && paths[0] == canonicalRepairPath(aliasFile) {
			select {
			case <-reached:
			default:
				close(reached)
			}
		}
	}
	t.Cleanup(func() { repairMutationBeforeLock = originalHook })

	type outcome struct {
		unlock func()
		err    error
	}
	resultCh := make(chan outcome, 1)
	go func() {
		unlock, err := lockRepairMutations(aliasFile)
		resultCh <- outcome{unlock, err}
	}()
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		holder()
		t.Fatal("alias lock did not wait on real-path holder")
	}
	select {
	case got := <-resultCh:
		if got.unlock != nil {
			got.unlock()
		}
		holder()
		t.Fatalf("alias lock acquired while real path held: err=%v", got.err)
	case <-time.After(150 * time.Millisecond):
	}
	holder()
	got := <-resultCh
	if got.err != nil {
		t.Fatalf("alias lock after release: %v", got.err)
	}
	got.unlock()
}

func TestRepairMutationLockSerializesUpdateRollbackAndPrepare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "reasonix-desktop")
	originalExecutable := repairExecutable
	repairExecutable = func() (string, error) { return filepath.Join(dir, "reasonix-guard"), nil }
	t.Cleanup(func() { repairExecutable = originalExecutable })
	if err := os.WriteFile(target, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	tx, err := PrepareFileUpdate("v1", "v2", target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}

	holder, err := lockRepairMutations(target)
	if err != nil {
		t.Fatal(err)
	}
	reached := make(chan struct{})
	originalHook := repairMutationBeforeLock
	repairMutationBeforeLock = func(paths []string) {
		if len(paths) == 1 && paths[0] == canonicalRepairPath(target) {
			select {
			case <-reached:
			default:
				close(reached)
			}
		}
	}
	t.Cleanup(func() { repairMutationBeforeLock = originalHook })

	var once sync.Once
	resultCh := make(chan error, 1)
	go func() {
		_, err := RollbackPendingUpdateExact(tx)
		resultCh <- err
	}()
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		holder()
		t.Fatal("rollback did not wait for shared target lock")
	}
	select {
	case err := <-resultCh:
		holder()
		t.Fatalf("rollback completed while target locked: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	once.Do(holder)
	if err := <-resultCh; err != nil {
		t.Fatalf("rollback after target unlock: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "old" {
		t.Fatalf("target after serialized rollback = %q, %v", got, err)
	}
}
