package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"reasonix/internal/provider"
)

// 任务978 实验开关（默认关）：REASONIX_EXPERIMENT_TL978_EARLY_FOLD=1 时生效。
// 治理对象是「投影失效回退全量」（tl 正文所谓 token_mode=full 全量暴露档）会话的
// 压缩重建迟滞：视图回退全量后，压缩是唯一的回投影正道，但原 0.8×窗口触发线让
// canonical 在回退态多涨一整段——摘要请求随体积变大，越窗 400/超时的概率随之
// 上升，「越大越压不动」的恶性循环入口就在这里（ai-passport 20261018 现场
// fold_tokens=1501109 才落成，对照投影侧车 54536）。
var earlyFoldEnabled = os.Getenv("REASONIX_EXPERIMENT_TL978_EARLY_FOLD") == "1"

const (
	// fallbackEarlyFoldRatio 把回退态触发线压到原线的这一比例：0.80×0.75=0.60×
	// 窗口，与任务719 受限尾视图的 tailViewTargetShare 同档——回退态的会话
	// 在 60% 窗口处开始重建投影，摘要输入更小、一次成功率更高。投影有效态
	// 不受影响：健康会话的 0.8 线原样保留（正常小会话不得被反复压缩是本件
	// 最大回归红线，TestTask978HealthyProjectionNeverEarlyFolds 钉住）。
	fallbackEarlyFoldRatio = 0.75
	// maintenanceRetryGrowthRatioFallback 是回退态下同 turn 重试门的增长门槛
	// （原 5% 见 maintenanceRetryGrowthRatio）。回退全量态里每 1% 窗口的涨量
	// 都是真实发送体积的增长，等满 5%（1M 窗=50K tokens）才允许重试意味着
	// 全量视图要多发好几轮；压到 1% 让重试随涨量尽快发生。一次 Prepare 仍然
	// 只跑一次 ladder，且受维护单飞门（任务920）与 failedTurn 记录约束，
	// 不会退化成摘要请求风暴（978 现场「请求数+12」的教训）。
	maintenanceRetryGrowthRatioFallback = 0.01
)

