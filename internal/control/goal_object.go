package control

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/i18n"
)

// tl1472 目标对象化：goal 从「目标模式的附属物」升格为 session-local 独立对象。
// 本文件是对象的词汇表与唯一映射点：
//   - phase：对象级状态词（active/paused/blocked/complete/budget_limited），由
//     既有 status+stopCause 单向推导（compatStatusProjection 的镜像），映射表
//     只此一份，契约测试钉死。
//   - activation：armed/disarmed。状态牌与续跑解耦——phase=active 只说明目标
//     在 pursuit 中；是否允许宿主自动续跑由 activation 单独裁决（续跑准入=
//     active+armed，迁移裁决①）。disarm 全集：①用户任何新消息 ②显式 stop
//     ③completed/failed ④预算耗尽 ⑤连续 N 轮无进展。每次变化打点（谁改、为什么）。
//   - summary：透明展示用的整理后摘要（非原始输入全文），立 goal 时同步生成
//     首版；模型通道由 create_goal 显式给，人通道派生。
//   - revision：防陈旧写令牌。1459 的 continuationEpoch 语义并入 revision——
//     同一个计数器（goalMachine.continuationEpoch），没有第二个真相源。
//
// 非目标（不迁移，架构红线）：消息语义路由（active 期间输入解释为
// continuation）；每轮 checkpoint 进度存档（无消费者；1460 落「因X续」即
// 唯一进度锚）。status 字段仍是侧车/通知/UI 的兼容投影，全部经 phase() 映射
// 表读取，状态一致性由契约测试守护。

// Goal phase vocabulary (tl1472). The object-level states; the legacy status
// strings stay the wire/compat projection derived one-way from these.
const (
	GoalPhaseActive        = "active"
	GoalPhasePaused        = "paused"
	GoalPhaseBlocked       = "blocked"
	GoalPhaseComplete      = "complete"
	GoalPhaseBudgetLimited = "budget_limited"
)

// Goal activation vocabulary (tl1472). armed admits the host's auto-continue
// loop; disarmed means the goal object stays for visibility but every turn
// runs without automatic continuation until a human re-arms (/goal resume,
// or a fresh /goal set).
const (
	GoalActivationArmed    = "armed"
	GoalActivationDisarmed = "disarmed"
)

// Activation actors: who moved the switch. Recorded on every change (打点).
const (
	goalActivationActorUser  = "user"
	goalActivationActorHost  = "host"
	goalActivationActorFSM   = "fsm"
	goalActivationActorModel = "model"
)

// goalNoProgressDisarmTurns is disarm 全集⑤'s N: consecutive goal turns with
// no new host progress evidence (todos unchanged, no successful tool output)
// disarm the goal instead of looping forever. Naive criterion per the ruling;
// tl1460 owns the smarter judgement layer.
const goalNoProgressDisarmTurns = 3

// goalBlockedMinTurns is update_goal(blocked)'s minimum-turn threshold
// (权限分层裁决：blocked 可报但有门槛). The goal must be at least in its second
// turn before a blocked claim is accepted; before that the model keeps working
// or reports continue with the blocker.
const goalBlockedMinTurns = 2

const goalActivationLogMessage = "controller: goal activation change"

