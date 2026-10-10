package main

// Task 500 — heartbeat "reuse conversation" rotation (the handoff relay).
//
// Why: a heartbeat task that wakes the same conversation forever (ReuseSession,
// or the legacy fixed-topicId mode) grows that session's event log without
// bound — the measured trigger was a guard task whose session reached 122.6MB
// / 1055 turns in 9 days, amplifying the task-499 decode-path memory cost.
// The user's ruling (2026-10-05): heartbeat conversations MUST keep reusing
// their session; when a session outgrows its thresholds the remedy is one
// handoff relay — the OLD session writes a handoff document first (hard
// precondition, user instruction ④), then a fresh session takes over and the
// old task rows retire. reuseSession:false was explicitly rejected.
//
// How (all engine-side, zero schema change to heartbeat-tasks.json):
//
//	thresholds  <userDir>/heartbeat-rotation.json      — human/AI-editable;
//	              missing file = built-in defaults (eventsMB 128, turns 1000,
//	              ageDays 14 suggest-only, autoRotate on)
//	state       <userDir>/heartbeat-rotation-state.json — engine-owned, the
//	              per-topic rotation state machine (mirrors the run-history
//	              sidecar precedent: engine state lives outside the task file
//	              so an older binary's full-table save cannot drop it)
//	marker      <userDir>/heartbeat-rotation/<topicID>.handoff.done — written
//	              by the OLD session after its handoff document exists; the
//	              engine trusts the marker only when the document is really
//	              there and non-empty
//
// The state machine, per topic (a topic may be bound by several tasks — the
// zcode-parallel day/night pair share one session — so rotation is grouped by
// topic, never by single task):
//
//	(none) ──over threshold──▶ handoff_requested
//	handoff_requested ──a due group run carries the handoff prompt──▶ handoff_pending
//	handoff_pending ──valid marker──▶ rotating ──topic created + rows cloned──▶ done
//	handoff_pending ──attempts exhausted / bad marker──▶ failed (terminal, logged)
//
// Safety valves, each answering a concrete failure mode:
//   - autopilot guard tasks never rotate (task 326 owns their lifecycle; they
//     follow their owner session), goal-mode tasks never rotate (a handoff
//     instruction must not fight a goal loop), fresh-per-run tasks are immune
//     by construction;
//   - manualTaskIds in the threshold config opts a task out of AUTO rotation —
//     suggest-only — so a line whose rotation timing the user owns (the fork
//     dev main line, task 500 ruling 3) is never hijacked mid-flight;
//   - age is deliberately suggest-only: size and turns measure actual harm,
//     age alone does not, so an old-but-tiny weekly session is reported, not
//     rotated;
//   - every auto action writes config through mutateTasks (CAS + revision++),
//     old rows stay in place disabled with a 退役 title — traceable, never
//     deleted;
//   - the old session itself is NEVER archived or trashed by this mechanism:
//     it holds real history; disposal stays a human decision.

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
	"reasonix/internal/store"
)

// ── Threshold config (heartbeat-rotation.json) ──────────────────────────────

const heartbeatRotationSchemaVersion = 1

// heartbeatRotationConfig is the editable half of the mechanism. Zero value
// for a numeric criterion means "criterion off" — an explicit 0 disables that
// check rather than silently falling back, so a typo can never re-enable a
// threshold the user zeroed out.
type heartbeatRotationConfig struct {
	SchemaVersion      int      `json:"schemaVersion,omitempty"`
	Enabled            bool     `json:"enabled"`            // master switch for the whole mechanism
	AutoRotate         bool     `json:"autoRotate"`         // false = detect and log only, never act
	EventsMB           float64  `json:"eventsMB"`           // main event log size threshold (MB)
	Turns              int      `json:"turns"`              // persisted turn counter threshold
	AgeDays            float64  `json:"ageDays"`            // suggest-only age threshold
	MaxHandoffAttempts int      `json:"maxHandoffAttempts"` // handoff prompt submissions before failed
	ManualTaskIDs      []string `json:"manualTaskIds"`      // suggest-only tasks (never auto-rotated)
}

// defaultHeartbeatRotationConfig is what runs when the config file is absent.
// eventsMB 128 / turns 1000 are the user's 2026-10-05 ruling (the values the
// 509 audit script has been reporting against since); ageDays 14 was the
// task-500 proposal, kept at the suggest tier.
func defaultHeartbeatRotationConfig() heartbeatRotationConfig {
	return heartbeatRotationConfig{
		SchemaVersion:      heartbeatRotationSchemaVersion,
		Enabled:            true,
		AutoRotate:         true,
		EventsMB:           128,
		Turns:              1000,
		AgeDays:            14,
		MaxHandoffAttempts: 3,
	}
}

