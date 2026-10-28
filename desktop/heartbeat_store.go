package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
)

// loadTasks reads tasks from disk.
func (e *HeartbeatEngine) loadTasks() []HeartbeatTask {
	snapshot, err := e.readConfigSnapshot()
	if err != nil {
		return nil
	}
	return snapshot.cfg.Tasks
}

func (e *HeartbeatEngine) readConfigSnapshot() (heartbeatConfigSnapshot, error) {
	path := e.configPath()
	b, err := readFileUTF8(path)
	if err != nil {
		if os.IsNotExist(err) {
			return heartbeatConfigSnapshot{}, nil
		}
		return heartbeatConfigSnapshot{}, err
	}
	var cfg heartbeatConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return heartbeatConfigSnapshot{}, fmt.Errorf("invalid config: %w", err)
	}
	// Reject future schemas on read as well as write: the scheduler must not
	// execute tasks with scheduling or approval semantics this binary does not
	// understand.
	if cfg.SchemaVersion > heartbeatSchemaVersion {
		return heartbeatConfigSnapshot{}, fmt.Errorf("heartbeat config schemaVersion %d is newer than this binary supports (%d); upgrade Reasonix", cfg.SchemaVersion, heartbeatSchemaVersion)
	}
	// Merge the run-history sidecar (execution journal kept outside the main
	// config so an older binary cannot drop it on a full-table save). Union by
	// execution timestamp and keep the newest maxRunHistory entries.
	runs, err := e.readRunHistorySidecar(cfg)
	if err != nil {
		return heartbeatConfigSnapshot{}, err
	}
	if len(runs) > 0 {
		for i := range cfg.Tasks {
			if hist, ok := runs[cfg.Tasks[i].ID]; ok && len(hist) > 0 {
				cfg.Tasks[i].RunHistory = mergeRunHistory(cfg.Tasks[i].RunHistory, hist)
			}
		}
	}
	snapshot := heartbeatConfigSnapshot{
		cfg:    cfg,
		digest: sha256.Sum256(b),
		exists: true,
	}
	return snapshot, nil
}

func (e *HeartbeatEngine) recordConfigSnapshotLocked(snapshot heartbeatConfigSnapshot) {
	e.cfgRevision = snapshot.cfg.Revision
	e.cfgDigest = snapshot.digest
	e.cfgKnown = snapshot.exists
	e.cfgInitialized = true
	if snapshot.exists {
		e.cfgDeleted = false
	}
}

// adoptExternalEditsLocked compares the exact content digest so edits are
// detected even on filesystems with coarse mtimes.
func (e *HeartbeatEngine) adoptExternalEditsLocked() {
	snapshot, err := e.readConfigSnapshot()
	if err != nil {
		// 任务 823 ② fail-open: an invalid external edit is logged and the
		// in-memory task list keeps running unchanged (the scheduler must not
		// drop every task because one hand-edit broke a field type); the
		// health error is recorded so the panel banner stays up until the
		// file reads clean again.
		e.configHealthErr = err.Error()
		log.Printf("[heartbeat] invalid external config: %v (fail-open: keeping %d in-memory task(s))", err, len(e.tasks))
		return
	}
	e.configHealthErr = ""
	if !snapshot.exists {
		// Treat deletion of a previously observed config as authoritative.
		// Retaining the old tasks would execute them on the next tick and recreate
		// the file from stale state.
		if e.cfgInitialized && e.cfgKnown {
			e.tasks = nil
			e.pendingTopics = make(map[string]heartbeatPendingTopic)
			e.cfgDeleted = true
			e.recordConfigSnapshotLocked(snapshot)
		}
		return
	}
	if e.cfgKnown && snapshot.digest == e.cfgDigest {
		return
	}
	e.recordConfigSnapshotLocked(snapshot)
	e.tasks = snapshot.cfg.Tasks
	e.prunePendingTopicsLocked(e.tasks)
}