// phase maps the machine's status (and its stop cause) to the object-level
// phase vocabulary. THE single mapping table — every object-level reader goes
// through here, and the contract test pins each row.
//
//	running → active
//	blocked + budget_spend → budget_limited（预算分牌）
//	blocked + 其余 stopCause(manual/evaluator/recovery/ask) → paused（安全暂停，可 resume）
//	blocked + 无 stopCause（report/evaluator 判 blocked）→ blocked
//	complete → complete
//	budget_exhausted / time_limit_reached → budget_limited
//	stopped / failed / cancelled（终态，cause 族承载原因）→ blocked
//	"" → ""（从未装过目标，无 phase）
func (g *goalMachine) phaseLocked() string {
	switch g.status {
	case GoalStatusRunning:
		return GoalPhaseActive
	case GoalStatusComplete:
		return GoalPhaseComplete
	case GoalStatusBudgetExhausted, GoalStatusTimeLimitReached:
		return GoalPhaseBudgetLimited
	case GoalStatusBlocked:
		switch g.stopCause {
		case stopCauseBudgetSpend:
			// The spend boundary is a budget 牌（显式 budget_limited）, not a
			// generic pause — resume semantics keep, the phase names why.
			return GoalPhaseBudgetLimited
		case "":
			return GoalPhaseBlocked
		default:
			// Safe pause: someone recorded a recoverable boundary (manual pause,
			// evaluator, recovery, ask wait). Resume semantics keep.
			return GoalPhasePaused
		}
	case GoalStatusStopped, GoalStatusFailed, GoalStatusCancelled:
		// Terminal leaves of the 1459 cause family: the fine-grained why lives
		// in lastStopCause, the phase says only "not active, not complete".
		return GoalPhaseBlocked
	default:
		// "" (no goal ever installed) has no phase.
		return ""
	}
}

func (g *goalMachine) phase() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.phaseLocked()
}

// activation returns the current activation. Zero value (fresh machine, old
// sidecar without the field) reads armed: historical running goals could
// auto-continue before tl1472, and the object must not silently strand them.
func (g *goalMachine) activationLocked() string {
	if g.activation == "" {
		return GoalActivationArmed
	}
	return g.activation
}

// activationValue reads the switch. (Named activationValue because the field
// itself is activation; Go forbids a method sharing the field's name.)
func (g *goalMachine) activationValue() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activationLocked()
}

// armedForAutoContinue is the lock-taking 续跑准入 read (active + armed).
func (g *goalMachine) armedForAutoContinue() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.armedForAutoContinueLocked()
}

// armForUserTurnGrant consumes the one-shot user-turn grant of the current
// armed stint. /goal set 与 /goal resume 都是人类直接请求建立的 stint：其后
// 第一条用户消息是这个 stint 的喂入，不是接管（否则 resume 之后任何消息都会
// 立即 disarm，resume 永远无法真正重启续跑——控制项失去意义）。grant 只护
// 一条消息；之后的插话照常构成 disarm 全集①。
func (g *goalMachine) armForUserTurnGrant() bool {
	g.mu.Lock()
	granted := g.armGrant
	g.armGrant = false
	g.mu.Unlock()
	return granted
}

// armedForAutoContinueLocked reports the 续跑准入 gate: active + armed.
func (g *goalMachine) armedForAutoContinueLocked() bool {
	return strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning &&
		g.activationLocked() == GoalActivationArmed
}

// setActivationLocked moves the switch and 打点s every change (谁改的、为什么).
// Caller holds g.mu. Returns whether the value actually changed.
func (g *goalMachine) setActivationLocked(armed bool, actor, cause string) bool {
	next := GoalActivationDisarmed
	if armed {
		next = GoalActivationArmed
	}
	if g.activationLocked() == next {
		return false
	}
	from := g.activationLocked()
	g.activation = next
	slog.Info(goalActivationLogMessage,
		"from", from, "to", next,
		"actor", actor, "cause", cause,
		"phase", g.phaseLocked(),
		"revision", g.continuationEpoch,
		"scope", shortGoalScope(g.scopeID))
	return true
}

// disarm drops the auto-continue permit while keeping the goal object. Callers
// persist the returned state like every other FSM transition.
func (g *goalMachine) disarm(actor, cause string, todos []evidence.TodoItem) (string, []byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.setActivationLocked(false, actor, cause)
	return g.buildStateLocked(todos)
}

// summaryText returns the display digest (整理后摘要). Falls back to a derived
// digest of the raw goal text so the sidebar is never blank.
func (g *goalMachine) summaryText() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := strings.TrimSpace(g.summary); s != "" {
		return s
	}
	return deriveGoalSummary(g.goal)
}

