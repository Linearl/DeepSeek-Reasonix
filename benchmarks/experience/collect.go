package main

// 任务 414 阶段 1：desktop.log 打点 → 四类体验样本（boot / open-topic /
// tab-switch / 首响的日志侧部分）。样本结构统一带时间戳，窗口过滤在采集层做。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// sample 是一项体验读数。MS 是毫秒读数；Kind 区分同源不同口径。
type sample struct {
	At   time.Time
	Tab  string
	MS   int64
	Kind string
}

// bootSample 保留完整分相，供热路径分类与慢相归因。
type bootSample struct {
	At     time.Time // boot: stage timings 落行时刻（=构建结束）
	Total  int64
	Stages map[string]int64
	Model  string
	Root   string
	Hot    bool // 本次 boot 真的走了复用路径，或分相预算启发式命中
	Reuse  bool // 与 boot: assembly reuse 行配对成功（boot 自报的复用事实）
	PoolHit bool // 与 desktop: runtime assembly hit 配对成功（池里有货，不等于走了复用）
	// HotBy 记录热路径判定依据："reuse"（boot 自报复用）/ "budget"（分相预算
	// 启发式）/ "pool-hit"（仅池命中——旧口径的假门来源，不再单独成立）。
	HotBy string
}

// reuseMark 是 boot 自报的一次复用事实（boot: assembly reuse 行）。
type reuseMark struct {
	At     time.Time
	Prompt bool
	Memory bool
	Shell  bool
}

// censusRow 是「越线普查」计数：desktop 后端只记录越过慢线的 stage
// （tab_timing.go：普通线 150ms / switch-out 线 50ms），所以每条日志本身就是
// 一次越线，这里只统计条数与读数，不做百分比门。
type censusRow struct {
	Stage string
	Count int
	P50MS int64
	MaxMS int64
}

// warnLine 是窗口内一条 level=WARN 日志的最小留档（阶段 4 WARN 巡检用：
// 模式匹配在 msg+detail 上做，模式本身由 -warn-patterns 配置）。
type warnLine struct {
	At     time.Time
	Msg    string
	Detail string
}

// warnCollectCap 限制留存的 WARN 巡检行数。**采集期就按模式过滤**
// （2026-10-15 实测教训：全量留档时 168h 窗内 6.1 万条 WARN 噪音
// （config: unknown key ×51k）在旧文件优先顺序下先吃满上限，把近期
// 慢 switch 行挤出池子 → 巡检假 PASS；按模式过滤后同类行仅百级）。
// 截断仍视为错误状态：丢行即置 WarnTruncated，判定期出 FAIL 行，不许静默。
const warnCollectCap = 20000

// logCollect 是一轮日志采集的全部产物。
type logCollect struct {
	Window          time.Duration
	FilesParsed     int
	LinesParsed     int
	HotBudgetMS     int64      // 热路径分类的重组相合计预算（0 = 用默认值）
	AssemblyHits    []time.Time // desktop: runtime assembly hit（363A 复用命中的直接证据）
	ReuseMarks      []reuseMark // boot: assembly reuse（boot 自报：本次真的复用了什么）
	Boots           []bootSample
	OpenTopicTotal  []sample // frontend open-topic summary 的 total（无条件记录）
	OpenTopicAnc    []sample // 后端 open-topic:ancillary effort（慢线普查）
	OpenSessionSlow []sample // desktop: open session slow（≥2s 才记，elapsedMs）
	OpenSessionFail int      // desktop: open session failed 条数（任何一条即体验事故）
	SwitchTotal     []sample // frontend switch-tab summary total
	SwitchRender    []sample // frontend tab switch render
	FirstFrame      []sample // frontend transcript first frame
	BackendCensus   []censusRow
	// 任务 909（验收自动化一期）：切换耗时三读数。
	ModelSwitchFast   []sample // desktop: model switch（fast-same-model/fast-per-request，elapsed_ms）
	ModelSwitchFallbk int      // desktop: runtime build end trigger=set-model 条数（走重建=非快路径；tl1389 P4 单源）
	EffortSwitchFast  []sample // desktop: effort switch（fast-same-level/fast-per-request，elapsed_ms）
	EffortSwitchFallb int      // desktop: runtime build end trigger=set-effort 条数（快路径双双拒收）
	EffortReadSlow    []sample // desktop: effort read breakdown（≥150ms 才落行，截断样本普查）

	Warns         []warnLine // 窗口内命中巡检模式的 level=WARN 行（阶段 4 巡检池）
	WarnTruncated bool       // 巡检池达上限丢过行：判定期 FAIL 显式化，绝不静默
	warnPatterns  []string   // 采集期过滤模式（= -warn-patterns，collectLogDir 注入）
	backendRaw    []sample   // 后端 stage 行（待归并为普查）
}