func heartbeatRotationConfigPath() string {
	dir := config.MemoryUserDir()
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "heartbeat-rotation.json")
}

// loadHeartbeatRotationConfig reads the editable thresholds. A missing file is
// the normal first-run shape (defaults); a corrupt file falls back to defaults
// with a log line — threshold rotation must never take the scheduler down.
func loadHeartbeatRotationConfig() heartbeatRotationConfig {
	def := defaultHeartbeatRotationConfig()
	b, err := readFileUTF8(heartbeatRotationConfigPath())
	if err != nil {
		return def // missing (or unreadable) → defaults
	}
	var cfg heartbeatRotationConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Printf("[heartbeat-rotation] invalid %s: %v — using defaults", heartbeatRotationConfigPath(), err)
		return def
	}
	// Absent booleans unmarshal as false, but "file present yet flags missing"
	// is indistinguishable from an explicit false — an explicit false is a
	// legitimate off switch, so it is honored as written. Numeric zeros mean
	// "criterion off" per the struct contract; maxHandoffAttempts<=0 falls
	// back to the default because zero attempts would make the relay hang in
	// handoff_requested forever.
	if cfg.MaxHandoffAttempts <= 0 {
		cfg.MaxHandoffAttempts = def.MaxHandoffAttempts
	}
	return cfg
}

func (c heartbeatRotationConfig) manualTaskSet() map[string]bool {
	out := make(map[string]bool, len(c.ManualTaskIDs))
	for _, id := range c.ManualTaskIDs {
		out[strings.TrimSpace(id)] = true
	}
	return out
}

// ── Rotation state (engine-owned sidecar) ───────────────────────────────────

// Rotation phases. Exported names stay unexported-string simple: the state
// file is engine-owned, but keeping it legible helps the audit script and the
// occasional hand inspection.
const (
	rotationPhaseRequested = "handoff_requested" // waiting for a due group run to carry the handoff prompt
	rotationPhasePending   = "handoff_pending"   // handoff prompt submitted; waiting for the marker
	rotationPhaseRotating  = "rotating"          // marker accepted; topic/rows work in progress (retry-safe)
	rotationPhaseDone      = "done"              // successor rows live, old rows retired
	rotationPhaseFailed    = "failed"            // terminal: attempts exhausted or marker lied
)

type heartbeatTopicRotation struct {
	Phase        string   `json:"phase"`
	TopicID      string   `json:"topicId"`
	TaskIDs      []string `json:"taskIds"`     // the auto-rotation group (manual tasks excluded)
	Attempts     int      `json:"attempts"`    // handoff prompts actually submitted
	StartedAt    int64    `json:"startedAt"`   // unix millis
	UpdatedAt    int64    `json:"updatedAt"`   // unix millis
	HandoffPath  string   `json:"handoffPath"` // where the old session was told to write
	MarkerPath   string   `json:"markerPath"`  // completion marker the old session writes
	Reason       string   `json:"reason"`      // human-readable threshold verdict
	NewTopicID   string   `json:"newTopicId,omitempty"`
	SuccessorIDs []string `json:"successorIds,omitempty"`
	NextRetryAt  int64    `json:"nextRetryAt,omitempty"` // backoff for rotating-phase retries
}

type heartbeatRotationState struct {
	SchemaVersion int                                `json:"schemaVersion"`
	LastFullScan  int64                              `json:"lastFullScan,omitempty"` // unix millis
	Rotations     map[string]*heartbeatTopicRotation `json:"rotations"`              // key: topicID
	Bootstrap     map[string]string                  `json:"bootstrap,omitempty"`    // successor taskID -> handoff path (pending first wake)
}

func (e *HeartbeatEngine) rotationStatePath() string {
	dir := config.MemoryUserDir()
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "heartbeat-rotation-state.json")
}

func (e *HeartbeatEngine) loadRotationStateLocked() *heartbeatRotationState {
	if e.rotationState == nil {
		state := &heartbeatRotationState{
			SchemaVersion: heartbeatRotationSchemaVersion,
			Rotations:     map[string]*heartbeatTopicRotation{},
			Bootstrap:     map[string]string{},
		}
		b, err := readFileUTF8(e.rotationStatePath())
		if err == nil {
			var disk heartbeatRotationState
			if err := json.Unmarshal(b, &disk); err != nil {
				log.Printf("[heartbeat-rotation] invalid state %s: %v — starting empty", e.rotationStatePath(), err)
			} else {
				if disk.Rotations != nil {
					state.Rotations = disk.Rotations
				}
				if disk.Bootstrap != nil {
					state.Bootstrap = disk.Bootstrap
				}
				state.LastFullScan = disk.LastFullScan
			}
		}
		e.rotationState = state
	}
	return e.rotationState
}