// deriveGoalSummary generates the first-version digest at goal creation time
// (待答点处置：立 goal 时同步生成首版摘要，存对象字段).
func deriveGoalSummary(goal string) string {
	goal = strings.Join(strings.Fields(goal), " ")
	runes := []rune(goal)
	const max = 120
	if len(runes) <= max {
		return goal
	}
	return string(runes[:max]) + "..."
}

// GoalObjectView is the visibility carrier (迁移裁决⑥): everything the
// sidebar needs, read under one lock. Phase and activation are BOTH exposed —
// showing only the running half would replicate the 1459 mirror incident
// ("显示 running 其实早已 disarmed").
type GoalObjectView struct {
	Goal          string `json:"goal,omitempty"`
	Summary       string `json:"summary,omitempty"`
	Phase         string `json:"phase"`
	Activation    string `json:"activation"`
	Status        string `json:"status,omitempty"`
	Revision      uint64 `json:"revision"`
	StopCause     string `json:"stopCause,omitempty"`
	LastStopCause string `json:"lastStopCause,omitempty"`
}

func (g *goalMachine) objectView() GoalObjectView {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.TrimSpace(g.goal) == "" && g.status == "" {
		return GoalObjectView{Activation: GoalActivationDisarmed}
	}
	return GoalObjectView{
		Goal:          g.goal,
		Summary:       deriveGoalSummaryIfEmpty(g.summary, g.goal),
		Phase:         g.phaseLocked(),
		Activation:    g.activationLocked(),
		Status:        g.status,
		Revision:      g.continuationEpoch,
		StopCause:     g.stopCause,
		LastStopCause: g.lastStopCause,
	}
}

func deriveGoalSummaryIfEmpty(summary, goal string) string {
	if s := strings.TrimSpace(summary); s != "" {
		return s
	}
	return deriveGoalSummary(goal)
}

// revision exposes the anti-stale-write token. tl1472 folds the 1459 epoch
// semantics into the object revision: it is the SAME counter as
// continuationToken (no second source of truth), renamed at the object
// boundary because install/clear/stop/pause/resume/advance/restore all move it.
func (g *goalMachine) revision() uint64 {
	return g.continuationToken()
}

// goalModelCreateRefusal explains why a model create_goal was refused. The
// placeholder lock (迁移裁决⑤) admits replacement only from complete.
type goalModelCreateRefusal struct {
	cause  string
	detail string
}

// createByModel installs a model-authored goal (双通道·模型通道). The
// placeholder lock refuses silently replacing a live goal: an existing goal in
// any phase other than complete blocks creation with a readable cause; a
// completed (or absent) goal may be replaced. On success the goal is
// active+armed with its digest stored (DSH 参照：模型 create 即 active+armed).
func (g *goalMachine) createByModel(text, summary string, todos []evidence.TodoItem) (bool, goalModelCreateRefusal, string, []byte, bool) {
	text = strings.TrimSpace(text)
	summary = strings.TrimSpace(summary)
	g.mu.Lock()
	defer g.mu.Unlock()
	if text == "" {
		return false, goalModelCreateRefusal{
			cause:  "empty_goal",
			detail: "create_goal: the goal text is empty — no goal was created",
		}, "", nil, false
	}
	if strings.TrimSpace(g.goal) != "" && g.phaseLocked() != GoalPhaseComplete {
		return false, goalModelCreateRefusal{
			cause: "goal_occupied",
			detail: "create_goal: this session already holds a live goal (" +
				g.phaseLocked() + "): \"" + g.summaryTextLocked() + "\" — no goal was replaced. " +
				"Only a completed goal can be replaced by the model; finish it, or ask the user to clear it with /goal clear before creating a new one.",
		}, "", nil, false
	}
	g.installGoalLocked(text, ClassifyGoalBudget(text))
	g.summary = summary
	g.setActivationLocked(true, goalActivationActorModel, "create_goal")
	path, data, ok := g.buildStateLocked(todos)
	return true, goalModelCreateRefusal{}, path, data, ok
}

