package main

import (
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/secrets"
)

// 任务 921：存量超限 events 会话的空闲期兜底清（884 遗留①）——auto 模式下空闲期
// 强制维护重写（CompactSessionFile），接管 fold-no-shrink WARN 的真超限会话与
// 「无 save 无旋转」的死会话。完整设计与实测见 tmp/handoff-921.md。

const (
	// 首扫延迟：给启动期让路（配置加载、tab 恢复），也给「刚重启的会话」
	// 一个被 lease 保护的窗口。
	eventsIdleSweepFirstDelay = 5 * time.Minute
	// 常规 tick。比 cold-cache 的 10 分钟稀：本循环每次可能 load+rewrite
	// 百 MB 级会话，超限存量又是低频事件。
	eventsIdleSweepTickInterval = 30 * time.Minute
	// 每 tick 处理上限：CompactSessionFile 是全量 load+rewrite，串行限量
	// 防止升级后首扫面对大批量超限存量时一次性堆叠 IO。
	eventsIdleSweepMaxPerPass = 3
	// park 重入线：清完后日志再涨 10%（或会话有新活动）才允许重清——
	// 与 fold-no-shrink WARN 的重警增长（710，1.10）同一语义，防止
	// 「清完又清」的每 tick 重写循环。
	eventsIdleSweepParkRegrow = 1.10
	// park 表防御性帽：会话数远小于此；撞帽整体重置（代价只是至多一次
	// 重复清理），不做逐条淘汰猜测（与 warnRotateSkipOnce 同一纪律）。
	eventsIdleSweepParkMax = 512
	// tl 1172 busy 维护窗口：连续 busy 达到该 tick 数（4 tick ≈ 2 小时）后，
	// 本进程 tab 打开的超限会话获得一次 L2 压缩机会。busy 的两种来源分开
	// 处理：本进程 tab（lease 是我们自己的，文件锁保证压缩与保存互斥，安全）
	// 与外部 lease（别的运行时在写，压缩会迫使其下一次保存走整场冲突重放，
	// 得不偿失 ⇒ 不做，等 lease 空闲的 tick）。
	eventsIdleSweepBusyWindowTicks = 4
)

// eventsIdleSweepSlim carries the tl-1172 degradation ladder wiring for one
// pass. 零值 = 关（experimental_events_slim_oversize 默认关）：关闭时 pass 的
// 判定与动作和引入前逐字节一致。
type eventsIdleSweepSlim struct {
	Enabled bool
	// CapBytes 是 L3 的触发线：L2 压缩后归档字节数仍超 cap 才裁剪。
	CapBytes int64
	// Compress 是 L2（agent.CompressSessionEventLogZst）；Trim 是 L3
	// （agent.TrimSessionOversizeToolResults）。注入以便测试驱动。
	Compress func(path string) (int64, int64, error)
	Trim     func(path string) (int64, int64, int, error)
}

// eventsIdleSweepConfig is the effective knob set for one tick: the sweep
// exists only under auto rotation with a cap — off/manual are explicit user
// choices (task 333: manual keeps today's behaviour, entries stay in the
// storage panel), so the sweep never overrides them.
type eventsIdleSweepConfig struct {
	enabled bool
	factor  float64
	capMB   int64
	// slim 是 tl-1172 三级降级的开关面（experimental_events_slim_oversize），
	// 只在 auto+cap 主开关之下生效（本 sweep 的存在前提不变）。
	slim bool
}

func loadEventsIdleSweepConfig() eventsIdleSweepConfig {
	cfg, err := config.Load()
	if err != nil {
		return eventsIdleSweepConfig{}
	}
	return eventsIdleSweepFromConfig(cfg)
}

// eventsIdleSweepFromConfig is the knob judgment in one pure function: the
// sweep exists only under auto rotation with a cap — off/manual are explicit
// user choices (task 333), so it never overrides them. cap 是 fold-no-shrink
// 判定和本 sweep 的共同前提：无 cap 的 auto 模式只有因子判据，保存路径已
// 在管，本循环没有额外可清的形态。nil/err 读作全关。
func eventsIdleSweepFromConfig(cfg *config.Config) eventsIdleSweepConfig {
	if cfg == nil || config.EventsAutoRotationMode(cfg) != config.EventsAutoRotationAuto {
		return eventsIdleSweepConfig{}
	}
	factor, capMB := eventsRotationJudgmentFrom(cfg)
	return eventsIdleSweepConfig{enabled: capMB > 0, factor: factor, capMB: capMB, slim: cfg.ExperimentalEventsSlimOversize}
}