func (e *HeartbeatEngine) saveRotationStateLocked() {
	state := e.loadRotationStateLocked()
	state.SchemaVersion = heartbeatRotationSchemaVersion
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("[heartbeat-rotation] marshal state: %v", err)
		return
	}
	if err := fileutil.AtomicWriteFile(e.rotationStatePath(), b, 0o644); err != nil {
		log.Printf("[heartbeat-rotation] write state: %v", err)
	}
}

// rotationMarkerDir is where old-session handoff markers land. The directory
// is created lazily by whoever hands out a marker path first.
func rotationMarkerDir() string {
	dir := config.MemoryUserDir()
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "heartbeat-rotation")
}

func rotationMarkerPath(topicID string) string {
	return filepath.Join(rotationMarkerDir(), strings.TrimSpace(topicID)+".handoff.done")
}

// ── Eligibility, grouping, verdicts ─────────────────────────────────────────

// heartbeatRotationEligibleTask reports whether a task's bound conversation is
// one this mechanism may rotate. Fresh-per-run tasks cannot accumulate; guard
// tasks are task 326's property; goal mode would fight a handoff instruction;
// a disabled task never wakes anything.
func heartbeatRotationEligibleTask(t HeartbeatTask) bool {
	if !t.Enabled || t.GoalMode || isAutopilotGuardTask(t) {
		return false
	}
	return !heartbeatFreshConversationMode(t)
}

// heartbeatTopicAge derives a topic's age from the timestamp embedded in the
// topic id (topic_YYYYMMDD-HHMMSS_…). Parse failure → ok=false and the caller
// treats age as unknown (never a suggestion).
func heartbeatTopicAge(topicID string, now time.Time) (time.Duration, bool) {
	m := heartbeatTopicIDTimeRe.FindStringSubmatch(topicID)
	if m == nil {
		return 0, false
	}
	created, err := time.ParseInLocation("20060102-150405", m[1], time.Local)
	if err != nil {
		return 0, false
	}
	age := now.Sub(created)
	if age < 0 {
		age = 0
	}
	return age, true
}

var heartbeatTopicIDTimeRe = regexp.MustCompile(`topic_(\d{8}-\d{6})_`)

// heartbeatSessionStats is one topic's growth footprint. The measurement
// convention mirrors the 508/509 audit exactly (main session events file size,
// listing-meta turn counter) so the engine and the audit script can reconcile.
type heartbeatSessionStats struct {
	TopicID     string
	EventsBytes int64
	Turns       int
	Age         time.Duration
	AgeKnown    bool
	SessionPath string
}

// rotationStatsResolver resolves a topic to its stats. A var-shaped func field
// on the engine (nil = real filesystem resolver) keeps the state-machine tests
// off the disk.
type rotationStatsResolver func(topicID string) (heartbeatSessionStats, bool)

// heartbeatSessionStatsForTopic resolves via the topic-session index. Only the
// newest MAIN session of the topic is measured — guardian sub-sessions own a
// separate lifecycle and the audit convention counts the main log.
func (e *HeartbeatEngine) heartbeatSessionStatsForTopic(topicID string) (heartbeatSessionStats, bool) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" || e.app == nil {
		return heartbeatSessionStats{}, false
	}
	var newest *topicSessionMatch
	for _, dir := range e.app.knownSessionDirs() {
		index, err := topicSessionIndexForDir(dir)
		if err != nil {
			continue
		}
		for i := range index.byTopic[topicID] {
			match := index.byTopic[topicID][i]
			if strings.Contains(strings.ToLower(filepath.Base(match.path)), ".guardian.") {
				continue // sub-session, separate lifecycle
			}
			if newest == nil || match.updatedAt.After(newest.updatedAt) {
				cp := match
				newest = &cp
			}
		}
	}
	if newest == nil {
		return heartbeatSessionStats{}, false
	}
	stats := heartbeatSessionStats{TopicID: topicID, SessionPath: newest.path}
	// Turns live in the listing sidecar (<session>.jsonl.meta); a missing or
	// unreadable sidecar means "unknown", which only silences the turn
	// criterion — the size criterion still applies.
	if b, err := readFileUTF8(newest.path + ".meta"); err == nil {
		var meta struct {
			Turns int `json:"turns"`
		}
		if json.Unmarshal(b, &meta) == nil {
			stats.Turns = meta.Turns
		}
	}
	if st, err := os.Stat(store.SessionEventLog(newest.path)); err == nil {
		stats.EventsBytes = st.Size()
	}
	// Age prefers the topic id timestamp; the session file birth date is not
	// portable across filesystems.
	if age, ok := heartbeatTopicAge(topicID, time.Now()); ok {
		stats.Age, stats.AgeKnown = age, true
	}
	return stats, true
}