// projectionFallbackActive reports the tl-978 full-fallback state: the
// projection is unusable AND the canonical transcript still fits under the
// hard input ceiling. Past the ceiling 任务719's bounded tail view owns the
// request and est >= hard force-folds regardless of the trigger line, so the
// moved trigger only needs to speak for the in-between state — the exact
// window where the raw full transcript keeps getting sent (烧缓存、涨 events)
// while compaction waits for the 0.8 line.
func (a *Agent) projectionFallbackActive() bool {
	if a == nil || a.sess.conversation == nil {
		return false
	}
	if a.sess.tailView.active() {
		return false
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	return !projectionValid(st, msgs)
}

// compactionProgress is how compaction is faring in this session: whether a
// fold stopped reducing, how many ran back to back, and which retries already
// ran in the active turn. The fields are cleared together on lineage resets.
type compactionProgress struct {
	stuck          bool   // a fold landed above the trigger, so the same-view pressure retry is pointless
	stuckInputHash string // provider-visible view covered by stuck; changed input may retry
	consecutive    int    // back-to-back folds since one last helped
	// failedTurn backs off changed-view retries within one active tool loop.
	// A later user turn may retry, while hard-ceiling recovery bypasses it.
	failedTurn atomic.Int64
	// 任务867: consecutive failed compaction passes, surfaced as the "Nth
	// attempt" counter on the failed CompactionDone event; success resets it.
	failureStreak atomic.Int64
	// lastTurn stops the post-turn observer and the pre-send preflight from
	// paying for two summaries during one active tool loop.
	lastTurn atomic.Int64
	// 任务461-P13② growth watch: lastPrepareEst is the previous maintenance
	// check's context estimate; growthWarnedTurn/lastGrowthWarnAt dedupe the
	// sharp-growth warning. All three are only touched under compactionRunMu
	// (prepareOnce → observeContextGrowth), so plain fields suffice.
	lastPrepareEst   int64
	growthWarnedTurn int64
	lastGrowthWarnAt time.Time
	// 任务516④ fallback watch: fallbackWarnAt throttles the "view fell back to
	// the full canonical transcript" warning to one line per cooldown. Same
	// compactionRunMu discipline as the growth-watch fields.
	fallbackWarnAt time.Time
}

// ContextManager is the sole owner of provider-visible context maintenance.
// Canonical session messages are immutable inputs; Prepare evolves only the
// durable projection and returns the exact visible view for one sampling round.
type ContextManager struct {
	agent *Agent
}

// ContextPreparePolicy describes one maintenance transaction.
type ContextPreparePolicy struct {
	Trigger      string
	Instructions string
	Force        bool
	// ObservedInputTokens is used by compatibility harnesses that invoke the
	// old post-turn shim directly. Production Prepare estimates the current view
	// from its calibrated final request shape.
	ObservedInputTokens int
	// AllowChunkedFallback enables fragment/tree-reduce recovery after a single
	// summary fails. Ordinary pressure/overflow leave this false.
	AllowChunkedFallback bool
}

// PreparedContext is the frozen result of a successful Prepare transaction.
type PreparedContext struct {
	Messages          []provider.Message
	InputTokens       int
	ProjectionVersion uint64
}

func (a *Agent) contextManager() ContextManager { return ContextManager{agent: a} }

// PrepareContext is the public automatic-maintenance entry used by smoke tools
// and controllers that need a one-shot Prepare without sampling.
func (a *Agent) PrepareContext(ctx context.Context) error {
	_, err := a.contextManager().Prepare(ctx, ContextPreparePolicy{Trigger: CompactionTriggerPressure})
	return err
}

// ObserveUsage is retained as a compatibility hook. Usage observations never
// mutate the provider-visible checkpoint.
func (m ContextManager) ObserveUsage(u *provider.Usage) {
	_ = u
}

// Prepare is the sole automatic maintenance entry. Below compact_ratio it does
// nothing. At or above the trigger it runs one single-flight prune/summary
// transaction, with at most two successful summary attempts under pressure.
func (m ContextManager) Prepare(ctx context.Context, policy ContextPreparePolicy) (PreparedContext, error) {
	// Legacy desktop callers can compact before their runtime context is installed.
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PreparedContext{}, err
	}
	if policy.Trigger == "" {
		policy.Trigger = CompactionTriggerPressure
	}
	if m.agent == nil {
		return PreparedContext{}, nil
	}
	m.agent.sess.compactionRunMu.Lock()
	defer m.agent.sess.compactionRunMu.Unlock()
	// Cancellation may have arrived while another maintenance transaction held the lock.
	// Reject it before any fast path or projection maintenance can run.
	if err := ctx.Err(); err != nil {
		return PreparedContext{}, err
	}
	return m.prepareOnce(ctx, policy)
}