// eventsIdleSweepPark records the last completed sweep of one session: the
// log size it landed at and the activity stamp it was made for. Re-entry
// needs either ~10% regrowth or new activity — whichever comes first.
type eventsIdleSweepPark struct {
	attemptSize int64
	activityAt  int64
}

// eventsIdleSweepCandidate is one session's sweep input: plain stats plus the
// busy mark and the sidecar multi-head mirror, before any judgment. Factored
// so tests can drive the pass without a live session catalog.
type eventsIdleSweepCandidate struct {
	Path           string
	EventsBytes    int64
	LiveBytes      int64
	LastActivityAt int64
	Busy           bool
	// MultiHead mirrors sidecar HeadCount>1 (schema-2 only): the only
	// sessions where a stale concurrent head can be hiding behind the bytes
	// the sweep is about to rewrite (task 945).
	MultiHead bool
	// TabOpen marks a busy session whose lease is held by THIS process's own
	// tab (tl 1172): the busy-window slim path may compress it between turns —
	// the session file lock serializes against the tab's own saves. A busy
	// session WITHOUT TabOpen is lease-held by another runtime and is never
	// touched while busy.
	TabOpen bool
}

// eventsIdleSweepDecision is the whole per-session policy in one testable
// function: it reports whether the sweep may rewrite the session right now,
// and why not otherwise (the skip taxonomy the pass and the tests share).
//
// Park semantics mirror coldCacheCompactDecision's once-per-window guard,
// keyed on the size the last sweep landed at instead of an attempt stamp:
// a swept session whose log stayed over cap (真超限类, rotate cannot get
// under cap) must not be rewritten every tick — the rewrite freed everything
// it could, doing it again frees nothing but IO. New activity or ~10% regrowth
// is the re-arm signal.
//
// Task 1073 P0-1: a fold-profitability gate (agent.EventsFoldProfitable, the
// SAME expression the save path uses) sits here as a second over-limit line:
// over the cap but the fold would save <10% ⇒ skip ("fold would not shrink"),
// because a rewrite that cannot shrink the log frees nothing but IO (73
// measured no-gain sweeps freed [−217, 0] bytes each). This guard was missing
// on the sweep path — the save path had it since task 339 — which is why the
// sweep kept re-attempting sessions that were already at the fold floor.
func eventsIdleSweepDecision(enabled bool, overLimit, busy, foldProfitable bool, currentSize int64, park *eventsIdleSweepPark, lastActivityAt int64) (bool, string) {
	if !enabled {
		return false, "auto rotation off"
	}
	if !overLimit {
		return false, "under limit"
	}
	if !foldProfitable {
		return false, "fold would not shrink"
	}
	if busy {
		return false, "session busy"
	}
	if park != nil && park.activityAt == lastActivityAt &&
		currentSize < int64(float64(park.attemptSize)*eventsIdleSweepParkRegrow) {
		return false, "parked: awaiting regrowth or new activity"
	}
	return true, "eligible"
}

// eventsIdleSweepResult reports one sweep attempt for the accounting log,
// reusing the storage-panel result shape so both surfaces read the same.
type eventsIdleSweepResult = SessionEventsCompactResult

// eventsIdleSweepSkipReasons is the fixed skip taxonomy the pass counts per
// tick (task 1073 P0-2). "under limit" gets its own bucket because it is the
// overwhelming majority of the ~130 candidates a tick walks — folding it into
// one total would drown the rare, actionable reasons (busy/parked/no-gain).
// tl 1172 adds the two slim-side refusal buckets: foreign-lease busy（不与在写
// 运行时争抢）与压缩失败（下 tick 重试）。
var eventsIdleSweepSkipReasons = []string{
	"auto rotation off", "under limit", "fold would not shrink",
	"session busy", "parked: awaiting regrowth or new activity",
	"slim: foreign lease busy", "slim: compress failed (will retry)",
}

