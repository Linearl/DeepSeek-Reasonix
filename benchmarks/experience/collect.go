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
	Hot    bool // 复用命中配对（363A 直接证据）或分相预算启发式
	Reuse  bool // 与 desktop: runtime assembly hit 配对成功
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

// warnCollectCap 限制一轮采集留存的 WARN 行数（防极端场景内存膨胀；
// 实测日常 72h 窗口几十条，20000 是宽松上限不是采样）。
const warnCollectCap = 20000

// logCollect 是一轮日志采集的全部产物。
type logCollect struct {
	Window          time.Duration
	FilesParsed     int
	LinesParsed     int
	AssemblyHits    []time.Time // desktop: runtime assembly hit（363A 复用命中的直接证据）
	Boots           []bootSample
	OpenTopicTotal  []sample // frontend open-topic summary 的 total（无条件记录）
	OpenTopicAnc    []sample // 后端 open-topic:ancillary effort（慢线普查）
	OpenSessionSlow []sample // desktop: open session slow（≥2s 才记，elapsedMs）
	OpenSessionFail int      // desktop: open session failed 条数（任何一条即体验事故）
	SwitchTotal     []sample // frontend switch-tab summary total
	SwitchRender    []sample // frontend tab switch render
	FirstFrame      []sample // frontend transcript first frame
	BackendCensus   []censusRow
	Warns           []warnLine // 窗口内全部 level=WARN 行（阶段 4 巡检池）
	backendRaw      []sample   // 后端 stage 行（待归并为普查）
}

// collectLogDir 扫描目录内 desktop.log 与轮转文件（desktop.log.N，N 越大越旧），
// 解析窗口内的行。目录不存在返回错误。
func collectLogDir(dir string, window time.Duration, now time.Time) (*logCollect, error) {
	c := &logCollect{Window: window}
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
	// 阶段 4：WARN 行全量留档进巡检池（窗口过滤已在上面做过）；
	// 模式匹配推迟到 benchWarnPatrol（-warn-patterns 用户可配）。
	if ln.level == "WARN" && len(c.Warns) < warnCollectCap {
		c.Warns = append(c.Warns, warnLine{At: ln.ts, Msg: ln.msg, Detail: ln.attrs["detail"]})
	}
	switch ln.msg {
	case "desktop: runtime assembly hit":
		// 363A 复用命中的直接证据：acquire 发生在 BuildWithAssembly 起步前，
		// 与随后 boot summary 的 (At - Total) 起点对齐即可判定该次 boot 走热路径。
		c.AssemblyHits = append(c.AssemblyHits, ln.ts)
	case "boot: stage timings":
		total, stages, ok := parseBootSummary(ln.attrs["summary"])
		if !ok {
			return
		}
		c.Boots = append(c.Boots, bootSample{
			At: ln.ts, Total: total, Stages: stages,
			Model: ln.attrs["model"], Root: ln.attrs["root"],
			Hot: bootIsHot(stages),
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
	}
}

// pairReuseHits 把 desktop: runtime assembly hit 与 boot summary 配对：
// hit 落在 BuildWithAssembly 起步前，故 boot 的构建起点 = At - Total。
// 每个 hit 只消费一次；|hit - 构建起点| ≤ 2s 判同一次构建（实测最大偏差
// 1.7s，含日志落行抖动）。配对成功 = 该 boot 走了 363A 热路径。
func (c *logCollect) pairReuseHits() {
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
			b.Reuse = true
			b.Hot = true
		}
	}
}

// bootIsHot 按组合预算把 boot 分为热路径/冷启动（启发式兜底口径）：
// 363A 的热路径=runtime 复用（prompt 免重组、agent:tools 免重建），
// 落到日志上就是三大重组相（prompt/mcp/agent:tools）合计很小。
// 主口径是 pairReuseHits 的 hit 配对（直接证据）；hit 行缺失的老日志
// （复用功能上线前）退化到本启发式，阈值可在 CLI 覆盖（bootHotBudgetMS）。
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