func (m ContextManager) prepareOnce(ctx context.Context, policy ContextPreparePolicy) (PreparedContext, error) {
	a := m.agent
	if a == nil || a.sess.conversation == nil {
		return PreparedContext{}, nil
	}
	visible := a.modelVisibleMessages()
	// Threshold uses the stable pre-interceptor request shape (messages + tools
	// + role projection). Extension interceptors run only on the real sampling
	// request so side-effecting plugins are not double-invoked; if they expand
	// the prompt past the hard ceiling, overflow recovery still fires.
	est := a.estimatedVisibleRequestTokens(visible)
	viewEst := est
	prepared := PreparedContext{
		Messages:          append([]provider.Message(nil), visible...),
		InputTokens:       est,
		ProjectionVersion: a.currentProjectionVersion(),
	}
	if a.effectiveContextWindow() <= 0 || len(visible) == 0 {
		return prepared, nil
	}
	// 任务 1183: when the provider's own usage anchors the context size, the
	// trigger judges on that real count instead of the local estimate — the
	// estimate's ~7% undershoot (tokenizer, image and tool-definition drift)
	// let sessions ride past the configured ratio. Every threshold below (fold
	// trigger, hard ceiling, rescue eligibility) reads this one value; without
	// a trusted baseline the estimate stands exactly as before. The 978
	// early-fold line below keeps reshaping `fold` only — threshold geometry
	// and measurement caliber are orthogonal.
	if obs, ok := a.observedContextTokens(visible); ok {
		est = obs
		prepared.InputTokens = est
	}
	fold := a.compactTrigger()
	hard := a.hardInputCeiling()
	// 任务978（实验开关默认关）：回退全量态把触发线前移到 0.60×窗口，让投影
	// 重建在 canonical 更小时启动。投影有效态走原线——这条分支必须永远摸不到
	// 健康会话，见 fallbackEarlyFoldRatio 与 TestTask978HealthyProjectionNeverEarlyFolds。
	if earlyFoldEnabled && a.projectionFallbackActive() {
		fold = max(1, int(float64(fold)*fallbackEarlyFoldRatio))
	}
	// 任务822: every threshold comparison below (fold trigger, hard ceiling,
	// backoff bypass, rescue eligibility) must read the session's physical
	// occupancy, not the degraded view's clamped size. 任务719's bounded tail
	// view only shrinks the request payload — an active degraded view means
	// the canonical transcript sits at or above the hard ceiling, so judging
	// by the clamped view lets a session sit past thresholds it can never
	// re-cross: the fold never triggers, ceiling rescues never fire, and the
	// failed-receipt backoff holds forever. Observed provider usage keeps
	// priority: it is the authoritative measurement the maintenance contract
	// (issue 7935) is written against.
	//
	// tl1203: a stale-serving projection body is the same shape — the old fold
	// shrinks the request payload while the canonical transcript is the size
	// the rebuild must shrink, so the physical occupancy owns the thresholds
	// here too (otherwise the forced stale rebuild parks with no pressure).
	if policy.ObservedInputTokens <= 0 && (a.sess.tailView.active() || a.staleProjectionServing()) {
		est = a.maintenanceOccupancyEstimate()
		prepared.InputTokens = est
	}
	if policy.ObservedInputTokens > 0 {
		est = policy.ObservedInputTokens
		prepared.InputTokens = est
	}
	inputHash := a.contextMaintenanceInputHash(visible)
	// 任务461-P13② growth watch: record the estimate delta since the previous
	// maintenance check BEFORE any early return, so a view that balloons inside
	// one tool loop (replayed guidance, repeated sub-agent payloads) leaves a
	// log trail even when it stays below the fold trigger.
	a.observeContextGrowth(est)
	a.observeProjectionFallback(est)
	// Receipts back off sub-critical retries only. A failed summary never
	// fabricates a digest; at the ceiling the lossy truncation rescue is the
	// last resort, so the turn still leaves with a view the provider accepts.
	if blocked, reason := a.contextMaintenanceBlocked(inputHash, viewEst); blocked && policy.Trigger != CompactionTriggerManual &&
		policy.Trigger != CompactionTriggerOverflow && est < hard {
		// 任务912 P1-c: a backed-off view is still the whole transcript. Once
		// the failure is a pattern the turn must not leave with it — cap it to
		// the sliding window instead of handing back the full history.
		if capped, ok := m.slidingWindowFallback(policy, errors.New(reason)); ok {
			return capped, nil
		}
		return prepared, nil
	}
	if est < fold {
		a.resetCompactionProgress()
	}
	if a.sess.compaction.stuck && a.sess.compaction.stuckInputHash != inputHash {
		// The previous projection could not reclaim enough from its exact view,
		// but newly appended messages create a new fold boundary and may retry.
		a.sess.compaction.stuck = false
		a.sess.compaction.stuckInputHash = ""
		a.sess.compaction.consecutive = 0
	}
	if a.sess.compaction.stuck && policy.Trigger == CompactionTriggerPressure && est < hard {
		return prepared, nil
	}
	// One user trigger. Overflow is a one-shot physical recovery path only.
	// 任务822: the 任务719 tailView.active() term is gone — with the estimate
	// above reading the physical occupancy, a degraded view lands in
	// est >= hard on its own (the view only degrades at or above the hard
	// ceiling), and the old term wrongly force-folded sessions whose observed
	// usage sat below the fold trigger (the issue 7935 contract).
	forceFold := policy.Force || policy.Trigger == CompactionTriggerManual || policy.Trigger == CompactionTriggerOverflow || est >= hard
	if est < fold && !forceFold {
		return prepared, nil
	}

	// 任务920 压缩单飞门：过了 above-trigger 快路径 = 本事务一定会做真实的
	// prune/fold/rescue 工作（分钟级摘要窗口从这里开始）。维护 in-flight 计数
	// 在每个出口（含 panic 的 defer）归零并触发宿主 settle 钩子，控制器据此把
	// 压缩窗口内新提交的轮次排队，压完统一放行——治「在途注入窗口」家族。
	// 快路径（低于触发线/被 block 的视图）不碰计数，普通采样轮零门成本（验收②）。
	a.maintaining.Add(1)
	defer a.endMaintenance()

	// A manual compact over the hard ceiling is a rescue, not a convenience:
	// prune first so the never-folded recent tail can shrink too.
	if shouldPruneBeforeFold(policy.Trigger, est >= hard) {
		applied, err := a.pruneToolResultsToProjectionLocked(policy.Trigger)
		if err != nil {
			return PreparedContext{}, err
		}
		if applied {
			prepared = m.currentPrepared()
			// 任务822: re-read the physical occupancy, not the refreshed view —
			// the rescue-level decisions below (maxSummaries, ladder-end
			// truncation) must keep seeing the pre-prune overshoot the view
			// alone can no longer express.
			est = a.maintenanceOccupancyEstimate()
			inputHash = a.contextMaintenanceInputHash(prepared.Messages)
			if (policy.Trigger == CompactionTriggerPressure && est < fold) ||
				(policy.Trigger == CompactionTriggerOverflow && est < hard) {
				return prepared, nil
			}
		}
	}

	return m.foldContext(ctx, prepared, policy, inputHash, est, fold, hard, forceFold)
}