func (g *goalMachine) summaryTextLocked() string {
	if s := strings.TrimSpace(g.summary); s != "" {
		return s
	}
	return deriveGoalSummary(g.goal)
}

// turnNumberForReport returns the in-flight goal turn's 1-based number for the
// blocked-report threshold, and whether the turn is still active.
func (g *goalMachine) turnNumberForReport(scopeID string, epoch uint64) (int, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.TrimSpace(g.goal) == "" || g.status != GoalStatusRunning ||
		g.scopeID != scopeID || g.continuationEpoch != epoch {
		return 0, false
	}
	return g.turnsUsed + 1, true
}

// GoalObject is the frontend port over the visibility carrier (迁移裁决⑥):
// phase AND activation together — showing only the running half would
// replicate the 1459 mirror incident.
func (c *Controller) GoalObject() GoalObjectView {
	return c.goals.objectView()
}

// disarmGoalForIncomingMessage is disarm 全集①: any real user message (composer
// submit, remote submit, direct user turn, accepted steer) withdraws the
// auto-continue permit before the turn runs, so a user takeover can never be
// followed by the old goal's loop. The goal object stays active+visible;
// /goal resume re-arms it. Exception (stint grant): the first user message
// after an explicit human /goal set or /goal resume is that stint's input,
// not a takeover — see armForUserTurnGrant. Returns whether a disarm actually
// happened.
func (c *Controller) disarmGoalForIncomingMessage(channel string) bool {
	if c.goals.armForUserTurnGrant() {
		return false
	}
	if !c.goals.active() {
		return false
	}
	path, data, ok := c.goals.disarm(goalActivationActorUser, "user_message", c.goalTodos())
	c.persistGoalState(path, data, ok)
	c.emitGoalNotice(event.NoticeCodeGoalDisarmed, event.LevelInfo,
		i18n.M.GoalDisarmed,
		goalNoticeDetail("user_message", c.goals.statusForDisplay(), channel))
	return true
}

// markGoalCreatedByModel records that the running turn installed a
// model-authored goal, so the loop's advance step treats the turn as the
// goal's first turn instead of reporting a recorder-less gate exit.
func (c *Controller) markGoalCreatedByModel() {
	c.mu.Lock()
	c.goalCreatedByModelThisTurn = true
	c.mu.Unlock()
}

func (c *Controller) consumeGoalCreatedByModel() bool {
	c.mu.Lock()
	marked := c.goalCreatedByModelThisTurn
	c.goalCreatedByModelThisTurn = false
	c.mu.Unlock()
	return marked
}

// CreateGoalByModel is the model channel of 双通道创建 (DSH 参照): the model
// may install a goal any time — even without goal mode — but it can never
// silently replace a live one (占位锁, 迁移裁决⑤). The returned string is the
// tool-result text shown to the model.
func (c *Controller) CreateGoalByModel(text, summary string) (string, error) {
	accepted, refusal, path, data, ok := c.goals.createByModel(text, summary, c.goalTodos())
	if !accepted {
		return "", fmt.Errorf("%s", refusal.detail)
	}
	c.persistGoalState(path, data, ok)
	c.markGoalCreatedByModel()
	summaryShown := c.goals.summaryText()
	c.emitGoalNotice(event.NoticeCodeGoalCreated, event.LevelInfo,
		fmt.Sprintf(i18n.M.GoalCreatedFmt, ShortGoalForNotice(summaryShown)),
		goalNoticeDetail("model_created", GoalStatusRunning, ""))
	return "create_goal: the goal is now active and armed — the host continues it automatically after this turn ends. Do not call update_goal for the turn that created the goal (no goal turn was bound); pursue the goal from the next automatic turn onward. The sidebar shows the summary: " + summaryShown, nil
}
