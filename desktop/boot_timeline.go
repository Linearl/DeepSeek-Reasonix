package main

import (
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 任务 824：boot 启动链分阶段打点取证（0930 报告《桌面启动状态恢复慢-根因调研》
// T-C「取证先行」的落地）。启动链五段此前只有零散日志：P1 串行段完全没打点、
// P3/P4 每路 boot 只有 boot 包内部一行 stage timings、P5 hydrate 只有前端
// console 侧的分段、P6 UI 状态（折叠/分屏/面板）apply 时刻完全未打点——
// 「一次冷启动全链耗时表」无法从日志一次读出。
//
// bootTimeline 把五段收进一个进程级收集器：
//   - P1 串行段：restoreOrBuildTabs 的 loadTabsFile / topic reconcile /
//     config.Load / restoreRemoteTabShells 逐段（bootTimelineNoteP1）；
//   - P3/P4 每路构建：leg total + boot 内部 stage（mcp/prompt/agent:tools）
//     （bootTimelineNoteLeg；stage 串来自 boot 包既有的 bootTiming.summary）；
//   - P5 hydrate 到达：前端经 ReportFrontendLog(feature="boot-timeline") 上报
//     各 tab 的 hydrate 到达时刻（bootTimelineNoteFrontendEvent）；
//   - P6 UI 状态 apply：同通道上报折叠/分屏/面板的 apply 时刻；
//   - 汇总：frontend-ready +10s（或 tabsRestored 兜底 +45s）发一条
//     "boot: summary"（bootTimelineEmitSummary），总墙钟 + 各段 p50/p95 + tab 数。
//
// 纪律（预取自证陷阱）：纯计时 + 日志，零行为变更——
//   - 不参与任何决策、不改任何执行顺序/并发结构；
//   - 收集窗关闭（汇总已发）后所有 note 均为 no-op，启动后的正常运行
//     每次调用只付一次互斥锁内的 bool 判断，不产生任何日志噪音；
//   - OnStageTiming / 前端上报通道缺省时行为与改动前逐字节一致。

// bootTimelineStartedAt 是进程级起点（包初始化时刻，先于 main 第一行）。
// boot:summary 的 wall_ms 以它为原点，覆盖「进程启动 → 汇总发出」全程。
var bootTimelineStartedAt = time.Now()

// bootTimeline collects one cold start's five-segment timings. All fields are
// guarded by mu; the zero value is ready to use.
type bootTimeline struct {
	mu sync.Mutex

	startedAt time.Time
	tabs      int
	p1Names   []string
	p1Ms      map[string]int64
	legs      []bootTimelineLeg
	arrivals  []bootTimelineArrival
	emitted   bool

	// tl 1163：主链里程碑（进程时间轴相对 ms，首到生效）。覆盖任务 824 五段
	// 之外的「main → wails.Run → startup → domReady → window shown →
	// frontend ready」骨架，让 WebView2 冷启动段（pre_wails_done → startup_cb）
	// 第一次有独立读数。
	markNames []string
	markMs    map[string]int64
}

// bootTimelineLeg is one P3/P4 controller build (a "leg"): the desktop-side
// total plus the boot-internal stage map parsed from bootTiming.summary().
type bootTimelineLeg struct {
	TabID   string
	Outcome string // "ok" | "error" | "superseded"
	TotalMs int64
	Stages  map[string]int64 // "total" / "config" / "mcp" / "prompt" / "agent:tools" / ...
}

// bootTimelineArrival is one frontend-side arrival: a tab's hydrate landing
// (P5), the shell's persisted UI state applying (P6), or the renderer's
// first-contentful-paint (tl 1163). AtMs is relative to the FRONTEND page load
// (performance.now origin), a different clock from the Go-side wall_ms — the
// summary names the fields accordingly. EpochMs is the wall-clock instant of
// the same event (renderer Date.now()); when present it bridges the event onto
// the Go process timeline as AtGoMs (same machine, same clock source).
type bootTimelineArrival struct {
	Kind      string // "hydrate" | "ui" | "fcp"
	TabID     string
	AtMs      int64
	HydrateMs int64 // hydrate-only: the hydrate's own wall clock

	EpochMs   int64
	AtGoMs    int64 // epochMs - startedAt.UnixMilli(); valid only when AtGoValid
	AtGoValid bool
}

// bootTimelineObs is the process-wide collector. Desktop boots exactly one
// shell per process; a package-level singleton keeps every instrumentation
// call site a plain function call (no App wiring, no nil-receiver checks at
// call sites that run before NewApp).
var bootTimelineObs = &bootTimeline{startedAt: bootTimelineStartedAt}

// bootLegSlotQueueLogMs：slot 等待超过该值才值得一行日志（0930 基线的锁等
// 4-6.6s 远高于此；阈值只滤掉常态零等待）。
//
// tl 1284 判定（20261030 desktop.log 取证，勿再沿本行追设置页慢）：
// "leg queued for slot waited_ms=8645/12342/16650" 与 settings dataReady
// 17.5s/23.5s 同窗出现，但两者无数据链依赖——同窗内 "settings view loaded"
// 仅 176~364ms 且 settings step 各块 lock_wait_ms=0（Go Settings() 不等任何
// boot leg）。同窗实测为启动恢复期：3 条 leg 排 2 个槽（启动建流节流，见
// tabs.go startupBootConcurrency 注释），等待有界且只发生在启动窗；放大槽池
// 会回到任务 405 实测过的 4 路并发 mcp 段 21-38s 互踩，故维持 2 槽。
// 设置页读数的真伪与前端消费空窗归因见 tl 1284（settingsPrefetch startedAt）。
const bootLegSlotQueueLogMs = 100

// bootTimelineNoteMark records one main-chain milestone as its process-relative
// ms (first arrival wins: a milestone is a point in time, not an accumulator —
// repeated calls keep the earliest reading). Pure计时 + map 写；emitted 后
// no-op，与既有 note 纪律一致。
func bootTimelineNoteMark(name string) {
	bootTimelineNoteMarkAt(name, time.Since(bootTimelineStartedAt))
}

// bootTimelineNoteMarkAt is the injectable-clock form of bootTimelineNoteMark
// (tests pin the reading instead of racing time.Since).
func bootTimelineNoteMarkAt(name string, d time.Duration) {
	obs := bootTimelineObs
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.emitted {
		return
	}
	if obs.markMs == nil {
		obs.markMs = make(map[string]int64)
	}
	if _, seen := obs.markMs[name]; seen {
		return
	}
	obs.markNames = append(obs.markNames, name)
	obs.markMs[name] = d.Milliseconds()
}

// renderMarksLocked renders the milestones in arrival order as "name=12ms …"
// (caller holds mu).
func renderMarksLocked(obs *bootTimeline) string {
	if len(obs.markNames) == 0 {
		return ""
	}
	parts := make([]string, 0, len(obs.markNames))
	for _, name := range obs.markNames {
		parts = append(parts, name+"="+strconv.FormatInt(obs.markMs[name], 10)+"ms")
	}
	return strings.Join(parts, " ")
}

// bootTimelineNoteP1 records one P1 serial-stage duration.
func bootTimelineNoteP1(name string, d time.Duration) {
	obs := bootTimelineObs
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.emitted {
		return
	}
	if obs.p1Ms == nil {
		obs.p1Ms = make(map[string]int64)
	}
	if _, seen := obs.p1Ms[name]; !seen {
		obs.p1Names = append(obs.p1Names, name)
	}
	obs.p1Ms[name] += d.Milliseconds()
}

// bootTimelineNoteTabs records how many persisted tabs the restore found.
func bootTimelineNoteTabs(n int) {
	obs := bootTimelineObs
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.emitted {
		return
	}
	obs.tabs = n
}

// bootTimelineNoteLeg records one controller build. stageSummary is the boot
// package's "total=…ms config=…ms …" line ("" when boot never completed). The
// window is open (summary not yet emitted) the leg is also announced with its
// own greppable Info line — P3/P4 的「各路 total+分段」独立成行，不依赖汇总。
func bootTimelineNoteLeg(tabID, outcome string, total time.Duration, stageSummary string) {
	leg := bootTimelineLeg{TabID: tabID, Outcome: outcome, TotalMs: total.Milliseconds()}
	leg.Stages = parseBootStageSummary(stageSummary)
	obs := bootTimelineObs
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.emitted {
		return
	}
	obs.legs = append(obs.legs, leg)
	slog.Info("boot: tab leg complete",
		"tab", tabID,
		"outcome", outcome,
		"leg_ms", leg.TotalMs,
		"boot_stages", stageSummary)
}

// bootTimelineNoteSlotWait records how long a throttled startup build waited
// for a concurrency slot (0930 基线的「锁等 4-6.6s」在这里现形).
func bootTimelineNoteSlotWait(tabID string, waited time.Duration) {
	if waited.Milliseconds() < bootLegSlotQueueLogMs {
		return
	}
	slog.Info("boot: leg queued for slot", "tab", tabID, "waited_ms", waited.Milliseconds())
}

// bootTimelineNoteFrontendEvent feeds one frontend boot-timeline report into
// the collector. detail is the compact key=value tail the frontend sends
// (kind=hydrate tab=… atMs=… [hydrateMs=…] / kind=ui atMs=… …). Unparsable
// details are dropped silently — a diagnostic must never fail its caller.
func bootTimelineNoteFrontendEvent(detail string) {
	fields := parseKeyValueFields(detail)
	kind := fields["kind"]
	atMs, err := strconv.ParseInt(fields["atMs"], 10, 64)
	if kind == "" || err != nil {
		return
	}
	arrival := bootTimelineArrival{Kind: kind, TabID: fields["tab"], AtMs: atMs}
	if hydrateMs, err := strconv.ParseInt(fields["hydrateMs"], 10, 64); err == nil {
		arrival.HydrateMs = hydrateMs
	}
	// tl 1163: the renderer's wall-clock instant bridges the event onto the
	// Go process timeline (same machine, same clock source). Older callers
	// without epochMs keep the front-end-axis-only shape — pure增量。
	if epochMs, err := strconv.ParseInt(fields["epochMs"], 10, 64); err == nil {
		arrival.EpochMs = epochMs
		arrival.AtGoMs = epochMs - bootTimelineObs.startedAt.UnixMilli()
		arrival.AtGoValid = true
	}
	obs := bootTimelineObs
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.emitted {
		return
	}
	obs.arrivals = append(obs.arrivals, arrival)
}

// parseKeyValueFields splits "a=1 b=two" into a map. Values keep their order-
// independent lookup; malformed pairs are skipped.
func parseKeyValueFields(detail string) map[string]string {
	out := make(map[string]string)
	for _, pair := range strings.Fields(detail) {
		k, v, ok := strings.Cut(pair, "=")
		if ok && k != "" {
			out[k] = v
		}
	}
	return out
}

// parseBootStageSummary parses bootTiming.summary()'s "total=2740ms
// config=12ms …" into stage→ms. Unparsable input yields nil (boot failed
// before its summary, or an older boot build).
func parseBootStageSummary(summary string) map[string]int64 {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	out := make(map[string]int64)
	for _, pair := range strings.Fields(summary) {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			continue
		}
		value = strings.TrimSuffix(value, "ms")
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		out[name] = ms
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// logBootP1Stages emits the P1 serial-stage line the moment restoreOrBuildTabs
// returns (every return path, panics included — recoverToPending runs outside
// it). P1 取证不依赖汇总窗：崩溃在汇总前发生时这一行仍然在 desktop.log 里。
func (a *App) logBootP1Stages() {
	obs := bootTimelineObs
	obs.mu.Lock()
	summary := renderP1Locked(obs)
	obs.mu.Unlock()
	if summary == "" {
		return
	}
	slog.Info("boot: p1 serial stages", "summary", summary)
}

// renderP1Locked renders the P1 stages as "name=12ms …" (caller holds mu).
func renderP1Locked(obs *bootTimeline) string {
	if len(obs.p1Names) == 0 {
		return ""
	}
	parts := make([]string, 0, len(obs.p1Names))
	for _, name := range obs.p1Names {
		parts = append(parts, name+"="+strconv.FormatInt(obs.p1Ms[name], 10)+"ms")
	}
	return strings.Join(parts, " ")
}

// bootTimelineEmitSummary emits the one boot:summary line. Idempotent: the
// first trigger wins, later triggers and every collector note become no-ops.
// Returns whether this call actually emitted. tl 1163：同一次 emit 把一行式
// breakdown 追加落盘到 startup-metrics.log（cwd，best effort——文件写失败只
// 留 debug 行，绝不影响 slog 输出与调用方）。
func bootTimelineEmitSummary(trigger string) bool {
	obs := bootTimelineObs
	obs.mu.Lock()
	if obs.emitted {
		obs.mu.Unlock()
		return false
	}
	obs.emitted = true
	line := renderStartupMetricsLineLocked(obs, trigger)
	slog.Info("boot: summary", append([]any{"trigger", trigger}, bootSummaryAttrsLocked(obs)...)...)
	obs.mu.Unlock()
	// File IO stays outside the collector lock: a slow disk must not stall a
	// concurrent note. appendStartupMetricsFile is best effort and never fails
	// its caller.
	appendStartupMetricsFile(startupMetricsPath(), line)
	return true
}

// bootSummaryAttrsLocked renders the summary payload (caller holds mu) as
// slog attrs. Missing segments are omitted rather than zero-filled so the
// line stays readable; p50/p95 cover successful legs only (failures are
// counted, not averaged in).
func bootSummaryAttrsLocked(obs *bootTimeline) []any {
	attrs := make([]any, 0, 24)
	attrs = append(attrs, "wall_ms", time.Since(obs.startedAt).Milliseconds())
	attrs = append(attrs, "tabs", obs.tabs)
	if summary := renderMarksLocked(obs); summary != "" {
		attrs = append(attrs, "marks", summary)
	}
	if summary := renderP1Locked(obs); summary != "" {
		attrs = append(attrs, "p1", summary)
	}
	okTotals := make([]int64, 0, len(obs.legs))
	okBootTotals := make([]int64, 0, len(obs.legs))
	mcpMs := make([]int64, 0, len(obs.legs))
	promptMs := make([]int64, 0, len(obs.legs))
	agentToolsMs := make([]int64, 0, len(obs.legs))
	errors := 0
	for _, leg := range obs.legs {
		if leg.Outcome != "ok" {
			errors++
			continue
		}
		okTotals = append(okTotals, leg.TotalMs)
		if leg.Stages != nil {
			if v, ok := leg.Stages["total"]; ok {
				okBootTotals = append(okBootTotals, v)
			}
		}
		if v, ok := leg.Stages["mcp"]; ok {
			mcpMs = append(mcpMs, v)
		}
		if v, ok := leg.Stages["prompt"]; ok {
			promptMs = append(promptMs, v)
		}
		if v, ok := leg.Stages["agent:tools"]; ok {
			agentToolsMs = append(agentToolsMs, v)
		}
	}
	if len(obs.legs) > 0 {
		attrs = append(attrs, "legs", len(obs.legs)-errors, "leg_errors", errors)
		if len(okTotals) > 0 {
			attrs = append(attrs,
				"leg_total_p50_ms", percentile(okTotals, 50),
				"leg_total_p95_ms", percentile(okTotals, 95))
		}
		if len(okBootTotals) > 0 {
			attrs = append(attrs,
				"boot_total_p50_ms", percentile(okBootTotals, 50),
				"boot_total_p95_ms", percentile(okBootTotals, 95))
		}
		if len(mcpMs) > 0 {
			attrs = append(attrs, "mcp_p50_ms", percentile(mcpMs, 50), "mcp_p95_ms", percentile(mcpMs, 95))
		}
		if len(promptMs) > 0 {
			attrs = append(attrs, "prompt_p50_ms", percentile(promptMs, 50), "prompt_p95_ms", percentile(promptMs, 95))
		}
		if len(agentToolsMs) > 0 {
			attrs = append(attrs, "agent_tools_p50_ms", percentile(agentToolsMs, 50), "agent_tools_p95_ms", percentile(agentToolsMs, 95))
		}
	}
	hydrateAt := make([]int64, 0, len(obs.arrivals))
	hydrateAtGo := make([]int64, 0, len(obs.arrivals))
	hydrateMs := make([]int64, 0, len(obs.arrivals))
	var uiAtMs int64 = -1
	var uiAtGoMs int64 = -1
	var fcpAtMs int64 = -1
	var fcpAtGoMs int64 = -1
	for _, arrival := range obs.arrivals {
		switch arrival.Kind {
		case "hydrate":
			hydrateAt = append(hydrateAt, arrival.AtMs)
			// The bridged process-axis reading only when the renderer supplied
			// epochMs — the _go field must never carry a front-end-axis value.
			if arrival.AtGoValid {
				hydrateAtGo = append(hydrateAtGo, arrival.AtGoMs)
			}
			if arrival.HydrateMs > 0 {
				hydrateMs = append(hydrateMs, arrival.HydrateMs)
			}
		case "ui":
			// Earliest apply wins: the shell applies its persisted state once.
			if uiAtMs < 0 || arrival.AtMs < uiAtMs {
				uiAtMs = arrival.AtMs
			}
			if arrival.AtGoValid && (uiAtGoMs < 0 || arrival.AtGoMs < uiAtGoMs) {
				uiAtGoMs = arrival.AtGoMs
			}
		case "fcp":
			// First report wins: FCP happens once per page load.
			if fcpAtMs < 0 {
				fcpAtMs = arrival.AtMs
				if arrival.AtGoValid {
					fcpAtGoMs = arrival.AtGoMs
				}
			}
		}
	}
	if len(hydrateAt) > 0 {
		attrs = append(attrs, "hydrate_tabs", len(hydrateAt),
			"hydrate_at_p50_ms", percentile(hydrateAt, 50),
			"hydrate_at_p95_ms", percentile(hydrateAt, 95))
		if len(hydrateAtGo) > 0 {
			attrs = append(attrs, "hydrate_at_go_p50_ms", percentile(hydrateAtGo, 50))
		}
		if len(hydrateMs) > 0 {
			attrs = append(attrs, "hydrate_ms_p50_ms", percentile(hydrateMs, 50))
		}
	}
	if uiAtMs >= 0 {
		attrs = append(attrs, "ui_at_ms", uiAtMs)
	}
	if uiAtGoMs >= 0 {
		attrs = append(attrs, "ui_at_go_ms", uiAtGoMs)
	}
	if fcpAtMs >= 0 {
		attrs = append(attrs, "fcp_at_ms", fcpAtMs)
		if fcpAtGoMs >= 0 {
			attrs = append(attrs, "fcp_at_go_ms", fcpAtGoMs)
		}
	}
	return attrs
}

// percentile returns the p-th percentile of ms values (nearest-rank on a
// sorted copy: rank = ceil(p/100 × N)). Caller-owned slice is not modified.
func percentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