func shouldPruneBeforeFold(trigger string, overHardCeiling bool) bool {
	switch trigger {
	case CompactionTriggerPressure, CompactionTriggerOverflow:
		return true
	case CompactionTriggerManual:
		return overHardCeiling
	default:
		return false
	}
}

// manualRecoverySummaries bounds the rescue loop for a manual compact that
// starts at or above the hard input ceiling. Each batch folds the largest
// admissible prefix, so a handful of batches recovers even a view several
// times the window while capping summarizer spend on pathological input.
const manualRecoverySummaries = 4

func maxSummariesFor(policy ContextPreparePolicy, overCeiling bool) int {
	switch {
	case policy.Trigger == CompactionTriggerManual && overCeiling:
		return manualRecoverySummaries
	case policy.Trigger == CompactionTriggerPressure:
		return 2
	default:
		return 1
	}
}

func (m ContextManager) foldContext(ctx context.Context, prepared PreparedContext, policy ContextPreparePolicy, inputHash string, est, fold, hard int, forceFold bool) (PreparedContext, error) {
	a := m.agent
	maxSummaries := maxSummariesFor(policy, est >= hard)
	ladder := newSummaryLadder(maxSummaries)
	result := prepared
	// Task 303: tracks whether an earlier ladder round already installed a
	// fold, so a later round's summary failure is reported as a summary-only
	// problem (compaction kept) instead of "the compaction failed".
	foldInstalled := false
	for ladder.next() {
		mustFree := policy.Trigger == CompactionTriggerOverflow || result.InputTokens >= hard
		outcome, err := a.compactToProjectionLocked(ctx, policy.Trigger, policy.Instructions,
			ladder.request(forceFold, mustFree, policy.AllowChunkedFallback))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return PreparedContext{}, err
			}
			if ladder.absorbOverflow(err) {
				continue
			}
			return m.summaryFailed(policy, inputHash, hard, est, foldInstalled, err)
		}
		if outcome == CompactionNoop {
			return m.summaryNoop(policy, inputHash, hard, est)
		}
		foldInstalled = true

		result = m.currentPrepared()
		if foldLanded(policy, result.InputTokens, fold, hard) {
			a.resetCompactionProgress()
			// 任务 801（决策模式提取 MVP）：压缩成功落地 = C 混合触发的主腿。
			// 开关关闭时该调用在首个分支返回，零开销零行为；开启时异步扫描，
			// 任何失败只记日志，绝不影响压缩结果（禁区：提取失败不阻断）。
			a.maybeExtractDecisionPatterns(true)
			return result, nil
		}
		forceFold = false
		inputHash = a.contextMaintenanceInputHash(result.Messages)
	}

	reason := fmt.Sprintf("summary result remains above fold trigger after %d attempts (%d >= %d)", maxSummaries, result.InputTokens, fold)
	blockedInputHash := a.contextMaintenanceInputHash(result.Messages)
	a.recordContextMaintenanceBlocked(blockedInputHash, policy.Trigger, "summary", reason)
	a.sess.compaction.stuck = true
	a.sess.compaction.stuckInputHash = blockedInputHash
	a.sess.compaction.consecutive += maxSummaries
	if policy.Trigger == CompactionTriggerOverflow || result.InputTokens >= hard {
		return m.rescueByTruncation(policy, hard, errors.New(reason))
	}
	slog.Info("agent: context maintenance paused below hard ceiling", "reason", reason)
	// 任务912 P1-c: the ladder exhausted and the view is still the whole
	// transcript. 永不全量 — after N consecutive failures the turn leaves with
	// the capped sliding window, never the full canonical history.
	if capped, ok := m.slidingWindowFallback(policy, slidingWindowCapCause(errors.New(reason))); ok {
		return capped, nil
	}
	return result, nil
}

