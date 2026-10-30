package agent

import (
	"math"
	"sync"
	"sync/atomic"
)

// eventsRotationConfig is the agent-side view of the task-333 rotation gate.
// The agent package must not import internal/config (layering), so the
// desktop host pushes the normalized settings here on boot and on every
// settings change — atomic.Pointer keeps the save path read cheap and makes a
// threshold change take effect on the very next save, with no restart.
type eventsRotationConfig struct {
	mode   string // "off" | "manual" (default) | "auto"
	factor float64
	capMB  int64
}

var eventsRotationStore atomic.Pointer[eventsRotationConfig]

// Events rotation modes (task 333). "manual" mirrors today's behavior: the
// built-in factor gate runs and slimming happens through the explicit UI/CLI
// entries.
const (
	eventsRotationOff    = "off"
	eventsRotationManual = "manual"
	eventsRotationAuto   = "auto"
)

// Auto-mode threshold bounds, kept in lockstep with internal/config
// (EventsRotationFactorMin/Max/Default, EventsRotationCapMBMax) — the desktop
// setter normalizes first, these bounds are the agent-side backstop. The cap
// bound keeps capMB<<20 inside int64 (1 TiB shifts to 1<<50): an unbounded
// cap would wrap the shift negative and rotate every log every save (review
// finding, 2026-09-28).
const (
	eventsRotationFactorMin     = 2.0
	eventsRotationFactorMax     = 16.0
	eventsRotationFactorDefault = 4.0
	eventsRotationCapMBMax      = int64(1) << 30
)

// SetEventsAutoRotation stores the rotation gate settings (task 333). Invalid
// values are clamped rather than refused: this is a backstop behind the
// config-layer setters, and a rejected update would silently leave the gate on
// the previous thresholds.
func SetEventsAutoRotation(mode string, factor float64, capMB int64) {
	normalized := eventsRotationManual
	switch mode {
	case eventsRotationOff, eventsRotationManual, eventsRotationAuto:
		normalized = mode
	}
	if math.IsNaN(factor) || math.IsInf(factor, 0) {
		factor = eventsRotationFactorDefault
	}
	if factor < eventsRotationFactorMin {
		factor = eventsRotationFactorMin
	}
	if factor > eventsRotationFactorMax {
		factor = eventsRotationFactorMax
	}
	if capMB < 0 {
		capMB = 0
	}
	if capMB > eventsRotationCapMBMax {
		capMB = eventsRotationCapMBMax
	}
	eventsRotationStore.Store(&eventsRotationConfig{mode: normalized, factor: factor, capMB: capMB})
}

// currentEventsRotation returns the active gate settings; before the desktop
// host's first push the zero value is exactly the default configuration
// (manual, 4x, no cap), so plain CLI/test runs behave as before task 333.
func currentEventsRotation() eventsRotationConfig {
	if cfg := eventsRotationStore.Load(); cfg != nil {
		return *cfg
	}
	return eventsRotationConfig{mode: eventsRotationManual, factor: eventsRotationFactorDefault}
}

// eventsLogAboveFactor is the oversized judgment factored out so all three
// modes share one expression: the log must exceed max(floor, content×factor).
func eventsLogAboveFactor(logSize, contentBytes int64, factor float64) bool {
	limit := sessionEventLogCompactFloor
	if scaled := int64(math.Round(float64(contentBytes) * factor)); scaled > limit {
		limit = scaled
	}
	return logSize > limit
}

// EventsLogAboveThreshold is the judgment with caller-supplied thresholds: the
// task-333 storage panel's inventory marks sessions over-limit through this
// one function so the UI's statistic card, the gate and the CLI stay on the
// same source of truth (the factor threshold and the optional MiB cap OR
// together, matching the auto-mode gate). Both inputs are defensively clamped:
// a raw caller-supplied cap beyond the bound would overflow capMB<<20 into a
// negative threshold and mark every log over (review finding, 2026-09-28).
func EventsLogAboveThreshold(logSize, contentBytes int64, factor float64, capMB int64) bool {
	if math.IsNaN(factor) || math.IsInf(factor, 0) {
		factor = eventsRotationFactorDefault
	}
	if capMB > eventsRotationCapMBMax {
		capMB = eventsRotationCapMBMax
	}
	return eventsLogAboveFactor(logSize, contentBytes, factor) ||
		(capMB > 0 && logSize > capMB<<20)
}

