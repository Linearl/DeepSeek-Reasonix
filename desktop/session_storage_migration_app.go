package main

// Task 805: the storage panel's migration entry points. The kernels live in
// internal/session (PlanV4Import/RunV4Import for the v4→v3 backfill,
// PlanV5Import/RunV5Import for the upstream v5 import, task 773); this file
// only wires config paths, serializes runs, and forwards progress lines to
// the webview. The panel drives two-step confirmation on top of the plan.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/session"
)

// The Wails-exposed entry points. Every one serializes on a package mutex so
// two panels (or a panel and a stray call) cannot race the same stores.

// PlanSessionV4Backfill is the dry run for the v4→v3 backfill.
func (a *App) PlanSessionV4Backfill() (*SessionStorageMigrationPlanView, error) {
	sessionStorageMigrationMu.Lock()
	defer sessionStorageMigrationMu.Unlock()
	return a.planSessionV4Backfill()
}

// RunSessionV4Backfill executes the v4→v3 backfill.
func (a *App) RunSessionV4Backfill() (*SessionStorageMigrationReportView, error) {
	sessionStorageMigrationMu.Lock()
	defer sessionStorageMigrationMu.Unlock()
	return a.runSessionV4Backfill()
}

// PlanSessionV5Import is the dry run for the upstream v5 import (task 773).
func (a *App) PlanSessionV5Import() (*SessionStorageMigrationPlanView, error) {
	sessionStorageMigrationMu.Lock()
	defer sessionStorageMigrationMu.Unlock()
	return a.planSessionV5Import()
}

// RunSessionV5Import executes the upstream v5 import.
func (a *App) RunSessionV5Import() (*SessionStorageMigrationReportView, error) {
	sessionStorageMigrationMu.Lock()
	defer sessionStorageMigrationMu.Unlock()
	return a.runSessionV5Import()
}

// sessionStorageMigrationProgressEvent is the webview event the panel listens
// on while a migration runs.
const sessionStorageMigrationProgressEvent = "sessionStorageMigrationProgress"