func foldLanded(policy ContextPreparePolicy, tokens, fold, hard int) bool {
	switch policy.Trigger {
	case CompactionTriggerManual, CompactionTriggerOverflow:
		return tokens < hard || tokens < fold
	default:
		return tokens < fold
	}
}

func (m ContextManager) summaryFailed(policy ContextPreparePolicy, inputHash string, hard, est int, foldInstalled bool, err error) (PreparedContext, error) {
	a := m.agent
	if errors.Is(err, errCompressStaleContext) && policy.Trigger != CompactionTriggerManual {
		reason := "context changed during summary; automatic retry blocked for this generation"
		slog.Warn("agent: context summary blocked", "reason", reason, "trigger", policy.Trigger, "fold_installed", foldInstalled)
		a.recordContextMaintenanceBlocked(inputHash, policy.Trigger, "summary", reason)
		return m.rescueOrFail(policy, hard, est, errors.New(reason))
	}
	status := "failed"
	if errors.Is(err, errSummaryOutputTruncated) || errors.Is(err, errCheckpointRejected) {
		status = "blocked"
	}
	// Task 303: compaction result and summary result are separate outcomes.
	// When an earlier ladder round already installed the fold, this round's
	// failure (typically a provider stream error mid-summary — the mimo
	// INTERNAL_ERROR sample) must not present as the compaction failing: the
	// projection is live, only the summary refresh is missing, and it stays
	// retryable on the next generation.
	reason := fmt.Sprintf("context summary failed: %v", err)
	if foldInstalled {
		reason = fmt.Sprintf("summary refresh failed after fold was applied (compaction kept): %v", err)
	}
	slog.Warn("agent: context summary failed",
		"status", status, "trigger", policy.Trigger, "fold_installed", foldInstalled,
		"model_ref", a.destinationModelRef(),
		"transient_stream", summaryTransientRetryable(err), "err", err)
	a.recordContextMaintenanceOutcome(inputHash, policy.Trigger, "summary", status, reason, foldInstalled)
	// Task 307: a candidate at or above the physical ceiling would be rejected
	// again with the very same oversized projection, so waiting for the view
	// itself to cross the ceiling (rescueOrFail's below-hard path) only lets
	// the context keep growing - the 17:50 event did exactly that: three
	// back-to-back rejections and no rescue, on to 3.8M tokens. End the loop
	// on the first rejection: truncation is the same recovery overflow uses.
	// Manual compaction never reaches this branch (the ceiling check skips
	// manual triggers) and keeps its fail-visible semantics.
	// Task 516: the acceptance path now installs partial progress instead of
	// producing this rejection, so the branch is defensive only — it stays so a
	// re-introduced ceiling rejection can never regress into the 17:50 loop.
	if errors.Is(err, errCheckpointCeiling) && policy.Trigger != CompactionTriggerManual {
		return m.rescueByTruncation(policy, hard, err)
	}
	return m.rescueOrFail(policy, hard, est, err)
}

