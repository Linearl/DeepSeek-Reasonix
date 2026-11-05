package control

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Fork trigger classes (tl1443): the write path whose save met a snapshot
// conflict. One closed set for the decision log, conflicts.jsonl and doctor;
// a stack no row matches reads "other" and its caller chain still names it.
// New classes land here and in forkTriggerRows, nowhere else.
const (
	forkTriggerTurnAutosave    = "turn_autosave"
	forkTriggerTurnEnd         = "turn_end"
	forkTriggerTurnCancel      = "turn_cancel"
	forkTriggerTurnInterrupted = "turn_interrupted"
	forkTriggerToolCheckpoint  = "tool_checkpoint"
	forkTriggerInbox           = "inbox"
	forkTriggerCompact         = "compact"
	forkTriggerRewind          = "rewind"
	forkTriggerBranchOp        = "branch_op"
	forkTriggerNewSession      = "new_session"
	forkTriggerSelfHeal        = "selfheal"
	forkTriggerTabAutosave     = "tab_autosave"
	forkTriggerTabClose        = "tab_close"
	forkTriggerAppLifecycle    = "app_lifecycle"
	forkTriggerSessionSwitch   = "session_switch"
	forkTriggerModelRebind     = "model_rebind"
	forkTriggerWorkspace       = "workspace_rebind"
	forkTriggerArchive         = "archive"
	forkTriggerSessionRemove   = "session_remove"
	forkTriggerIdleRelease     = "idle_release"
	forkTriggerTakeover        = "takeover"
	forkTriggerShutdown        = "shutdown"
	forkTriggerACP             = "acp"
	forkTriggerOther           = "other"
)

// Fork entry classes: the controller save API that issued the failing save.
// "direct" is a control-internal call of the save core (autosave tick, inbox
// turn end, interrupted-turn repair, post-cancel flush, new session).
const (
	forkEntrySnapshot          = "snapshot"
	forkEntryDurable           = "durable"
	forkEntryActivity          = "activity"
	forkEntryRewrite           = "rewrite"
	forkEntryShutdown          = "shutdown"
	forkEntryIfPathFree        = "if_path_free"
	forkEntryActivityIfChanged = "activity_if_changed"
	forkEntryCheckpoint        = "checkpoint"
	forkEntryDirect            = "direct"
)

type forkFrameRow struct{ fn, class string }