// SessionStorageMigrationEntryView is one session's route through a migration
// plan (a shared shape over the v4 backfill and the v5 import plans, so the
// panel renders both with one component).
type SessionStorageMigrationEntryView struct {
	SessionID  string `json:"sessionId"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	TargetPath string `json:"targetPath,omitempty"`
	Mapping    string `json:"mapping,omitempty"`
	FromCount  int    `json:"fromCount,omitempty"`
	ToCount    int    `json:"toCount,omitempty"`
	DeltaCount int    `json:"deltaCount,omitempty"`
	Title      string `json:"title,omitempty"`
}

// SessionStorageMigrationPlanView is the dry-run result the first confirm step
// shows: what a run would do, before it does it.
type SessionStorageMigrationPlanView struct {
	Kind               string                             `json:"kind"` // v4-backfill | v5-import
	Mode               string                             `json:"mode,omitempty"`
	SourceRoot         string                             `json:"sourceRoot"`
	V5StoreFound       bool                               `json:"v5StoreFound"`
	Entries            []SessionStorageMigrationEntryView `json:"entries"`
	SkipCount          int                                `json:"skipCount"`
	AppendCount        int                                `json:"appendCount"`
	RebuildCount       int                                `json:"rebuildCount"`
	ConflictCount      int                                `json:"conflictCount"`
	FailCount          int                                `json:"failCount"`
	TotalDeltaMessages int                                `json:"totalDeltaMessages"`
	GeneratedAt        time.Time                          `json:"generatedAt"`
	// tl1287④: the v5 registry pre-check. The panel shows a warning before the
	// confirm button when the registry is missing or unreadable — every session
	// then falls back to the caller's default directory (v5import_plan.go
	// keeps going with no owners), which the operator must see BEFORE running.
	RegistryFile  string `json:"registryFile,omitempty"`
	RegistryFound bool   `json:"registryFound"`
	RegistryError string `json:"registryError,omitempty"`
}

// SessionStorageMigrationBackupView records the pre-run backup.
type SessionStorageMigrationBackupView struct {
	Dir   string `json:"dir,omitempty"`
	Files int    `json:"files,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

// SessionStorageMigrationReportView is the run receipt: verification state,
// backup location (the restore guidance points here) and the loss notes.
type SessionStorageMigrationReportView struct {
	Kind          string                             `json:"kind"`
	Verified      bool                               `json:"verified"`
	SourceUnmoved bool                               `json:"sourceUnmoved"`
	Backup        SessionStorageMigrationBackupView  `json:"backup"`
	AppendCount   int                                `json:"appendCount"`
	RebuildCount  int                                `json:"rebuildCount"`
	SkipCount     int                                `json:"skipCount"`
	ConflictCount int                                `json:"conflictCount"`
	FailCount     int                                `json:"failCount"`
	TouchedFiles  int                                `json:"touchedFiles"`
	Accounting    string                             `json:"accounting"`
	Losses        []string                           `json:"losses"`
	FinishedAt    time.Time                          `json:"finishedAt"`
	Entries       []SessionStorageMigrationEntryView `json:"entries,omitempty"`
}

// sessionStorageMigrationMu serializes migration runs (and plans, cheaply) so
// two panels cannot race the same stores.
var sessionStorageMigrationMu sync.Mutex

// planSessionV4Backfill turns the kernel plan into the shared view, wiring the
// desktop's own store paths: the v4 mirror next to the global session dir and
// every session directory the catalog indexes (global + per-project + tabs).
func (a *App) planSessionV4Backfill() (*SessionStorageMigrationPlanView, error) {
	v3Dir := config.SessionDir()
	if v3Dir == "" {
		return nil, fmt.Errorf("session storage: the user state directory is unavailable")
	}
	plan, err := session.PlanV4Import(context.Background(), session.V4ImportOptions{
		V4Root:     config.SessionStoreDir(),
		V3Dirs:     v4BackfillV3Dirs(a),
		RebuildDir: v3Dir,
	})
	if err != nil {
		return nil, err
	}
	view := &SessionStorageMigrationPlanView{
		Kind:               "v4-backfill",
		Mode:               plan.Mode,
		SourceRoot:         plan.V4Root,
		V5StoreFound:       true,
		Entries:            make([]SessionStorageMigrationEntryView, 0, len(plan.Entries)),
		SkipCount:          plan.SkipCount,
		AppendCount:        plan.AppendCount,
		RebuildCount:       plan.RebuildCount,
		ConflictCount:      plan.ConflictCount,
		FailCount:          plan.FailCount,
		TotalDeltaMessages: plan.TotalTailMessages,
		GeneratedAt:        plan.GeneratedAt,
	}
	for _, entry := range plan.Entries {
		view.Entries = append(view.Entries, SessionStorageMigrationEntryView{
			SessionID:  entry.SessionID,
			Status:     string(entry.Status),
			Reason:     entry.Reason,
			TargetPath: entry.TargetPath,
			Mapping:    entry.Mapping,
			FromCount:  entry.V3Count,
			ToCount:    entry.V4Count,
			DeltaCount: entry.TailCount,
			Title:      entry.Title,
		})
	}
	return view, nil
}

// runSessionV4Backfill executes the backfill with progress forwarded to the
// webview.
func (a *App) runSessionV4Backfill() (*SessionStorageMigrationReportView, error) {
	plan, err := a.planSessionV4Backfill()
	if err != nil {
		return nil, err
	}
	progress := func(format string, args ...any) {
		runtimeEventsEmitFallback(a.ctx, sessionStorageMigrationProgressEvent, map[string]string{
			"kind": "v4-backfill",
			"line": fmt.Sprintf(format, args...),
		})
	}
	report, err := session.RunV4Import(context.Background(), session.V4ImportRunOptions{
		V4ImportOptions: session.V4ImportOptions{
			V4Root:     config.SessionStoreDir(),
			V3Dirs:     v4BackfillV3Dirs(a),
			RebuildDir: config.SessionDir(),
		},
		BackupRoot: filepath.Dir(config.SessionDir()),
		Progress:   progress,
	})
	if err != nil {
		return nil, err
	}
	return v4BackfillReportView("v4-backfill", plan, report), nil
}

// v4BackfillV3Dirs collects the candidate legacy stores for the mapping
// anchors: the global session dir plus every directory the catalog indexes
// (per-project roots, tab-owned dirs). Both the plan and the run use it, so
// the anchors stay identical.
func v4BackfillV3Dirs(a *App) []string {
	dirs := []string{}
	if dir := config.SessionDir(); dir != "" {
		dirs = append(dirs, dir)
	}
	for _, target := range a.sessionCatalogTargets() {
		if strings.TrimSpace(target.Path) == "" {
			continue
		}
		duplicate := false
		for _, dir := range dirs {
			if strings.EqualFold(filepath.Clean(dir), filepath.Clean(target.Path)) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			dirs = append(dirs, filepath.Clean(target.Path))
		}
	}
	return dirs
}

func v4BackfillReportView(kind string, plan *SessionStorageMigrationPlanView, report *session.V4ImportReport) *SessionStorageMigrationReportView {
	view := &SessionStorageMigrationReportView{
		Kind:          kind,
		Verified:      report.Verified,
		SourceUnmoved: report.SourceUnmoved,
		Backup: SessionStorageMigrationBackupView{
			Dir:   report.Backup.Dir,
			Files: report.Backup.Files,
			Bytes: report.Backup.Bytes,
		},
		AppendCount:   report.AppendCount,
		RebuildCount:  report.RebuildCount,
		SkipCount:     report.SkipCount,
		ConflictCount: report.ConflictCount,
		FailCount:     report.FailCount,
		TouchedFiles:  len(report.TouchedFiles),
		Accounting:    report.Accounting,
		Losses:        report.Losses,
		FinishedAt:    report.FinishedAt,
		Entries:       plan.Entries,
	}
	return view
}

// SessionV5StoreAvailable reports whether an upstream v5 store exists next to
// the fork's user state directory (the panel's hint switches on it; since
// tl1287 the import entry itself is always rendered — a missing store surfaces
// as a clear plan error instead of a silently hidden button).
func (a *App) SessionV5StoreAvailable() bool {
	dir := defaultV5SourceRoot()
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// SessionV5StoreLocation exposes the probed default v5 source root so the
// panel's not-found hint can say WHERE it looked (tl1287④ user-tested gap:
// the button existed but was invisible whenever this directory was absent).
func (a *App) SessionV5StoreLocation() string {
	return defaultV5SourceRoot()
}

// defaultV5SourceRoot is the upstream desktop store location: the sibling
// desktop-sessions-v5/by-id next to the fork's session directory.
func defaultV5SourceRoot() string {
	dir := config.SessionDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), "desktop-sessions-v5", "by-id")
}