func (m ContextManager) summaryNoop(policy ContextPreparePolicy, inputHash string, hard, est int) (PreparedContext, error) {
	// Task 424: the reason doubles as the ErrNoFoldableRegion sentinel so
	// failure consumers recognise the terminal "nothing foldable left" class
	// without string matching (the desktop cold-cache loop parks on it).
	m.agent.recordContextMaintenanceBlocked(inputHash, policy.Trigger, "summary", ErrNoFoldableRegion.Error())
	latest := m.currentPrepared()
	// 任务822: the ceiling comparison reads the physical occupancy — a clamped
	// tail view can never reach hard, so the view-sized read would park a
	// physically-oversized session on a digest-less no-op forever.
	switch {
	case policy.Trigger == CompactionTriggerOverflow || est >= hard:
		// 任务1029①: 无 foldable region 的超窗会话同样走换道阶梯——只截不换
		// 会在"截不动"形态（保护尾巨 unit）下死循环到熔断。
		return m.rescueByTruncationOrSlidingWindow(policy, hard, ErrNoFoldableRegion)
	case policy.Force:
		return PreparedContext{}, fmt.Errorf("%w: %w", ErrCompactionRequired, ErrNoFoldableRegion)
	default:
		return latest, nil
	}
}

// rescueOrFail decides what a failed summary means: below the ceiling
// automatic maintenance waits for the next view and a manual compact reports
// the error; at or above the ceiling the lossy truncation rescue runs first
// and the sliding-window cap takes over when even that cannot land the view
// under the ceiling. 任务822: the ceiling read is per-trigger. Manual
// compaction is an explicit demand that the session return to an in-window
// track, so it reads the physical occupancy (est) — a clamped tail view never
// reaches hard, and the rescue would never fire. 任务719 made the bounded view
// admissible for automatic maintenance, so pressure keeps the view-sized read:
// the truncation rescue loses its premise when the turn already leaves with an
// admissible view (compact_summary_guard contract, 813 parked-episode pin).
func (m ContextManager) rescueOrFail(policy ContextPreparePolicy, hard, est int, cause error) (PreparedContext, error) {
	if policy.Trigger == CompactionTriggerOverflow {
		return m.rescueByTruncationOrSlidingWindow(policy, hard, cause)
	}
	overCeiling := m.currentPrepared().InputTokens >= hard
	if policy.Trigger == CompactionTriggerManual {
		overCeiling = est >= hard
	}
	if !overCeiling {
		if policy.Trigger == CompactionTriggerManual {
			return PreparedContext{}, cause
		}
		// 任务912 P1-c: below the ceiling a failed summary used to hand the turn
		// the whole transcript, every time, forever. One failure is recoverable;
		// after N in a row the view is capped to the sliding window instead.
		if capped, ok := m.slidingWindowFallback(policy, slidingWindowCapCause(cause)); ok {
			return capped, nil
		}
		return m.currentPrepared(), nil
	}
	return m.rescueByTruncationOrSlidingWindow(policy, hard, cause)
}

// rescueByTruncationOrSlidingWindow is the over-ceiling rescue ladder
// (任务1029①): the two degradation paths used to be mutually deadlocked. The
// truncation rescue cuts at replay-unit granularity, so a tail dominated by
// one giant replay unit (the 587K-char tool result sample) or by drop-
// protected session-context carriers stayed at e.g. 1066351 >= 1048320
// forever; slidingWindowFallback was reachable only below the ceiling; the
// turn kept failing and autopilot sealed the session (15 亿 tokens 烧穿).
// When truncation cannot land the view under the ceiling, switch lanes to the
// sliding-window cap — the same mechanism the below-ceiling path already
// uses, but gated on the over-ceiling fact itself instead of the failure
// streak: a view that physically cannot be sent is stronger evidence than
// three failed summaries.
func (m ContextManager) rescueByTruncationOrSlidingWindow(policy ContextPreparePolicy, hard int, cause error) (PreparedContext, error) {
	capped, truncErr := m.rescueByTruncation(policy, hard, cause)
	if truncErr == nil {
		return capped, nil
	}
	// 视图本就低于物理天花板时不存在"发不出"的死锁——重复的 overflow 信号
	// 落在已恢复的视图上属于无事可做，保持失败可见（同轮恢复契约：新的
	// provider-visible 输入才允许再次动手，compact_same_turn_overflow 契约）。
	if m.currentPrepared().InputTokens < hard {
		return PreparedContext{}, truncErr
	}
	fallback, _ := m.slidingWindowCap(policy, slidingWindowCapCause(cause))
	if fallback.InputTokens < hard {
		m.agent.resetCompactionProgress()
		return fallback, nil
	}
	return PreparedContext{}, truncErr
}