// runEventsIdleSweepPass runs one sweep pass over pre-collected candidates:
// over-limit judgment, decision, then the injected compact action — largest
// first so the per-pass budget lands on the biggest offenders, capped at
// eventsIdleSweepMaxPerPass rewrites. Both actions are injected so tests can
// drive pass behaviour without a live session; the production wiring passes
// agent.CompactSessionFile and agent.RetireStaleConcurrentHeadsNow.
//
// Task 945: a multi-head candidate about to be rewritten retires its stale
// concurrent heads first — rotation only drops bytes a retired head no longer
// makes reachable. A retire failure never blocks the rewrite.
//
// park updates happen here (success records the landed size; lease-busy and
// failures leave the park untouched so the next tick retries — same contract
// as runEventsRepairRounds' skipped/failed rows).
// runEventsIdleSweepPass runs one sweep pass over pre-collected candidates:
// over-limit judgment, decision, then the injected compact action — largest
// first so the per-pass budget lands on the biggest offenders, capped at
// eventsIdleSweepMaxPerPass rewrites. Both actions are injected so tests can
// drive pass behaviour without a live session; the production wiring passes
// agent.CompactSessionFile and agent.RetireStaleConcurrentHeadsNow.
//
// Task 945: a multi-head candidate about to be rewritten retires its stale
// concurrent heads first — rotation only drops bytes a retired head no longer
// makes reachable. A retire failure never blocks the rewrite.
//
// park updates happen here (success records the landed size; lease-busy and
// failures leave the park untouched so the next tick retries — same contract
// as runEventsRepairRounds' skipped/failed rows).
//
// tl 1172 三级降级（slim.Enabled 时；关态本函数行为与引入前逐字节一致）：
// L1 折叠仍是第一级（10% 门槛原样保留——门槛拒绝落在 "fold would not shrink"
// 跳过，这正是被降级接管的对象）：
//   - L2 整文件 zstd 归档（agent.CompressSessionEventLogZst，无损可逆）：
//     空闲会话直接压；本进程 tab 打开的会话连续 busy 满
//     eventsIdleSweepBusyWindowTicks 后获得一次维护窗口；外部 lease busy
//     不碰（不与在写运行时争抢）；
//   - L3 确定性裁剪（agent.TrimSessionOversizeToolResults，有损、头尾+标记）：
//     仅当 L2 后归档字节数仍超 slim.CapBytes；
//   - L1 跑了但没省到字节（freed ≤ 0：sweep 的 live 代理与真实 rotated
//     content 判断分歧的形态）⇒ 同样降级 L2——任一级真正省到字节即停。
//
// busyStreak 由调用方（loop）持有、本 pass 就地更新：连续 busy 计数，任何
// 成功动作或转闲即清零。
func runEventsIdleSweepPass(cands []eventsIdleSweepCandidate, knobs eventsIdleSweepConfig, park map[string]eventsIdleSweepPark, retire func(path string) ([]string, error), compact func(path string) (int64, int64, error), slim eventsIdleSweepSlim, busyStreak map[string]int) []eventsIdleSweepResult {
	sorted := append([]eventsIdleSweepCandidate(nil), cands...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].EventsBytes > sorted[j].EventsBytes
	})
	results := []eventsIdleSweepResult{}
	skips := make(map[string]int, len(eventsIdleSweepSkipReasons))
	rewrites := 0
	for _, c := range sorted {
		if rewrites >= eventsIdleSweepMaxPerPass {
			break
		}
		overLimit := agent.EventsLogAboveThreshold(c.EventsBytes, c.LiveBytes, knobs.factor, knobs.capMB)
		p, hasPark := park[c.Path]
		var parkPtr *eventsIdleSweepPark
		if hasPark {
			parkPtr = &p
		}
		// Task 1073 P0-1: the sweep now shares the save path's profitability
		// guard — a fold that would save <10% of the log is refused here too,
		// so the pass no longer burns its 3-rewrite budget on rewrites that
		// free nothing (73 measured no-gain sweeps). The LiveBytes proxy is
		// the main transcript the candidate already carries.
		profitable := agent.EventsFoldProfitable(c.EventsBytes, c.LiveBytes)
		ok, reason := eventsIdleSweepDecision(knobs.enabled, overLimit, c.Busy, profitable, c.EventsBytes, parkPtr, c.LastActivityAt)
		if !ok {
			// tl 1172: L1 被门槛正确拒绝后的降级动作（L1 现状不变，见函数头）。
			if slim.Enabled && overLimit && !profitable {
				// park 闸与 L1 同一语义：上次压缩落地的尺寸 +10% 增长或新活动
				// 才允许再压。没有这道闸，物化回明文的会话会在同尺寸下每个
				// tick 重压一次——正是验收 2 要防的「频繁重写」。
				if hasPark && parkPtr.activityAt == c.LastActivityAt &&
					c.EventsBytes < int64(float64(parkPtr.attemptSize)*eventsIdleSweepParkRegrow) {
					skips["parked: awaiting regrowth or new activity"]++
					results = append(results, eventsIdleSweepResult{Path: c.Path, Skipped: true,
						Error: "parked: awaiting regrowth or new activity"})
					continue
				}
				switch {
				case c.Busy && !c.TabOpen:
					// 外部运行时持有 lease：不争抢，等它空闲的 tick。
					skips["slim: foreign lease busy"]++
				case c.Busy && c.TabOpen:
					if busyStreak != nil {
						busyStreak[c.Path]++
					}
					if busyStreak == nil || busyStreak[c.Path] < eventsIdleSweepBusyWindowTicks {
						skips[reason]++
						results = append(results, eventsIdleSweepResult{Path: c.Path, Skipped: true, Error: reason})
						continue
					}
					res, handled := slimAttempt(c, slim, park, "tl1172-L2-busy-window")
					if handled {
						if busyStreak != nil {
							delete(busyStreak, c.Path)
						}
						rewrites++
					} else {
						skips["slim: compress failed (will retry)"]++
					}
					results = append(results, res)
					continue
				default:
					res, handled := slimAttempt(c, slim, park, "tl1172-L2-fold-no-shrink")
					if handled {
						rewrites++
					} else {
						skips["slim: compress failed (will retry)"]++
					}
					results = append(results, res)
					continue
				}
			}
			skips[reason]++
			if reason != "under limit" && reason != "auto rotation off" {
				results = append(results, eventsIdleSweepResult{Path: c.Path, Skipped: true, Error: reason})
			}
			continue
		}
		if !c.Busy && busyStreak != nil {
			// 转闲：连续 busy 计数清零。
			delete(busyStreak, c.Path)
		}
		res := eventsIdleSweepResult{Path: c.Path}
		if c.MultiHead && retire != nil {
			if heads, err := retire(c.Path); err != nil {
				slog.Warn("desktop: stale concurrent head retire before sweep rewrite failed (rewrite continues)",
					"path", c.Path, "err", secrets.RedactError(err))
			} else if len(heads) > 0 {
				slog.Info("desktop: stale concurrent heads retired before sweep rewrite",
					"path", c.Path, "heads", len(heads))
			}
		}
		before, after, err := compact(c.Path)
		res.Before, res.After, res.Freed = before, after, before-after
		if err != nil {
			// Lease-busy/失败都不 park：下一 tick 重试。重写是原子的
			// （SaveRewriteCompact 的 CAS+staged write），失败不损原日志。
			res.Error = err.Error()
			res.Skipped = isSessionNotIdle(err)
			if !res.Skipped {
				slog.Warn("desktop: events idle sweep compact failed (will retry next tick)",
					"path", c.Path, "bytes", c.EventsBytes, "err", secrets.RedactError(err))
			}
		} else {
			park[c.Path] = eventsIdleSweepPark{attemptSize: after, activityAt: c.LastActivityAt}
			rewrites++
			slog.Info("desktop: events idle sweep compacted",
				"path", c.Path, "bytesBefore", before, "bytesAfter", after,
				"freedBytes", before-after)
			// tl 1172: L1 跑了但没省到字节——sweep 的 live 代理判「折叠有收益」
			// 而真实 rotated content 拒绝旋转的分歧形态（107MB 实证会话）。
			// 任一级「真正省到字节」即停：L1 无收益 ⇒ 降级 L2。
			if slim.Enabled && res.Freed <= 0 && overLimit {
				slimRes, handled := slimAttempt(c, slim, park, "tl1172-L2-after-no-gain-fold")
				if handled {
					rewrites++
					results = append(results, slimRes)
					continue
				}
				skips["slim: compress failed (will retry)"]++
			}
		}
		results = append(results, res)
	}
	// Task 1073 P0-2: one INFO per pass with every skip reason bucketed, so
	// "why was this session never swept" is answerable from the log alone
	// (the cooking-20260804-085122 investigation was blocked precisely because
	// the skip branches were silent). Under-limit is its own bucket.
	if len(skips) > 0 || rewrites > 0 {
		fields := make([]any, 0, len(eventsIdleSweepSkipReasons)*2+2)
		for _, reason := range eventsIdleSweepSkipReasons {
			fields = append(fields, "skipped_"+reason, skips[reason])
		}
		slog.Info("desktop: events idle sweep skip summary", fields...)
	}
	return results
}