// EventsFoldProfitable is the shared "would folding actually save bytes"
// guard (task 1073 P0-1): the folded log lands at ~contentBytes, so folding
// only pays when that is at least 10% under the current log size. A fold at
// or above that line only appends a replace-record header of pure IO
// (measured: freedBytes ∈ [−217, 0] across 73 no-gain sweeps). The save path
// already carried this guard inside sessionEventLogOversized; the desktop
// idle sweep ran WITHOUT it and burned its per-pass budget on rewrites that
// freed nothing. Both call sites share this one expression so they cannot
// drift. The 10% margin sits just above the replace header's ~200-byte cost
// (the 73 no-gain sweeps freed −217..0 bytes) so a rewrite that would net
// zero or negative bytes is refused before it is attempted.
func EventsFoldProfitable(logSize, contentBytes int64) bool {
	if logSize <= 0 {
		return false
	}
	return float64(contentBytes) <= float64(logSize)*0.9
}

// EventsAutoRotationSnapshot reports the gate settings currently live in the
// save path. Host-side tests use it to verify a settings push landed; a nil
// store reads as the default configuration (manual, 4x, no cap).
func EventsAutoRotationSnapshot() (mode string, factor float64, capMB int64) {
	cfg := currentEventsRotation()
	return cfg.mode, cfg.factor, cfg.capMB
}

// ─── tl 1285: fold-probe ladder（预估不达标时的实测对账与地板兜底）──────────
//
// 缺陷：auto+cap 之下 fold 预估不达标（EventsFoldProfitable==false，折后省
// 不到 10%）的会话被永久豁免——save 路径每次保存都拒绝折叠并重复 WARN
// "left in place"，sweep 侧同判据同样跳过，最大的日志（live 内容本身即日志
// 主体）只涨不缩（20261026 实测 107.3MB WARN 反复；危害 tl1279/tl1284：
// 184MB 日志每次保存 full replay 4.4s）。1073 P1-a 的内层补救
// EventsFoldProfitable(logSize*8/10, ·) 位于 content > 0.9·log 的前提分支下，
// 却要求 content ≤ 0.72·log——自相矛盾，恒假，是死代码，随本修复移除。
//
// 修法（任务书方向①+②，全部复用已装机原语）：
//  ② 预估与实测对账：预估说不缩 ≠ 豁免——放行一次真实折叠（probe），由调用
//     方在动作后回报实测收益（noteSessionFoldOutcome）。实测省到 ≥10% ⇒ 估计
//     错了，清零连败继续正常折叠；实测连续 foldProbeMaxStrikes 次低于阈值 ⇒
//     折叠地板由「测量」而非「估计」证明，本 regime 内才允许不折叠。
//  ① 地板兜底：地板证明后不得死在 "left in place"——save 尾部（文件锁内）把
//     日志无损归档（tl1172 L2 zstd 原语，JSONL 实测 5~10×，红线：不删历史、
//     可逆、自描述），落盘体积真实下降可量化；每 regime 至多一次（archivedAt
//     闩），有损的 L3 裁剪仍归 desktop sweep 的实验开关管，save 路径不碰。
//
// regime 语义与 710 的重警再武装（foldNoShrinkReWarnGrowth=1.10）、921 的
// park 再武装同一常数：日志较 regime 基准再涨 ≥10% 才整梯重新起跑——豁免
// 永远是「本 regime 内」而非永久（tl1285 验收①）。
const foldProbeMaxStrikes = 2

// foldProbeMemo carries the per-path probe ladder state: the size regime base,
// the consecutive measured no-gain count, the floor-archive latch (the
// plaintext size when the archive last ran), and the per-save floor verdict
// the gate hands to the save tail. Bounded like foldNoShrinkWarn.lastSize:
// sessions are few, and on overflow the whole state resets (cost: at most one
// extra probe round), never an unbounded map.
//
// 键口径：gate 的两个调用方形态不同——schema-1 传会话路径（save.go），DAG 传
// 已解析日志路径（sessionDAGLogOversized 的 st.path），而 save 尾部的地板归档
// 拿的是会话路径。三个入口统一在 foldProbeKey 归一成日志路径，同一会话在
// probe 记账与归档兜底之间才不会各记一本账。
var foldProbeMemo struct {
	mu         sync.Mutex
	regime     map[string]int64
	strikes    map[string]int
	archivedAt map[string]int64
	floorSeen  map[string]bool
}

// foldProbeKey normalizes either caller shape (session path or resolved event
// log path) into the one key the ladder books state under. Pure string ops.
func foldProbeKey(path string) string {
	return sessionEventLogPlainPath(path)
}