// rescueByTruncation installs the lossy truncation projection aimed at the
// fold trigger so the turn leaves the ceiling with headroom. cause is the
// summary failure it stands in for and stays in the error when even that fails.
func (m ContextManager) rescueByTruncation(policy ContextPreparePolicy, hard int, cause error) (PreparedContext, error) {
	a := m.agent
	applied, err := a.truncateToProjectionLocked(policy.Trigger, a.compactTrigger())
	if err != nil {
		return PreparedContext{}, fmt.Errorf("%w: %w (truncation: %w)", ErrCompactionRequired, cause, err)
	}
	if !applied {
		return PreparedContext{}, fmt.Errorf("%w: %w", ErrCompactionRequired, cause)
	}
	latest := m.currentPrepared()
	if latest.InputTokens >= hard {
		return PreparedContext{}, fmt.Errorf("%w: truncated view still %d >= %d", ErrCompactionRequired, latest.InputTokens, hard)
	}
	a.resetCompactionProgress()
	return latest, nil
}

func (a *Agent) resetCompactionProgress() {
	a.sess.compaction.stuck = false
	a.sess.compaction.stuckInputHash = ""
	a.sess.compaction.consecutive = 0
	a.sess.compaction.failedTurn.Store(0)
}

func (m ContextManager) currentPrepared() PreparedContext {
	if m.agent == nil {
		return PreparedContext{}
	}
	visible := m.agent.modelVisibleMessages()
	return PreparedContext{
		Messages:          append([]provider.Message(nil), visible...),
		InputTokens:       m.agent.estimatedVisibleRequestTokens(visible),
		ProjectionVersion: m.agent.currentProjectionVersion(),
	}
}

// estimatedVisibleRequestTokens sizes the pre-interceptor sampling shape:
// ModelMessages + role projection + tool schemas. Extension interceptors are
// intentionally omitted here (see prepareOnce) to avoid double side effects.
func (a *Agent) estimatedVisibleRequestTokens(visible []provider.Message) int {
	if a == nil {
		return 0
	}
	msgs := a.normalizeModelRequestMessages(visible)
	tools := a.providerToolSchemas()
	return a.estimatedRequestTokens(provider.Request{
		Messages:    msgs,
		Tools:       tools,
		MaxTokens:   a.destinationMaxOutputTokens(),
		Temperature: provider.OptionalTemperature(a.temperature),
	})
}

// contextGrowthWarnRatio is the share of the model window one maintenance
// interval may add before the growth watch speaks up (任务461-P13②). The
// 0.80 compaction trigger already bounds steady growth; this catches the
// pathological shape where a single tool loop injects a large fraction of the
// window between two checks — exactly the replay-storm feeding the user saw.
const contextGrowthWarnRatio = 0.40

// contextGrowthWarnCooldown throttles repeat warnings inside one turn so a
// continuously ballooning loop stays visible without spamming the log.
const contextGrowthWarnCooldown = 5 * time.Minute