// sweepLegacyAutopilotGuardTasks removes heartbeat tasks left behind by the
// retired tl-547 autopilot auto-guard (ids prefixed "autoguard-", tl 1211).
// With the mechanism gone such a task would keep firing on its old cadence
// with no sweep, no panel entry and no owner session. Idempotent by
// construction: a task table without the prefix costs one read and no write,
// and the flag flip means later loads produce no side effects. Hand-created
// guard tasks (guard-* and plain random ids) never carry the prefix and are
// never touched.
func (e *HeartbeatEngine) sweepLegacyAutopilotGuardTasks() {
	snapshot, err := e.readConfigSnapshot()
	if err != nil {
		// Same fail-open posture as adoptExternalEditsLocked (任务 823 ②):
		// log and retry on the next tick instead of dropping the sweep.
		log.Printf("[heartbeat] legacy autoguard sweep skipped, config read failed: %v", err)
		return
	}
	legacy := 0
	for _, t := range snapshot.cfg.Tasks {
		if strings.HasPrefix(t.ID, "autoguard-") {
			legacy++
		}
	}
	if legacy == 0 {
		e.mu.Lock()
		e.legacyGuardSwept = true
		e.mu.Unlock()
		return
	}
	if err := e.mutateTasks("legacy-autoguard-sweep", func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error) {
		out := make([]HeartbeatTask, 0, len(tasks))
		changed := false
		for _, t := range tasks {
			if strings.HasPrefix(t.ID, "autoguard-") {
				changed = true
				continue
			}
			out = append(out, t)
		}
		return out, changed, nil
	}); err != nil {
		log.Printf("[heartbeat] legacy autoguard sweep failed: %v (will retry next tick)", err)
		return
	}
	e.mu.Lock()
	e.legacyGuardSwept = true
	e.mu.Unlock()
	slog.Info(fmt.Sprintf("旧版 autopilot 自动守护机制已移除，已清理 %d 个遗留任务", legacy))
}

// saveTasks writes tasks to disk atomically. The default writer tag covers
// test seeding and any caller that has no richer identity; production paths
// pass their own (task 941).
func (e *HeartbeatEngine) saveTasks(tasks []HeartbeatTask) error {
	return e.writeTasks(tasks, heartbeatConfigSnapshot{}, false, "save-tasks")
}

