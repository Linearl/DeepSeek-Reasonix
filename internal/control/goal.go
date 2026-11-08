package control

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/evidence"
	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/goaleval"
	"reasonix/internal/store"

	"reasonix/internal/tool"
)

const (
	goalContinueTurn   = "Continue pursuing the active goal under its task contract. Do the next useful work, then call update_goal with your disposition: continue (include the next concrete step in next_action), complete (when the request is done and verification was attempted or reported unavailable), or blocked (when only the user can unblock)."
	goalCompleteNotice = "goal complete"
	unlimitedGoalTurns = -1

	// Bound the persisted novelty window. Signatures are compact hashes, and
	// retaining the most recent window is enough to stop short repeat cycles
	// without allowing an unbounded Goal sidecar.
	maxGoalProgressEvidence = 512
)

// Budget class aliases remain as sidecar/CLI compatibility metadata only.
const (
	budgetClassSimple   = BudgetClassSimple
	budgetClassWrite    = BudgetClassWrite
	budgetClassResearch = BudgetClassResearch
)

// Stop causes distinguish a safe pause from a genuine block. Removed numeric
// causes remain migration-only constants so old sidecars can be normalized.
const (
	stopCauseBudgetTurns   = "budget_turns" // legacy; the class-derived turn quota is gone
	stopCauseBudgetSpend   = "budget_spend"
	stopCauseBudgetTokens  = "budget_tokens"   // legacy; never written by current runtime
	stopCauseNoProgress    = "no_progress"     // legacy; never written by current runtime
	stopCauseGoalRunBudget = "goal_run_budget" // legacy; the per-Run round ceiling is gone
	stopCauseGoalStuck     = "goal_stuck"
	stopCauseEvaluator     = "evaluator_unavailable"
	// stopCauseRecoveryPaused ends an unattended run that hit a pause only a
	// human could lift (task 109 B5). Interactive runs keep the same pause
	// without a terminal transition, because their next user turn resumes it.
	stopCauseRecoveryPaused = "recovery_paused"
	// stopCauseAskUnanswered ends an unattended run whose high-risk question ran
	// out of its wait with nobody there to answer it (task 109 B4). The safe
	// valve is intact - the run did not answer - and it now reports the failure
	// instead of hanging on it.
	stopCauseAskUnanswered = "ask_unanswered"
	stopCauseLegacyArchive = "legacy_archive"
	stopCauseManual        = "manual"
	// maxAutopilotEvaluatorFails bounds how many uncertain/failed evaluator
	// verdicts an unattended run continues through before treating the goal as
	// failed (task 109 B7). Interactive runs keep the fail-closed Blocked pause.
	maxAutopilotEvaluatorFails = 2
)

// Goal transition causes (tl1459): why a running Goal left running. They are
// observational — recorded as lastStopCause, carried by the goal_* notices and
// the exit log — and no decision reads them, so stopCause keeps its resume
// semantics untouched. A FSM branch that leaves running without one of these is
// caught by TestGoalStatusTransitionsDeclareACause.
const (
	goalCauseComplete         = "complete"
	goalCauseCompletePartial  = "complete_partial"
	goalCauseReportBlocked    = "report_blocked"
	goalCauseEvaluatorBlocked = "evaluator_blocked"
	goalCauseTimeLimit        = "autopilot_time_limit"
	// Host stops: the loop stopped the Goal because the turn context died.
	goalCauseDurability    = "durability_failure" // a durable write failed and cancelled the turn
	goalCauseStopRequested = "stop_requested"     // the user (or a remote stop) asked to stop
	goalCauseCancelled     = "context_cancelled"  // the turn context ended without a stop request
	goalCauseCleared       = "cleared"            // the Goal was cleared or replaced by an empty Goal
)

// budgetClassForLegacyMode translates old sidecars and deprecated CLI flags at
// the compatibility boundary. The active Goal runtime stores only budgetClass.
func budgetClassForLegacyMode(goal string, researchMode GoalResearchMode) string {
	switch researchMode {
	case GoalResearchOn:
		return budgetClassResearch
	case GoalResearchOff:
		if GoalNeedsWriteBudget(goal) {
			return budgetClassWrite
		}
		return budgetClassSimple
	default:
		return ClassifyGoalBudget(goal)
	}
}

// goalMachine owns the active goal FSM and its persistence. It is a strict
// leaf: methods take only machine locks and never call back into Controller.
// advance() takes already-gathered inputs so no disk/executor work holds mu.
type goalMachine struct {
	// mu guards the FSM fields below; every critical section under it is short
	// and non-blocking (no disk I/O, no executor calls).
	mu                 sync.Mutex
	goal               string
	status             string
	scopeID            string
	deliveryCheckpoint evidence.DeliveryCheckpoint
	block              string
	strict             bool
	continuationEpoch  uint64
	// activation (tl1472) is the armed/disarmed switch: the goal object is the
	// session's pursuit contract, and this field alone admits or refuses the
	// host's auto-continue loop. Zero reads as armed (goal_object.go).
	activation string
	// armGrant (tl1472, runtime-only) is the current armed stint's one-shot
	// user-turn grant: the first user message after an explicit human install
	// or resume belongs to the stint instead of disarming it. Never persisted.
	armGrant bool
	// summary (tl1472) is the 整理后摘要 for transparent display — a digest,
	// never the raw input text. Set at goal creation (derived) or by the
	// model's create_goal call.
	summary string

	tokenBudget int // configured ceiling for an unattended loop; 0 = unbounded

	// Autopilot (task 49 A1): nobody is watching, so the run carries its own
	// wall-clock bound. deadline is zero unless autopilot is on; a machine that
	// says autopilot without one is refused at install time rather than trusted.
	autopilot bool
	deadline  time.Time

	// Runtime statistics and optional user-selected spend state, persisted
	// across turns and restarts. turnsUsed and noProgressTurns are observational;
	// tokensLimit is non-zero only when the user configured a Goal token budget.
	budgetClass            string
	turnsUsed              int
	turnsLimit             int
	tokensUsed             int
	requestsUsed           int
	workDurationMs         int64
	tokensLimit            int // always 0 at runtime; deprecated hard limit
	noProgressTurns        int
	noProgressLimit        int
	lastContinuationReason string
	launch                 goalLaunchState
	lastEvaluatorReason    string
	stopCause              string
	// lastStopCause (tl1459) is why the Goal last left running (a goalCause*
	// or stopCause* value). Observational only; cleared when a Goal is
	// installed or resumed.
	lastStopCause    string
	budgetExtensions int // deprecated historical sidecar field
	progressEvidence []string
	// evaluatorFailTurns counts consecutive uncertain/failed evaluator verdicts
	// under autopilot so a flaky evaluator does not kill the run on the first
	// blip and cannot spin forever (task 109 B7).
	evaluatorFailTurns int
	// stateExtra preserves fields written by a newer peer during read/modify/
	// write cycles. Known current fields always win on serialization.
	stateExtra map[string]json.RawMessage
	// legacyTaskID is retained only while a historical AutoResearch archive is
	// awaiting migration. It is serialized on fail-closed blocked sidecars so a
	// restart can retry the migration without treating the raw archive path as a
	// new Goal.
	legacyTaskID string

	// statePath is the persisted goal-state sidecar; empty disables persistence.
	statePath string
	// writeMu serializes goal-state disk writes so concurrent saves don't
	// interleave or land out of order. Taken OFF mu by writeState.
	writeMu sync.Mutex
}

