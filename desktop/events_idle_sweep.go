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
	"reasonix/internal/store"
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
)

// eventsIdleSweepConfig is the effective knob set for one tick: the sweep
// exists only under auto rotation with a cap — off/manual are explicit user
// choices (task 333: manual keeps today's behaviour, entries stay in the
// storage panel), so the sweep never overrides them.
type eventsIdleSweepConfig struct {
	enabled bool
	factor  float64
	capMB   int64
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
	return eventsIdleSweepConfig{enabled: capMB > 0, factor: factor, capMB: capMB}
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
func eventsIdleSweepDecision(enabled bool, overLimit, busy bool, currentSize int64, park *eventsIdleSweepPark, lastActivityAt int64) (bool, string) {
	if !enabled {
		return false, "auto rotation off"
	}
	if !overLimit {
		return false, "under limit"
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

// runEventsIdleSweepPass runs one sweep pass over pre-collected candidates:
// over-limit judgment, decision, then the injected compact action — largest
// first so the per-pass budget lands on the biggest offenders, capped at
// eventsIdleSweepMaxPerPass rewrites. Both actions are injected so tests can
// drive pass behaviour without a live session; the production wiring passes
// agent.CompactSessionFile and agent.RetireStaleConcurrentHeadsNow.
//
// Task 945: for a multi-head candidate about to be rewritten, the stale-head
// retire runs first — the rewrite only frees bytes that rotation can drop,
// and rotation only drops unreachable ones, and a concurrent head only
// becomes unreachable once retired. A retire failure never blocks the
// rewrite: without it the pass just frees what it frees today.
//
// park updates happen here (success records the landed size; lease-busy and
// failures leave the park untouched so the next tick retries — same contract
// as runEventsRepairRounds' skipped/failed rows).
func runEventsIdleSweepPass(cands []eventsIdleSweepCandidate, knobs eventsIdleSweepConfig, park map[string]eventsIdleSweepPark, retire func(path string) ([]string, error), compact func(path string) (int64, int64, error)) []eventsIdleSweepResult {
	sorted := append([]eventsIdleSweepCandidate(nil), cands...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].EventsBytes > sorted[j].EventsBytes
	})
	results := []eventsIdleSweepResult{}
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
		ok, reason := eventsIdleSweepDecision(knobs.enabled, overLimit, c.Busy, c.EventsBytes, parkPtr, c.LastActivityAt)
		if !ok {
			if reason != "under limit" { // under-limit rows are noise at pass volume
				results = append(results, eventsIdleSweepResult{Path: c.Path, Skipped: true, Error: reason})
			}
			continue
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
		}
		results = append(results, res)
	}
	return results
}

type eventsIdleSweepLoop struct {
	a    *App
	mu   sync.Mutex
	park map[string]eventsIdleSweepPark
}

// startEventsIdleSweepLoop boots the unconditioned ticker: the lab switch is
// read live on every tick, so OFF costs one config read and never touches a
// session, while switching auto on applies without a restart.
func (a *App) startEventsIdleSweepLoop() {
	l := &eventsIdleSweepLoop{a: a, park: make(map[string]eventsIdleSweepPark)}
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
			eventsBytes := statFileSize(store.SessionEventLog(info.Path))
			if eventsBytes <= 0 {
				continue // nothing on disk to clear
			}
			busy := false
			if _, ctrl := l.a.controllerForSessionPath(info.Path); ctrl != nil {
				busy = true
			} else if leaseInfo, leaseErr := agent.LoadSessionLeaseInfo(info.Path); leaseErr == nil && leaseInfo != nil {
				busy = true
			}
			// Task 945: the sidecar mirror cheaply marks the sessions that can
			// even have a stale concurrent head — the retire probe (a replay)
			// is then only spent on the rare multi-head log, never on the
			// single-head majority.
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
	results := runEventsIdleSweepPass(cands, knobs, l.park,
		func(path string) ([]string, error) { return agent.RetireStaleConcurrentHeadsNow(path, time.Now()) },
		agent.CompactSessionFile)
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