// writeTasks persists the full task list. writer names the caller identity
// (task 941: every config write is stamped with who wrote it — run-state
// merge, panel save, agent upsert, guard sweep, rotation relay — so a
// high-frequency write storm can be attributed from the log instead of a
// snapshot-diff stakeout).
func (e *HeartbeatEngine) writeTasks(tasks []HeartbeatTask, expected heartbeatConfigSnapshot, compare bool, writer string) error {
	if tasks == nil {
		tasks = []HeartbeatTask{}
	}
	path := e.configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := filelock.Acquire(lockCtx, path+".lock")
	if err != nil {
		return err
	}
	defer release()

	current, err := e.readConfigSnapshot()
	if err != nil {
		return err
	}
	// Forward protection: a config written by a future binary carries a
	// schemaVersion this binary does not understand. Refuse to overwrite it
	// with a full-table save instead of silently downgrading the schema.
	if current.exists && current.cfg.SchemaVersion > heartbeatSchemaVersion {
		return fmt.Errorf("heartbeat config schemaVersion %d is newer than this binary supports (%d); upgrade Reasonix before editing", current.cfg.SchemaVersion, heartbeatSchemaVersion)
	}
	if compare && (current.exists != expected.exists || current.digest != expected.digest || current.cfg.Revision != expected.cfg.Revision) {
		return ErrHeartbeatConfigConflict
	}
	// Task 327: re-enabling a task whose run budget is spent starts a fresh
	// budget (重开 = 重置计数). This is the single persistence choke point every
	// editor goes through — the settings panel, the agent tools'
	// heartbeat_task_enable / heartbeat_task_upsert, and the engine's own
	// writes — so the rule has exactly one home. A hand-edit of the JSON
	// bypasses it by design: an external re-enable keeps its counter (clear
	// runsUsed by hand if that is what you mean).
	for i := range tasks {
		if !tasks[i].Enabled || tasks[i].RunsUsed == 0 {
			continue
		}
		if prev := heartbeatTaskByID(current.cfg.Tasks, tasks[i].ID); prev != nil && prev.Enabled {
			continue // unchanged or still-running task: keep the counter
		}
		tasks[i].RunsUsed = 0
	}
	revision := current.cfg.Revision + 1
	if !current.exists {
		revision = 1
	}
	// Keep run history only in the sidecar so older full-table writers cannot
	// drop it from heartbeat-tasks.json. The engine writes that owned state back
	// through writeRunHistorySidecar.
	sidecar := make(map[string][]HeartbeatRun, len(tasks))
	mainTasks := make([]HeartbeatTask, len(tasks))
	for i, t := range tasks {
		mainTasks[i] = t
		mainTasks[i].RunHistory = nil
		if len(t.RunHistory) > 0 {
			sidecar[t.ID] = t.RunHistory
		}
	}
	cfg := heartbeatConfig{SchemaVersion: heartbeatSchemaVersion, Revision: revision, Tasks: mainTasks}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Publish the sidecar first; the main config is the two-file commit marker.
	// If the config write fails, restore the previous sidecar while the config
	// lock remains held.
	previousSidecar, sidecarReadErr := os.ReadFile(e.runHistoryPath())
	previousSidecarExists := sidecarReadErr == nil
	if sidecarReadErr != nil && !os.IsNotExist(sidecarReadErr) {
		return sidecarReadErr
	}
	if previousSidecarExists {
		var persistedSidecar heartbeatRunHistorySidecar
		if err := json.Unmarshal(previousSidecar, &persistedSidecar); err == nil && persistedSidecar.SchemaVersion > heartbeatRunHistorySchemaVersion {
			return fmt.Errorf("heartbeat run-history sidecar schemaVersion %d is newer than this binary supports (%d); upgrade Reasonix before editing", persistedSidecar.SchemaVersion, heartbeatRunHistorySchemaVersion)
		}
	}
	var previousGeneration *heartbeatRunHistoryGeneration
	if current.exists {
		previousGeneration = &heartbeatRunHistoryGeneration{
			Revision: current.cfg.Revision,
			Runs:     heartbeatRunHistoryByTask(current.cfg.Tasks),
		}
	}
	if err := e.writeRunHistorySidecar(revision, sidecar, previousGeneration); err != nil {
		return err
	}
	if err := fileutil.AtomicWriteFile(path, b, 0o644); err != nil {
		var rollbackErr error
		if previousSidecarExists {
			rollbackErr = fileutil.AtomicWriteFile(e.runHistoryPath(), previousSidecar, 0o644)
		} else if removeErr := os.Remove(e.runHistoryPath()); removeErr != nil && !os.IsNotExist(removeErr) {
			rollbackErr = removeErr
		}
		if rollbackErr != nil {
			return fmt.Errorf("write heartbeat config: %w (restore run-history sidecar: %w)", err, rollbackErr)
		}
		return err
	}
	// Task 941: stamp every config write with the writer's identity and a
	// field-level change summary, so a high-frequency write storm (the
	// 20261017 07:1x incident: revision 6439→6460+ in minutes) is attributable
	// from the log alone instead of a snapshot-diff stakeout.
	slog.Info("heartbeat config written",
		"writer", writer,
		"rev_from", current.cfg.Revision,
		"rev_to", revision,
		"changes", heartbeatConfigChangeSummary(current.cfg.Tasks, tasks))
	return nil
}