// goalState is the serializable form of a running goal. New fields are
// safe-to-omit JSON: old readers ignore them, and restoreFromState re-derives
// defaults when they are missing.
type goalState struct {
	Goal               string                      `json:"goal,omitempty"`
	Status             string                      `json:"status,omitempty"`
	ResearchMode       GoalResearchMode            `json:"researchMode,omitempty"`
	AutoResearchTaskID string                      `json:"autoResearchTaskID,omitempty"`
	ScopeID            string                      `json:"scopeID,omitempty"`
	DeliveryCheckpoint evidence.DeliveryCheckpoint `json:"deliveryCheckpoint,omitempty"`
	Turns              int                         `json:"turns,omitempty"`
	Blocks             int                         `json:"blocks,omitempty"`
	Block              string                      `json:"block,omitempty"`
	Strict             bool                        `json:"strict,omitempty"`
	Todos              []evidence.TodoItem         `json:"todos,omitempty"`

	BudgetClass            string   `json:"budgetClass,omitempty"`
	TurnsUsed              int      `json:"turnsUsed,omitempty"`
	TurnsLimit             int      `json:"turnsLimit,omitempty"`
	TokensUsed             int      `json:"tokensUsed,omitempty"`
	RequestsUsed           int      `json:"requestsUsed,omitempty"`
	WorkDurationMs         int64    `json:"workDurationMs,omitempty"`
	TokensLimit            int      `json:"tokensLimit,omitempty"`
	NoProgressTurns        int      `json:"noProgressTurns,omitempty"`
	NoProgressLimit        int      `json:"noProgressLimit,omitempty"`
	LastContinuationReason string   `json:"lastContinuationReason,omitempty"`
	LastEvaluatorReason    string   `json:"lastEvaluatorReason,omitempty"`
	StopCause              string   `json:"stopCause,omitempty"`
	LastStopCause          string   `json:"lastStopCause,omitempty"`
	BudgetExtensions       int      `json:"budgetExtensions,omitempty"`
	ProgressEvidence       []string `json:"progressEvidence,omitempty"`

	// Autopilot records that this goal ran unattended (task 49 A2). It is persisted
	// rather than re-read from config so a restart resumes the run under the flag it
	// was started with: turning the setting off later must not adopt an old goal,
	// and turning it on must not resurrect an interactive one.
	Autopilot bool `json:"autopilot,omitempty"`

	// Activation (tl1472) is the armed/disarmed switch; omitted means armed
	// (the historical default — pre-1472 running goals could auto-continue).
	Activation string `json:"activation,omitempty"`
	// Summary (tl1472) is the 整理后摘要 shown in the sidebar; the raw goal
	// text stays in Goal.
	Summary string `json:"summary,omitempty"`
}

// goalAdvanceInput carries everything the FSM needs for one continuation step,
// gathered by the caller off the machine's lock. The FSM is the exclusive
// decision point: it applies readiness and decides complete / continue /
// blocked / evaluator fail-closed pause.
type goalAdvanceInput struct {
	report           *goalTurnReport // validated update_goal report; nil when none
	readiness        agent.ReadinessResult
	evaluator        *goalEvaluatorVerdict // evaluator verdict; nil when not run
	evaluatorFailed  string                // evaluator error/timeout text; pause fail-closed
	todos            []evidence.TodoItem
	progressEvidence []string // host evidence identities visible after this turn
	pauseCause       string   // explicit spend boundary reported by the Agent
	pauseReason      string
	expectedEpoch    *uint64
	// createdThisTurn (tl1472) marks the turn that installed a model-authored
	// goal: no update_goal report exists for it and no evaluator verdict is
	// owed — the goal was just created, so the loop continues straight away.
	createdThisTurn bool
}

// goalEvaluatorVerdict is the bounded evaluator's structured outcome.
type goalEvaluatorVerdict struct {
	outcome goaleval.Outcome
	reason  string
}

// goalAdvanceResult reports the FSM step's outcome. data/path/ok describe the
// state to persist (built under mu when something changed); notice is surfaced
// to the user; cont reports whether the goal loop should continue; intercept
// (with interceptNotice) is the next synthetic turn's prompt.
type goalAdvanceResult struct {
	notice            string
	intercept         string
	interceptNotice   string
	cont              bool
	continuationEpoch uint64
	// cause (tl1459) is set whenever this step took the Goal out of running.
	cause string
	// halted (tl1472) names why an in-pursuit goal stopped auto-continuing
	// without leaving running: "disarmed" (a user message took over) or
	// "no_progress" (连续 N 轮无进展，disarm 全集⑤). Observational; the
	// activation log line carries the same fact with its actor.
	halted string
	path   string
	data   []byte
	ok     bool
}

// goalContinuationSnapshot binds a continuation to the exact Goal lifecycle
// state admitted for its synthetic turn. The orchestrator uses these captured
// fields throughout the turn instead of re-reading a possibly replaced Goal.
type goalContinuationSnapshot struct {
	goal    string
	scopeID string
}

// goalStatePath derives a session's persisted goal-state sidecar.
func goalStatePath(sessionPath string) string {
	return store.SessionGoalState(sessionPath)
}

func (g *goalMachine) setStatePath(path string) {
	g.mu.Lock()
	g.statePath = path
	g.mu.Unlock()
}

// snapshot returns the fields Compose injects into outgoing turns.
func (g *goalMachine) snapshot() (goal, status string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.goal, g.status
}

func (g *goalMachine) goalText() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.goal
}

// continuationToken captures the Goal lifecycle that owns an outgoing turn.
// The matching assistant output may advance the FSM only while this epoch is
// still current.
func (g *goalMachine) continuationToken() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.continuationEpoch
}

func (g *goalMachine) deliveryScope() (id, task string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.TrimSpace(g.goal) == "" || g.status != GoalStatusRunning {
		return "", "", false
	}
	if g.scopeID == "" {
		g.scopeID = newGoalScopeID()
	}
	return g.scopeID, g.goal, true
}

// goalScopeIDForTurn resolves the active goal scope for an outgoing turn: the
// continuation snapshot's scope, or the running goal's (assigning one when
// needed). ok=false means no active goal.
func (g *goalMachine) goalScopeIDForTurn(continuation *goalContinuationSnapshot) (string, bool) {
	if continuation != nil {
		return continuation.scopeID, true
	}
	id, _, ok := g.deliveryScope()
	return id, ok
}

func newGoalScopeID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return fmt.Sprintf("goal-%x", raw[:])
	}
	return fmt.Sprintf("goal-fallback-%d-%d", os.Getpid(), time.Now().UnixNano())
}

// active reports whether a goal is currently running.
func (g *goalMachine) active() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning
}

// statusForDisplay maps the empty zero status to "stopped" for frontends.
func (g *goalMachine) statusForDisplay() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.status == "" {
		return GoalStatusStopped
	}
	return g.status
}