// forkTriggerRows maps a function (package qualifier stripped; its closures
// and method values included) to a trigger class. The nearest matching frame
// wins, so a specific write path beats the generic caller around it.
var forkTriggerRows = []forkFrameRow{
	{"(*Controller).autosaveWhileRunning", forkTriggerTurnAutosave},
	{"(*Controller).finishInFlightTurn", forkTriggerTurnEnd},
	{"(*Controller).snapshotActivityIfChanged", forkTriggerTurnEnd},
	{"(*Controller).replaceSessionAfterCancel", forkTriggerTurnCancel},
	{"(*Controller).recoverInterruptedTurn", forkTriggerTurnInterrupted},
	{"(*Controller).checkpointToolTranscript", forkTriggerToolCheckpoint},
	{"(*Controller).onInboxTurnDone", forkTriggerInbox},
	{"(*Controller).submitCommandOrTurnReady", forkTriggerCompact},
	{"conversationApplier.ApplyConversationTruncate", forkTriggerRewind},
	{"conversationApplier.RestoreConversation", forkTriggerRewind},
	{"(*Controller).forkNamedReady", forkTriggerBranchOp},
	{"(*Controller).branchTip", forkTriggerBranchOp},
	{"(*Controller).Branches", forkTriggerBranchOp},
	{"(*Controller).NewSession", forkTriggerNewSession},
	{"(*Controller).SelfHealOpenSession", forkTriggerSelfHeal},
	{"dietSessionProjection", forkTriggerSelfHeal},
	{"(*Agent).runOverflowDiet", forkTriggerSelfHeal},
	{"(*App).tabSnapshotLoop", forkTriggerTabAutosave},
	{"(*App).closeTabRuntime", forkTriggerTabClose},
	{"(*App).keepOnlyVisibleTab", forkTriggerTabClose},
	{"(*App).keepOnlyRemoteVisibleTab", forkTriggerTabClose},
	{"(*App).removeVisibleTabRuntimeAdmissionHeld", forkTriggerTabClose},
	{"(*App).snapshotMergedWorktreeCloseTab", forkTriggerTabClose},
	{"(*App).snapshotAllTabs", forkTriggerAppLifecycle},
	{"(*App).rebindViaDetachedReattach", forkTriggerSessionSwitch},
	{"(*App).rebindViaFreshBuild", forkTriggerSessionSwitch},
	{"(*App).SetModelForTab", forkTriggerModelRebind},
	{"(*App).SetEffortForTab", forkTriggerModelRebind},
	{"(*App).snapshotSettingsRebuildSource", forkTriggerModelRebind},
	{"(*App).ensureTabControllerWorkspace", forkTriggerWorkspace},
	{"(*App).snapshotTopicRuntimeBindings", forkTriggerArchive},
	{"(*App).prepareRemovedSessionRuntimes", forkTriggerSessionRemove},
	{"(*App).removeWorkspaceSessions", forkTriggerSessionRemove},
	{"(*App).releaseDetachedSession", forkTriggerIdleRelease},
	{"(*takeoverMirror).demote", forkTriggerTakeover},
	{"(*App).shutdownBody", forkTriggerShutdown},
	{"(*chatTUI).runModelSubcommand", forkTriggerModelRebind},
	{"(*chatTUI).runEffortCommand", forkTriggerModelRebind},
	{"(*chatTUI).scheduleRuntimeReload", forkTriggerModelRebind},
	{"(*chatTUI).scheduleCurrentControllerRebuild", forkTriggerModelRebind},
	{"(*chatTUI).scheduleSkillSessionRefresh", forkTriggerModelRebind},
	{"adoptCarriedHistoryPreservingProfileAndGrants", forkTriggerModelRebind},
	{"(*chatTUI).runResumeCommand", forkTriggerSessionSwitch},
	{"chatTUI.applyResumePick", forkTriggerSessionSwitch},
	{"chatTUI.update", forkTriggerCompact},
	{"(*chatTUI).runTakeoverCommand", forkTriggerTakeover},
	{"(*cliTakeoverManager).returnMirrorTransaction", forkTriggerTakeover},
	{"chatTUI.shutdownAndQuit", forkTriggerShutdown},
	{"(*Server).switchModelLocked", forkTriggerModelRebind},
	{"(*Server).reloadExtensions", forkTriggerModelRebind},
	{"(*Server).rebuildDetachedModelSettings", forkTriggerModelRebind},
	{"(*Server).reattachDetached", forkTriggerSessionSwitch},
	{"(*Server).handoffLocked", forkTriggerTakeover},
	{"(*Server).reclaimMirroredLocked", forkTriggerTakeover},
	{"(*Server).snapshotForeground", forkTriggerTakeover},
	{"snapshotACPController", forkTriggerACP},
}

var forkEntryRows = []forkFrameRow{
	{"(*Controller).Snapshot", forkEntrySnapshot},
	{"(*Controller).SnapshotDurable", forkEntryDurable},
	{"(*Controller).SnapshotActivity", forkEntryActivity},
	{"(*Controller).SnapshotRewrite", forkEntryRewrite},
	{"(*Controller).SnapshotForShutdown", forkEntryShutdown},
	{"(*Controller).SnapshotIfSavePathFree", forkEntryIfPathFree},
	{"(*Controller).snapshotActivityIfChanged", forkEntryActivityIfChanged},
	{"(*Controller).checkpointToolTranscript", forkEntryCheckpoint},
}

// forkCoreFrames are the save/recovery machinery between a write path and the
// conflict; they never name a trigger and are left out of caller chains.
var forkCoreFrames = []string{
	"(*Controller).recoverSnapshotConflict",
	"(*Controller).recoverSnapshotConflictLadder",
	"(*Controller).recoverShutdownSnapshot",
	"(*Controller).recoverShutdownSave",
	"(*Controller).recoverExternallyRemovedSession",
	"(*Controller).takeoverDivergedRevLag",
	"(*Controller).snapshotWithDurability",
	"(*Controller).snapshot",
	"(*Controller).beginForkTrace",
	"persistSessionSnapshot",
	"persistSessionSnapshotMode",
	"persistSessionSnapshotIfPathFree",
	"retrySameRevisionDivergedRewrite",
	"newForkTrace",
	"traceConflictEscalation",
}

