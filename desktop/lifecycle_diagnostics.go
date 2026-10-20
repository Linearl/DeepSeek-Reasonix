package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
	"reasonix/internal/repair"
)

const (
	desktopLifecycleSchemaVersion = 2
	desktopLifecycleRetention     = 30 * 24 * time.Hour
	maxDesktopLifecycleRecords    = 20
	desktopLifecycleReadRetries   = 12
	desktopLifecycleReadBackoff   = 20 * time.Millisecond
	// Task 1002: the consume-pass stamp (last-consume-pass.json) records when
	// the most recent boot scanned this directory for dead-process evidence.
	// Its schema version is deliberately NOT desktopLifecycleSchemaVersion:
	// consumePrevious's future-format preservation then keeps the file
	// verbatim instead of consuming it as a lifecycle record (same treatment
	// as the launcher's last-abnormal-exit.json).
	lifecycleConsumePassFile   = "last-consume-pass.json"
	lifecycleConsumePassSchema = 1
)

type desktopLifecycleState struct {
	SchemaVersion int    `json:"schemaVersion"`
	PID           int    `json:"pid"`
	RunID         string `json:"runId"`
	Version       string `json:"version,omitempty"`
	Channel       string `json:"channel,omitempty"`
	Phase         string `json:"phase"`
	StartedAt     string `json:"startedAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type desktopLifecycleObservation struct {
	Version   string
	Channel   string
	Phase     string
	StartedAt string
	UpdatedAt string
	// Task 736 (issue #39): attribution evidence. PID identifies the dead
	// desktop; DetectedAt is the moment this instance proved the process dead
	// (death-time upper bound). DeathAt/ExitCode are filled from the
	// launcher's consume-once record when its PID matches — the launcher is
	// the only witness of the actual death moment.
	PID        int
	DetectedAt string
	DeathAt    string
	ExitCode   int
}

type desktopLifecycleRuntime struct {
	previousRun  repair.PreviousRunObservation
	previousRuns []desktopLifecycleObservation
	tracker      *desktopLifecycleTracker
	// noiseGate (task 377): when on, recordPreviousRunDiagnostics skips the
	// crash-pending report for clean-shutdown phases (shutting_down/healthy).
	// Default off — every dead-process record keeps reporting exactly as before.
	noiseGate bool
	// priorPassAt (task 1002) is when the PREVIOUS boot's consume pass scanned
	// the lifecycle directory, read from the on-disk stamp before this boot
	// overwrites it. recordPreviousRunDiagnostics uses it to distinguish a
	// direct successor's first boot (real "上次运行" crash evidence) from a
	// late claimant: a record whose last phase write predates that pass was
	// already visible to an intermediate boot, which either reported it once
	// or lost the claim race — either way this boot is NOT its successor and
	// must not re-surface it as "last run's crash". Zero (no stamp, unparseable
	// stamp) keeps today's report-always behavior.
	priorPassAt time.Time
}

// lifecycleConsumePassStamp is the on-disk shape of the consume-pass stamp.
type lifecycleConsumePassStamp struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	At            string `json:"at"`
}

// desktopLifecycleDir is the single construction of the per-user lifecycle
// evidence directory (tracker records, launcher death evidence, and the
// consume-pass stamp all live here).
func desktopLifecycleDir(root string) string {
	return filepath.Join(root, "diagnostics", "lifecycle")
}

// lifecycleConsumePassPathForRoot builds the stamp path for an arbitrary
// lifecycle root; the tracker's own method covers its configured dir.
func lifecycleConsumePassPathForRoot(root string) string {
	return filepath.Join(desktopLifecycleDir(root), lifecycleConsumePassFile)
}

type desktopLifecycleTracker struct {
	mu           sync.Mutex
	dir          string
	path         string
	state        desktopLifecycleState
	now          func() time.Time
	processAlive func(int) bool
	updates      chan string
	writerStop   chan struct{}
	writerDone   chan struct{}
	writerOnce   sync.Once
	stopOnce     sync.Once
	writerActive bool
	// priorPassAt (task 1002) is captured by consumePrevious from the stamp
	// file BEFORE this pass writes its own; it is boot-time single-threaded
	// state, so no lock guards it.
	priorPassAt time.Time
}

func newDesktopLifecycleTracker(root, appVersion, appChannel string) *desktopLifecycleTracker {
	now := time.Now().UTC()
	runID := newDesktopLifecycleRunID()
	dir := desktopLifecycleDir(root)
	return &desktopLifecycleTracker{
		dir:  dir,
		path: filepath.Join(dir, strconv.Itoa(os.Getpid())+"-"+runID+".json"),
		state: desktopLifecycleState{
			SchemaVersion: desktopLifecycleSchemaVersion,
			PID:           os.Getpid(),
			RunID:         runID,
			Version:       appVersion,
			Channel:       appChannel,
			Phase:         "starting",
			StartedAt:     now.Format(time.RFC3339Nano),
			UpdatedAt:     now.Format(time.RFC3339Nano),
		},
		now:          func() time.Time { return time.Now().UTC() },
		processAlive: desktopProcessAlive,
		updates:      make(chan string, 1),
		writerStop:   make(chan struct{}),
		writerDone:   make(chan struct{}),
	}
}

func prepareDesktopDiagnostics(app *App) {
	if app == nil || app.remoteWindowTicket != "" || version == "dev" {
		return
	}
	root := config.MemoryUserDir()
	if root == "" {
		return
	}
	diagnosticsDir := filepath.Join(root, "diagnostics")
	if err := os.MkdirAll(diagnosticsDir, 0o700); err != nil {
		return
	}
	release, err := filelock.TryAcquire(filepath.Join(diagnosticsDir, "primary.lock"))
	if err != nil {
		return
	}
	app.diagnosticsOwner = true
	app.diagnosticsOwnerRelease = release

	cfg, err := config.Load()
	if err != nil {
		return
	}
	app.diagnosticsConfigLoaded = true
	if !cfg.DesktopTelemetry() {
		return
	}
	app.diagnosticsTelemetry = true
	tracker := newDesktopLifecycleTracker(root, version, channel)
	if tracker.start() == nil {
		app.lifecycle.tracker = tracker
	}
}

func (a *App) releaseDesktopDiagnosticsOwnership() {
	if a == nil || a.diagnosticsOwnerRelease == nil {
		return
	}
	a.diagnosticsOwnerRelease()
	a.diagnosticsOwnerRelease = nil
	a.diagnosticsOwner = false
	a.diagnosticsConfigLoaded = false
	a.diagnosticsTelemetry = false
}

func initializeLifecycleDiagnostics(app *App) {
	if app == nil || app.remoteWindowTicket != "" {
		return
	}
	// Native WebKit recovery is a reliability mechanism, not telemetry. Always
	// install it; the flag only controls whether sanitized diagnostics upload.
	telemetry := app.diagnosticsOwner && app.diagnosticsConfigLoaded && version != "dev" && app.diagnosticsTelemetry
	installWebKitProcessObserver(app, telemetry)
	if !app.diagnosticsOwner {
		return
	}
	if !app.diagnosticsConfigLoaded || version == "dev" {
		return
	}
	tracker := app.lifecycle.tracker
	if tracker == nil {
		tracker = newDesktopLifecycleTracker(config.MemoryUserDir(), version, channel)
	}
	enabled := app.diagnosticsTelemetry
	legacy := repair.NewStartupTracker("").ObservePreviousRun()
	if enabled {
		app.lifecycle.previousRun = legacy
	}
	app.lifecycle.previousRuns = tracker.consumePrevious(enabled)
	// Task 1002: hand the previous boot's consume-pass time to the reporting
	// path so a late claimant can retire stale residue instead of surfacing it.
	if tracker != nil {
		app.lifecycle.priorPassAt = tracker.priorPassAt
	}
	// Task 377: the noise triage reads its own desktop switch; a load failure
	// leaves the gate off (the default), never silently on.
	if cfg, cfgErr := config.Load(); cfgErr == nil {
		app.lifecycle.noiseGate = cfg.Desktop.ExperimentalLifecycleNoiseGate
	}
	if enabled {
		refreshWebRuntimeContext()
	}
}

func (a *App) markDesktopHealthy() {
	a.startupReady.Store(true)
	a.lifecycle.tracker.markAsync("healthy")
}

func newDesktopLifecycleRunID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 16)
}

func (t *desktopLifecycleTracker) start() error {
	if t == nil || t.path == "" {
		return nil
	}
	if err := t.writeState(); err != nil {
		return err
	}
	t.writerOnce.Do(func() {
		t.mu.Lock()
		t.writerActive = true
		t.mu.Unlock()
		go t.runWriter()
	})
	return nil
}

func (t *desktopLifecycleTracker) runWriter() {
	defer close(t.writerDone)
	for {
		select {
		case phase := <-t.updates:
			t.mark(phase)
		case <-t.writerStop:
			select {
			case phase := <-t.updates:
				t.mark(phase)
			default:
			}
			return
		}
	}
}

// markAsync keeps normal startup and DOM-ready paths free from diagnostic I/O.
// The single-slot queue is last-state-wins because lifecycle phases are
// monotonic and only the newest pending phase is useful.
func (t *desktopLifecycleTracker) markAsync(phase string) {
	if t == nil || strings.TrimSpace(phase) == "" {
		return
	}
	select {
	case <-t.writerStop:
		return
	default:
	}
	select {
	case t.updates <- phase:
		return
	default:
	}
	select {
	case <-t.updates:
	default:
	}
	select {
	case t.updates <- phase:
	default:
	}
}

func (t *desktopLifecycleTracker) stopWriter() {
	if t == nil {
		return
	}
	t.mu.Lock()
	active := t.writerActive
	t.mu.Unlock()
	if !active {
		return
	}
	t.stopOnce.Do(func() { close(t.writerStop) })
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-t.writerDone:
	case <-timer.C:
	}
}

func (t *desktopLifecycleTracker) mark(phase string) {
	if t == nil || t.path == "" || strings.TrimSpace(phase) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Phase = phase
	t.state.UpdatedAt = t.now().Format(time.RFC3339Nano)
	_ = t.writeStateLocked()
}

// phase returns the last recorded phase for the shutdown watchdog's
// wedge-point log (task 272 L1): shutdownBody stamps stage markers so a
// forced exit names the section that blocked.
func (t *desktopLifecycleTracker) phase() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state.Phase
}

func (t *desktopLifecycleTracker) clean() {
	if t == nil || t.path == "" {
		return
	}
	t.stopWriter()
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = os.Remove(t.path)
}

func (t *desktopLifecycleTracker) writeState() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeStateLocked()
}

func (t *desktopLifecycleTracker) writeStateLocked() error {
	body, err := json.Marshal(t.state)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(t.path, body, 0o600)
}

// consumePrevious atomically owns every dead per-process record before
// returning it. When emit is false (telemetry opt-out), records are consumed
// without exposing their contents to the reporting path.
func (t *desktopLifecycleTracker) consumePrevious(emit bool) []desktopLifecycleObservation {
	if t == nil || t.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return nil
	}
	now := t.now()
	// Task 1002: read the PREVIOUS boot's pass stamp before this pass writes
	// its own. The stamp is what lets recordPreviousRunDiagnostics tell a
	// direct successor's first boot (genuine crash evidence) from a late
	// claimant whose record was already visible to an intermediate boot.
	priorPassAt := t.readConsumePassStamp()
	observations := make([]desktopLifecycleObservation, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(t.dir, entry.Name())
		state, readErr := readDesktopLifecycleState(path)
		if readErr != nil {
			if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) > desktopLifecycleRetention {
				_ = os.Remove(path)
			}
			continue
		}
		// A newer Desktop may own a lifecycle schema this version cannot safely
		// interpret. Preserve it verbatim so a downgrade never consumes or prunes
		// future-format evidence.
		if state.SchemaVersion != desktopLifecycleSchemaVersion {
			continue
		}
		if state.PID <= 0 || state.Phase == "" {
			if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) > desktopLifecycleRetention {
				_ = os.Remove(path)
			}
			continue
		}
		if t.processAlive(state.PID) {
			continue
		}
		claimed := path + ".claimed-" + t.state.RunID
		// Windows fails a bare rename with a sharing violation while any other
		// instance still holds the record open, so without the retry every
		// claimant can lose the same race and the evidence is dropped by all.
		if err := fileutil.ClaimRename(path, claimed); err != nil {
			continue
		}
		state, readErr = readClaimedLifecycleState(claimed)
		if readErr != nil || state.SchemaVersion != desktopLifecycleSchemaVersion {
			// The file changed between inspection and claim. Put it back when
			// possible instead of deleting data that may belong to another schema.
			_ = os.Rename(claimed, path)
			continue
		}
		_ = os.Remove(claimed)
		if !emit {
			continue
		}
		observations = append(observations, desktopLifecycleObservation{
			Version: state.Version, Channel: state.Channel, Phase: state.Phase,
			StartedAt: state.StartedAt, UpdatedAt: state.UpdatedAt,
			PID: state.PID, DetectedAt: now.Format(time.RFC3339Nano),
		})
	}
	// Task 1002: the pass completed — stamp it so the NEXT boot knows a
	// consume pass happened after any record this one saw (claimed or not).
	t.writeConsumePassStamp(now)
	t.priorPassAt = priorPassAt
	t.applyLauncherDeathEvidence(observations)
	t.pruneRecords()
	return observations
}

// consumePassPath is where this tracker's directory records the most recent
// consume pass.
func (t *desktopLifecycleTracker) consumePassPath() string {
	if t == nil || t.dir == "" {
		return ""
	}
	return filepath.Join(t.dir, lifecycleConsumePassFile)
}

// readConsumePassStamp returns the time of the newest recorded consume pass,
// or the zero time when no readable stamp exists (first install, or a stamp
// this build cannot parse — the report-always default then applies).
func (t *desktopLifecycleTracker) readConsumePassStamp() time.Time {
	path := t.consumePassPath()
	if path == "" {
		return time.Time{}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	var stamp lifecycleConsumePassStamp
	if json.Unmarshal(body, &stamp) != nil || stamp.SchemaVersion != lifecycleConsumePassSchema {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, stamp.At)
	if err != nil {
		return time.Time{}
	}
	return at
}

// writeConsumePassStamp records that a consume pass ran at `at`. Best-effort:
// a missing stamp only makes the next boot treat every record as fresh
// evidence (today's report-always behavior), so a write failure never hides
// a crash.
func (t *desktopLifecycleTracker) writeConsumePassStamp(at time.Time) {
	path := t.consumePassPath()
	if path == "" {
		return
	}
	body, err := json.Marshal(lifecycleConsumePassStamp{
		SchemaVersion: lifecycleConsumePassSchema,
		RunID:         t.state.RunID,
		At:            at.Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	_ = fileutil.AtomicWriteFile(path, body, 0o600)
}

// launcherDeathEvidence is the consume-once record the resident launcher
// writes when a supervised desktop exits abnormally (task 736, issue #39).
// The JSON mirrors the writer in internal/desktoplauncher — keep both in
// lockstep.
type launcherDeathEvidence struct {
	SchemaVersion int    `json:"schemaVersion"`
	PID           int    `json:"pid"`
	ExitCode      int    `json:"exitCode"`
	ExitedAt      string `json:"exitedAt"` // RFC3339Nano, launcher clock
}

const (
	launcherDeathEvidenceSchema   = 1
	launcherDeathEvidenceFileName = "last-abnormal-exit.json"
	// A no-match record must not survive to misattribute a reused PID; a week
	// comfortably covers every restart flow and matches the prune cadence of
	// the lifecycle records themselves.
	launcherDeathEvidenceRetention = 7 * 24 * time.Hour
)

func (t *desktopLifecycleTracker) launcherEvidencePath() string {
	if t == nil || t.dir == "" {
		return ""
	}
	return filepath.Join(t.dir, launcherDeathEvidenceFileName)
}

// applyLauncherDeathEvidence joins the launcher's death record into the dead
// lifecycle observations: an exact PID match upgrades the report's occurredAt
// from "last phase write" to the observed death moment and carries the exit
// code. Consume-once on match; unmatched records are kept until the retention
// sweep so a reporting attempt interrupted before upload can retry on the
// next start, and a stale record can never misattribute a reused PID.
func (t *desktopLifecycleTracker) applyLauncherDeathEvidence(observations []desktopLifecycleObservation) {
	path := t.launcherEvidencePath()
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var record launcherDeathEvidence
	if json.Unmarshal(body, &record) != nil || record.SchemaVersion != launcherDeathEvidenceSchema ||
		record.PID <= 0 || record.ExitedAt == "" {
		// Not a record this build understands: preserve it verbatim so a
		// newer writer's evidence is never consumed or pruned by an older
		// reader (same future-format rule as the lifecycle records).
		return
	}
	if exitedAt, parseErr := time.Parse(time.RFC3339Nano, record.ExitedAt); parseErr != nil ||
		t.now().Sub(exitedAt) > launcherDeathEvidenceRetention {
		_ = os.Remove(path)
		return
	}
	for i := range observations {
		if observations[i].PID != record.PID {
			continue
		}
		observations[i].DeathAt = record.ExitedAt
		observations[i].ExitCode = record.ExitCode
		_ = os.Remove(path)
		return
	}
}

// readClaimedLifecycleState re-reads a just-claimed record. Winning the rename
// does not make the file readable yet: an instance that was inspecting it still
// holds a handle, and Windows answers with a sharing violation until it drops.
// Only the open is retried — content this build cannot parse is a definite
// answer about the record, not a lock waiting to clear.
func readClaimedLifecycleState(path string) (desktopLifecycleState, error) {
	var body []byte
	var err error
	for attempt := range desktopLifecycleReadRetries {
		if body, err = os.ReadFile(path); err == nil || os.IsNotExist(err) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * desktopLifecycleReadBackoff)
	}
	if err != nil {
		return desktopLifecycleState{}, err
	}
	var state desktopLifecycleState
	err = json.Unmarshal(body, &state)
	return state, err
}

func readDesktopLifecycleState(path string) (desktopLifecycleState, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return desktopLifecycleState{}, err
	}
	var state desktopLifecycleState
	if err := json.Unmarshal(body, &state); err != nil {
		return desktopLifecycleState{}, err
	}
	return state, nil
}

func (t *desktopLifecycleTracker) pruneRecords() {
	entries, err := os.ReadDir(t.dir)
	if err != nil || len(entries) <= maxDesktopLifecycleRecords {
		return
	}
	type candidate struct {
		path string
		at   time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(t.dir, entry.Name())
		state, err := readDesktopLifecycleState(path)
		if err != nil || state.SchemaVersion != desktopLifecycleSchemaVersion || t.processAlive(state.PID) {
			continue
		}
		if info, err := entry.Info(); err == nil {
			candidates = append(candidates, candidate{path: path, at: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].at.Before(candidates[j].at) })
	for len(candidates) > maxDesktopLifecycleRecords {
		_ = os.Remove(candidates[0].path)
		candidates = candidates[1:]
	}
}
