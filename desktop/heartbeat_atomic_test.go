package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/fileutil"
)

// 任务 823 ③: saves are temp+rename (fileutil.AtomicWriteFile), so a crash
// mid-save can never leave a half-written heartbeat-tasks.json. Rename
// semantics are pinned in fileutil's atomicwrite_test.go (untouched).

// heartbeatTempResidue lists temp leftovers that would indicate a torn write.
func heartbeatTempResidue(t *testing.T, engine *HeartbeatEngine) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(engine.configPath()))
	if err != nil {
		t.Fatal(err)
	}
	var residue []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".atomic-") && strings.HasSuffix(e.Name(), ".tmp") {
			residue = append(residue, e.Name())
		}
	}
	return residue
}

// Crash injected at the save boundary (before the temp write exists): the
// previous config stays byte-identical — no torn file, no residue.
func TestHeartbeatSaveCrashBeforePublishLeavesOldConfigIntact(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	if err := engine.saveTasks([]HeartbeatTask{
		{ID: "survivor", Title: "崩溃前已有", Interval: "1h", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatal(err)
	}

	fileutil.CrashPoint = func(op, path string) {
		if op == "atomic-write" && path == engine.configPath() {
			panic("simulated process crash mid-save")
		}
	}
	defer func() { fileutil.CrashPoint = nil }()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("saveTasks did not crash through the injected CrashPoint")
			}
		}()
		_ = engine.saveTasks([]HeartbeatTask{{ID: "new", Title: "n", Interval: "5m", Enabled: true}})
	}()

	after, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("config changed after a crash before publish — a torn write leaked through")
	}
	var cfg heartbeatConfig
	if err := json.Unmarshal(after, &cfg); err != nil {
		t.Fatalf("surviving config not parseable: %v", err)
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].ID != "survivor" {
		t.Fatalf("surviving tasks=%+v, want the pre-crash list", cfg.Tasks)
	}
	if residue := heartbeatTempResidue(t, engine); len(residue) != 0 {
		t.Fatalf("temp residue after pre-publish crash: %v", residue)
	}
}

// Happy path: the save publishes the new config whole and leaves no
// .atomic-*.tmp behind — a concurrent reader sees old-or-new, never half.
func TestHeartbeatSavePublishesExactlyOneCompleteFile(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	if err := engine.saveTasks([]HeartbeatTask{{ID: "first", Title: "f", Interval: "1h", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if err := engine.saveTasks([]HeartbeatTask{
		{ID: "first", Title: "f", Interval: "1h", Enabled: true},
		{ID: "second", Title: "s", Interval: "30s", Enabled: false},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatal(err)
	}
	var cfg heartbeatConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("published config not parseable: %v", err)
	}
	if len(cfg.Tasks) != 2 {
		t.Fatalf("published tasks=%d, want 2", len(cfg.Tasks))
	}
	if cfg.Revision != 2 {
		t.Fatalf("published revision=%d, want 2 (one per save)", cfg.Revision)
	}
	if residue := heartbeatTempResidue(t, engine); len(residue) != 0 {
		t.Fatalf("temp residue after successful save: %v", residue)
	}
}
