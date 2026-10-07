package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/taskcatalog"
	"reasonix/internal/taskmonitor"
)

// JobView is one running background job (bash/task started with
// run_in_background) for the status-bar indicator.
type JobView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"`
	Tps       int    `json:"tps,omitempty"` // sub-agent streaming rate (tok/s), omitted when unknown (#9521)
}

// Jobs returns the still-running background jobs for the status bar. It refreshes
// on demand (mount, turn end, and on each notice the frontend receives).
func (a *App) Jobs() []JobView {
	return a.JobsForTab("")
}

func (a *App) JobsForTab(tabID string) []JobView {
	out := []JobView{}
	ctrl := a.ctrlForRuntimeTabID(tabID)
	return a.jobsForCtrl(ctrl, out)
}

// CancelJob stops one running background job in the active tab.
func (a *App) CancelJob(jobID string) (bool, error) {
	return a.CancelJobForTab("", jobID)
}

// CancelJobForTab stops one running background job without relying on whatever
// tab happens to be active when the asynchronous frontend call completes.
func (a *App) CancelJobForTab(tabID, jobID string) (bool, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return false, fmt.Errorf("job id is required")
	}
	if tabID != "" {
		if a.isRemoteTab(tabID) {
			if err := a.CancelRemoteTabJobs(tabID, []string{jobID}); err != nil {
				return false, err
			}
			return true, nil
		}
		ctrl := a.ctrlForRuntimeTabID(tabID)
		if ctrl != nil {
			return cancelJobForController(ctrl, jobID)
		}
		return false, nil
	}
	return cancelJobForController(a.ctrlForRuntimeTabID(tabID), jobID)
}

func cancelJobForController(ctrl control.SessionAPI, jobID string) (bool, error) {
	if ctrl == nil {
		return false, nil
	}
	canceller, ok := ctrl.(interface{ CancelJob(string) bool })
	if !ok {
		return false, fmt.Errorf("background job cancellation is unavailable")
	}
	return canceller.CancelJob(jobID), nil
}

func (a *App) jobsForCtrl(ctrl control.SessionAPI, out []JobView) []JobView {
	if ctrl == nil {
		return out
	}
	for _, v := range ctrl.Jobs() {
		out = append(out, JobView{ID: v.ID, Kind: v.Kind, Label: v.Label, Status: v.Status, StartedAt: v.StartedAt, Tps: v.Tps})
	}
	return out
}

// taskStore is the Store backing the task monitor panel.
func (a *App) taskStore() taskmonitor.WriteStore {
	return taskcatalog.ObservedStore()
}

// taskControl returns the process-wide ControlService backing the task
// monitor panel. A single instance keeps control operations serialized within
// this process (across processes the FileStore's per-task lock still
// arbitrates), and avoids re-creating the service on every Wails call.
func (a *App) taskControl() *taskmonitor.ControlService {
	a.taskCtrlOnce.Do(func() {
		a.taskCtrl = taskmonitor.NewControlService(a.taskStore())
	})
	return a.taskCtrl
}

func (a *App) projectDir() string {
	return a.activeWorkspaceRoot()
}

type taskMonitorTabTarget struct {
	projectDir  string
	sessionDir  string
	sessionPath string
	sessionID   string
}

// taskMonitorTargetForTab snapshots the workspace and session identity owned by
// tabID. Wails dispatches bound calls concurrently, so resolving the active tab
// inside a task operation would allow a later tab switch to retarget it.
func (a *App) taskMonitorTargetForTab(tabID string) (taskMonitorTabTarget, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return taskMonitorTabTarget{}, fmt.Errorf("task monitor tab id is required")
	}

	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return taskMonitorTabTarget{}, fmt.Errorf("task monitor tab %q is unavailable", tabID)
	}
	workspaceRoot := strings.TrimSpace(tab.WorkspaceRoot)
	tabSessionPath := strings.TrimSpace(tab.SessionPath)
	ctrl := tab.Ctrl
	leaseKey := tab.sessionLeaseRuntimeKey()
	a.mu.RUnlock()

	projectDir := workspaceRoot
	if projectDir == "" {
		projectDir = "."
	}
	sessionDir := desktopSessionDir(workspaceRoot)
	sessionPath := tabSessionPath
	if ctrl != nil {
		if dir := strings.TrimSpace(ctrl.SessionDir()); dir != "" {
			sessionDir = dir
		}
		if path := strings.TrimSpace(ctrl.SessionPath()); path != "" {
			sessionPath = path
		}
	}
	// During a recovery handoff the lease-backed tab path is newer than the
	// controller path until the controller commits the handoff.
	if tabSessionPath != "" && sessionRuntimeKey(tabSessionPath) == leaseKey {
		sessionPath = tabSessionPath
		sessionDir = filepath.Dir(tabSessionPath)
	} else if ctrl == nil && tabSessionPath != "" {
		sessionDir = filepath.Dir(tabSessionPath)
	}

	target := taskMonitorTabTarget{
		projectDir:  projectDir,
		sessionDir:  sessionDir,
		sessionPath: sessionPath,
	}
	if sessionPath != "" {
		target.sessionID = agent.BranchID(sessionPath)
	}
	return target, nil
}

func (a *App) ListTasks() ([]taskmonitor.TaskSnapshot, error) {
	return a.taskStore().ListTasks(a.ctx, a.projectDir())
}

// CurrentTaskSessionID returns the stable branch ID for the active desktop
// session. Task Monitor uses this as an optional view filter; an empty value
// means that the active tab has no session controller yet.
func (a *App) CurrentTaskSessionID() string {
	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return ""
	}
	return agent.BranchID(ctrl.SessionPath())
}

