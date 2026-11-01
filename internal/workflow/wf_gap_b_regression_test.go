package workflow

// G3/G4/G6 回归测试 —— 工作流引擎缺口最小复现（批 B + 观测面）。
//
// 来源：审计线 docs/report/代码审计-20261031-工作流/回归复现/
// wf-gap-b-regression_test.go（2026-11-01），tl1324 落库时物理进仓。
// 原 overlay 挂载（tmp_wf_gap_b_regression_test.go）不再需要，测试名
// 去掉 Tmp 前缀；G3/G4 断言原样保留（缺口未修，wantCurrentlyPass 留档）。
//
// 覆盖三条缺口：
//   G3(medium) findings 跨节点去重不足 —— bag.add 的去重键是 (Where, What) 全等，
//         同一问题在扫描节点与复核节点措辞不同 ⇒ 全等判据失效 ⇒ 重复入袋。
//         ⚠️ 修正说明：bag.add 本身**有锁**且**已按 (where, what) 去重**
//         （internal/workflow/report.go:41-60，基线 70780f0cb 即如此）。
//         真实缺口不是「无去重」，而是「去重键过严：要求 what 全等」。
//         ⇒ 按 where 单独去重会误合并同一文件的不同问题（报告里
//           internal/tool/builtin/webfetch.go 有 3 个不同问题同文件）。
//         ✅ tl1326 已修（选型 B1：引擎分配来源序号 <nodeID>#<n> + 复核节点
//         回填序号；add/promote 共用 sameFinding 键）。G3 两钉已按审计线
//         README 翻正为修复后正向断言，另补三条硬条件钉（⑥同源键/⑦回填
//         合并可升级/③部分成立）。
//   G4(medium) 引擎按拓扑序串行执行 —— engine.go runAll 每轮只 execute
//         一个节点就 break 重扫。附带两个隐藏前置的读数：
//         ①bag.add 有锁（report.go:52）⇒ 无数据竞争，但并行化仍需确认；
//         ②并行化必须复用 agent 层 DefaultMaxParallelWriters。
//   G6(medium) 工作流子代理在任务/子代理面板不可见 —— 已由 tl1324 修复
//         （方案 A 修正版：TaskSubagentRunner.Run 节点级登记进 taskmonitor，
//         复用 controller 装配的同一 recorder 实例）。两条 G6 钉已改为
//         修复后断言（wantPassAfterFix），与 taskmonitor_pin_test.go 的
//         验收钉共用测试设施。
//
// 状态标记约定：
//   - `wantCurrentlyPass` = 断言在**现状（未修）**代码上应通过（证明缺口成立）
//   - `wantPassAfterFix`    = 断言在**修复后**代码上应通过（修复前会红）

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/taskmonitor"
)

// ---- 共用：可控 runner ----

// gapBRunner 按节点 ID 返回预置答案，并记录 prompt。
type gapBRunner struct {
	mu      sync.Mutex
	prompts []string
	answer  string
	byNode  map[string]string
	// delay 让某些节点睡指定时长（用于观察并行/串行耗时）
	delayByNode map[string]time.Duration
	// onRun 回调用于在执行中做断言（如观察 bag 状态）
	onRun func(nodeID string)
}

