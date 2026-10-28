package boot

import (
	"fmt"
	"strings"
	"time"
)

// tl1229（只带仪器不带优化）：boot「prompt」阶段内部分段计时。
//
// tl1223 实测定案：boot 慢的大头是 prompt 阶段（1.0–11.8s，占 total 60–83%），
// 其中 memory 段（~26 万字符）最可疑。但 prompt 阶段此前只有一根总耗时
// mark（bootTime.mark("prompt")），内部各段（base prompt 组装 / 环境探测 /
// 记忆迁移/装载/组装 / skills 发现 / persona）没有独立读数，1193 族的优化
// 没有 before 数据可对照。
//
// 纪律与 bootTiming 完全同款（纯计时 + 一行日志，零行为变更）：mark 不改任何
// 控制流、不改提示词内容；汇总在 prompt 阶段收口处发一条可 grep 的
// "boot: prompt segments" 行，与既有 boot stage summary 行互不相干（desktop
// 的 bootTimeline 只按名取 total/config/mcp/prompt/agent:tools，新增行不进
// 那条链）。
//
// 分段名即段尾标记（mark(X) = 自上一 mark 以来、以收口名 X 记账，与
// bootTiming.mark 同语义）：
//   shell        shell 解析
//   base_prompt  系统提示词解析 + outputstyle + 核心策略 + rules + policy 块
//   env_probes   session-context 静态段 + 环境探测（含探测快照读写）
//   memory_migrate  记忆库 MigrateV2
//   memory_pinned   MaterializeLegacyPinned
//   memory_load     memory.Load
//   host_checks     instruction.ExtractHostChecks
//   memory_compose  memory.Compose（# Memory 段组装）
//   skills          skills 发现（双 store List，共享 parse cache）
//   collab_block    通讯录索引段
//   persona         官方 persona 折叠
type promptSegments struct {
	start  time.Time
	last   time.Time
	stages []promptSegment
}

type promptSegment struct {
	name string
	ms   int64
}

func newPromptSegments() *promptSegments {
	now := time.Now()
	return &promptSegments{start: now, last: now}
}

// mark closes the segment that just ended under name. Same shape as
// bootTiming.mark; never touches control flow.
func (p *promptSegments) mark(name string) {
	if p == nil {
		return
	}
	now := time.Now()
	p.markAt(name, now.Sub(p.last))
}

// markAt is the injectable-clock form of mark (tests pin the accounting
// instead of racing time.Since).
func (p *promptSegments) markAt(name string, d time.Duration) {
	if p == nil {
		return
	}
	p.stages = append(p.stages, promptSegment{name: name, ms: d.Milliseconds()})
	p.last = p.last.Add(d)
}

// summary renders one greppable line body: "total=…ms shell=3ms base_prompt=12ms …".
// The total is the whole prompt window (wall clock to emit time); the segment
// list sums to the same window minus sub-millisecond stubs.
func (p *promptSegments) summary() string {
	if p == nil {
		return ""
	}
	total := time.Since(p.start).Milliseconds()
	parts := make([]string, 0, len(p.stages)+1)
	parts = append(parts, fmt.Sprintf("total=%dms", total))
	for _, s := range p.stages {
		parts = append(parts, fmt.Sprintf("%s=%dms", s.name, s.ms))
	}
	return strings.Join(parts, " ")
}

// segmentsSummary renders only the segment list ("shell=3ms base_prompt=12ms …")
// with no leading total — the injectable-clock assertion form.
func (p *promptSegments) segmentsSummary() string {
	if p == nil {
		return ""
	}
	parts := make([]string, 0, len(p.stages))
	for _, s := range p.stages {
		parts = append(parts, fmt.Sprintf("%s=%dms", s.name, s.ms))
	}
	return strings.Join(parts, " ")
}