// foldProbeFloorReached reports whether the measured fold floor is proven for
// path at the given size: foldProbeMaxStrikes consecutive measured no-gain
// folds within the current size regime. A regime expires when the log regrows
// ≥10% past the regime base — the reset side effect lives here so the gate is
// the single place the ladder re-arms from, and the exemption can never
// outlive the size shape it was proven on.
func foldProbeFloorReached(path string, logSize int64) bool {
	if path == "" || logSize <= 0 {
		return false
	}
	key := foldProbeKey(path)
	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	base, seen := foldProbeMemo.regime[key]
	if !seen || logSize >= int64(float64(base)*foldNoShrinkReWarnGrowth) {
		foldProbeMemo.regime[key] = logSize
		delete(foldProbeMemo.strikes, key)
		delete(foldProbeMemo.archivedAt, key)
		delete(foldProbeMemo.floorSeen, key)
		return false
	}
	return foldProbeMemo.strikes[key] >= foldProbeMaxStrikes
}

// foldProbeNoteFloorSeen records the gate's floor verdict for THIS save. The
// save tail consumes it instead of re-deriving the floor from the post-write
// size: a schema-1 floor save appends a full replace record (the log roughly
// doubles after the gate judged), and re-deriving at the tail would read the
// doubled size as a regime change and silently drop the fallback (the e2e
// regression TestFoldFloorFallbackThroughRealSave pins exactly this).
func foldProbeNoteFloorSeen(path string) {
	key := foldProbeKey(path)
	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	foldProbeMemo.floorSeen[key] = true
}

// foldProbeTakeFloorSeen consumes the per-save floor verdict: true means the
// gate just measured the fold floor for this session and the tail may act on
// it. Consumed even when the archive then fails — the next floor save re-arms
// it (self-healing), and a stale verdict must not archive a session whose
// shape changed meanwhile.
func foldProbeTakeFloorSeen(path string) bool {
	key := foldProbeKey(path)
	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	seen := foldProbeMemo.floorSeen[key]
	delete(foldProbeMemo.floorSeen, key)
	return seen
}

// noteSessionFoldOutcome records the MEASURED result of a maintenance fold the
// gate authorized. Callers that act on sessionEventLogOversized == true by
// rotating/compacting MUST report the sizes they saw: the ladder trusts
// measurements, never estimates. A fold that really freed ≥10% clears the
// strike count and the floor-archive latch (the log just changed shape, the
// next floor verdict must be re-proven from it); a fold that freed <10% counts
// one strike. Unlogged outcomes simply leave the ladder where it was — the
// next probe re-measures.
func noteSessionFoldOutcome(path string, before, after int64) {
	if path == "" || before <= 0 {
		return
	}
	key := foldProbeKey(path)
	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	if base, seen := foldProbeMemo.regime[key]; !seen || before > base {
		foldProbeMemo.regime[key] = before
	}
	if EventsFoldProfitable(before, after) {
		foldProbeMemo.strikes[key] = 0
		delete(foldProbeMemo.archivedAt, key)
		delete(foldProbeMemo.floorSeen, key)
		return
	}
	foldProbeMemo.strikes[key]++
}

// foldProbeFloorArchiveDue reports whether the floor archive may run now: the
// latch re-arms only past the same 10% growth constant, so a busy floor
// session pays the archive (and the next save's materialization) once per
// regime, not once per save.
func foldProbeFloorArchiveDue(path string, logSize int64) bool {
	key := foldProbeKey(path)
	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	at, ran := foldProbeMemo.archivedAt[key]
	return !ran || at <= 0 || logSize >= int64(float64(at)*foldNoShrinkReWarnGrowth)
}

// foldProbeNoteFloorArchived latches the floor archive at the plaintext size
// it acted on. Only called after a successful archive; a failed attempt stays
// unlatched so the next save retries.
func foldProbeNoteFloorArchived(path string, plainSize int64) {
	key := foldProbeKey(path)
	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	foldProbeMemo.archivedAt[key] = plainSize
}

// resetFoldProbeMemoForTest clears the package-global ladder state (the same
// isolation discipline resetEventsRotation applies to the other memos).
func resetFoldProbeMemoForTest() {
	foldProbeMemo.mu.Lock()
	defer foldProbeMemo.mu.Unlock()
	foldProbeMemo.regime = map[string]int64{}
	foldProbeMemo.strikes = map[string]int{}
	foldProbeMemo.archivedAt = map[string]int64{}
	foldProbeMemo.floorSeen = map[string]bool{}
}