func (r *gapBRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	r.mu.Lock()
	r.prompts = append(r.prompts, req.Prompt)
	answer := r.answer
	if a, ok := r.byNode[req.NodeID]; ok {
		answer = a
	}
	delay := r.delayByNode[req.NodeID]
	hook := r.onRun
	r.mu.Unlock()

	if hook != nil {
		hook(req.NodeID)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	return answer, nil
}

func (r *gapBRunner) promptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

// gapBParse 解析工作流文档。
func gapBParse(t *testing.T, doc string) *Workflow {
	t.Helper()
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return wf
}

// ---- G3 ----

// gapBFinding 构造一条 finding JSON 片段。
func gapBFinding(where, what string) string {
	return fmt.Sprintf(`{"where":%q,"what":%q,"evidence":"ev","severity":"medium"}`, where, what)
}

// gapBFindingWithID 构造一条带引擎来源序号的 finding JSON 片段（B1 回填形态）。
func gapBFindingWithID(where, what, id string) string {
	return strings.TrimSuffix(gapBFinding(where, what), "}") + `,"id":"` + id + `"}`
}

// TestGapB_G3_SameIssueDifferentWordingLandsTwice —— 名字是缺口存证：修复前
// 同一 where 因措辞不同落袋 2 条（(where, what) 全等去重失效）。
// tl1326 B1 修复后的正向断言（原 wantCurrentlyPass 断言已按审计线 README 翻正）：
// 引擎给扫描条目分配来源序号（scanner#1）并随 {{scanner.findings}} 传给复核
// 节点；复核节点回填该序号 ⇒ add 按序号把改写措辞合并进原条目 ⇒ 只落 1 条。
func TestGapB_G3_SameIssueDifferentWordingLandsTwice(t *testing.T) {
	const where = "internal/tool/builtin/webfetch.go:200"

	// 扫描者原话 vs 复核者改写后的话 —— 指向同一处代码，措辞不同。
	// 复核者回填引擎注扎的序号（生产链路：{{scanner.findings}} 携带
	// "id":"scanner#1"，复核复制回自己重报的条目上）。
	scannerWording := gapBFinding(where, "每次请求新建 Transport，无 idle 超时")
	reviewerWording := gapBFindingWithID(where, "Transport 未复用且缺少 idle 超时设置", "scanner#1")

	runner := &gapBRunner{byNode: map[string]string{
		"scanner":  `{"findings":[` + scannerWording + `],"count":1,"coverage":"c"}`,
		"reviewer": `{"confirmed":[` + reviewerWording + `],"rejected":[],"high_count":0,"medium_count":1,"low_count":0}`,
	}}

	doc := `{
  "version": 1,
  "name": "gap-g3",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"review {{scanner.findings}}",
     "result":[{"name":"confirmed","type":"array"},{"name":"rejected","type":"array"}],
     "findings_field":"confirmed"}
  ],
  "edges": [{"from":"scanner","to":"reviewer"}]
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	got := run.bag.snapshot()
	if len(got) != 1 {
		t.Fatalf("修复后（B1 序号回填合并）同一问题应只落 1 条，实际 %d 条：%+v", len(got), got)
	}
	if got[0].What != "Transport 未复用且缺少 idle 超时设置" {
		t.Fatalf("合并应采纳拓扑在后（复核）的措辞，实际 what=%q", got[0].What)
	}
	if got[0].SourceID != "scanner#1" {
		t.Fatalf("合并条目应保留引擎来源序号，实际 %q", got[0].SourceID)
	}
	t.Logf("G3 已修（B1）：复核回填序号 scanner#1，改写措辞合并进原条目，只落 1 条")
}

// TestGapB_G3_SameIssueIdenticalWordingLandsOnce 是对照组：
// 措辞**完全相同** ⇒ 全等去重生效 ⇒ 只落 1 条。
//
// 这一条现状应通过，且修复后**必须保持绿** —— 它是「去重修复不能过度合并」的护栏。
func TestGapB_G3_SameIssueIdenticalWordingLandsOnce(t *testing.T) {
	const where = "internal/tool/builtin/webfetch.go:200"
	const wording = "每次请求新建 Transport，无 idle 超时"
	f := gapBFinding(where, wording)
	runner := &gapBRunner{byNode: map[string]string{
		"scanner":  `{"findings":[` + f + `],"count":1,"coverage":"c"}`,
		"reviewer": `{"confirmed":[` + f + `],"rejected":[],"high_count":0,"medium_count":1,"low_count":0}`,
	}}

	doc := `{
  "version": 1,
  "name": "gap-g3-control",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"review",
     "result":[{"name":"confirmed","type":"array"},{"name":"rejected","type":"array"}],
     "findings_field":"confirmed"}
  ],
  "edges": [{"from":"scanner","to":"reviewer"}]
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	got := run.bag.snapshot()
	if len(got) != 1 {
		t.Fatalf("措辞完全相同时应只落 1 条（现状全等去重已生效），实际 %d 条", len(got))
	}
	t.Logf("对照组成立：措辞完全相同 ⇒ 全等去重生效，只落 1 条")
}