// set installs a session-scoped goal (or clears it when goal is empty), resets
// the per-goal runtime counters, and returns the state to persist. ok is
// false (no persistence) when the goal is unchanged or no state path is
// configured.
func (g *goalMachine) set(goal, preferredBudgetClass string, todos []evidence.TodoItem) (string, []byte, bool) {
	goal = strings.TrimSpace(goal)
	if goal != "" && preferredBudgetClass == "" {
		preferredBudgetClass = ClassifyGoalBudget(goal)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if goal != "" && g.goal == goal && g.status == GoalStatusRunning && g.budgetClass == preferredBudgetClass {
		return "", nil, false
	}
	g.installGoalLocked(goal, preferredBudgetClass)
	return g.buildStateLocked(todos)
}

// setLegacyArchiveBlocked atomically installs and blocks an explicit legacy
// archive goal. A concurrent Goal replacement cannot be blocked between two
// separate FSM mutations.
func (g *goalMachine) setLegacyArchiveBlocked(goal, preferredBudgetClass, reason string, todos []evidence.TodoItem) (string, []byte, bool) {
	return g.setLegacyArchiveBlockedWithTaskID(goal, preferredBudgetClass, reason, "", todos)
}

func (g *goalMachine) setLegacyArchiveBlockedWithTaskID(goal, preferredBudgetClass, reason, taskID string, todos []evidence.TodoItem) (string, []byte, bool) {
	goal = strings.TrimSpace(goal)
	taskID = strings.TrimSpace(taskID)
	if goal != "" && preferredBudgetClass == "" {
		preferredBudgetClass = ClassifyGoalBudget(goal)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installGoalLocked(goal, preferredBudgetClass)
	if goal != "" {
		g.status = GoalStatusBlocked
		g.lastStopCause = stopCauseLegacyArchive
	}
	g.stopCause = stopCauseLegacyArchive
	g.block = clipGoalReason(reason)
	g.legacyTaskID = taskID
	return g.buildStateLocked(todos)
}

func (g *goalMachine) installGoalLocked(goal, preferredBudgetClass string) {
	if goal == "" {
		g.bumpEpochLocked(goalEpochClear)
	} else {
		g.bumpEpochLocked(goalEpochInstall)
	}
	g.turnsUsed, g.tokensUsed, g.requestsUsed, g.noProgressTurns = 0, 0, 0, 0
	g.workDurationMs = 0
	g.block = ""
	g.lastContinuationReason, g.lastEvaluatorReason = "", ""
	g.stopCause = ""
	g.budgetExtensions = 0
	g.progressEvidence = nil
	g.summary = ""
	if goal == "" {
		if strings.TrimSpace(g.goal) != "" {
			g.lastStopCause = goalCauseCleared
		}
		g.goal, g.status = "", GoalStatusStopped
		g.budgetClass = ""
		g.turnsLimit = 0
		g.noProgressLimit = 0
		g.scopeID = ""
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{}
		g.armGrant = false
		g.setActivationLocked(false, goalActivationActorUser, "clear")
	} else {
		g.goal, g.status = goal, GoalStatusRunning
		g.lastStopCause = ""
		g.scopeID = newGoalScopeID()
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{ScopeID: g.scopeID}
		g.budgetClass = preferredBudgetClass
		g.turnsLimit = unlimitedGoalTurns
		g.tokensLimit = g.tokenBudget
		g.noProgressLimit = 0
		// tl1472: a fresh install arms the object and grants its first user
		// turn; the digest derives from the goal text until create_goal stores
		// an explicit one.
		g.setActivationLocked(true, goalActivationActorUser, "install")
		g.armGrant = true
	}
	// Installing a normal Goal always abandons any pending legacy migration.
	g.legacyTaskID = ""
}

func (g *goalMachine) setStrict(strict bool, todos []evidence.TodoItem) (string, []byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.strict = strict
	return g.buildStateLocked(todos)
}

// autopilotDeadline returns the instant an unattended run must stop, or the zero
// time when the run is interactive. A non-positive runtime yields the zero time as
// well: the CLI already refuses --autopilot without --max-runtime, and this is the
// belt to that braces so a programmatic caller cannot slip an unbounded run past.
func autopilotDeadline(autopilot bool, maxRuntime time.Duration) time.Time {
	if !autopilot || maxRuntime <= 0 {
		return time.Time{}
	}
	return time.Now().Add(maxRuntime)
}

// autopilotDeadlineReached reports whether an unattended run has used up its
// wall clock. Interactive machines always answer false - they have a human who
// can decide, and an accidental deadline must never cut a normal session short.
// autopilotDeadlineReachedLocked is the lock-held form for advance, which already
// owns g.mu; taking it again would deadlock.
func (g *goalMachine) autopilotDeadlineReachedLocked() bool {
	return g.autopilot && !g.deadline.IsZero() && !time.Now().Before(g.deadline)
}

func (g *goalMachine) autopilotDeadlineReached(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.autopilot && !g.deadline.IsZero() && !now.Before(g.deadline)
}

// stop transitions a running goal to the given terminal status and clears the
// transient runtime bookkeeping. stopCause is cleared: a host stop is not a
// safe pause. cause (tl1459) is required and recorded as lastStopCause; left
// reports whether a running Goal actually left running, which is what decides
// whether the controller owes the user a notice.
func (g *goalMachine) stop(status, cause string, todos []evidence.TodoItem) (path string, data []byte, ok, left bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bumpEpochLocked(goalEpochStop)
	if strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning {
		g.status = status
		g.lastStopCause = cause
		left = true
	}
	// tl1472 disarm 全集②: an explicit stop always withdraws the auto-continue
	// permit, even when the interactive goal stays running for its scope.
	g.setActivationLocked(false, goalActivationActorHost, cause)
	g.stopCause = ""
	g.noProgressTurns = 0
	path, data, ok = g.buildStateLocked(todos)
	return path, data, ok, left
}

// pauseFor transitions a running goal to a safe pause: status blocked plus a
// stop cause, keeping every runtime counter for a later resume.
func (g *goalMachine) pauseFor(stopCause, reason string, todos []evidence.TodoItem) (string, []byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bumpEpochLocked(goalEpochPause)
	if strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning {
		g.status = GoalStatusBlocked
		g.lastStopCause = stopCause
	}
	// tl1472 disarm 全集②④: a pause (manual or budget boundary) withdraws the
	// auto-continue permit; resume() re-arms it.
	g.setActivationLocked(false, goalActivationActorHost, stopCause)
	g.stopCause = stopCause
	if reason != "" {
		g.block = reason
	}
	return g.buildStateLocked(todos)
}

// resume re-enters a recoverable blocked/stopped goal without resetting scope
// or runtime history. Continuous Goals never extend a numeric quota.
func (g *goalMachine) resume(todos []evidence.TodoItem) (path string, data []byte, persist, resumed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopCause == stopCauseLegacyArchive {
		// A legacy archive block is recoverable only through the read-only
		// archive boundary; never reinterpret it as an ordinary Goal resume.
		return "", nil, false, false
	}
	if strings.TrimSpace(g.goal) == "" || g.status == GoalStatusComplete {
		return "", nil, false, false
	}
	// A user-selected spend pause grants one fresh configured slice. Usage
	// remains cumulative; only the absolute threshold moves forward.
	spentBudget := g.stopCause == stopCauseBudgetSpend
	g.bumpEpochLocked(goalEpochResume)
	g.status = GoalStatusRunning
	g.lastStopCause = ""
	g.block = ""
	g.stopCause = ""
	g.noProgressTurns = 0
	g.turnsLimit = unlimitedGoalTurns
	g.noProgressLimit = 0
	g.budgetExtensions = 0
	// tl1472: /goal resume is a human direct request — it re-arms the object
	// and grants its first user turn (权限分层：resume 须人类直接请求).
	g.setActivationLocked(true, goalActivationActorUser, "resume")
	g.armGrant = true
	g.grantSpendSliceLocked(spentBudget)
	if g.scopeID == "" {
		g.scopeID = newGoalScopeID()
	}
	path, data, persist = g.buildStateLocked(todos)
	return path, data, persist, true
}

func (g *goalMachine) setDeliveryCheckpoint(checkpoint evidence.DeliveryCheckpoint, todos []evidence.TodoItem) (string, []byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.scopeID == "" || checkpoint.ScopeID != g.scopeID {
		return "", nil, false
	}
	g.deliveryCheckpoint = checkpoint
	return g.buildStateLocked(todos)
}

func (g *goalMachine) deliveryState() evidence.DeliveryCheckpoint {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deliveryCheckpoint
}

// acceptContinuation checks an advance result before the orchestrator surfaces
// its notice. admitContinuation revalidates after synchronous notice callbacks
// and captures the Goal state at the synthetic-turn admission boundary.
func (g *goalMachine) acceptContinuation(res goalAdvanceResult) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !res.cont ||
		res.continuationEpoch != g.continuationEpoch ||
		strings.TrimSpace(g.goal) == "" ||
		g.status != GoalStatusRunning {
		return "", false
	}
	return res.intercept, true
}

// admitContinuation atomically validates an advance result and captures the
// Goal state used to compose and scope its synthetic turn. Keeping validation
// and capture in one critical section prevents a stale intercept from being
// paired with a replacement Goal between those operations.
func (g *goalMachine) admitContinuation(res goalAdvanceResult) (goalContinuationSnapshot, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !res.cont ||
		res.continuationEpoch != g.continuationEpoch ||
		strings.TrimSpace(g.goal) == "" ||
		g.status != GoalStatusRunning {
		return goalContinuationSnapshot{}, false
	}
	if g.scopeID == "" {
		g.scopeID = newGoalScopeID()
	}
	return goalContinuationSnapshot{
		goal:    g.goal,
		scopeID: g.scopeID,
	}, true
}

// advance runs one continuation step of the goal FSM from already-gathered
// inputs. It mutates the machine, decides whether to keep looping, and builds
// the state to persist after every Goal turn.
//
// Decision priority (the FSM is the exclusive decision point):
//  1. complete + readiness ready, or a repeated complete whose only leftovers
//     are unavailable checks → complete
//  2. blocked (report or evaluator) → blocked immediately (no triple confirm)
//  3. evaluator failed/uncertain → safe pause (fail closed, never default to continue)
//  4. otherwise continue, carrying the missing requirements or next_action
func (g *goalMachine) advance(in goalAdvanceInput) goalAdvanceResult {
	g.mu.Lock()
	defer g.mu.Unlock()
	if in.expectedEpoch != nil && *in.expectedEpoch != g.continuationEpoch {
		return goalAdvanceResult{cont: false}
	}
	if strings.TrimSpace(g.goal) == "" || g.status != GoalStatusRunning {
		return goalAdvanceResult{cont: false}
	}
	g.bumpEpochLocked(goalEpochAdvance)
	// A top-level goal turn (the first turn or a synthetic continuation) counts
	// as an observational statistic; the in-Run model/tool loop is not re-counted.
	g.turnsUsed++
	var notice string
	var intercept string
	var interceptNotice string
	// cause (tl1459) names why a terminal branch took the Goal out of
	// running; every such branch must set it (AST-guarded).
	var cause string
	evaluatorComplete := in.evaluator != nil && in.evaluator.outcome == goaleval.OutcomeComplete
	evaluatorBlocked := in.evaluator != nil && in.evaluator.outcome == goaleval.OutcomeBlocked
	// Terminal dispositions first, then evaluator fail-closed; otherwise continue.
	reportBlocked := in.report != nil && in.report.status == GoalStatusBlocked
	reportComplete := in.report != nil && in.report.status == GoalStatusComplete
	complete := g.completeDecision(in, reportComplete, evaluatorComplete)
	g.observeGoalProgress(in, complete.accept || reportBlocked || evaluatorBlocked)
	if in.evaluatorFailed == "" && (in.evaluator == nil || in.evaluator.outcome != goaleval.OutcomeUncertain) {
		g.evaluatorFailTurns = 0
	}
	// tl1472 disarm 全集⑤: goalNoProgressDisarmTurns consecutive turns with no
	// new host progress evidence withdraw the auto-continue permit. The naive
	// criterion reads the same progress window the FSM already tracks; tl1460
	// owns the smarter judgement layer and must remain the only progress anchor
	// (不迁移红线：不得两套并存).
	if g.noProgressTurns >= goalNoProgressDisarmTurns {
		g.setActivationLocked(false, goalActivationActorFSM, "no_progress")
	}
	// The activation gate: only an armed goal admits a synthetic continuation
	// turn (续跑准入 = active + armed). Terminal branches below still apply to
	// a disarmed goal — a user-takeover turn can legitimately report complete.
	armed := g.activationLocked() == GoalActivationArmed
	// tl1472: a disarmed goal is no longer under pursuit — the user drives.
	// With nothing to judge (no report, no readiness gap, no pause boundary,
	// no evaluator input) the turn ends the loop without FSM side effects: no
	// fail-closed evaluator pause, no budget/deadline transition. The goal
	// object stays active+visible with the armed permit withdrawn. Terminal
	// shapes (report, readiness partial, pause) still fall through to the
	// switch below so a takeover turn can legitimately complete the goal.
	if !armed && in.report == nil && len(in.readiness.Missing) == 0 &&
		in.pauseCause == "" && in.evaluatorFailed == "" && in.evaluator == nil {
		halted := "disarmed"
		if g.noProgressTurns >= goalNoProgressDisarmTurns {
			halted = "no_progress"
		}
		res := goalAdvanceResult{
			notice:            "",
			cont:              false,
			halted:            halted,
			continuationEpoch: g.continuationEpoch,
		}
		res.path, res.data, res.ok = g.buildStateLocked(in.todos)
		return res
	}
	switch {
	case g.autopilotDeadlineReachedLocked():
		// An unattended run that has used its wall clock stops before any other
		// disposition: the model may still believe it is making progress, but nobody
		// is there to extend the budget - that decision was made in advance.
		g.status = GoalStatusTimeLimitReached
		cause = goalCauseTimeLimit
		g.stopCause = ""
		g.lastContinuationReason = "autopilot time limit reached"
		notice = "goal stopped: autopilot time limit reached"
	case reportBlocked:
		// A single blocked report ends the goal immediately; the host no longer
		// repeats a three-turn confirmation ritual.
		reason := cleanGoalBlockReason(in.report.reason)
		if reason == "" {
			reason = "blocked"
		}
		g.status = GoalStatusBlocked
		cause = goalCauseReportBlocked
		g.block = reason
		g.stopCause = ""
		g.lastContinuationReason = clipGoalReason(in.report.reason)
		notice = "goal blocked: " + reason
	case evaluatorBlocked:
		reason := cleanGoalBlockReason(in.evaluator.reason)
		if reason == "" {
			reason = "blocked"
		}
		g.status = GoalStatusBlocked
		cause = goalCauseEvaluatorBlocked
		g.block = reason
		g.stopCause = ""
		g.lastEvaluatorReason = clipGoalReason(in.evaluator.reason)
		notice = "goal blocked: " + reason
	case complete.accept:
		g.goal = ""
		g.status = GoalStatusComplete
		cause = goalCauseComplete
		g.block = ""
		g.stopCause = ""
		g.progressEvidence = nil
		g.lastContinuationReason, g.lastEvaluatorReason = "", ""
		notice = goalCompleteNotice
	case g.tokensLimit > 0 && g.tokensUsed >= g.tokensLimit:
		reason := fmt.Sprintf("token budget reached (%d/%d tokens used)", g.tokensUsed, g.tokensLimit)
		g.stopCause = stopCauseBudgetSpend
		cause = stopCauseBudgetSpend
		g.block = clipGoalReason(reason)
		if g.autopilot {
			// Nobody is around to top the budget up, so this is terminal rather than a
			// pause awaiting a human. Naming it separately is what lets the
			// morning-after report say "budget" instead of a generic "blocked".
			g.status = GoalStatusBudgetExhausted
			notice = "goal stopped: " + reason
		} else {
			g.status = GoalStatusBlocked
			notice = "goal paused: " + reason
		}
	case in.evaluatorFailed != "" || (in.evaluator != nil && in.evaluator.outcome == goaleval.OutcomeUncertain):
		// Fail closed: an unavailable, erroring, or uncertain evaluator pauses
		// the goal instead of defaulting to continue.
		reason := "the completion evaluator is unavailable or could not judge the turn"
		if in.evaluatorFailed != "" {
			reason = "the completion evaluator failed: " + in.evaluatorFailed
		}
		if g.autopilot {
			// Unattended: a single uncertain verdict must not kill a long run
			// (API blips), and must not spin forever either (task 109 B7).
			g.evaluatorFailTurns++
			if g.evaluatorFailTurns <= maxAutopilotEvaluatorFails && armed {
				g.lastEvaluatorReason = clipGoalReason(reason)
				notice = ""
				// Fall through to continue below via applyContinue.
				intercept, interceptNotice = g.applyContinue(in, reportComplete, evaluatorComplete, complete)
				notice = appendGoalStopReport(notice, g)
				res := goalAdvanceResult{
					notice:            notice,
					intercept:         intercept,
					interceptNotice:   interceptNotice,
					cont:              notice == "",
					continuationEpoch: g.continuationEpoch,
				}
				res.path, res.data, res.ok = g.buildStateLocked(in.todos)
				return res
			}
			if g.evaluatorFailTurns <= maxAutopilotEvaluatorFails {
				// Disarmed: keep the fail-tolerance counter semantics but halt
				// the loop without a terminal transition (牌=disarmed ⇒ 必不续).
				g.lastEvaluatorReason = clipGoalReason(reason)
				res := goalAdvanceResult{
					notice:            "",
					cont:              false,
					halted:            "disarmed",
					continuationEpoch: g.continuationEpoch,
				}
				res.path, res.data, res.ok = g.buildStateLocked(in.todos)
				return res
			}
			g.status = GoalStatusFailed
			cause = stopCauseEvaluator
			g.stopCause = stopCauseEvaluator
			g.block = clipGoalReason(reason)
			g.lastEvaluatorReason = clipGoalReason(reason)
			notice = "goal stopped: " + reason
			break
		}
		g.status = GoalStatusBlocked
		cause = stopCauseEvaluator
		g.stopCause = stopCauseEvaluator
		g.block = clipGoalReason(reason)
		g.lastEvaluatorReason = clipGoalReason(reason)
		notice = "goal paused: " + reason
	case in.pauseCause != "":
		reason := strings.TrimSpace(in.pauseReason)
		if reason == "" {
			reason = "the current Goal run reached a recoverable execution boundary"
		}
		g.stopCause = in.pauseCause
		cause = in.pauseCause
		g.block = clipGoalReason(reason)
		if g.autopilot && (in.pauseCause == stopCauseRecoveryPaused || in.pauseCause == stopCauseAskUnanswered) {
			// Nobody is here to send "continue" after Auto recovery paused the
			// turn, and a high-risk ask left unanswered must not hang (tasks
			// 109 B5/B4). Leave a terminal Failed instead of a fake-Running goal.
			g.status = GoalStatusFailed
			g.lastContinuationReason = clipGoalReason(reason)
			notice = "goal stopped: " + reason
			break
		}
		g.status = GoalStatusBlocked
		g.lastContinuationReason = clipGoalReason(reason)
		notice = "goal paused: " + reason
	case len(in.readiness.Missing) > 0 && g.noProgressTurns >= 2 && len(g.progressEvidence) > 0 &&
		g.lastContinuationReason == clipGoalReason("readiness missing: "+in.readiness.Reason) &&
		repeatedCompleteMayFinish(in.readiness.Missing, in.todos):
		// After real Goal work, a repeated verification/review-only gap is
		// Partial. Do not replay the same intercept forever.
		g.goal = ""
		g.status = GoalStatusComplete
		cause = goalCauseCompletePartial
		g.block = ""
		g.stopCause = ""
		g.progressEvidence = nil
		g.lastContinuationReason, g.lastEvaluatorReason = "", ""
		notice = goalCompleteNotice
	case in.createdThisTurn && armed:
		// tl1472: the turn that installed a model-authored goal had no report
		// and owes no evaluator verdict — the loop continues straight away with
		// the standard continuation prompt (plus readiness guidance if todos
		// are outstanding).
		intercept, interceptNotice = g.applyContinue(in, false, false, completeDecision{})
	default:
		if !armed {
			// 牌=disarmed ⇒ 必不续 (状态一致性契约). The goal object stays
			// active for visibility and scope; the loop halts instead.
			halted := "disarmed"
			if g.noProgressTurns >= goalNoProgressDisarmTurns {
				halted = "no_progress"
			}
			res := goalAdvanceResult{
				notice:            "",
				cont:              false,
				halted:            halted,
				continuationEpoch: g.continuationEpoch,
			}
			res.path, res.data, res.ok = g.buildStateLocked(in.todos)
			return res
		}
		intercept, interceptNotice = g.applyContinue(in, reportComplete, evaluatorComplete, complete)
	}
	// An unattended run leaves nobody to ask what happened, so every terminal notice
	// carries the run's spend. Appending it once here, rather than in each branch,
	// keeps a newly added terminal state from shipping without a report.
	notice = appendGoalStopReport(notice, g)
	res := goalAdvanceResult{
		notice:            notice,
		intercept:         intercept,
		interceptNotice:   interceptNotice,
		cont:              notice == "",
		continuationEpoch: g.continuationEpoch,
	}
	if g.status != GoalStatusRunning {
		g.lastStopCause = cause
		res.cause = cause
		// tl1472 disarm 全集③④: every terminal transition withdraws the
		// auto-continue permit in one place (consolidated rather than per-branch
		// so a newly added terminal state cannot ship armed).
		g.setActivationLocked(false, goalActivationActorFSM, cause)
	}
	res.path, res.data, res.ok = g.buildStateLocked(in.todos)
	return res
}

// goalStatusIsTerminal reports whether a status ends the run: no further turns follow.
func goalStatusIsTerminal(status string) bool {
	switch status {
	case GoalStatusComplete, GoalStatusBlocked, GoalStatusStopped,
		GoalStatusBudgetExhausted, GoalStatusTimeLimitReached,
		GoalStatusFailed, GoalStatusCancelled:
		return true
	default:
		return false
	}
}

// appendGoalStopReport adds the run's spend to a terminal notice. The notice says
// what happened; this says how far the run got, which is the part of an
// unattended run nobody can ask about afterwards - the user was, by definition,
// not watching when it stopped.
func appendGoalStopReport(notice string, g *goalMachine) string {
	if notice == "" || g == nil || !goalStatusIsTerminal(g.status) {
		return notice
	}
	parts := []string{fmt.Sprintf("turns %d", g.turnsUsed)}
	if g.tokensUsed > 0 {
		parts = append(parts, fmt.Sprintf("tokens %d", g.tokensUsed))
	}
	if g.workDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("work %s", formatGoalSpendDuration(g.workDurationMs)))
	}
	return notice + " — " + strings.Join(parts, ", ")
}