// heartbeatConfigChangeSummary condenses one full-table save into a log-sized
// string: added (+id) and removed (-id) rows, plus per changed row the JSON
// field names that actually differ. Field names come from a JSON map diff, so
// future task fields are covered without touching this helper.
func heartbeatConfigChangeSummary(before, after []HeartbeatTask) string {
	beforeByID := make(map[string]HeartbeatTask, len(before))
	for _, t := range before {
		beforeByID[t.ID] = t
	}
	afterByID := make(map[string]HeartbeatTask, len(after))
	for _, t := range after {
		afterByID[t.ID] = t
	}
	parts := make([]string, 0, len(after))
	for _, t := range after {
		prev, ok := beforeByID[t.ID]
		if !ok {
			parts = append(parts, "+"+t.ID)
			continue
		}
		if fields := heartbeatTaskChangedFields(prev, t); len(fields) > 0 {
			parts = append(parts, t.ID+":{"+strings.Join(fields, ",")+"}")
		}
	}
	for _, t := range before {
		if _, ok := afterByID[t.ID]; !ok {
			parts = append(parts, "-"+t.ID)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// heartbeatTaskChangedFields lists the JSON field names that differ between
// two versions of one task, derived by unmarshalling both into generic maps —
// a structural diff that stays correct when the struct gains fields.
func heartbeatTaskChangedFields(prev, next HeartbeatTask) []string {
	prevJSON, err1 := json.Marshal(prev)
	nextJSON, err2 := json.Marshal(next)
	if err1 != nil || err2 != nil {
		return nil // cannot diff — the write stamp still lands, just without field detail
	}
	var prevMap, nextMap map[string]any
	if json.Unmarshal(prevJSON, &prevMap) != nil || json.Unmarshal(nextJSON, &nextMap) != nil {
		return nil
	}
	fields := make([]string, 0, 4)
	for key := range nextMap {
		if !reflect.DeepEqual(prevMap[key], nextMap[key]) {
			fields = append(fields, key)
		}
	}
	for key := range prevMap {
		if _, ok := nextMap[key]; !ok {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}

// heartbeatUpdateIDs condenses a run-state update map into a sorted,
// comma-joined id list for log lines (task 941 observability).
func heartbeatUpdateIDs(updates map[string]HeartbeatTask) string {
	ids := make([]string, 0, len(updates))
	for id := range updates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func (e *HeartbeatEngine) mergeRunUpdatesLocked(updates map[string]HeartbeatTask) {
	if len(updates) == 0 {
		return
	}
	// Rebase onto the human/AI-editable disk list before saving. The engine owns
	// only run-state fields; task definitions added, edited, or deleted outside
	// this process remain authoritative.
	for attempt := 1; attempt <= 3; attempt++ {
		expected, err := e.readConfigSnapshot()
		if err != nil {
			log.Printf("[heartbeat] cannot read config before run-state merge: %v", err)
			return
		}
		tasks := expected.cfg.Tasks
		if !expected.exists {
			switch {
			case e.cfgDeleted || (e.cfgInitialized && e.cfgKnown):
				// Observe deletion in this CAS loop too. A run can finish before the
				// next scheduler tick adopts external edits; relying only on tick
				// would let that completion recreate a file the user just removed.
				e.tasks = nil
				e.pendingTopics = make(map[string]heartbeatPendingTopic)
				e.cfgDeleted = true
				e.recordConfigSnapshotLocked(expected)
				return
			case !e.cfgInitialized:
				// Only an engine that has never observed disk may bootstrap from an
				// in-memory list. Once a config existed, deletion is authoritative.
				tasks = append([]HeartbeatTask(nil), e.tasks...)
			}
		}
		mergeHeartbeatRunUpdates(tasks, updates)
		if err := e.writeTasks(tasks, expected, true, "run"); err != nil {
			if errors.Is(err, ErrHeartbeatConfigConflict) {
				// Task 941: an optimistic-lock steal used to retry silently —
				// seven consecutive steals during the 20261017 write storm left
				// no trace in the log. Every conflict retry is now visible.
				slog.Warn("heartbeat run-state merge: config changed concurrently, retrying",
					"attempt", attempt, "tasks", heartbeatUpdateIDs(updates))
				continue
			}
			log.Printf("[heartbeat] run-state merge failed: %v", err)
			return
		}
		latest, err := e.readConfigSnapshot()
		if err != nil {
			log.Printf("[heartbeat] reload after run-state merge: %v", err)
			return
		}
		e.recordConfigSnapshotLocked(latest)
		e.tasks = tasks
		e.prunePendingTopicsLocked(tasks)
		return
	}
	slog.Warn("heartbeat run-state merge lost repeated config races; next tick will retry",
		"tasks", heartbeatUpdateIDs(updates))
}

func mergeHeartbeatRunUpdates(tasks []HeartbeatTask, updates map[string]HeartbeatTask) {
	for i := range tasks {
		update, ok := updates[tasks[i].ID]
		if !ok {
			continue
		}
		// Run state is monotonic. A runtime that lost the cross-process lease may
		// merge a stale snapshot later, but must not roll back the owner's timestamp
		// or fresh-conversation topic.
		newerRun := update.LastRunAt > tasks[i].LastRunAt
		if update.TopicID != "" && (tasks[i].TopicID == "" || newerRun) {
			tasks[i].TopicID = update.TopicID
		}
		if newerRun {
			tasks[i].LastRunAt = update.LastRunAt
		}
		if tasks[i].CreatedAt == 0 && update.CreatedAt != 0 {
			tasks[i].CreatedAt = update.CreatedAt
		}
		// Merge run history: the on-disk list may be a stale snapshot (external
		// edits or a tick that raced), so union by execution timestamp and keep
		// the most recent maxRunHistory entries.
		if len(update.RunHistory) > 0 {
			tasks[i].RunHistory = mergeRunHistory(tasks[i].RunHistory, update.RunHistory)
		}
		// Task 327: the run budget is engine-owned run state. Publishing only
		// timestamps would silently resurrect a task the runtime just disabled
		// for spending maxRuns — the next tick would see it enabled again while
		// the counter still read "spent". Take the higher counter so a stale
		// snapshot cannot hand out extra runs, and honor the runtime's terminal
		// disable against the disk's (authoritative) budget.
		if update.RunsUsed > tasks[i].RunsUsed {
			tasks[i].RunsUsed = update.RunsUsed
		}
		if tasks[i].MaxRuns > 0 && update.RunsUsed >= tasks[i].MaxRuns && !update.Enabled {
			tasks[i].Enabled = false
		}
		// IdleStreak is engine-owned run state: without this merge the streak
		// would reset to whatever the (possibly stale) disk snapshot carried,
		// so task 244 B1's idle-terminate could never count consecutive idle
		// runs. The update is rebuilt from the freshest config on every
		// trigger, so it is the authoritative value.
		tasks[i].IdleStreak = update.IdleStreak
	}
}

// mergeRunHistory unions two run-history lists by execution timestamp (At),
// dedupes, sorts oldest-first and keeps the newest maxRunHistory entries. Used
// by both the update-merge and the disk-protection paths below.
func mergeRunHistory(base, extra []HeartbeatRun) []HeartbeatRun {
	merged := append([]HeartbeatRun(nil), base...)
	seen := make(map[int64]bool, len(merged))
	for _, r := range merged {
		seen[r.At] = true
	}
	for _, r := range extra {
		if !seen[r.At] {
			merged = append(merged, r)
		}
	}
	sort.Slice(merged, func(a, b int) bool { return merged[a].At < merged[b].At })
	if len(merged) > maxRunHistory {
		merged = merged[len(merged)-maxRunHistory:]
	}
	return merged
}

// mergeHeartbeatDiskRunHistory protects engine-owned execution state during a
// frontend full-list save. A stale snapshot must not roll back TopicID or
// LastRunAt, and run history is unioned by timestamp so both snapshots survive.
func mergeHeartbeatDiskRunHistory(submitted, disk []HeartbeatTask) []HeartbeatTask {
	if len(disk) == 0 {
		return submitted
	}
	diskByID := make(map[string]HeartbeatTask, len(disk))
	for _, d := range disk {
		diskByID[d.ID] = d
	}
	out := make([]HeartbeatTask, len(submitted))
	copy(out, submitted)
	for i := range out {
		diskTask, ok := diskByID[out[i].ID]
		if !ok {
			continue
		}
		out[i].TopicID = diskTask.TopicID
		out[i].LastRunAt = diskTask.LastRunAt
		// Task 327: runsUsed is engine-owned like LastRunAt — a stale panel
		// snapshot must not roll the counter back and grant extra runs. The one
		// legitimate reset (re-enable) is applied afterwards in writeTasks.
		if diskTask.RunsUsed > out[i].RunsUsed {
			out[i].RunsUsed = diskTask.RunsUsed
		}
		// Always union by At: once history reaches maxRunHistory, a new disk run
		// replaces the oldest entry without changing length, so length comparison
		// would incorrectly drop the engine's new run.
		out[i].RunHistory = mergeRunHistory(out[i].RunHistory, diskTask.RunHistory)
	}
	return out
}