// heartbeatRotationVerdict folds stats against the config. auto=true means
// "rotate now" (size or turns — measured harm); suggest=true means "report
// only" (age hygiene, or an auto-eligible topic while autoRotate is off).
func heartbeatRotationVerdict(stats heartbeatSessionStats, cfg heartbeatRotationConfig) (auto bool, suggest bool, reason string) {
	if stats.EventsBytes <= 0 && stats.Turns <= 0 && !stats.AgeKnown {
		return false, false, "" // nothing measurable: never act on a guess
	}
	mb := float64(stats.EventsBytes) / (1024.0 * 1024.0)
	if cfg.EventsMB > 0 && mb >= cfg.EventsMB {
		return true, true, fmt.Sprintf("events %.1fMB >= %.0fMB", mb, cfg.EventsMB)
	}
	if cfg.Turns > 0 && stats.Turns >= cfg.Turns {
		return true, true, fmt.Sprintf("turns %d >= %d", stats.Turns, cfg.Turns)
	}
	if cfg.AgeDays > 0 && stats.AgeKnown && stats.Age >= time.Duration(cfg.AgeDays*24*float64(time.Hour)) {
		return false, true, fmt.Sprintf("age %.0fd >= %.0fd（建议级）", stats.Age.Hours()/24, cfg.AgeDays)
	}
	return false, false, ""
}

// heartbeatRotationGroup partitions the eligible tasks by bound topic. The
// returned map is topic → group member ids sorted for determinism; topics with
// no eligible member are absent.
func heartbeatRotationGroup(tasks []HeartbeatTask) map[string][]string {
	byTopic := map[string][]string{}
	for _, t := range tasks {
		if !heartbeatRotationEligibleTask(t) {
			continue
		}
		topic := strings.TrimSpace(t.TopicID)
		if topic == "" {
			continue // first run creates the topic — nothing accumulated yet
		}
		byTopic[topic] = append(byTopic[topic], t.ID)
	}
	for topic := range byTopic {
		sort.Strings(byTopic[topic])
	}
	return byTopic
}

// ── Prompts ─────────────────────────────────────────────────────────────────

// heartbeatHandoffPrompt is the ONE instruction a rotation wake delivers to the
// old session. It enforces the user's hard precondition (instruction ④: the
// old conversation writes the handoff) and nothing else — a rotation wake must
// not let a scheduler start unrelated work.
func heartbeatHandoffPrompt(rot heartbeatTopicRotation) string {
	var b strings.Builder
	b.WriteString("【心跳会话轮换 · 交接】本会话是心跳任务复用的对话，已触发轮换阈值（" + rot.Reason + "）。")
	b.WriteString("按轮换协议，本次唤醒只做「写交接」这一件事，完成即停止：\n\n")
	b.WriteString("1. 把本会话的职责交接写到工作区 docs/handoff/ 目录（不存在则先创建），文件名 heartbeat-<主题>-<今天YYYYMMDD>.md，包含五节：\n")
	b.WriteString("   ① 职责与协作协议（总线优先 / 文件板等约定）；\n")
	b.WriteString("   ② 当前目标与在办事项；\n")
	b.WriteString("   ③ 关键决策与坑；\n")
	b.WriteString("   ④ 与其他会话/工作线的接口（谁依赖本会话、本会话依赖谁）；\n")
	b.WriteString("   ⑤ 未完成事项与下一步。\n")
	b.WriteString("2. 交接文件写好后，创建标记文件 " + rot.MarkerPath + " ，内容只写交接文件的绝对路径（一行）。\n\n")
	b.WriteString("除此之外不要执行任何其他操作、不要开启新工作、不要修改任何配置。新会话会读取交接文件接手职责；本会话随后退役（保留归档，不再被唤醒）。")
	return b.String()
}

// heartbeatBootstrapPrompt is the one-shot first wake of a successor session:
// read the handoff, then do the task's normal job.
func heartbeatBootstrapPrompt(handoffPath, taskPrompt string) string {
	var b strings.Builder
	b.WriteString("【轮换接手】本会话是从旧会话轮换接任的心跳会话。本次唤醒先读交接文件 " + handoffPath)
	b.WriteString(" ，按其中「职责与协作协议 / 当前目标 / 未完成事项」接手上下文，然后继续执行下面的本职任务：\n\n")
	b.WriteString(taskPrompt)
	return b.String()
}

// ── The engine hooks ────────────────────────────────────────────────────────

type rotationPromptKind int

const (
	rotationPromptNone rotationPromptKind = iota
	rotationPromptHandoff
	rotationPromptBootstrap
)