// collectLogDir 扫描目录内 desktop.log 与轮转文件（desktop.log.N，N 越大越旧），
// 解析窗口内的行。目录不存在返回错误。warnPatterns 为 WARN 巡检模式（采集期
// 按模式过滤入池；nil/空 = 不采集 WARN 池，与 -warn-patterns 为空语义一致）。
// hotBudgetMS 为热路径分类的重组相合计预算（0 = 用默认值）。
func collectLogDir(dir string, window time.Duration, now time.Time, warnPatterns []string, hotBudgetMS int64) (*logCollect, error) {
	c := &logCollect{Window: window, warnPatterns: warnPatterns, HotBudgetMS: hotBudgetMS}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if n == "desktop.log" || strings.HasPrefix(n, "desktop.log.") {
			names = append(names, n)
		}
	}
	// 旧文件在前、新文件在后，保证配对（summary→ancillary）按时间顺序消费。
	sort.Slice(names, func(i, j int) bool {
		ri, rj := rankOf(names[i]), rankOf(names[j])
		if ri != rj {
			return ri > rj
		}
		return names[i] < names[j]
	})
	cutoff := now.Add(-window)
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		c.FilesParsed++
		for _, line := range strings.Split(string(data), "\n") {
			c.eatLine(line, cutoff, now)
		}
	}
	c.rollupCensus()
	c.pairReuseHits()
	c.classifyBoots()
	return c, nil
}