// formatGoalSpendDuration keeps the report readable at both ends of the scale:
// seconds for a short run, minutes for one that ate an afternoon or a night.
func formatGoalSpendDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

// foldUsage attributes a turn's billable tokens to the goal, but only while the
// goal lifecycle still matches the recorder's scope+epoch; stale or replaced
// goals reject late usage.
func (g *goalMachine) foldUsage(scopeID string, epoch uint64, tokens, requests int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if (tokens <= 0 && requests <= 0) || g.scopeID != scopeID || g.continuationEpoch != epoch {
		return false
	}
	if tokens > 0 {
		g.tokensUsed += tokens
	}
	if requests > 0 {
		g.requestsUsed += requests
	}
	return true
}

// foldWorkDuration attributes one Run's cumulative assistant work duration to
// the Goal. The caller supplies the maximum WorkDurationMs among messages
// created by that Run, so multi-round cumulative values are not double-counted.
func (g *goalMachine) foldWorkDuration(scopeID string, epoch uint64, durationMs int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if durationMs <= 0 || g.scopeID != scopeID || g.continuationEpoch != epoch {
		return false
	}
	g.workDurationMs += durationMs
	return true
}

// buildStateLocked marshals the current goal state for persistence. The caller
// holds mu; this only reads in-memory state, never touching disk. Returns ok=false
// when persistence is disabled (no state path). The matching writeState does the
// disk write OFF mu so the per-turn save can't stall a status poll.
func (g *goalMachine) buildStateLocked(todos []evidence.TodoItem) (path string, data []byte, ok bool) {
	if g.statePath == "" {
		return "", nil, false
	}
	state := goalState{
		Goal:                   g.goal,
		Status:                 g.status,
		ScopeID:                g.scopeID,
		DeliveryCheckpoint:     g.deliveryCheckpoint,
		Turns:                  g.turnsUsed,
		Block:                  g.block,
		Strict:                 g.strict,
		Todos:                  todos,
		BudgetClass:            g.budgetClass,
		TurnsUsed:              g.turnsUsed,
		TurnsLimit:             g.turnsLimit,
		TokensUsed:             g.tokensUsed,
		RequestsUsed:           g.requestsUsed,
		WorkDurationMs:         g.workDurationMs,
		TokensLimit:            g.tokensLimit,
		NoProgressTurns:        g.noProgressTurns,
		NoProgressLimit:        g.noProgressLimit,
		LastContinuationReason: g.lastContinuationReason,
		LastEvaluatorReason:    g.lastEvaluatorReason,
		StopCause:              g.stopCause,
		LastStopCause:          g.lastStopCause,
		BudgetExtensions:       g.budgetExtensions,
		ProgressEvidence:       append([]string(nil), g.progressEvidence...),
		Autopilot:              g.autopilot,
		Activation:             g.activation,
		Summary:                g.summary,
	}
	// GoalResearchOff is a downgrade fence for ordinary Goal sidecars. A
	// fail-closed legacy migration keeps its task identity and compatibility mode
	// until the archive has been validated and the Goal-only state is committed.
	if g.legacyTaskID != "" && g.status == GoalStatusBlocked && g.stopCause == stopCauseLegacyArchive {
		state.AutoResearchTaskID = g.legacyTaskID
		state.ResearchMode = GoalResearchOn
	} else {
		state.ResearchMode = GoalResearchOff
	}
	b, err := marshalGoalState(state, g.stateExtra)
	if err != nil {
		slog.Warn("controller: marshal goal state", "err", err)
		return "", nil, false
	}
	return g.statePath, b, true
}