// observeContextGrowth compares this maintenance check's context estimate with
// the previous one. A delta of at least contextGrowthWarnRatio × window logs a
// sharp-growth warning (deduped per turn, re-armed after the cooldown). It is
// pure observation: no behavior changes, it only gives the diagnosis a named
// entry point in the log alongside the existing maintenance receipts.
func (a *Agent) observeContextGrowth(est int) bool {
	if a == nil || est <= 0 {
		return false
	}
	window := a.effectiveContextWindow()
	if window <= 0 {
		return false
	}
	prev := a.sess.compaction.lastPrepareEst
	a.sess.compaction.lastPrepareEst = int64(est)
	if prev <= 0 || est <= int(prev) {
		return false
	}
	delta := est - int(prev)
	if delta < int(float64(window)*contextGrowthWarnRatio) {
		return false
	}
	now := time.Now()
	turn := a.activeTurnCreatedAt.Load()
	sameTurn := turn != 0 && turn == a.sess.compaction.growthWarnedTurn
	if sameTurn && now.Sub(a.sess.compaction.lastGrowthWarnAt) < contextGrowthWarnCooldown {
		return false
	}
	a.sess.compaction.growthWarnedTurn = turn
	a.sess.compaction.lastGrowthWarnAt = now
	slog.Warn("agent: context estimate grew sharply between maintenance checks",
		"prev_tokens", prev, "now_tokens", est, "delta_tokens", delta,
		"window", window, "turn", turn)
	return true
}

// projectionFallbackWarnCooldown throttles the invalid-projection warning so a
// stuck episode stays visible without spamming every maintenance check.
const projectionFallbackWarnCooldown = 5 * time.Minute

// 任务516④ fallback watch: after a lineage change (model switch, resume
// rebuild) or a history rewrite, a stale-or-missing projection legitimately
// serves the whole canonical transcript; the view can then dwarf the window
// while compaction looks healthy, and diagnosis had to infer the fallback from
// compaction source spikes. observeProjectionFallback names the reason once per
// cooldown. Pure observation: no behavior reads it.
//
// Gates keep normal states quiet: a body-less projection below the hard input
// ceiling is the ordinary fresh-session shape, and a valid projection re-arms
// the throttle so the next episode warns immediately.
func (a *Agent) observeProjectionFallback(est int) {
	if a == nil || a.sess.conversation == nil || est <= 0 {
		return
	}
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	window := a.effectiveContextWindow()
	if len(st.Projection.Messages) == 0 {
		// Fresh or invalidated session: only worth a line once the full
		// canonical view itself is past the physical ceiling (the 2.7M/1M shape).
		if est < a.hardInputCeiling() {
			return
		}
		a.warnProjectionFallback("no_projection", st, est, window)
		return
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	key := a.currentPromptCacheKeyLocked()
	if projectionContentValid(st, msgs) {
		// 任务638: a lineage-key change rebinds the cache namespace; it is not a
		// view fallback, so a content-valid projection stays quiet. Re-arm the
		// throttle so a later episode warns on its first check.
		a.sess.compaction.fallbackWarnAt = time.Time{}
		return
	}
	// Content drift: name whether the lineage key also moved.
	if key != "" {
		if _, ok := lineageKeyCompatible(st.PromptCacheKey, key); !ok {
			a.warnProjectionFallback("lineage_key_mismatch", st, est, window)
			return
		}
	}
	a.warnProjectionFallback("content_mismatch", st, est, window)
}

// warnProjectionFallback emits the throttled fallback warning.
func (a *Agent) warnProjectionFallback(reason string, st CompactionState, est, window int) {
	if !a.sess.compaction.fallbackWarnAt.IsZero() && time.Since(a.sess.compaction.fallbackWarnAt) < projectionFallbackWarnCooldown {
		return
	}
	a.sess.compaction.fallbackWarnAt = time.Now()
	// tl1203: with a stale body in memory the wire serves the old fold over
	// the canonical tail (never the full transcript), so the message names the
	// episode instead of claiming a full-canonical fallback. The body-less
	// shape (no_projection) still degrades per 任务719.
	slog.Warn("agent: context projection invalid — fallback view serves the wire",
		"reason", reason, "view_tokens", est, "window", window,
		"projection_version", st.Projection.ProjectionVersion,
		"prompt_cache_key", st.PromptCacheKey)
	// 任务1114: a real fallback episode with the canonical at or above the hard
	// ceiling is the 新9 shape (3.02M/1M with no auto convergence until manual
	// help) — arm the local overflow diet so the episode converges on its own.
	// The kick re-checks the ceiling, dedupes, and runs in the background.
	a.kickOverflowDiet(reason, est)
}