// planSessionV5Import wraps the 773 kernel with the desktop's own routing
// (registry workspace roots → project session dirs).
func (a *App) planSessionV5Import() (*SessionStorageMigrationPlanView, error) {
	source := defaultV5SourceRoot()
	if source == "" {
		return nil, fmt.Errorf("session storage: the user state directory is unavailable")
	}
	plan, err := session.PlanV5Import(context.Background(), session.V5ImportOptions{
		SourceRoot:    source,
		ResolveTarget: desktopV5TargetResolver(),
	})
	if err != nil {
		// tl1287④: name the probed path — the user-tested gap was a hidden
		// button with no way to learn why; a failed plan must say where it
		// looked before giving up.
		return nil, fmt.Errorf("session storage: cannot open the upstream v5 store at %s: %w", source, err)
	}
	view := &SessionStorageMigrationPlanView{
		Kind:         "v5-import",
		SourceRoot:   plan.SourceRoot,
		V5StoreFound: true,
		Entries:      make([]SessionStorageMigrationEntryView, 0, len(plan.Entries)),
		SkipCount:    plan.SkipCount,
		FailCount:    plan.FailCount,
		GeneratedAt:  plan.GeneratedAt,
		RegistryFile: plan.RegistryFile,
	}
	if !plan.RegistryFound {
		for _, failure := range plan.Failures {
			if failure.SessionID == "(registry)" {
				view.RegistryError = failure.Reason
				break
			}
		}
	} else {
		view.RegistryFound = true
	}
	for _, entry := range plan.Entries {
		delta := entry.MessageCount
		if entry.Status != "import" {
			delta = 0
		}
		view.Entries = append(view.Entries, SessionStorageMigrationEntryView{
			SessionID:  entry.SessionID,
			Status:     v5StatusView(entry.Status),
			Reason:     entry.Reason,
			TargetPath: entry.TargetPath,
			ToCount:    entry.MessageCount,
			DeltaCount: delta,
			Title:      entry.Title,
		})
		if entry.Status == "import" {
			view.RebuildCount++
			view.TotalDeltaMessages += entry.MessageCount
		}
	}
	view.FailCount += len(plan.Failures)
	return view, nil
}