// forkCallerDepth bounds the caller chain logged per trace.
const forkCallerDepth = 5

type forkFrame struct {
	pkg  string // last import-path element: control, agent, main, desktop
	fn   string // function without package: (*Controller).Snapshot
	file string // base file name
	line int
}

// forkOrigin is a classified stack: trigger and entry classes plus the
// compact caller chain (innermost first, "<" reads "called by").
type forkOrigin struct {
	trigger string
	entry   string
	caller  string
}

func captureForkFrames(skip int) []forkFrame {
	var pcs [48]uintptr
	n := runtime.Callers(skip+2, pcs[:])
	return forkFramesFromPCs(pcs[:n])
}

func forkFramesFromPCs(pcs []uintptr) []forkFrame {
	if len(pcs) == 0 {
		return nil
	}
	out := make([]forkFrame, 0, len(pcs))
	frames := runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			pkg, fn := splitForkFunction(frame.Function)
			out = append(out, forkFrame{pkg: pkg, fn: fn, file: filepath.Base(frame.File), line: frame.Line})
		}
		if !more {
			return out
		}
	}
}

func splitForkFunction(full string) (string, string) {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		full = full[i+1:]
	}
	if i := strings.Index(full, "."); i >= 0 {
		return full[:i], full[i+1:]
	}
	return "", full
}

// forkFrameMatches reports whether fn is row or one of its closures
// (row.funcN) or method values (row-fm).
func forkFrameMatches(fn, row string) bool {
	if !strings.HasPrefix(fn, row) {
		return false
	}
	rest := fn[len(row):]
	return rest == "" || rest[0] == '.' || rest[0] == '-'
}

func forkFrameIsCore(f forkFrame) bool {
	if f.pkg == "runtime" {
		return true
	}
	for _, core := range forkCoreFrames {
		if forkFrameMatches(f.fn, core) {
			return true
		}
	}
	return false
}

func forkFrameClass(f forkFrame, rows []forkFrameRow) string {
	for _, row := range rows {
		if forkFrameMatches(f.fn, row.fn) {
			return row.class
		}
	}
	return ""
}

// classifyForkFrames finds the nearest trigger and entry rows outside the
// save machinery and renders the caller chain from the first such frame.
// Session-layer (agent) frames stay out of the chain but may still name a
// trigger: a self-heal diet commits through the same save core.
func classifyForkFrames(frames []forkFrame) forkOrigin {
	origin := forkOrigin{trigger: forkTriggerOther, entry: forkEntryDirect}
	var chain []string
	triggerSet, entrySet := false, false
	for _, f := range frames {
		if forkFrameIsCore(f) {
			continue
		}
		if !triggerSet {
			if class := forkFrameClass(f, forkTriggerRows); class != "" {
				origin.trigger, triggerSet = class, true
			}
		}
		if f.pkg == "agent" {
			continue
		}
		if len(chain) < forkCallerDepth {
			chain = append(chain, f.label())
		}
		if !entrySet {
			if class := forkFrameClass(f, forkEntryRows); class != "" {
				origin.entry, entrySet = class, true
			}
		}
	}
	origin.caller = strings.Join(chain, "<")
	return origin
}

func (f forkFrame) label() string {
	return strings.NewReplacer("(*", "", ")", "").Replace(f.fn) + "@" + f.file + ":" + strconv.Itoa(f.line)
}

// ForkTriggerForFunction classifies one runtime function name (as
// runtime.FuncForPC reports it) into its fork trigger class, "" when no row
// matches. Hosts pin their rows against real method names with it.
func ForkTriggerForFunction(name string) string {
	pkg, fn := splitForkFunction(name)
	return forkFrameClass(forkFrame{pkg: pkg, fn: fn}, forkTriggerRows)
}