// writeState preserves the existing best-effort behavior for background Goal
// progress. Callers that need transactional persistence use writeStateErr.
func (g *goalMachine) writeState(path string, data []byte) {
	if err := g.writeStateErr(path, data); err != nil {
		slog.Warn("controller: write goal state", "err", err)
	}
}

// persistWithTodos re-persists goal state with the given todos, without
// changing any in-memory goal fields. Used after force-completing todos on
// goal completion so a session reload does not revert to the old incomplete
// todo state.
func (g *goalMachine) persistWithTodos(todos []evidence.TodoItem) {
	g.mu.Lock()
	path, data, ok := g.buildStateLocked(todos)
	g.mu.Unlock()
	if ok {
		g.writeState(path, data)
	}
}

// terminalTodosFromState reads the persisted goal-state sidecar and returns its
// todo snapshot only after the goal has reached a terminal state. Running goal
// state is not refreshed on every todo_write, so its todos may be older than the
// transcript rebuilt by Agent.SetSession.
// resumableAutopilotGoal returns the goal a freshly started controller should pick
// back up: an unattended run that was still going when the process stopped (task 49
// A2). Only a persisted Autopilot flag with a live Running status qualifies - a
// completed or terminal goal is history, and a goal that stopped for a human is
// waiting for one, not for a restart.
func resumableAutopilotGoal(sessionPath string) (string, bool) {
	if strings.TrimSpace(sessionPath) == "" {
		return "", false
	}
	data, err := fileencoding.ReadFileUTF8(goalStatePath(sessionPath))
	if err != nil {
		return "", false
	}
	var state goalState
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("controller: parse goal state for autopilot resume", "err", err)
		return "", false
	}
	if !state.Autopilot || state.Status != GoalStatusRunning {
		return "", false
	}
	goal := strings.TrimSpace(state.Goal)
	if goal == "" {
		return "", false
	}
	return goal, true
}