func v5StatusView(status string) string {
	// The v5 importer's "import" writes brand-new legacy files — the same
	// user-facing route the backfill calls a rebuild.
	if status == "import" {
		return "rebuild"
	}
	return status
}

// runSessionV5Import executes the v5 import with progress forwarded to the
// webview.
func (a *App) runSessionV5Import() (*SessionStorageMigrationReportView, error) {
	plan, err := a.planSessionV5Import()
	if err != nil {
		return nil, err
	}
	progress := func(format string, args ...any) {
		runtimeEventsEmitFallback(a.ctx, sessionStorageMigrationProgressEvent, map[string]string{
			"kind": "v5-import",
			"line": fmt.Sprintf(format, args...),
		})
	}
	report, err := session.RunV5Import(context.Background(), session.V5ImportRunOptions{
		V5ImportOptions: session.V5ImportOptions{
			SourceRoot:    defaultV5SourceRoot(),
			ResolveTarget: desktopV5TargetResolver(),
		},
		BackupRoot: backupRootForV5Import(),
		Progress:   progress,
	})
	if err != nil {
		return nil, err
	}
	view := &SessionStorageMigrationReportView{
		Kind:          "v5-import",
		Verified:      report.Verified,
		SourceUnmoved: report.SourceUnmoved,
		Backup: SessionStorageMigrationBackupView{
			Dir:   report.Backup.Dir,
			Files: report.Backup.Files,
			Bytes: report.Backup.Bytes,
		},
		RebuildCount: report.ImportCount,
		SkipCount:    report.SkipCount,
		FailCount:    report.FailCount,
		TouchedFiles: len(report.PublishedFiles),
		Accounting:   report.Accounting,
		Losses:       report.Losses,
		FinishedAt:   report.FinishedAt,
		Entries:      plan.Entries,
	}
	return view, nil
}

// backupRootForV5Import puts the v5 import backup next to the user state root
// (never inside a session store).
func backupRootForV5Import() string {
	dir := config.SessionDir()
	if dir == "" {
		return ""
	}
	return filepath.Dir(dir)
}

// desktopV5TargetResolver routes registry workspace roots onto the fork's own
// session directories (the same shape the CLI uses, without the override).
func desktopV5TargetResolver() func(string) (string, error) {
	return func(workspaceRoot string) (string, error) {
		if strings.TrimSpace(workspaceRoot) != "" {
			if dir := config.ProjectSessionDir(workspaceRoot); dir != "" {
				return dir, nil
			}
			return "", fmt.Errorf("cannot resolve the project session directory for workspace %q", workspaceRoot)
		}
		dir := config.SessionDir()
		if dir == "" {
			return "", fmt.Errorf("cannot resolve the global session directory (user state dir unavailable)")
		}
		return dir, nil
	}
}