// slimAttempt runs the L2 archive (and, only when the archived size still
// exceeds the cap, the L3 deterministic trim followed by one re-archive) and
// records the park on success so the landed size gates re-entry until new
// activity or ~10% regrowth — the same re-arm semantics an L1 rewrite gets.
// handled=false means the compress failed and the caller should count a retry
// next tick; the original log is untouched in that case (staged write +
// publish-time validation, agent 层契约).
func slimAttempt(c eventsIdleSweepCandidate, slim eventsIdleSweepSlim, park map[string]eventsIdleSweepPark, reason string) (eventsIdleSweepResult, bool) {
	res := eventsIdleSweepResult{Path: c.Path}
	before, after, err := slim.Compress(c.Path)
	if err != nil {
		res.Error = err.Error()
		res.Skipped = true
		slog.Warn("desktop: events idle sweep slim compress failed (will retry next tick)",
			"path", c.Path, "bytes", c.EventsBytes, "err", secrets.RedactError(err))
		return res, false
	}
	// L2 后仍超 cap ⇒ L3（有损，头尾保留 + 显式标记；红线要求可解释）。
	if slim.Trim != nil && slim.CapBytes > 0 && after > slim.CapBytes {
		tb, ta, trimmed, trimErr := slim.Trim(c.Path)
		if trimErr != nil {
			slog.Warn("desktop: events idle sweep slim trim failed (archive kept)",
				"path", c.Path, "archivedBytes", after, "err", secrets.RedactError(trimErr))
		} else if trimmed > 0 {
			slog.Info("desktop: events idle sweep slim trimmed oversized tool results",
				"path", c.Path, "fieldsTrimmed", trimmed, "bytesBefore", tb, "bytesAfter", ta)
			// 裁剪经 SaveRewriteCompact 发布了明文 ⇒ 归档已过期，再压一次收尾。
			if _, a2, cerr := slim.Compress(c.Path); cerr != nil {
				slog.Warn("desktop: events idle sweep slim re-archive after trim failed (plaintext kept)",
					"path", c.Path, "err", secrets.RedactError(cerr))
				after = ta
			} else {
				before, after = tb, a2
			}
		}
	}
	res.Before, res.After, res.Freed = before, after, before-after
	if park != nil {
		park[c.Path] = eventsIdleSweepPark{attemptSize: before, activityAt: c.LastActivityAt}
	}
	slog.Info("desktop: events idle sweep slimmed oversized session",
		"path", c.Path, "reason", reason, "bytesBefore", before, "bytesAfter", after,
		"freedBytes", before-after)
	return res, true
}