// ListTasksForSession limits the project task view to one desktop session.
// The unfiltered ListTasks method remains for compatibility with existing
// callers and project-wide diagnostics.
func (a *App) ListTasksForSession(sessionID string) ([]taskmonitor.TaskSnapshot, error) {
	tasks, err := a.ListTasks()
	if err != nil || strings.TrimSpace(sessionID) == "" {
		return tasks, err
	}
	return filterTasksBySession(tasks, sessionID), nil
}

// ListTasksForTab returns the task view owned by tabID and filters it to that
// tab's session when one is available. It deliberately avoids active-tab state.
func (a *App) ListTasksForTab(tabID string) ([]taskmonitor.TaskSnapshot, error) {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return nil, err
	}
	tasks, err := a.taskStore().ListTasks(a.ctx, target.projectDir)
	if err != nil || target.sessionID == "" {
		return tasks, err
	}
	return filterTasksBySession(tasks, target.sessionID), nil
}

func filterTasksBySession(tasks []taskmonitor.TaskSnapshot, sessionID string) []taskmonitor.TaskSnapshot {
	filtered := make([]taskmonitor.TaskSnapshot, 0, len(tasks))
	for _, task := range tasks {
		if task.SessionID == sessionID {
			filtered = append(filtered, task)
		}
	}
	return filtered
}

func (a *App) GetTask(taskID string) (*taskmonitor.TaskSnapshot, error) {
	return a.taskStore().GetTask(a.ctx, a.projectDir(), taskID)
}

func (a *App) ListTaskEvents(taskID string, afterSequence int) ([]taskmonitor.TaskEvent, error) {
	return a.taskStore().ListEvents(a.ctx, a.projectDir(), taskID, afterSequence)
}

func (a *App) ListTaskEventsForTab(tabID, taskID string, afterSequence int) ([]taskmonitor.TaskEvent, error) {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return nil, err
	}
	return a.taskStore().ListEvents(a.ctx, target.projectDir, taskID, afterSequence)
}

func (a *App) StopTask(taskID string, expectedVersion uint64, reason, idemKey string) (taskmonitor.ControlResult, error) {
	projectDir := a.projectDir()
	return a.taskControl().StopTaskWithKiller(
		a.ctx, projectDir, taskID, expectedVersion, reason, idemKey,
		desktopTaskJobKiller{app: a, projectDir: projectDir},
	)
}

func (a *App) StopTaskForTab(tabID, taskID string, expectedVersion uint64, reason, idemKey string) (taskmonitor.ControlResult, error) {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return taskmonitor.ControlResult{}, err
	}
	return a.taskControl().StopTaskWithKiller(
		a.ctx, target.projectDir, taskID, expectedVersion, reason, idemKey,
		desktopTaskJobKiller{app: a, projectDir: target.projectDir},
	)
}

func (a *App) CancelTask(taskID string, expectedVersion uint64, reason, idemKey string) (taskmonitor.ControlResult, error) {
	projectDir := a.projectDir()
	return a.taskControl().CancelTaskWithKiller(
		a.ctx, projectDir, taskID, expectedVersion, reason, idemKey,
		desktopTaskJobKiller{app: a, projectDir: projectDir},
	)
}

func (a *App) CancelTaskForTab(tabID, taskID string, expectedVersion uint64, reason, idemKey string) (taskmonitor.ControlResult, error) {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return taskmonitor.ControlResult{}, err
	}
	return a.taskControl().CancelTaskWithKiller(
		a.ctx, target.projectDir, taskID, expectedVersion, reason, idemKey,
		desktopTaskJobKiller{app: a, projectDir: target.projectDir},
	)
}

func (a *App) RequeueTask(taskID string, expectedVersion uint64, idemKey string) (taskmonitor.ControlResult, error) {
	return a.taskControl().RequeueTask(a.ctx, a.projectDir(), taskID, expectedVersion, idemKey)
}

func (a *App) RequeueTaskForTab(tabID, taskID string, expectedVersion uint64, idemKey string) (taskmonitor.ControlResult, error) {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return taskmonitor.ControlResult{}, err
	}
	return a.taskControl().RequeueTask(a.ctx, target.projectDir, taskID, expectedVersion, idemKey)
}

func (a *App) OpenTaskSession(taskID string) (taskmonitor.ControlResult, error) {
	return a.taskControl().OpenTaskSession(a.ctx, a.projectDir(), taskID)
}

func (a *App) OpenTaskSessionForTab(tabID, taskID string) (taskmonitor.ControlResult, error) {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return taskmonitor.ControlResult{}, err
	}
	return a.taskControl().OpenTaskSession(a.ctx, target.projectDir, taskID)
}

type desktopTaskJobKiller struct {
	app        *App
	projectDir string
}

func (k desktopTaskJobKiller) Kill(sessionID, taskID string) bool {
	// Legacy task records without a session ID cannot be routed safely because
	// jobs.Manager IDs restart at task-1 for each controller.
	if k.app == nil || sessionID == "" || strings.TrimSpace(k.projectDir) == "" {
		return false
	}

	k.app.mu.RLock()
	tabs := k.app.runtimeTabsLocked()
	controllers := make([]control.SessionAPI, 0, len(tabs))
	for _, tab := range tabs {
		if tab != nil && tab.Ctrl != nil && sameProjectRoot(tab.WorkspaceRoot, k.projectDir) {
			controllers = append(controllers, tab.Ctrl)
		}
	}
	k.app.mu.RUnlock()

	for _, ctrl := range controllers {
		if agent.BranchID(ctrl.SessionPath()) != sessionID {
			continue
		}
		if killer, ok := ctrl.(interface{ CancelJob(string) bool }); ok && killer.CancelJob(taskID) {
			return true
		}
	}
	return false
}
