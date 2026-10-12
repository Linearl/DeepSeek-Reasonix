package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 任务 823 ② fail-open contract: an invalid config never collapses the panel
// to an empty list. The engine keeps its in-memory tasks, the view carries
// ConfigError, a fix clears both, and a degraded save is refused.

const heartbeatBrokenConfig = `{"schemaVersion": 4, "tasks": [ {"id": "a", "interval": "5m", "createdAt": "yesterday"} ]}`

// writeHeartbeatConfigFile writes raw bytes to the engine's config path.
func writeHeartbeatConfigFile(t *testing.T, engine *HeartbeatEngine, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(engine.configPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine.configPath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatFailOpenKeepsInMemoryTasksOnBrokenExternalEdit(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	if err := engine.saveTasks([]HeartbeatTask{
		{ID: "kept", Title: "旧任务", Interval: "1h", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.readConfigSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	engine.recordConfigSnapshotLocked(snapshot)
	engine.tasks = snapshot.cfg.Tasks
	engine.mu.Unlock()

	// The incident: a hand edit writes a wrong-typed field. The scheduler's
	// next adoption pass must keep the in-memory task (fail-open) and record
	// the health error.
	writeHeartbeatConfigFile(t, engine, heartbeatBrokenConfig)
	engine.mu.Lock()
	engine.adoptExternalEditsLocked()
	kept := len(engine.tasks)
	health := engine.configHealthErr
	engine.mu.Unlock()

	if kept != 1 {
		t.Fatalf("in-memory tasks=%d, want 1 kept by fail-open", kept)
	}
	if health == "" {
		t.Fatal("configHealthErr empty after invalid external edit")
	}
}

func TestHeartbeatFailOpenReloadReturnsTasksPlusConfigError(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	if err := engine.saveTasks([]HeartbeatTask{
		{ID: "kept", Title: "旧任务", Interval: "1h", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	snapshot, err := engine.readConfigSnapshot()
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	engine.recordConfigSnapshotLocked(snapshot)
	engine.tasks = snapshot.cfg.Tasks
	engine.mu.Unlock()

	writeHeartbeatConfigFile(t, engine, heartbeatBrokenConfig)
	view := engine.ReloadConfig()

	if len(view.Tasks) != 1 || view.Tasks[0].ID != "kept" {
		t.Fatalf("view tasks=%+v, want the in-memory task list (fail-open), not an empty list", view.Tasks)
	}
	if view.ConfigError == "" {
		t.Fatal("view.ConfigError empty on invalid config")
	}
	if !strings.Contains(view.ConfigError, "createdAt") {
		t.Fatalf("ConfigError=%q want it to name the offending field", view.ConfigError)
	}
	// The degraded view carries an empty ETag so a stale full-table save
	// conflicts against the broken on-disk state instead of overwriting it.
	if view.ETag != "" {
		t.Fatalf("degraded view ETag=%q, want empty (saves must conflict)", view.ETag)
	}
}

func TestHeartbeatFailOpenClearsWhenConfigIsFixed(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	if err := engine.saveTasks([]HeartbeatTask{
		{ID: "kept", Title: "旧任务", Interval: "1h", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	writeHeartbeatConfigFile(t, engine, heartbeatBrokenConfig)
	if view := engine.ReloadConfig(); view.ConfigError == "" {
		t.Fatal("precondition failed: broken config did not surface ConfigError")
	}

	// The user fixes the field type; the next reload must restore the disk
	// list and clear the banner payload.
	fixed := `{"schemaVersion": 4, "revision": 2, "tasks": [{"id": "fixed", "interval": "5m", "enabled": true, "createdAt": 1700000000000}]}`
	writeHeartbeatConfigFile(t, engine, fixed)
	view := engine.ReloadConfig()
	if view.ConfigError != "" {
		t.Fatalf("ConfigError=%q after fix, want empty", view.ConfigError)
	}
	if len(view.Tasks) != 1 || view.Tasks[0].ID != "fixed" {
		t.Fatalf("tasks=%+v, want the repaired disk list", view.Tasks)
	}
	engine.mu.Lock()
	health := engine.configHealthErr
	engine.mu.Unlock()
	if health != "" {
		t.Fatalf("engine health=%q after fix, want empty", health)
	}
}

func TestHeartbeatFailOpenColdStartRecordsHealthAndRuns(t *testing.T) {
	isolateDesktopUserDirs(t)
	writeHeartbeatConfigFile(t, newHeartbeatEngine(nil), heartbeatBrokenConfig)
	engine := newHeartbeatEngine(nil)
	engine.Start()
	defer engine.Stop()

	engine.mu.Lock()
	running, health, tasks := engine.running, engine.configHealthErr, len(engine.tasks)
	engine.mu.Unlock()
	if !running {
		t.Fatal("engine not running on invalid config (fail-open must keep the scheduler alive)")
	}
	if health == "" {
		t.Fatal("cold start on invalid config left no health error")
	}
	if tasks != 0 {
		t.Fatalf("cold start tasks=%d, want 0 (nothing in memory to keep)", tasks)
	}

	// The panel path (HeartbeatReloadConfig → view) carries the banner even
	// though the list is empty, so an empty task list is never silent.
	view := engine.ReloadConfig()
	if view.ConfigError == "" {
		t.Fatal("cold-start reload view missing ConfigError")
	}
}

func TestHeartbeatFailOpenRejectsSaveFromDegradedView(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	if err := engine.saveTasks([]HeartbeatTask{
		{ID: "kept", Title: "旧任务", Interval: "1h", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	writeHeartbeatConfigFile(t, engine, heartbeatBrokenConfig)
	view := engine.ReloadConfig()
	if view.ConfigError == "" {
		t.Fatal("precondition failed: no ConfigError on broken config")
	}

	// A save attempted from the degraded view must fail — the on-disk file is
	// still unreadable, and overwriting it from a stale list would destroy
	// whatever the user's hand edit was trying to do.
	_, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: view.Tasks})
	if err == nil {
		t.Fatal("ReplaceConfig from degraded view succeeded; want a conflict/read error")
	}
}

// configPath 需要可写目录：确保 isolateDesktopUserDirs 决定的目录与引擎一致
// （configPath 用 config.MemoryUserDir()，isolate 已把它指进临时目录）。
var _ = filepath.Join