// rotationPromptOverride decides whether THIS run should carry a rotation
// prompt instead of the task's own. Called with e.mu NOT held; internal state
// transitions take e.mu. A handoff carry is possible in BOTH armed phases:
// requested (first carry, CAS'd to pending so two group tasks due in the same
// tick cannot both deliver) and pending (the re-carry: the previous wake's
// handoff instruction produced no marker yet, so the next due run reminds the
// old session — attempts counts these, and the cap eventually fails the
// relay).
func (e *HeartbeatEngine) rotationPromptOverride(t HeartbeatTask) (string, rotationPromptKind) {
	if e == nil || !heartbeatRotationEligibleTask(t) {
		return "", rotationPromptNone
	}
	cfg := loadHeartbeatRotationConfig()
	if !cfg.Enabled {
		return "", rotationPromptNone
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.loadRotationStateLocked()
	// Bootstrap first: a successor's first wake must read the handoff before
	// its normal duty.
	if path, ok := state.Bootstrap[t.ID]; ok {
		return heartbeatBootstrapPrompt(path, t.Prompt), rotationPromptBootstrap
	}
	rot, ok := state.Rotations[strings.TrimSpace(t.TopicID)]
	if !ok || (rot.Phase != rotationPhaseRequested && rot.Phase != rotationPhasePending) {
		return "", rotationPromptNone
	}
	// Only a member of the recorded auto-group may carry the handoff; manual
	// tasks were excluded at detection and stay excluded here.
	member := false
	for _, id := range rot.TaskIDs {
		if id == t.ID {
			member = true
			break
		}
	}
	if !member {
		return "", rotationPromptNone
	}
	rot.Attempts++
	rot.Phase = rotationPhasePending
	rot.UpdatedAt = time.Now().UnixMilli()
	e.saveRotationStateLocked()
	slog.Info("heartbeat rotation: handoff prompt carried to old session",
		"task", t.ID, "topic", rot.TopicID, "attempt", rot.Attempts, "reason", rot.Reason)
	return heartbeatHandoffPrompt(*rot), rotationPromptHandoff
}

// rotationPromptSubmitFailed reverts a carry whose submit was rejected, so the
// next due run can try again instead of the relay hanging in handoff_pending
// with nothing in flight.
func (e *HeartbeatEngine) rotationPromptSubmitFailed(t HeartbeatTask, kind rotationPromptKind) {
	if kind != rotationPromptHandoff {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.loadRotationStateLocked()
	rot, ok := state.Rotations[strings.TrimSpace(t.TopicID)]
	if !ok || rot.Phase != rotationPhasePending {
		return
	}
	rot.Attempts--
	if rot.Attempts < 0 {
		rot.Attempts = 0
	}
	rot.Phase = rotationPhaseRequested
	rot.UpdatedAt = time.Now().UnixMilli()
	e.saveRotationStateLocked()
}

// rotationBootstrapDelivered clears the one-shot bootstrap after its prompt
// was actually submitted.
func (e *HeartbeatEngine) rotationBootstrapDelivered(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.loadRotationStateLocked()
	if _, ok := state.Bootstrap[taskID]; !ok {
		return
	}
	delete(state.Bootstrap, taskID)
	e.saveRotationStateLocked()
}

// heartbeatRotationScanInterval throttles the filesystem scan; the marker
// checks for in-flight rotations run every tick regardless (one stat each).
var heartbeatRotationScanInterval = 30 * time.Minute

// heartbeatRotationRetryBackoff spaces out rotating-phase retries (CreateTopic
// or config write failing must not spam every 30s tick).
var heartbeatRotationRetryBackoff = 10 * time.Minute

// rotationDoneTTL retires terminal bookkeeping so the state file cannot grow
// forever.
const rotationDoneTTL = 30 * 24 * time.Hour

// reconcileRotationsCheap is the tick hook. Cheap gate first (config + an
// in-memory eligible scan), then two phases: advance in-flight rotations
// (marker checks, finishing work) and, throttled, scan for new over-threshold
// topics. Never holds e.mu across I/O-heavy calls or across mutateTasks.
func (e *HeartbeatEngine) reconcileRotationsCheap() {
	if e == nil || e.app == nil {
		return
	}
	cfg := loadHeartbeatRotationConfig()
	if !cfg.Enabled {
		return
	}
	e.mu.Lock()
	tasks := append([]HeartbeatTask(nil), e.tasks...)
	e.mu.Unlock()

	eligible := make([]HeartbeatTask, 0, len(tasks))
	for _, t := range tasks {
		if heartbeatRotationEligibleTask(t) {
			eligible = append(eligible, t)
		}
	}
	if len(eligible) == 0 {
		return
	}
	e.advanceInFlightRotations(cfg)
	e.scanForRotationCandidates(eligible, cfg)
}

// advanceInFlightRotations moves pending → rotating → done and enforces the
// attempts cap. Runs every tick; per entry the cost is one marker stat.
func (e *HeartbeatEngine) advanceInFlightRotations(cfg heartbeatRotationConfig) {
	now := time.Now()
	e.mu.Lock()
	state := e.loadRotationStateLocked()
	var inFlight []*heartbeatTopicRotation
	for _, rot := range state.Rotations {
		switch rot.Phase {
		case rotationPhasePending, rotationPhaseRotating:
			inFlight = append(inFlight, rot)
		}
	}
	e.mu.Unlock()

	for _, rot := range inFlight {
		if rot.Phase == rotationPhasePending {
			if !rotationMarkerValid(rot) {
				// Fail only when the cap is hit AND the last carry had time to
				// land (a slow session writing its marker seconds after this
				// check must not be failed on a technicality).
				lastCarryAge := now.UnixMilli() - rot.UpdatedAt
				if rot.Attempts >= cfg.MaxHandoffAttempts && lastCarryAge > heartbeatRotationGraceWindow.Milliseconds() {
					e.mu.Lock()
					rot.Phase = rotationPhaseFailed
					rot.UpdatedAt = now.UnixMilli()
					e.saveRotationStateLocked()
					e.mu.Unlock()
					slog.Warn("heartbeat rotation: handoff attempts exhausted — giving up, manual follow-up needed",
						"topic", rot.TopicID, "attempts", rot.Attempts, "reason", rot.Reason)
				}
				continue // marker not there yet (or invalid): keep waiting
			}
			e.mu.Lock()
			rot.Phase = rotationPhaseRotating
			rot.UpdatedAt = now.UnixMilli()
			e.saveRotationStateLocked()
			e.mu.Unlock()
		}
		if rot.Phase == rotationPhaseRotating {
			if rot.NextRetryAt > now.UnixMilli() {
				continue
			}
			if err := e.finishTopicRotation(rot); err != nil {
				e.mu.Lock()
				rot.NextRetryAt = now.Add(heartbeatRotationRetryBackoff).UnixMilli()
				e.saveRotationStateLocked()
				e.mu.Unlock()
				log.Printf("[heartbeat-rotation] rotate topic %s failed (retry after backoff): %v", rot.TopicID, err)
			}
		}
	}
}

// heartbeatRotationGraceWindow keeps a slow-but-honest old session from being
// failed between its marker write and the engine's next tick.
var heartbeatRotationGraceWindow = 10 * time.Minute

// rotationMarkerValid trusts a marker only when both the marker and the
// handoff document it names really exist and are non-empty.
func rotationMarkerValid(rot *heartbeatTopicRotation) bool {
	b, err := readFileUTF8(rot.MarkerPath)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return false
	}
	handoffPath := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if handoffPath == "" {
		handoffPath = rot.HandoffPath
	}
	st, err := os.Stat(handoffPath)
	return err == nil && st.Size() > 0
}

// scanForRotationCandidates finds topics over threshold and opens a rotation
// for each. Throttled to one full scan per heartbeatRotationScanInterval; age
// suggestions are log-only by design.
func (e *HeartbeatEngine) scanForRotationCandidates(eligible []HeartbeatTask, cfg heartbeatRotationConfig) {
	now := time.Now()
	e.mu.Lock()
	state := e.loadRotationStateLocked()
	if state.LastFullScan != 0 && now.UnixMilli()-state.LastFullScan < heartbeatRotationScanInterval.Milliseconds() {
		e.mu.Unlock()
		return
	}
	state.LastFullScan = now.UnixMilli()
	e.saveRotationStateLocked()
	e.mu.Unlock()

	byTopic := heartbeatRotationGroup(eligible)
	manual := cfg.manualTaskSet()
	for topic, ids := range byTopic {
		e.mu.Lock()
		_, active := state.Rotations[topic]
		e.mu.Unlock()
		if active {
			continue
		}
		// Split the group: manual members never auto-rotate.
		autoIDs := make([]string, 0, len(ids))
		suggestIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			if manual[id] {
				suggestIDs = append(suggestIDs, id)
			} else {
				autoIDs = append(autoIDs, id)
			}
		}
		stats, ok := e.resolveRotationStats(topic)
		if !ok {
			continue
		}
		auto, suggest, reason := heartbeatRotationVerdict(stats, cfg)
		if auto && len(autoIDs) > 0 && cfg.AutoRotate {
			rot := &heartbeatTopicRotation{
				Phase:       rotationPhaseRequested,
				TopicID:     topic,
				TaskIDs:     autoIDs,
				StartedAt:   now.UnixMilli(),
				UpdatedAt:   now.UnixMilli(),
				MarkerPath:  rotationMarkerPath(topic),
				Reason:      reason,
				HandoffPath: rotationHandoffDocSuggestion(),
			}
			e.mu.Lock()
			state := e.loadRotationStateLocked()
			if _, exists := state.Rotations[topic]; !exists {
				state.Rotations[topic] = rot
				e.saveRotationStateLocked()
				slog.Info("heartbeat rotation: threshold hit — handoff relay armed",
					"topic", topic, "tasks", strings.Join(autoIDs, ","), "reason", reason)
			}
			e.mu.Unlock()
			continue
		}
		if suggest {
			who := append([]string(nil), autoIDs...)
			who = append(who, suggestIDs...)
			slog.Info("heartbeat rotation: suggestion (no auto action) — run the audit script for the full report",
				"topic", topic, "tasks", strings.Join(who, ","), "reason", reason)
		}
	}
	e.pruneFinishedRotations(now)
}