type eventsIdleSweepLoop struct {
	a    *App
	mu   sync.Mutex
	park map[string]eventsIdleSweepPark
	// busyStreak 记录每个会话连续 busy 的 tick 数（tl 1172 busy 维护窗口）。
	// 持锁纪律与 park 相同：只在 tick 内的 pass 前后触碰。
	busyStreak map[string]int
}

// startEventsIdleSweepLoop boots the unconditioned ticker: the lab switch is
// read live on every tick, so OFF costs one config read and never touches a
// session, while switching auto on applies without a restart.
func (a *App) startEventsIdleSweepLoop() {
	l := &eventsIdleSweepLoop{a: a, park: make(map[string]eventsIdleSweepPark), busyStreak: make(map[string]int)}
	a.goSafe("eventsIdleSweepLoop", l.run)
}

func (l *eventsIdleSweepLoop) run() {
	timer := time.NewTimer(eventsIdleSweepFirstDelay)
	defer timer.Stop()
	<-timer.C
	l.tick(time.Now())
	ticker := time.NewTicker(eventsIdleSweepTickInterval)
	defer ticker.Stop()
	for now := range ticker.C {
		l.tick(now)
	}
}

// tick collects candidates across every known session directory (oversized
// sessions span project roots — the 2026-10-14 victims sat in both the
// global workspace and cooking_action_recognition), then hands them to the
// pass. Busy marks combine this process's live tabs with any lease record,
// mirroring the storage panel's Busy field.
func (l *eventsIdleSweepLoop) tick(now time.Time) {
	knobs := loadEventsIdleSweepConfig()
	if !knobs.enabled {
		return
	}
	var cands []eventsIdleSweepCandidate
	for _, dir := range l.a.knownSessionDirs() {
		infos, err := agent.ListSessions(dir)
		if err != nil {
			continue
		}
		for _, info := range infos {
			// tl 1172 L2: 归档态（明文缺失）按 .zst 字节数计——压缩态会话以
			// 归档尺寸参与超限判定（远离 cap ⇒ 判「未超限」⇒ 不再被碰）。
			eventsBytes := agent.SessionEventLogZstSize(info.Path)
			if eventsBytes <= 0 {
				continue // nothing on disk to clear
			}
			busy := false
			tabOpen := false
			if _, ctrl := l.a.controllerForSessionPath(info.Path); ctrl != nil {
				busy = true
				tabOpen = true
			} else if leaseInfo, leaseErr := agent.LoadSessionLeaseInfo(info.Path); leaseErr == nil && leaseInfo != nil {
				busy = true
			}
			// Task 945: the sidecar mirror cheaply marks sessions that can hold a
			// stale concurrent head — the retire probe (a replay) is then spent
			// only on the rare multi-head log, never on the single-head majority.
			multiHead := false
			if meta, ok, metaErr := agent.LoadBranchMeta(info.Path); metaErr == nil && ok {
				multiHead = meta.LogSchema == 2 && meta.HeadCount > 1
			}
			cands = append(cands, eventsIdleSweepCandidate{
				Path:           info.Path,
				EventsBytes:    eventsBytes,
				LiveBytes:      statFileSize(info.Path),
				LastActivityAt: info.LastActivityAt.UnixMilli(),
				Busy:           busy,
				MultiHead:      multiHead,
				TabOpen:        tabOpen,
			})
		}
	}
	if len(cands) == 0 {
		return
	}
	l.mu.Lock()
	if len(l.park) > eventsIdleSweepParkMax {
		l.park = make(map[string]eventsIdleSweepPark)
	}
	if len(l.busyStreak) > eventsIdleSweepParkMax {
		l.busyStreak = make(map[string]int)
	}
	// tl 1172 三级降级接线：开关关态 Compress/Trim 不注入、Enabled=false，
	// pass 行为与引入前逐字节一致（铁律 2）。
	slim := eventsIdleSweepSlim{
		Enabled:  knobs.slim,
		CapBytes: knobs.capMB << 20,
		Compress: agent.CompressSessionEventLogZst,
		Trim:     agent.TrimSessionOversizeToolResults,
	}
	results := runEventsIdleSweepPass(cands, knobs, l.park,
		func(path string) ([]string, error) { return agent.RetireStaleConcurrentHeadsNow(path, time.Now()) },
		agent.CompactSessionFile, slim, l.busyStreak)
	l.mu.Unlock()
	compacted, skipped := 0, 0
	for _, res := range results {
		if res.Error != "" || res.Skipped {
			skipped++
		} else {
			compacted++
		}
	}
	slog.Info("desktop: events idle sweep pass done",
		"at", now.Format(time.RFC3339), "candidates", len(cands),
		"compacted", compacted, "skipped", skipped)
}

// statFileSize is the never-erroring stat the sweep candidates need: a missing
// file reads as zero (skipped by the candidate filter).
func statFileSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}