func (g *goalMachine) terminalTodosFromState(sessionPath string) ([]evidence.TodoItem, bool) {
	if strings.TrimSpace(sessionPath) == "" {
		return nil, false
	}
	data, err := fileencoding.ReadFileUTF8(goalStatePath(sessionPath))
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("controller: read goal state", "err", err)
		}
		return nil, false
	}
	var state goalState
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("controller: parse goal state", "err", err)
		return nil, false
	}
	switch state.Status {
	case GoalStatusComplete, GoalStatusBlocked, GoalStatusStopped,
		GoalStatusBudgetExhausted, GoalStatusTimeLimitReached,
		GoalStatusFailed, GoalStatusCancelled:
	default:
		return nil, false
	}
	if len(state.Todos) == 0 {
		return nil, false
	}
	return append([]evidence.TodoItem(nil), state.Todos...), true
}

// restoreFromState reloads Goal state from the sidecar. The sidecar is
// authoritative; active Goals are normalized to continuous-runtime sentinels.
// migrated means path/data were atomically rewritten (without a provider call).
// legacyTaskID is returned only
// so Controller can fill missing goal text from a historical archive.
func (g *goalMachine) restoreFromState(sessionPath string) (path string, data []byte, migrated bool, legacy legacyGoalRestore) {
	if strings.TrimSpace(sessionPath) == "" {
		return "", nil, false, legacyGoalRestore{}
	}
	// Ensure write path is bound even when the controller rebuilds.
	if g.statePath == "" {
		g.setStatePath(goalStatePath(sessionPath))
	}
	raw, err := fileencoding.ReadFileUTF8(goalStatePath(sessionPath))
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("controller: read goal state", "err", err)
		}
		return "", nil, false, legacyGoalRestore{}
	}
	var state goalState
	if err := json.Unmarshal(raw, &state); err != nil {
		slog.Warn("controller: parse goal state", "err", err)
		return "", nil, false, legacyGoalRestore{}
	}
	g.mu.Lock()
	g.stateExtra = goalStateUnknownFields(raw)
	g.goal = strings.TrimSpace(state.Goal)
	g.status = state.Status
	if g.status == "" {
		g.status = GoalStatusStopped
	}
	// Legacy task identity is migration-only compatibility data. It is returned to
	// the Controller's archive boundary and retained in the machine only while a
	// fail-closed migration remains pending.
	legacy = legacyGoalRestore{
		taskID: strings.TrimSpace(state.AutoResearchTaskID),
		todos:  append([]evidence.TodoItem(nil), state.Todos...),
	}
	// A task id is pending only when the sidecar has no Goal text. A legacy
	// sidecar that already contains an objective can be migrated directly and
	// must serialize as ordinary Goal state on the first write.
	if g.goal == "" {
		g.legacyTaskID = legacy.taskID
	} else {
		g.legacyTaskID = ""
	}
	if legacy.taskID != "" && g.goal != "" {
		// Sidecars that already carry the Goal objective do not depend on the
		// historical archive. Complete the migration immediately.
		migrated = true
	}
	g.scopeID = strings.TrimSpace(state.ScopeID)
	if g.scopeID == "" {
		g.scopeID = strings.TrimSpace(state.DeliveryCheckpoint.ScopeID)
	}
	if g.goal != "" && g.scopeID == "" {
		g.scopeID = newGoalScopeID()
	}
	g.deliveryCheckpoint = state.DeliveryCheckpoint
	if g.scopeID == "" {
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{}
	} else if g.deliveryCheckpoint.ScopeID == "" {
		g.deliveryCheckpoint.ScopeID = g.scopeID
	} else if g.deliveryCheckpoint.ScopeID != g.scopeID {
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{ScopeID: g.scopeID}
	}
	g.block = state.Block
	g.strict = state.Strict
	g.stopCause = state.StopCause
	g.lastStopCause = state.LastStopCause
	g.budgetExtensions = state.BudgetExtensions
	// tl1472: activation defaults to armed when the field is absent — a
	// pre-1472 running sidecar keeps its historical auto-continue permit.
	g.activation = state.Activation
	if g.activation == "" {
		g.activation = GoalActivationArmed
	}
	g.summary = strings.TrimSpace(state.Summary)
	g.progressEvidence, _ = mergeGoalProgressEvidence(nil, state.ProgressEvidence)
	g.lastContinuationReason = state.LastContinuationReason
	g.lastEvaluatorReason = state.LastEvaluatorReason
	// Old sidecars carry Turns (pre-budget counting); treat it as turn usage.
	g.turnsUsed = state.TurnsUsed
	if g.turnsUsed == 0 && state.Turns > 0 {
		g.turnsUsed = state.Turns
	}
	g.tokensUsed = state.TokensUsed
	g.requestsUsed = state.RequestsUsed
	g.workDurationMs = state.WorkDurationMs
	g.budgetClass = normalizeBudgetClass(g.goal, state.BudgetClass, state.ResearchMode)
	g.turnsLimit = state.TurnsLimit
	g.noProgressTurns = state.NoProgressTurns
	g.noProgressLimit = state.NoProgressLimit
	g.tokensLimit = state.TokensLimit
	// Roll back to the loaded, still-paused semantics if the atomic
	// normalization write fails. This prevents a session from appearing
	// unlocked only in memory while its sidecar remains blocked on disk.
	rollback := g.captureLocked()
	if goalStateNeedsMigration(state, g.budgetClass) {
		migrated = true
	}
	if g.normalizeContinuousState(state.ResearchMode, legacy.taskID) {
		migrated = true
	}
	g.bumpEpochLocked(goalEpochRestore)
	legacy.epoch = g.continuationEpoch
	pendingLegacyGoal := legacy.taskID != "" && g.goal == ""
	if migrated && !pendingLegacyGoal {
		// Migration rewrites only the removed budget state. Preserve the todo
		// snapshot carried by the authoritative sidecar instead of clearing it.
		path, data, ok := g.buildStateLocked(state.Todos)
		if ok {
			g.mu.Unlock()
			if err := g.writeStateErr(path, data); err != nil {
				slog.Warn("controller: persist normalized goal state", "err", err)
				g.restore(rollback)
				return "", nil, false, legacy
			}
			return path, data, true, legacy
		}
	}
	g.mu.Unlock()
	return "", nil, false, legacy
}