// resolveRotationStats goes through the injectable resolver.
func (e *HeartbeatEngine) resolveRotationStats(topic string) (heartbeatSessionStats, bool) {
	if e.rotationStats != nil {
		return e.rotationStats(topic)
	}
	return e.heartbeatSessionStatsForTopic(topic)
}

// rotationHandoffDocSuggestion is the default handoff path written into the
// rotation entry (the prompt names the docs/handoff/ convention; the entry
// records the workspace-root-shaped default for the audit script).
func rotationHandoffDocSuggestion() string {
	return filepath.Join("docs", "handoff", "heartbeat-<主题>-"+time.Now().Format("20060102")+".md")
}

// pruneFinishedRotations drops terminal entries older than rotationDoneTTL.
func (e *HeartbeatEngine) pruneFinishedRotations(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.loadRotationStateLocked()
	pruned := false
	for topic, rot := range state.Rotations {
		if rot.Phase != rotationPhaseDone && rot.Phase != rotationPhaseFailed {
			continue
		}
		if now.UnixMilli()-rot.UpdatedAt > rotationDoneTTL.Milliseconds() {
			delete(state.Rotations, topic)
			pruned = true
		}
	}
	if len(state.Rotations) > 100 { // hard cap even if entries keep refreshing
		topics := make([]string, 0, len(state.Rotations))
		for topic, rot := range state.Rotations {
			if rot.Phase == rotationPhaseDone || rot.Phase == rotationPhaseFailed {
				topics = append(topics, topic)
			}
		}
		sort.Strings(topics)
		for _, topic := range topics {
			if len(state.Rotations) <= 100 {
				break
			}
			delete(state.Rotations, topic)
			pruned = true
		}
	}
	if pruned {
		e.saveRotationStateLocked()
	}
}