// TestGapB_G3_DifferentIssuesSameFileMustNotMerge 是**过度合并护栏**：
// 同一文件下的**不同问题**（where 相同、what 不同）**必须**各落一条。
//
// 这一条现状应通过；修复后若把它改成「按 where 单独去重」就会变红 ——
// 正是它挡住了那种过度修法（报告里 internal/tool/builtin/webfetch.go 有 3 个不同问题）。
func TestGapB_G3_DifferentIssuesSameFileMustNotMerge(t *testing.T) {
	const where = "internal/tool/builtin/webfetch.go"
	f1 := gapBFinding(where, "问题A：Transport 未复用")
	f2 := gapBFinding(where, "问题B：缺少 idle 超时")
	f3 := gapBFinding(where, "问题C：SSRF 前置检查缺失")

	runner := &gapBRunner{byNode: map[string]string{
		"scanner":  `{"findings":[` + f1 + `,` + f2 + `,` + f3 + `],"count":3,"coverage":"c"}`,
		"reviewer": `{"confirmed":[],"rejected":[],"high_count":0,"medium_count":3,"low_count":0}`,
	}}

	doc := `{
  "version": 1,
  "name": "gap-g3-nomerge",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"review",
     "result":[{"name":"confirmed","type":"array"},{"name":"rejected","type":"array"}],
     "findings_field":"confirmed"}
  ],
  "edges": [{"from":"scanner","to":"reviewer"}]
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	got := run.bag.snapshot()
	if len(got) != 3 {
		t.Fatalf("同文件不同问题必须各落一条（不得按 where 粗暴合并），实际 %d 条", len(got))
	}
	t.Logf("过度合并护栏成立：同文件 3 个不同问题落 3 条")
}

// TestGapB_G3_MergedEntryPromotesToVerified 钉 tl1326 硬条件⑦（B1 全链路）：
// 扫描产出条目（引擎注序号 scanner#1）→ 复核 subagent 回填序号（措辞改写，
// 袋内条目数不增）→ confirm 节点以 verdicts 按序号逐条升级 ⇒ 袋内仍 1 条、
// 状态 verified、evidence 并入裁决 note；且 confirm 的 evidence 携带序号
// （复核看得见、回填得回来）。
func TestGapB_G3_MergedEntryPromotesToVerified(t *testing.T) {
	const where = "internal/tool/builtin/webfetch.go:200"
	runner := &gapBRunner{byNode: map[string]string{
		"scanner":  `{"findings":[` + gapBFinding(where, "每次请求新建 Transport，无 idle 超时") + `],"count":1,"coverage":"c"}`,
		"reviewer": `{"confirmed":[` + gapBFindingWithID(where, "Transport 未复用且缺少 idle 超时设置", "scanner#1") + `],"rejected":[]}`,
		"checker":  `{"confirmed":true,"reason":"reproduced","verdicts":[{"id":"scanner#1","confirmed":true,"note":"reproduced via benchmarks"}]}`,
	}}
	doc := `{
  "version": 1,
  "name": "gap-g3-b1-fullchain",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"review {{scanner.findings}}",
     "result":[{"name":"confirmed","type":"array"},{"name":"rejected","type":"array"}],
     "findings_field":"confirmed"},
    {"id":"checker","type":"confirm","target":"scanner"}
  ],
  "edges": [{"from":"scanner","to":"reviewer"}]
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	got := run.bag.snapshot()
	if len(got) != 1 {
		t.Fatalf("回填+确认后袋内应仍 1 条（条目数不得因回填增长），实际 %d：%+v", len(got), got)
	}
	if got[0].Status != FindingVerified {
		t.Fatalf("回填条目应被 verdicts 升为 verified，实际 %s", got[0].Status)
	}
	if got[0].What != "Transport 未复用且缺少 idle 超时设置" {
		t.Fatalf("条目措辞应为复核改写版（后写覆盖），实际 %q", got[0].What)
	}
	if !strings.Contains(got[0].Evidence, "[confirm] reproduced via benchmarks") {
		t.Fatalf("verdict note 应并入 evidence，实际 %q", got[0].Evidence)
	}
	// ⑦ 前半：传给 confirm 的 evidence 携带来源序号（复核可见、可回填）。
	var confirmPrompt string
	for _, p := range runner.prompts {
		if strings.Contains(p, "independent confirmer sub-agent") {
			confirmPrompt = p
		}
	}
	if confirmPrompt == "" || !strings.Contains(confirmPrompt, "scanner#1") {
		t.Fatalf("confirm evidence 应携带来源序号 scanner#1，prompts=%v", runner.prompts)
	}
	t.Logf("硬条件⑦成立：序号随 evidence 下行、复核回填合并不增条、verdicts 逐条升级 verified")
}