// formatIncompleteTodos renders the reminder shown when a complete claim
// arrives while the executor's canonical todos or project-readiness checks
// aren't done. Returns empty when nothing is blocking. Pure: the caller gathers
// todos and the readiness reason from the executor off the goal lock.
func formatIncompleteTodos(todos []evidence.TodoItem, readiness string) string {
	var parts []string
	if len(todos) > 0 {
		if incomplete := evidence.IncompleteTodos(todos); len(incomplete) > 0 {
			var b strings.Builder
			b.WriteString("the following tasks are still incomplete:")
			for _, t := range incomplete {
				fmt.Fprintf(&b, "\n  - %s (%s)", t.Content, t.Status)
			}
			parts = append(parts, b.String())
		}
	}
	if readiness != "" {
		parts = append(parts, readiness)
	}
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Goal signaled complete but issues remain:\n")
	for _, p := range parts {
		b.WriteString("- ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("Fix remaining work, or if a check cannot be run declare it in update_goal completion.unverified and report complete.")
	return b.String()
}

// clipGoalReason bounds a recorded reason for storage and display.
func clipGoalReason(reason string) string {
	reason = strings.TrimSpace(reason)
	const max = 400
	if r := []rune(reason); len(r) > max {
		return string(r[:max]) + "..."
	}
	return reason
}

func cleanGoalBlockReason(reason string) string {
	return strings.Trim(strings.TrimSpace(reason), " \t\r\n:：,，.。;；!！?？-—_[]()（）")
}

// ShortGoalForNotice collapses whitespace and truncates a goal for one-line UI.
func ShortGoalForNotice(goal string) string {
	goal = strings.Join(strings.Fields(goal), " ")
	runes := []rune(goal)
	const max = 160
	if len(runes) <= max {
		return goal
	}
	return string(runes[:max]) + "..."
}

// goalTodos snapshots the executor's canonical todos for goal-state persistence.
func (c *Controller) goalTodos() []evidence.TodoItem {
	if c.executor == nil {
		return nil
	}
	return c.executor.CanonicalTodoState()
}

// persistGoalState writes a freshly built goal state to disk, off c.mu. The
// executor guard preserves the original behavior of skipping persistence when
// no executor is attached.
func (c *Controller) persistGoalState(path string, data []byte, ok bool) {
	if !ok || c.executor == nil {
		return
	}
	c.goals.writeState(path, data)
}

func (c *Controller) persistGoalStateAtEpoch(epoch uint64, todos []evidence.TodoItem) (bool, error) {
	applied, err := c.goals.writeStateAtEpoch(epoch, todos)
	if err != nil {
		slog.Warn("controller: write goal state", "err", err)
	}
	return applied, err
}

func (c *Controller) restoreTerminalGoalTodos(sessionPath string) {
	if c.executor == nil {
		return
	}
	todos, ok := c.goals.terminalTodosFromState(sessionPath)
	if !ok {
		return
	}
	c.executor.ReplaceTodoState(todos)
}

// goalTurnRecorder is the per-turn recorder bound to one goal turn's scope and
// epoch. update_goal calls land here as candidate state; the FSM commits them
// only when the goal lifecycle still matches (scope + epoch), so late calls
// from a replaced or cleared goal are rejected. Usage events emitted during the
// turn are folded through the recorder into the goal's observational token total.
type goalTurnRecorder struct {
	mu               sync.Mutex
	machine          *goalMachine
	scopeID          string
	epoch            uint64
	recorded         bool
	terminal         bool
	status           string
	reason           string
	nextAction       string
	tokensUsed       int
	requestsUsed     int
	workDurationMs   int64
	durationRecorded bool
}

func (g *goalMachine) newTurnRecorder(scopeID string, epoch uint64) *goalTurnRecorder {
	return &goalTurnRecorder{machine: g, scopeID: scopeID, epoch: epoch}
}

// RecordGoalReport validates the report against the turn's goal lifecycle and
// records it as the turn's candidate disposition. Same-value repeats are
// idempotent; continue may upgrade to complete/blocked; complete and blocked
// are terminal and reject conflicting later calls.
func (r *goalTurnRecorder) RecordGoalReport(report tool.GoalReport) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.machine.turnActive(r.scopeID, r.epoch) {
		return "", fmt.Errorf("update_goal: report ignored because %s — no goal state was changed", r.machine.turnInactiveReason(r.scopeID, r.epoch))
	}
	// The wire status "continue" maps to the FSM's internal "running" state;
	// retain the wire value for user-facing acknowledgements and errors.
	wireStatus := report.Status
	if report.Status == "continue" {
		report.Status = GoalStatusRunning
	}
	// tl1472 权限分层：blocked 模型可报但有最小轮次门槛——刚立的目标第一轮
	// 就喊 blocked 视为逃逸，退回 continue 语义继续工作。
	if report.Status == GoalStatusBlocked {
		turnNumber, active := r.machine.turnNumberForReport(r.scopeID, r.epoch)
		if active && turnNumber < goalBlockedMinTurns {
			return "", fmt.Errorf(
				"update_goal: blocked requires at least %d goal turns of work (this is turn %d) — no goal state was changed. Keep working on what you can, or report continue with the blocker in reason; only the user can lift a genuine blocker, so give them the specifics",
				goalBlockedMinTurns, turnNumber)
		}
	}
	switch {
	case r.terminal:
		return "", fmt.Errorf("update_goal: this turn's disposition is already final (%s); conflicting %q report ignored", r.status, wireStatus)
	case !r.recorded:
		// first record
	case r.status == report.Status && r.reason == report.Reason && r.nextAction == report.NextAction:
		return fmt.Sprintf("update_goal: %s already recorded for this turn (identical report).", wireStatus), nil
	case r.status == GoalStatusRunning && (report.Status == GoalStatusComplete || report.Status == GoalStatusBlocked):
		// continue → terminal upgrade allowed.
	default:
		return "", fmt.Errorf("update_goal: conflicting reports this turn (%s then %s) — the later report was ignored", r.status, wireStatus)
	}
	r.recorded = true
	r.status = report.Status
	r.reason = report.Reason
	r.nextAction = report.NextAction
	if report.Status != GoalStatusRunning {
		r.terminal = true
	}
	return fmt.Sprintf("update_goal: %s recorded for this turn.", wireStatus), nil
}

func (r *goalTurnRecorder) addUsage(tokens int) {
	r.addUsageWithRequests(tokens, 0)
}

func (r *goalTurnRecorder) addUsageWithRequests(tokens, requests int) {
	if tokens <= 0 && requests <= 0 {
		return
	}
	r.mu.Lock()
	if r.machine.foldUsage(r.scopeID, r.epoch, tokens, requests) {
		if tokens > 0 {
			r.tokensUsed += tokens
		}
		if requests > 0 {
			r.requestsUsed += requests
		}
	}
	r.mu.Unlock()
}

func (r *goalTurnRecorder) usageTokens() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokensUsed
}

func (r *goalTurnRecorder) addWorkDuration(durationMs int64) {
	if durationMs <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.durationRecorded {
		return
	}
	if r.machine.foldWorkDuration(r.scopeID, r.epoch, durationMs) {
		r.workDurationMs = durationMs
		r.durationRecorded = true
	}
}

// validReport returns the recorded report only when the goal lifecycle still
// matches the recorder's binding; stale (replaced/cleared) turns report nothing.
func (r *goalTurnRecorder) validReport(expectedEpoch uint64) *goalTurnReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recorded || r.epoch != expectedEpoch || !r.machine.turnActive(r.scopeID, r.epoch) {
		return nil
	}
	return &goalTurnReport{status: r.status, reason: r.reason, nextAction: r.nextAction}
}

// goalTurnReport is the validated update_goal report for one goal turn.
type goalTurnReport struct {
	status     string
	reason     string
	nextAction string
}

// turnActive reports whether the machine's goal lifecycle matches the binding.
func (g *goalMachine) turnActive(scopeID string, epoch uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning &&
		g.scopeID == scopeID && g.continuationEpoch == epoch
}
