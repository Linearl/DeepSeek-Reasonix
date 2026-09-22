package tool

import "context"

// AutonomousUpdateController is the host side of the restart_update tool
// (task 254). It mirrors the RestartUpdater pattern (task 81): the interface
// lives in the tool package, the desktop app supplies the implementation, and
// the binding travels through the call context so the tool schema stays stable
// across hosts that cannot swap their own install.
//
// The controller is the agent-facing half of the versions/ mechanism:
// listing installed versions with a health bit, staging an update target, and
// executing it. The destructive halves themselves stay in the host's existing
// paths (RestartAndUpdate / SwitchToVersion), which carry their own guards.
type AutonomousUpdateController interface {
	// ListVersions returns every installed version tree plus the staged
	// build, each with a health bit (does the tree carry the desktop and CLI
	// binaries — a tree without the CLI bricks servepool, task 248), along
	// with the active version and the staged version label ("" when staging
	// is absent or unreadable).
	ListVersions(ctx context.Context) ([]VersionHealth, string, string, error)
	// SetTarget validates an update target — "staging" or an installed
	// version name for rollback — and remembers it for ExecuteTarget. It
	// returns a human-readable description of what execute will do.
	SetTarget(ctx context.Context, target string) (string, error)
	// ExecuteTarget performs the remembered target and relaunches.
	// callerSession is the session path whose turn issued the call: that turn
	// is exempt from the host's busy guard because the restart is meant to end
	// it (task 254). Like RestartAndUpdate, it returns once the swap is
	// committed; the restart follows shortly after and a success must not be
	// retried.
	ExecuteTarget(ctx context.Context, callerSession string) (string, error)
}

// VersionHealth is one row of the restart_update list_versions report.
type VersionHealth struct {
	// Version is the directory name and doubles as the activeVersion string
	// ("v1.38.3-20260923-1010"). Staging carries its version.txt label.
	Version string `json:"version"`
	// Active is true when current.json points at this version (staging is
	// never active).
	Active bool `json:"active"`
	// Healthy reports that the tree carries the desktop and CLI binaries;
	// a missing CLI means a servepool 503 after switching (task 248).
	Healthy bool `json:"healthy"`
	// Staging marks the unpublished staged build rather than a versions/ tree.
	Staging bool `json:"staging"`
	// ModTimeUnix is the tree's last modification time, as a publish hint.
	ModTimeUnix int64 `json:"modTimeUnix"`
}

type autonomousUpdateKey struct{}

// WithAutonomousUpdateController binds the host's autonomous-update
// implementation to this tool call.
func WithAutonomousUpdateController(ctx context.Context, controller AutonomousUpdateController) context.Context {
	if controller == nil {
		return ctx
	}
	return context.WithValue(ctx, autonomousUpdateKey{}, controller)
}

// AutonomousUpdateControllerFromContext returns the implementation bound to
// this tool call.
func AutonomousUpdateControllerFromContext(ctx context.Context) (AutonomousUpdateController, bool) {
	controller, ok := ctx.Value(autonomousUpdateKey{}).(AutonomousUpdateController)
	return controller, ok && controller != nil
}

// restartCallerSessionKey carries the session path of the turn issuing a tool
// call, so ExecuteTarget can exempt that turn from the host's busy guard
// (task 254): the restart ends the very turn that asked for it.
type restartCallerSessionKey struct{}

// WithRestartCallerSession binds the calling session path to this tool call.
func WithRestartCallerSession(ctx context.Context, sessionPath string) context.Context {
	if sessionPath == "" {
		return ctx
	}
	return context.WithValue(ctx, restartCallerSessionKey{}, sessionPath)
}

// RestartCallerSessionFromContext returns the calling session path, or "".
func RestartCallerSessionFromContext(ctx context.Context) string {
	sessionPath, _ := ctx.Value(restartCallerSessionKey{}).(string)
	return sessionPath
}