// rankOf 返回轮转序号：desktop.log→0，desktop.log.7→7。
func rankOf(name string) int {
	s := strings.TrimPrefix(name, "desktop.log")
	if s == "" {
		return 0
	}
	n := 0
	for _, r := range strings.TrimPrefix(s, ".") {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// eatLine 把一行日志按 msg 分派到对应样本桶。窗口外的行直接丢弃。
func (c *logCollect) eatLine(line string, cutoff, now time.Time) {
	ln, ok := parseSlogTextLine(line)
	if !ok || ln.ts.Before(cutoff) || ln.ts.After(now.Add(time.Minute)) {
		return
	}
	c.LinesParsed++
	// 阶段 4：WARN 行按巡检模式在**采集期**过滤入池（窗口过滤已在上面做过），
	// 防无模式全量留档被高频噪音行吃满上限造成假 PASS（见 warnCollectCap 注释）。
	if ln.level == "WARN" && len(c.warnPatterns) > 0 {
		w := warnLine{At: ln.ts, Msg: ln.msg, Detail: ln.attrs["detail"]}
		if warnLineMatchesAny(&w, c.warnPatterns) {
			if len(c.Warns) < warnCollectCap {
				c.Warns = append(c.Warns, w)
			} else {
				c.WarnTruncated = true
			}
		}
	}
	switch ln.msg {
	case "desktop: runtime assembly hit":
		// 363A 复用命中的**间接**证据：acquire 发生在 BuildWithAssembly 起步前，
		// 与随后 boot summary 的 (At - Total) 起点对齐即可判定该次 boot 走过
		// 池子。但「池里有货」不等于「本次 boot 真的复用了」——tl1683 之前
		// memory 三段无条件执行，命中配对的 boot 照样重跑完整 prompt 组装，
		// 于是 111/111 热路径样本全部来自这一支，启发式分支 0 样本，363①
		// 成了恒 FAIL 的假门（tl1683 归因报告 §4.4）。故本行只作标注
		// （PoolHit），不再单独构成热路径判定。
		c.AssemblyHits = append(c.AssemblyHits, ln.ts)
	case "boot: assembly reuse":
		// tl1683 ⑤：boot 自报的复用事实（本次 build 复用了 prompt / memory /
		// shell 中的哪些）。这是热路径判定的**直接**证据。
		c.ReuseMarks = append(c.ReuseMarks, reuseMark{
			At:     ln.ts,
			Prompt: ln.attrs["prompt"] == "true",
			Memory: ln.attrs["memory"] == "true",
			Shell:  ln.attrs["shell"] == "true",
		})
	case "boot: stage timings":
		total, stages, ok := parseBootSummary(ln.attrs["summary"])
		if !ok {
			return
		}
		c.Boots = append(c.Boots, bootSample{
			At: ln.ts, Total: total, Stages: stages,
			Model: ln.attrs["model"], Root: ln.attrs["root"],
			// 热/冷判定在采集后统一做（classifyBoots）：它需要本次 boot 的
			// 复用证据与预算，两者都在采集完成后才齐备。
		})
	case "desktop: frontend open-topic summary":
		d := parseKVList(ln.attrs["detail"])
		if ms, ok := parseMs(d["total"]); ok {
			c.OpenTopicTotal = append(c.OpenTopicTotal,
				sample{At: ln.ts, Tab: d["tab"], MS: ms, Kind: "summary"})
		}
	case "desktop: tab switch timing":
		tab := ln.attrs["tab"]
		stage := ln.attrs["stage"]
		ms, ok := parseMs(ln.attrs["ms"])
		if !ok {
			return
		}
		if stage == "open-topic:ancillary effort" {
			// 363⑥ 合成口径的第二半：summary 之后落的后端收尾读数。
			c.OpenTopicAnc = append(c.OpenTopicAnc,
				sample{At: ln.ts, Tab: tab, MS: ms, Kind: stage})
		}
		c.backendRaw = append(c.backendRaw, sample{At: ln.ts, Tab: tab, MS: ms, Kind: stage})
	case "desktop: open session slow":
		if ms, ok := parseMs(ln.attrs["elapsedMs"]); ok {
			c.OpenSessionSlow = append(c.OpenSessionSlow, sample{At: ln.ts, MS: ms, Kind: "slow-line"})
		}
	case "desktop: open session failed":
		c.OpenSessionFail++
	case "desktop: frontend switch-tab summary":
		d := parseKVList(ln.attrs["detail"])
		if ms, ok := parseMs(d["total"]); ok {
			c.SwitchTotal = append(c.SwitchTotal,
				sample{At: ln.ts, Tab: d["tab"], MS: ms, Kind: "summary"})
		}
	case "desktop: frontend tab switch render":
		d := parseKVList(ln.attrs["detail"])
		if ms, ok := parseMs(d["render"]); ok {
			c.SwitchRender = append(c.SwitchRender,
				sample{At: ln.ts, Tab: d["tab"], MS: ms, Kind: "render"})
		}
	case "desktop: frontend transcript first frame":
		d := parseKVList(ln.attrs["detail"])
		if ms, ok := parseMs(d["firstFrame"]); ok {
			c.FirstFrame = append(c.FirstFrame,
				sample{At: ln.ts, Tab: d["tab"], MS: ms, Kind: "firstFrame"})
		}
	// 任务 909：切换模型/effort 耗时。快路径行带 elapsed_ms（model 侧
	// 909 补齐，effort 侧 task 148 已有）；fallback 计数在 runtime build end
	// trigger 上单源统计（tl1389 P4，见该 case 注释）。
	case "desktop: model switch":
		path := ln.attrs["path"]
		if strings.HasPrefix(path, "fallback") {
			// tl1389 P4：不再按本行计数——persona 边界拒收后同一开关必发
			// trigger=set-model 的 build end，两处都计会双计；而 recovery-fork
			// / 无控制器等拒收根本不发本行（只在 agent 侧落 declined 行），
			// 只认 path=fallback* 才漏统计重建、误判「没有重建」。本行仅剩
			// 归因价值（拒收原因），不留档。
			return
		}
		if ms, ok := parseMs(ln.attrs["elapsed_ms"]); ok {
			c.ModelSwitchFast = append(c.ModelSwitchFast,
				sample{At: ln.ts, Tab: ln.attrs["tab"], MS: ms, Kind: path})
		}
	case "desktop: effort switch":
		path := ln.attrs["path"]
		if ms, ok := parseMs(ln.attrs["elapsed_ms"]); ok {
			c.EffortSwitchFast = append(c.EffortSwitchFast,
				sample{At: ln.ts, Tab: ln.attrs["tab"], MS: ms, Kind: path})
		}
	case "desktop: runtime build end":
		// effort 快路径双双拒收后落 rebuild；trigger=set-effort 的 end 行是
		// effort fallback 的计数源。tl1389 P4：model 侧同源——trigger=set-model
		// 的 end 行是「走重建」的唯一完整计数（重建路径只有 persona 边界
		// 一种拒收会发 path=fallback* 行，recovery-fork/无控制器不发，原先
		// 只认该行就把这些重建漏成了「没有重建」）。end 行在 build 阶段
		// 之后的失败路径也会落（defer 语义），与 effort 侧同口径。
		if ln.attrs["trigger"] == "set-effort" {
			c.EffortSwitchFallb++
		} else if ln.attrs["trigger"] == "set-model" {
			c.ModelSwitchFallbk++
		}
	case "desktop: effort read breakdown":
		// 截断样本（total_ms ≥150 才落行，effortBreakdownLogMs）：每条=一次
		// 越线读取，与 tab-switch 普查同语义——只计条数与读数，不做 P95 门。
		if ms, ok := parseMs(ln.attrs["total_ms"]); ok {
			c.EffortReadSlow = append(c.EffortReadSlow,
				sample{At: ln.ts, Tab: ln.attrs["tab"], MS: ms, Kind: ln.attrs["stage"]})
		}
	}
}

// pairReuseHits 把两类复用证据配到每次 boot 上：
//
// ① boot: assembly reuse（boot 自报）——**直接**证据。该行落在本次 boot 的
// [构建起点, 落行时刻] 窗口内（构建起点 = summary.At - Total），故归属
// 明确，无需时间容差。
// ② desktop: runtime assembly hit（池里有货）——**间接**证据，只作标注
// （PoolHit），不再单独构成热路径判定。
//
// 旧口径把 ② 直接当成热路径：命中配对只说明「池里有货」，不说明「本次
// build 真的复用了」——tl1683 之前 memory 三段无条件执行，命中配对的 boot
// 照样重跑完整 prompt 组装。于是第5/6 期热路径样本 100%（69/69、111/111）
// 来自 ②，启发式分支 0 样本，363① 成了恒 FAIL 的假门（tl1683 归因报告
// §4.4）。现在 ① 定热路径、② 只标注。
func (c *logCollect) pairReuseHits() {
	// ① boot 自报的复用事实：按构建窗口归属配对。
	marks := append([]reuseMark(nil), c.ReuseMarks...)
	sort.Slice(marks, func(i, j int) bool { return marks[i].At.Before(marks[j].At) })
	markUsed := make([]bool, len(marks))
	for i := range c.Boots {
		b := &c.Boots[i]
		start := b.At.Add(-time.Duration(b.Total) * time.Millisecond)
		for j, m := range marks {
			if markUsed[j] || m.At.Before(start) || m.At.After(b.At) {
				continue
			}
			markUsed[j] = true
			b.Reuse = true
			break
		}
	}

	// ② 池命中配对：只标注（PoolHit），不参与热路径判定。
	hits := append([]time.Time(nil), c.AssemblyHits...)
	sort.Slice(hits, func(i, j int) bool { return hits[i].Before(hits[j]) })
	used := make([]bool, len(hits))
	for i := range c.Boots {
		b := &c.Boots[i]
		start := b.At.Add(-time.Duration(b.Total) * time.Millisecond)
		best := -1
		bestD := 2 * time.Second
		for j, h := range hits {
			if used[j] {
				continue
			}
			d := h.Sub(start)
			if d < 0 {
				d = -d
			}
			if d <= bestD {
				best, bestD = j, d
			}
		}
		if best >= 0 {
			used[best] = true
			b.PoolHit = true
		}
	}
}

// classifyBoots 给每次 boot 定热路径/冷启动，并记录判定依据。
//
// 判定口径（tl1683 ⑤）：
//   - "reuse"：boot 自报走了复用分支（boot: assembly reuse 行落在本次 boot
//     的构建窗口内）——直接证据，优先级最高。
//   - "budget"：三大重组相（prompt/mcp/agent:tools）合计在预算内——启发式
//     兜底，覆盖复用功能上线前的老日志与未命中复用的快 boot。
//   - 池命中（PoolHit）**不**单独成立：它只说明池里有货。
//
// 预算默认值从 300ms 抬到 1500ms：tl1683 之前三大重组相在热路径上的 P50 是
// 4321ms，300ms 的预算对任何真实 boot 都够不到（这正是启发式分支 0 样本的
// 结构性原因）；修复后复用 boot 的重组相落在 ~340ms（P50）到 ~1.4s（P95 上界
// ＝ prompt≈0 + mcp 393 + agent:tools 833 + 余量），1500ms 既能接住修复后的
// 复用 boot，又接不住修复前那批 4.3s 起的重组相。
func (c *logCollect) classifyBoots() {
	budget := c.HotBudgetMS
	if budget <= 0 {
		budget = defaultBootHotBudgetMS
	}
	for i := range c.Boots {
		b := &c.Boots[i]
		switch {
		case b.Reuse:
			b.Hot = true
			b.HotBy = "reuse"
		case bootIsHotWith(b.Stages, budget):
			b.Hot = true
			b.HotBy = "budget"
		default:
			b.Hot = false
			b.HotBy = ""
		}
	}
}

// bootIsHot 按组合预算把 boot 分为热路径/冷启动（启发式兜底口径）：
// 363A 的热路径=runtime 复用（prompt 免重组、agent:tools 免重建），
// 落到日志上就是三大重组相（prompt/mcp/agent:tools）合计很小。
// 主口径是 boot 自报的复用事实（boot: assembly reuse，直接证据）；该行缺失
// 的老日志（复用功能上线前）退化到本启发式。
func bootIsHot(stages map[string]int64) bool {
	return bootIsHotWith(stages, defaultBootHotBudgetMS)
}

func bootIsHotWith(stages map[string]int64, budgetMS int64) bool {
	heavy := stages["prompt"] + stages["mcp"] + stages["agent:tools"]
	return heavy <= budgetMS
}

// rollupCensus 把后端 stage 行按 stage 名归并为普查行（switch-tab:* /
// switch-out:* / open-topic:* 各自一行）。行内按读数排序取 P50/Max。
func (c *logCollect) rollupCensus() {
	byStage := map[string][]int64{}
	for _, s := range c.backendRaw {
		byStage[s.Kind] = append(byStage[s.Kind], s.MS)
	}
	stages := make([]string, 0, len(byStage))
	for k := range byStage {
		stages = append(stages, k)
	}
	sort.Strings(stages)
	for _, st := range stages {
		vals := byStage[st]
		sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
		c.BackendCensus = append(c.BackendCensus, censusRow{
			Stage: st,
			Count: len(vals),
			P50MS: percentileInt64(vals, 0.50),
			MaxMS: vals[len(vals)-1],
		})
	}
}

// pairOpenTopic 把 frontend summary total 与其后同一 tab 的 ancillary effort
// 合成「打开历史对话→可发消息」的端到端口径（363 追加验收⑥：合成体感）。
// 每条 ancillary 只消费一次；summary 之后 60s 内最近的 ancillary 认配。
// 返回合成样本；无法配对的 summary 原值返回（kind=summary-only）。
func pairOpenTopic(total, anc []sample) []sample {
	pool := append([]sample(nil), anc...)
	sort.Slice(pool, func(i, j int) bool { return pool[i].At.Before(pool[j].At) })
	used := make([]bool, len(pool))
	out := make([]sample, 0, len(total))
	for _, s := range total {
		paired := -1
		for i, a := range pool {
			if used[i] || a.Tab != s.Tab {
				continue
			}
			dt := a.At.Sub(s.At)
			if dt < -2*time.Second || dt > 60*time.Second {
				continue
			}
			paired = i
			break
		}
		if paired >= 0 {
			used[paired] = true
			out = append(out, sample{
				At: s.At, Tab: s.Tab,
				MS:   s.MS + pool[paired].MS,
				Kind: "composed",
			})
			continue
		}
		out = append(out, sample{At: s.At, Tab: s.Tab, MS: s.MS, Kind: "summary-only"})
	}
	return out
}

// percentileInt64 就近秩百分位：sorted 升序，q∈[0,1]，返回 ceil(q*n)-1 位。
func percentileInt64(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(ceilFrac(q * float64(len(sorted))))
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

func ceilFrac(f float64) int {
	i := int(f)
	if float64(i) < f {
		i++
	}
	return i
}