// ── The rotation executor ───────────────────────────────────────────────────

var heartbeatRotationSuffixRe = regexp.MustCompile(`-rot\d{8}(-\d+)?$`)
var heartbeatRetiredMarkRe = regexp.MustCompile(`［已退役 \d{8} → \S+ 接任］$`)
var heartbeatRotateTitleRe = regexp.MustCompile(`（轮换 \d{8} 接任）$`)

// heartbeatRotationSuccessorID derives the successor id: previous -rot<date>
// suffixes are stripped so one duty line keeps a stable canonical id, and a
// same-day second rotation gets -2, -3, …
func heartbeatRotationSuccessorID(oldID string, existing map[string]bool, now time.Time) string {
	base := heartbeatRotationSuffixRe.ReplaceAllString(strings.TrimSpace(oldID), "")
	if base == "" {
		base = strings.TrimSpace(oldID)
	}
	id := fmt.Sprintf("%s-rot%s", base, now.Format("20060102"))
	for n := 2; existing[id]; n++ {
		id = fmt.Sprintf("%s-rot%s-%d", base, now.Format("20060102"), n)
	}
	return id
}

// finishTopicRotation performs the config-side relay: one new topic for the
// whole group, one successor row per group task (clone, new topic, reuse
// session, fresh run state), old rows disabled in place with a 退役 title.
// Safe to retry: the topic is created once and recorded in the state entry
// before any row is written.
func (e *HeartbeatEngine) finishTopicRotation(rot *heartbeatTopicRotation) error {
	now := time.Now()
	e.mu.Lock()
	tasks := append([]HeartbeatTask(nil), e.tasks...)
	handoffPath := rot.HandoffPath
	markerPath := rot.MarkerPath
	newTopicID := rot.NewTopicID
	e.mu.Unlock()

	// Keep only members that still exist; a task deleted mid-rotation must not
	// resurrect as a successor.
	present := map[string]HeartbeatTask{}
	var group []HeartbeatTask
	for _, id := range rot.TaskIDs {
		for _, t := range tasks {
			if t.ID == id {
				present[id] = t
				group = append(group, t)
				break
			}
		}
	}
	if len(group) == 0 {
		// Everyone vanished: the rotation has nothing to carry — retire it.
		e.mu.Lock()
		rot.Phase = rotationPhaseFailed
		rot.UpdatedAt = now.UnixMilli()
		e.saveRotationStateLocked()
		e.mu.Unlock()
		slog.Warn("heartbeat rotation: all group tasks deleted mid-rotation — marked failed",
			"topic", rot.TopicID)
		return nil
	}

	// 1) The successor conversation (once).
	if newTopicID == "" {
		first := group[0]
		scope, workspaceRoot := heartbeatRunScope(first.Scope, first.WorkspaceRoot)
		meta, err := e.app.CreateTopic(scope, workspaceRoot, "Heartbeat: "+rotationSuccessorTitle(first.Title, now))
		if err != nil {
			return fmt.Errorf("create successor topic: %w", err)
		}
		newTopicID = meta.ID
		e.mu.Lock()
		rot.NewTopicID = newTopicID
		e.saveRotationStateLocked()
		e.mu.Unlock()
		// Stamp the heartbeat origin like resolveHeartbeatTopic does for
		// self-created topics, so sidebar grouping finds the successor.
		if err := e.app.topicState.markTopicOrigin(workspaceRoot, newTopicID, heartbeatTopicOrigin, group[0].ID); err != nil {
			log.Printf("[heartbeat-rotation] markTopicOrigin(%q): %v", newTopicID, err)
		}
	}

	// 2) The row relay (one CAS write: add successors, retire originals).
	succIDs := map[string]bool{}
	err := e.mutateTasks(func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error) {
		changed := false
		existing := make(map[string]bool, len(tasks))
		for _, t := range tasks {
			existing[t.ID] = true
		}
		var out []HeartbeatTask
		for _, t := range tasks {
			if _, isRotating := present[t.ID]; isRotating {
				succID := heartbeatRotationSuccessorID(t.ID, existing, now)
				succ := t
				succ.ID = succID
				succ.Title = rotationSuccessorTitle(t.Title, now)
				succ.TopicID = newTopicID
				succ.ReuseSession = true // the ruling: heartbeat conversations keep reusing sessions
				succ.Enabled = true
				succ.CreatedAt = now.UnixMilli()
				succ.LastRunAt = 0
				succ.RunsUsed = 0
				succ.IdleStreak = 0
				succ.RunHistory = nil
				succIDs[succID] = true
				// The original stays in place where it stood, disabled and
				// marked; the successor follows it.
				retired := t
				retired.Enabled = false
				retired.Title = retiredTitle(t.Title, succID, now)
				out = append(out, retired, succ)
				changed = true
				existing[succID] = true
				continue
			}
			out = append(out, t)
		}
		return out, changed, nil
	})
	if err != nil {
		return fmt.Errorf("relay task rows: %w", err)
	}

	// 3) Bookkeeping: bootstrap one-shots for the successors, terminal state,
	// marker cleanup. The marker is removed only after the rows are in — a
	// crash before this point replays finishTopicRotation from the recorded
	// NewTopicID instead of creating a second successor conversation.
	e.mu.Lock()
	state := e.loadRotationStateLocked()
	for id := range succIDs {
		if _, exists := state.Bootstrap[id]; !exists {
			state.Bootstrap[id] = handoffPath
		}
	}
	rot.SuccessorIDs = make([]string, 0, len(succIDs))
	for id := range succIDs {
		rot.SuccessorIDs = append(rot.SuccessorIDs, id)
	}
	sort.Strings(rot.SuccessorIDs)
	rot.Phase = rotationPhaseDone
	rot.UpdatedAt = now.UnixMilli()
	e.saveRotationStateLocked()
	e.mu.Unlock()
	if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		log.Printf("[heartbeat-rotation] remove marker %s: %v", markerPath, err)
	}
	slog.Info("heartbeat rotation: relay complete",
		"topic", rot.TopicID, "new_topic", newTopicID,
		"successors", strings.Join(rot.SuccessorIDs, ","), "reason", rot.Reason)
	return nil
}

// rotationSuccessorTitle refreshes the trailing （轮换 <date> 接任） marker so
// the panel always shows the latest relay date, and strips a stray retired
// mark if a hand-edit left one on a live row.
func rotationSuccessorTitle(title string, now time.Time) string {
	title = heartbeatRetiredMarkRe.ReplaceAllString(title, "")
	title = heartbeatRotateTitleRe.ReplaceAllString(title, "")
	return strings.TrimRight(title, " ") + "（轮换 " + now.Format("20060102") + " 接任）"
}

// retiredTitle marks a retired row, matching the manual-rotation convention
// the 500 script writes (［已退役 YYYYMMDD → <id> 接任］).
func retiredTitle(title, successorID string, now time.Time) string {
	title = heartbeatRetiredMarkRe.ReplaceAllString(title, "")
	return strings.TrimRight(title, " ") + "［已退役 " + now.Format("20060102") + " → " + successorID + " 接任］"
}