// TestGapB_G3_PartialVerdictsPromoteOnlyConfirmed 钉 tl1326 硬条件③（B1 的
// 价值所在，最易漏）：复核对 3 条给「1 成立 2 不成立」⇒ 只升那 1 条，其余
// 2 条保持 unconfirmed 且一条不丢；幻觉序号（目标从未产出）不得入袋。
// 现状（修复前）引擎只知道一个总布尔，要么全升要么全不升——这条修复前必红。
func TestGapB_G3_PartialVerdictsPromoteOnlyConfirmed(t *testing.T) {
	items := []string{
		gapBFinding("a.go:1", "问题一"),
		gapBFinding("a.go:2", "问题二"),
		gapBFinding("a.go:3", "问题三"),
	}
	runner := &gapBRunner{byNode: map[string]string{
		"scanner": `{"findings":[` + strings.Join(items, ",") + `],"count":3,"coverage":"c"}`,
		"checker": `{"confirmed":true,"reason":"one reproduced","verdicts":[` +
			`{"id":"scanner#1","confirmed":true,"note":"reproduced"},` +
			`{"id":"scanner#2","confirmed":false},` +
			`{"id":"scanner#3","confirmed":false},` +
			`{"id":"scanner#9","confirmed":true}]}`,
	}}
	doc := `{
  "version": 1,
  "name": "gap-g3-partial",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"checker","type":"confirm","target":"scanner"}
  ],
  "edges": []
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	got := run.bag.snapshot()
	if len(got) != 3 {
		t.Fatalf("3 条发现一条不丢，实际 %d：%+v", len(got), got)
	}
	statuses := map[string]string{}
	for _, f := range got {
		statuses[f.SourceID] = f.Status
	}
	if statuses["scanner#1"] != FindingVerified {
		t.Fatalf("scanner#1（裁决成立）应升 verified，实际 %s", statuses["scanner#1"])
	}
	if statuses["scanner#2"] != FindingUnconfirmed || statuses["scanner#3"] != FindingUnconfirmed {
		t.Fatalf("被否决的 2 条应保持 unconfirmed（不升级、不丢弃），实际 %v", statuses)
	}
	if _, ok := statuses["scanner#9"]; ok {
		t.Fatalf("幻觉序号 scanner#9 不得入袋：%+v", got)
	}
	t.Logf("硬条件③成立：verdicts 逐条升级——1 升 verified、2 保持 unconfirmed、幻觉序号忽略")
}

// TestGapB_G3_BagAddIsLockedAndIdempotent 记录 reportBag 的既有能力：
// add 已有锁 + 已按 (where, what) 幂等。
//
// 这条**修正了首轮缺口分析里「bag 无去重 / 无锁」的表述** —— 是 G4 并行化的重要前置读数。
func TestGapB_G3_BagAddIsLockedAndIdempotent(t *testing.T) {
	b := &reportBag{}
	f := Finding{Where: "a.go:1", What: "w1", Evidence: "e", Severity: "medium"}

	// 幂等：同一条加 3 次只落 1 条。
	for i := 0; i < 3; i++ {
		b.add(f)
	}
	if got := b.snapshot(); len(got) != 1 {
		t.Fatalf("同一条重复 add 应只落 1 条，实际 %d", len(got))
	}

	// 并发安全：并发加同一条 100 次，仍只落 1 条（证明有锁 ⇒ 无重复入袋、无 race）。
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.add(f)
		}()
	}
	wg.Wait()
	if got := b.snapshot(); len(got) != 1 {
		t.Fatalf("并发重复 add 后应仍只落 1 条（证明 add 有锁），实际 %d", len(got))
	}
	t.Logf("reportBag 既有能力确认：add 有锁 + 按 (where, what) 幂等 ⇒ G4 并行化无数据竞争风险")

	// tl1326 B1 序号合并：同序号不同措辞 = 复核改写 ⇒ 合并为 1 条（措辞取后写）。
	b2 := &reportBag{}
	b2.add(Finding{Where: "a.go:1", What: "原话", Evidence: "e1", Severity: "medium", SourceID: "scan#1"})
	b2.add(Finding{Where: "a.go:1", What: "改写", Evidence: "e2", Severity: "high", SourceID: "scan#1"})
	if got := b2.snapshot(); len(got) != 1 || got[0].What != "改写" || got[0].Severity != "high" {
		t.Fatalf("同序号回填应合并为 1 条并采纳后写措辞，实际 %+v", got)
	}
	// 并发加同序号（措辞各异）仍只落 1 条：锁 + 序号合并协同。
	var wg2 sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			b2.add(Finding{Where: "a.go:1", What: fmt.Sprintf("并发改写%d", i), SourceID: "scan#1"})
		}(i)
	}
	wg2.Wait()
	if got := b2.snapshot(); len(got) != 1 || got[0].SourceID != "scan#1" {
		t.Fatalf("并发同序号 add 后应仍只 1 条，实际 %+v", got)
	}
	t.Logf("B1 序号合并确认：同序号回填（含并发）合并为 1 条，后写措辞生效")
}

// TestGapB_G3_FindingKeySharedByAddAndPromote 钉 tl1326 硬条件⑥：add 与
// promote 必须共用同一去重键（sameFinding）。行为级断言：若任一处退回
// 修复前的「仅 (where,what) 全等」旧键，下面两段必有一段红——这正是
// 「只改 add 不改 promote ⇒ 合并后永不升级」陷阱的防线。
func TestGapB_G3_FindingKeySharedByAddAndPromote(t *testing.T) {
	// add 侧：同序号、不同措辞 ⇒ 必须合并（旧全等键会落 2 条）。
	b := &reportBag{}
	b.add(Finding{Where: "a.go:1", What: "原话", SourceID: "s#1"})
	b.add(Finding{Where: "a.go:1", What: "改写", SourceID: "s#1"})
	if got := b.snapshot(); len(got) != 1 {
		t.Fatalf("add 未走序号键（疑似退回全等匹配）：应 1 条，实际 %d：%+v", len(got), got)
	}
	// promote 侧：同序号、措辞与袋内条目全不同 ⇒ 必须原地升级（旧全等键会
	// 匹配失败而追加一条 verified，袋里变 2 条且原条目永不升级）。
	b.promote(Finding{Where: "zz.go:9", What: "措辞完全不同", SourceID: "s#1"})
	got := b.snapshot()
	if len(got) != 1 {
		t.Fatalf("promote 未走序号键（疑似退回全等匹配而追加）：应仍 1 条，实际 %d：%+v", len(got), got)
	}
	if got[0].Status != FindingVerified {
		t.Fatalf("promote 应按序号升级原条目，实际 status=%s", got[0].Status)
	}
	t.Logf("硬条件⑥成立：add 与 promote 对同序号条目解析到同一条（共用 sameFinding 键）")
}

// ---- G4 ----

// TestGapB_G4_RunsSeriallyNotConcurrently 证明 G4：三个无依赖节点**串行**执行。
//
// 现状应通过（缺口成立）：总耗时 ≈ 各节点耗时之和。
// 修复后此测试应转红（届时断言总耗时 ≈ max(各节点耗时)）。
func TestGapB_G4_RunsSeriallyNotConcurrently(t *testing.T) {
	const each = 300 * time.Millisecond

	runner := &gapBRunner{
		answer: `{"findings":[],"count":0,"coverage":"c"}`,
		delayByNode: map[string]time.Duration{
			"n1": each,
			"n2": each,
			"n3": each,
		},
	}

	doc := `{
  "version": 1,
  "name": "gap-g4",
  "nodes": [
    {"id":"n1","type":"subagent","prompt":"a",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"},
    {"id":"n2","type":"subagent","prompt":"b",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"},
    {"id":"n3","type":"subagent","prompt":"c",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"}
  ],
  "edges": []
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)

	start := time.Now()
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	elapsed := time.Since(start)

	// 串行 ⇒ ≈3×each；并行 ⇒ ≈each。
	serialThreshold := 2*each + 100*time.Millisecond
	if elapsed < serialThreshold {
		t.Fatalf("wantCurrentlyPass 断言失败：现状应为串行（耗时 ≥ 2×单节点），实际 %v（若变红说明 G4 可能已修复）", elapsed)
	}
	t.Logf("G4 成立（当前未修）：3 节点各 %v，串行总耗时 %v（并行应 ≈%v）", each, elapsed, each)

	// 修复后的正向断言（现在必然失败，作为修复的验收标准留档）：
	//   parallelThreshold := each + 150*time.Millisecond
	//   if elapsed > parallelThreshold {
	//       t.Fatalf("修复后应并行，总耗时应 ≈%v，实际 %v", each, elapsed)
	//   }
}

// TestGapB_G4_ConcurrentBagAddKeepsAllFindings 是 G4 的**并行前置钉**：
// 并发执行多个 findings 节点时，bag 必须**一条不丢**。
//
// 现状应通过（runAll 串行 ⇒ 天然不丢），但它同时证明了 reportBag.add 的锁有效
// ⇒ 并行化时不需要为 bag 另加机制，只需保证「多节点并发调用 add」不丢条。
// 修复后（G4 改并行）此测试仍必须绿 —— 它就是并行化的验收钉。
func TestGapB_G4_ConcurrentBagAddKeepsAllFindings(t *testing.T) {
	const nodes = 8
	const perNode = 5

	byNode := map[string]string{}
	for i := 0; i < nodes; i++ {
		id := fmt.Sprintf("n%02d", i)
		var items []string
		for j := 0; j < perNode; j++ {
			items = append(items, gapBFinding(fmt.Sprintf("file%d.go:%d", i, j), fmt.Sprintf("问题%d-%d", i, j)))
		}
		byNode[id] = `{"findings":[` + strings.Join(items, ",") + `],"count":` + fmt.Sprint(perNode) + `,"coverage":"c"}`
	}

	runner := &gapBRunner{byNode: byNode}

	var edges []string
	var nodeDefs []string
	for i := 0; i < nodes; i++ {
		id := fmt.Sprintf("n%02d", i)
		nodeDefs = append(nodeDefs, fmt.Sprintf(
			`{"id":%q,"type":"subagent","prompt":"p","result":[{"name":"findings","type":"array"}],"findings_field":"findings"}`, id))
	}
	doc := `{"version":1,"name":"gap-g4-bag","nodes":[` + strings.Join(nodeDefs, ",") + `],"edges":[` + strings.Join(edges, ",") + `]}`

	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	want := nodes * perNode
	got := len(run.bag.snapshot())
	if got != want {
		t.Fatalf("多节点 findings 应一条不丢：期望 %d 条，实际 %d", want, got)
	}
	t.Logf("并行前置钉成立：%d 节点 × %d 条 = %d 条全部落袋（bag.add 有锁，无丢失/无重复）", nodes, perNode, got)
}

// TestGapB_G4_NodeOrderIsTopological 记录串行调度下的拓扑序行为：
// 依赖节点必须在其上游之后执行。
//
// 这是 G4 并行化后的**正确性护栏**：并行不得破坏「依赖先于被依赖」的次序。
func TestGapB_G4_NodeOrderIsTopological(t *testing.T) {
	var order []string
	var mu sync.Mutex

	runner := &gapBRunner{
		answer: `{"findings":[],"count":0,"coverage":"c"}`,
		onRun: func(nodeID string) {
			mu.Lock()
			order = append(order, nodeID)
			mu.Unlock()
		},
	}

	doc := `{
  "version": 1,
  "name": "gap-g4-order",
  "nodes": [
    {"id":"a","type":"subagent","prompt":"a",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"},
    {"id":"b","type":"subagent","prompt":"b",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"},
    {"id":"c","type":"subagent","prompt":"c",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"}
  ],
  "edges": [{"from":"a","to":"c"},{"from":"b","to":"c"}]
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	mu.Lock()
	got := strings.Join(order, ",")
	mu.Unlock()

	idxC := strings.Index(got, "c")
	if idxC < 0 {
		t.Fatalf("节点 c 未执行，order=%s", got)
	}
	idxA := strings.Index(got, "a")
	idxB := strings.Index(got, "b")
	if idxA > idxC || idxB > idxC {
		t.Fatalf("拓扑序被破坏：被依赖节点 c 必须在 a/b 之后，order=%s", got)
	}
	t.Logf("拓扑序护栏成立：执行序 %s（c 在其两个上游之后）", got)
}

// ---- G6（tl1324 修复后断言，wantPassAfterFix）----

// TestG6_WorkflowSubagentRegistersIntoTaskMonitor（原
// TestTmpGapB_G6_WorkflowSubagentNotRegisteredInTaskMonitor，缺口钉改修复钉）：
// 装配 recorder 后，工作流子代理节点经 TaskSubagentRunner 执行 ⇒ taskmonitor
// store 出现 wf:<runId>:<nodeID> 条目（running → succeeded 完整生命周期）。
//
// 缺口期读数（wantCurrentlyPass，已留档于审计报告）：工作流子代理经 runner
// 直调 TaskTool.Execute 前台同步路径，不进 jobs.Manager ⇒ 面板不可见。
func TestG6_WorkflowSubagentRegistersIntoTaskMonitor(t *testing.T) {
	_, store := newPinMonitor(t)

	doc := `{
  "version": 1,
  "name": "gap-g6",
  "nodes": [
    {"id":"worker","type":"subagent","prompt":"do work",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"}
  ],
  "edges": []
}`
	gapBParse(t, doc) // 文档合法性留档（执行走生产 adapter，见下）

	// 直接驱动生产 adapter（登记发生在 TaskSubagentRunner.Run 内，fake
	// runner 旁路登记——可见性钉的全链路断言在 taskmonitor_pin_test.go
	// 的 e2e（真实 TaskTool）里）。
	tr := NewTaskSubagentRunner(newPinTaskTool(t, okProvider))
	if _, err := tr.Run(jobs.WithSession(context.Background(), "sess-gap-b"), SubagentRequest{
		NodeID:      "worker",
		Prompt:      "do work",
		Description: "gap-b worker",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	tasks := store.listAll(t)
	if len(tasks) != 1 {
		t.Fatalf("修复后 store 应恰有 1 条工作流节点登记，实际 %d 条：%+v", len(tasks), tasks)
	}
	want := "wf:norun:worker" // 直接驱动 runner 无 engine runID（norun 兜底仍保配对）
	if tasks[0].JobID != want {
		t.Fatalf("JobID = %q, want %q", tasks[0].JobID, want)
	}
	if tasks[0].State != taskmonitor.TaskStateSucceeded || tasks[0].RuntimeState != taskmonitor.RuntimeStateExited {
		t.Fatalf("登记应已终态（succeeded/exited），实际 state=%s runtime=%s", tasks[0].State, tasks[0].RuntimeState)
	}
	t.Logf("G6 修复钉成立：工作流子代理登记进 taskmonitor（%s），面板可见", tasks[0].TaskID)
}

// TestG6_WorkflowNodeEventsCarryNodeState 保持观测面留档：工作流节点事件
// 到节点级状态（引擎事件面不在 tl1324 范围内——子代理过程细节仍由
// TaskTool 转录承载）。原样保留自审计线回归钉。
func TestG6_WorkflowNodeEventsCarryNodeState(t *testing.T) {
	// 用一个捕获 sink 收集事件。
	var events []string
	var mu sync.Mutex
	sink := gapBCaptureSink{fn: func(e event.Event) {
		mu.Lock()
		defer mu.Unlock()
		if e.Workflow != nil {
			events = append(events, fmt.Sprintf("%v|%s|%s|%v|%s",
				e.Kind, e.Workflow.NodeID, e.Workflow.NodeType, e.Workflow.State, e.Workflow.Detail))
			return
		}
		events = append(events, fmt.Sprintf("%v|<no workflow>|", e.Kind))
	}}

	runner := &gapBRunner{answer: `{"findings":[],"count":0,"coverage":"c"}`}
	doc := `{
  "version": 1,
  "name": "gap-g6-events",
  "nodes": [
    {"id":"worker","type":"subagent","prompt":"do work",
     "result":[{"name":"findings","type":"array"}],"findings_field":"findings"}
  ],
  "edges": []
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, sink)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	mu.Lock()
	got := strings.Join(events, "\n")
	mu.Unlock()

	if !strings.Contains(got, "|worker|") {
		t.Fatalf("事件里应含节点 ID worker，实际：\n%s", got)
	}
	t.Logf("G6 观测面现状：节点事件只到节点级（worker），无子代理过程：\n%s", got)
}

// gapBCaptureSink 是最小 event.Sink 实现，抓取事件里的工作流节点信息。
// ⚠️ 命名带 gapB 前缀是为了避开仓库既有测试里的 captureSink
// （internal/workflow/service_test.go）—— 同包同名的类型会编译冲突。
type gapBCaptureSink struct {
	fn func(event.Event)
}

func (c gapBCaptureSink) Emit(e event.Event) {
	if c.fn != nil {
		c.fn(e)
	}
}
