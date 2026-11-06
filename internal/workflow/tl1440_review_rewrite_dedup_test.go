package workflow

// tl1440 钉：对抗复核改写 what 后 (where,what) 去重键失配的确定性回填。
//
// 缺口（审计线 20261105 22:3x 复跑 run wf-04d73376e38d 实锤）：tl1326 B1 的
// 回填机制依赖复核节点 echo 引擎序号（prompt 契约只「请」模型带回），复核
// confirmed 改写措辞（「核实成立：…」）且不 echo 序号时，extractFindings 给
// 条目盖上复核节点自己的新序号，(where,what) 全等判据因 what 被改写而脱靶
// ⇒ 同一问题以新条目入袋：落袋 10 条 vs 真值 7 条（+43%），原条目滞留
// unconfirmed、复核改写版另标 verified。同 run 的对照：what 逐字未变的 3 条
// 靠幂等侥幸命中，被改写的 3 条全部失配。
//
// 修复（引擎侧确定性，不依赖模型复述序号）：completeSubagent 在落袋前把
// 「本节点新盖序号 + where 唯一命中 prompt 可见候选」的条目换键到原序号
// （raw map m["id"] 同步改写 ⇒ confirm evidence 携带原序号，verdicts 仍按
// 主键升级）；换键失败形态（幽灵空 where / 同 where 多候选）保持独立条目，
// 降级计数打 slog.Warn，conclusion 追加口径注明行。
//
// 与 TestGapB_G3_MergedEntryPromotesToVerified 的关系：那条钉「echo 序号的
// 同序号回填」形态；本文件补「复核改写 what 且不 echo 序号」的失配形态
// （单测盲区），并钉护栏不回归（同文件多问题独立保留）。

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// ---- fixture：run wf-04d73376e38d 的真实形态（where 保真，what 节选） ----

// tl1440 finding 片段：不带 id（复核未 echo 的真实形态）。
func tl1440Finding(where, what, evidence string) string {
	return `{"where":"` + where + `","what":"` + what + `","evidence":"` + evidence + `","severity":"low"}`
}

func tl1440WithSeverity(where, what, evidence, sev string) string {
	return `{"where":"` + where + `","what":"` + what + `","evidence":"` + evidence + `","severity":"` + sev + `"}`
}

const (
	tl1440Engine298 = "internal/workflow/engine.go:298"
	tl1440App149    = "desktop/app_session_files.go:149"
	tl1440Pool100   = "desktop/servepool_host.go:100"
	tl1440Pool326   = "desktop/servepool_host.go:326"
	tl1440Pool360   = "desktop/servepool_host.go:360"
	tl1440Net94     = "internal/netclient/netclient.go:94"
	tl1440Net245    = "internal/netclient/netclient.go:245"
)

// TestTL1440_RewrittenReviewMergesIntoOriginal 复跑实锤形态回放：三视角 7 条
// 原始（defect 1 + security 6）→ 对抗复核 confirmed 6 条全部不 echo 序号，
// 其中 3 条 what 被改写（复跑中失配入袋的正是这 3 条）→ 独立确认 verdicts
// 6 条成立。修复后：袋内 7 条（真值，不虚高）、改写回填原条目且升 verified、
// 淘汰保留项 pool326 不丢、同文件不同行独立成条、conclusion 注明口径。
func TestTL1440_RewrittenReviewMergesIntoOriginal(t *testing.T) {
	// 原始 7 条：what 原话。
	scanDefect := `{"findings":[` +
		tl1440Finding(tl1440Engine298, "gate 节点快照的 Round/Detail 写入全局 s.gateRound 而非该 gate 自己的轮次", "engine.go:93 单值字段；execute.go:266 每轮覆盖") +
		`],"count":1,"coverage":"c"}`
	scanSecurity := `{"findings":[` +
		tl1440WithSeverity(tl1440App149, "SessionTailExcerpts 与 SessionTailAgentReply（:161-167）接受前端传入的任意绝对路径，只校验 basename", "仅 TrimSpace + IsSessionTranscriptName", "medium") + "," +
		tl1440WithSeverity(tl1440Pool100, "servepool 网关 http.Server 未设置任何读写超时（ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout 均缺省为 0）", "net.Listen 0.0.0.0 + 无超时字段", "medium") + "," +
		tl1440Finding(tl1440Pool326, "ServePoolStatus 轮询返回值与 GatewayToken()（:333-335）把保护 0.0.0.0 网关的 bearer token 明文下发给前端 JS", "map 中 token 同一返回值") + "," +
		tl1440Finding(tl1440Pool360, "RequestOwnershipFromRemote 把 per-serve token 以 query 参数拼进 HTTP 请求 URL", "fmt.Sprintf 拼 release-session?token=") + "," +
		tl1440Finding(tl1440Net94, "NewHTTPClient 返回的 &http.Client{Transport: tr} 不设 client 级 Timeout", "boot.go:854 全零 opts 构造") + "," +
		tl1440Finding(tl1440Net245, "customProxyURL 对畸形 proxy_url 的 url.Parse 错误原样 fmt.Errorf 包装", "url.Error 含原始输入") +
		`],"count":6,"coverage":"c"}`

	// 复核 confirmed 6 条：全部无 id（真实失配形态）；app149/pool100/net94 的
	// what 被改写（正是复跑中脱靶的 3 条），engine298/pool360/net245 逐字未变
	// （复跑中靠幂等侥幸命中的 3 条）。evidence 一律复核改写版（「核实成立…」）。
	review := `{"confirmed":[` +
		tl1440Finding(tl1440Engine298, "gate 节点快照的 Round/Detail 写入全局 s.gateRound 而非该 gate 自己的轮次", "核实成立：engine.go:93 单值字段；execute.go:266 每轮覆盖") + "," +
		tl1440WithSeverity(tl1440App149, "SessionTailExcerpts 与 SessionTailAgentReply（:149-167）接受前端传入的任意绝对路径，只校验 basename 后缀即读取该文件尾部", "核实成立：仅 TrimSpace + IsSessionTranscriptName，无目录归属校验", "medium") + "," +
		tl1440WithSeverity(tl1440Pool100, "servepool 网关 http.Server 未设置任何读写超时（ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout 均为 0 默认值）", "核实成立：srv := &http.Server{Handler: gw} 无任何超时字段", "medium") + "," +
		tl1440Finding(tl1440Pool360, "RequestOwnershipFromRemote 把 per-serve token 以 query 参数拼进 HTTP 请求 URL", "核实成立：token 拼入 release-session?token=") + "," +
		tl1440Finding(tl1440Net94, "NewHTTPClient 返回 &http.Client{Transport: tr} 不设 client 级 Timeout；TransportOptions 零值", "核实成立：仅设置 Transport 无 Timeout") + "," +
		tl1440Finding(tl1440Net245, "customProxyURL 对畸形 proxy_url 的 url.Parse 错误原样 fmt.Errorf 包装", "核实成立：parse 错误文本含原始输入") +
		`],"rejected":[{"what":"无 where 的幻觉条目"}],"high_count":0,"medium_count":2,"low_count":4}`

	// 独立确认 verdicts：修复后 confirm evidence 里的 id 已被换键为原序号，
	// 模型按原序号回传（pool326=scan-security#3 未复核确认，verdict 省略）。
	confirm := `{"confirmed":true,"reason":"six reproduced","verdicts":[` +
		`{"id":"scan-defect#1","confirmed":true,"note":"reproduced"},` +
		`{"id":"scan-security#1","confirmed":true,"note":"reproduced"},` +
		`{"id":"scan-security#2","confirmed":true,"note":"reproduced"},` +
		`{"id":"scan-security#4","confirmed":true,"note":"reproduced"},` +
		`{"id":"scan-security#5","confirmed":true,"note":"reproduced"},` +
		`{"id":"scan-security#6","confirmed":true,"note":"reproduced"}]}`

	runner := &gapBRunner{byNode: map[string]string{
		"scan-defect":   scanDefect,
		"scan-security": scanSecurity,
		"reviewer":      review,
		"checker":       confirm,
	}}
	doc := `{
  "version": 1,
  "name": "tl1440-review-rewrite-dedup",
  "nodes": [
    {"id":"scan-defect","type":"subagent","prompt":"缺陷扫描",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"scan-security","type":"subagent","prompt":"安全扫描",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"对抗复核。缺陷视角：{{scan-defect.findings}} 安全视角：{{scan-security.findings}}",
     "result":[{"name":"confirmed","type":"array"},{"name":"rejected","type":"array"}],
     "findings_field":"confirmed"},
    {"id":"checker","type":"confirm","target":"reviewer"}
  ],
  "edges": [
    {"from":"scan-defect","to":"reviewer"},
    {"from":"scan-security","to":"reviewer"}
  ]
}`
	wf := gapBParse(t, doc)
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	report := run.buildReport(nil)

	got := report.Findings
	if len(got) != 7 {
		t.Fatalf("袋内应 7 条（复核改写回填原条目，不虚高；修复前为 10 条），实际 %d：%+v", len(got), got)
	}
	bySource := map[string]Finding{}
	for _, f := range got {
		if _, dup := bySource[f.SourceID]; dup {
			t.Fatalf("同一来源序号出现两次（重复入袋）：%s", f.SourceID)
		}
		bySource[f.SourceID] = f
	}
	// 复核改写且失配的 3 条：原条目被回填改写并升 verified（回填非新增）。
	for _, sid := range []string{"scan-security#1", "scan-security#2", "scan-security#5"} {
		f, ok := bySource[sid]
		if !ok {
			t.Fatalf("原条目 %s 丢失（被复核新条目顶替）：%+v", sid, got)
		}
		if f.Status != FindingVerified {
			t.Fatalf("%s 应经 confirm verdicts 升 verified（回填后按原序号升级），实际 %s", sid, f.Status)
		}
		if !strings.Contains(f.Evidence, "核实成立") {
			t.Fatalf("%s 的 evidence 应为复核改写版（回填非新增），实际 %q", sid, f.Evidence)
		}
		if !strings.Contains(f.Evidence, "[confirm] reproduced") {
			t.Fatalf("%s 的 evidence 应并入 confirm note，实际 %q", sid, f.Evidence)
		}
	}
	if f := bySource["scan-security#1"]; f.What != "SessionTailExcerpts 与 SessionTailAgentReply（:149-167）接受前端传入的任意绝对路径，只校验 basename 后缀即读取该文件尾部" {
		t.Fatalf("回填条目措辞应为复核改写版（后写覆盖），实际 %q", f.What)
	}
	// 淘汰保留项：pool326 保持 unconfirmed 不丢。
	if f := bySource["scan-security#3"]; f.Status != FindingUnconfirmed {
		t.Fatalf("复核未确认的 pool326 应保持 unconfirmed，实际 %s", f.Status)
	}
	// 逐字未变的 3 条：同样回填（evidence 换复核版）并升 verified。
	for _, sid := range []string{"scan-defect#1", "scan-security#4", "scan-security#6"} {
		f := bySource[sid]
		if f.Status != FindingVerified || !strings.Contains(f.Evidence, "核实成立") {
			t.Fatalf("%s 应回填复核 evidence 且 verified，实际 %s/%q", sid, f.Status, f.Evidence)
		}
	}
	// 同文件不同行（pool100/pool326/pool360 同文件三问题）独立成条：过度合并护栏。
	sameFile := 0
	for _, f := range got {
		if strings.HasPrefix(f.Where, "desktop/servepool_host.go:") {
			sameFile++
		}
	}
	if sameFile != 3 {
		t.Fatalf("同文件 3 个不同问题必须各落一条（过度合并护栏），实际 %d：%+v", sameFile, got)
	}
	// 换键落到 raw map：confirm evidence 携带原序号（模型可见、verdicts 可回）。
	raw, _ := json.Marshal(run.result["reviewer"].(map[string]any)["confirmed"])
	if strings.Contains(string(raw), "reviewer#") {
		t.Fatalf("复核 typed result 里的 id 应已换键为原序号（confirm evidence 契约），实际 %s", raw)
	}
	// conclusion 口径注明：13 条上报（7 原始 + 6 复核）合并为 7 条，两套数字对得上。
	if !strings.Contains(report.Conclusion, "dedup note: 13 findings were reported across nodes and merged into 7 entries") {
		t.Fatalf("conclusion 应注明口径（13 上报→7 条），实际 %q", report.Conclusion)
	}
	if !strings.Contains(report.Conclusion, "6 verified, 1 unconfirmed") {
		t.Fatalf("conclusion 口径应含状态计数（6 verified, 1 unconfirmed），实际 %q", report.Conclusion)
	}
	// 前置 sanity：复核 prompt 确实携带原序号（候选池成立的前提）。
	var reviewPrompt string
	for _, p := range runner.prompts {
		if strings.Contains(p, "对抗复核") {
			reviewPrompt = p
		}
	}
	if !strings.Contains(reviewPrompt, "scan-security#1") || !strings.Contains(reviewPrompt, "scan-defect#1") {
		t.Fatalf("复核 prompt 应携带上游序号（候选池前提），实际 %q", reviewPrompt)
	}
	t.Logf("tl1440 主形态成立：复核改写不 echo 序号时确定性回填——袋内 7 条真值、6 条 verified、pool326 保留、同文件 3 问题独立、conclusion 注明 13→7 口径")
}

// TestTL1440_GhostWhereAndMultiWhereGuard 幽灵 where 校验 + 同 where 多候选
// 护栏：复核输出空 where 条目（复跑 ⑥ 的幽灵形态）永不参与归并；上游同
// where 有两条（同文件两个问题）时一律不归并（G3 过度合并护栏不回归）——
// 两者都以独立条目落袋 + 降级计数可见。
func TestTL1440_GhostWhereAndMultiWhereGuard(t *testing.T) {
	// 扫描：a.go:1 两个不同问题 + b.go:2 一个问题。
	scan := `{"findings":[` +
		tl1440Finding("a.go:1", "问题甲：第一个校验缺失", "e1") + "," +
		tl1440Finding("a.go:1", "问题乙：第二个校验缺失", "e2") + "," +
		tl1440Finding("b.go:2", "问题丙：资源未释放", "e3") +
		`],"count":3,"coverage":"c"}`
	// 复核：改写 a.go:1 两条（同 where 多候选）+ 一条空 where（幽灵）+ b.go:2 改写一条。
	review := `{"confirmed":[` +
		tl1440Finding("a.go:1", "问题甲改写：第一个校验缺失", "核实成立：e1") + "," +
		tl1440Finding("a.go:1", "问题乙改写：第二个校验缺失", "核实成立：e2") + "," +
		tl1440Finding("", "幽灵条目：没给 where", "核实成立？") + "," +
		tl1440Finding("b.go:2", "问题丙改写：资源未释放", "核实成立：e3") +
		`],"rejected":[]}`
	runner := &gapBRunner{byNode: map[string]string{
		"scanner":  scan,
		"reviewer": review,
	}}
	doc := `{
  "version": 1,
  "name": "tl1440-ghost-multiwhere-guard",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"复核：{{scanner.findings}}",
     "result":[{"name":"confirmed","type":"array"}],
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
	if len(got) != 6 {
		t.Fatalf("同 where 多候选不归并 + 幽灵不归并 ⇒ 3 原始 + 3 复核 = 6 条独立条目，实际 %d：%+v", len(got), got)
	}
	// b.go:2 唯一候选：正常回填（不因前两条被护栏挡住而失效）。
	merged := false
	for _, f := range got {
		if f.SourceID == "scanner#3" && f.Status == FindingUnconfirmed && f.What == "问题丙改写：资源未释放" {
			merged = true
		}
	}
	if !merged {
		t.Fatalf("唯一 where 候选（b.go:2）应正常回填改写，实际 %+v", got)
	}
	// 口径行：raw 7 = 3 扫描 + 4 复核，落袋 6。
	report := run.buildReport(nil)
	if !strings.Contains(report.Conclusion, "dedup note: 7 findings were reported across nodes and merged into 6 entries") {
		t.Fatalf("conclusion 应注明 7→6 口径，实际 %q", report.Conclusion)
	}
	t.Logf("护栏成立：幽灵空 where 与同 where 多候选均不归并（独立条目保留），唯一候选仍回填，口径行 7→6")
}

// TestTL1440_DegradationVisible 退化可见性：复核不 echo 序号、引擎按 where
// 兜底归并时必须打 slog.Warn（run/node/计数可检索），不得静默——静默退化
// 正是复跑 +43% 虚高潜伏一整轮的原因。
func TestTL1440_DegradationVisible(t *testing.T) {
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	scan := `{"findings":[` + tl1440Finding("a.go:1", "原话甲", "e1") + `],"count":1,"coverage":"c"}`
	review := `{"confirmed":[` + tl1440Finding("a.go:1", "改写甲", "核实成立：e1") + `],"rejected":[]}`
	runner := &gapBRunner{byNode: map[string]string{
		"scanner":  scan,
		"reviewer": review,
	}}
	doc := `{
  "version": 1,
  "name": "tl1440-degradation-visible",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"复核：{{scanner.findings}}",
     "result":[{"name":"confirmed","type":"array"}],
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

	logs := buf.String()
	if !strings.Contains(logs, "findings merge fallback engaged") {
		t.Fatalf("引擎兜底归并必须打 WARN（不得静默），实际日志：%q", logs)
	}
	for _, want := range []string{"merged_by_where=1", "run=", "node=reviewer"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("WARN 应含可检索字段 %q，实际：%q", want, logs)
		}
	}
	if got := run.bag.snapshot(); len(got) != 1 || got[0].SourceID != "scanner#1" {
		t.Fatalf("兜底归并应回填原条目（1 条、原序号），实际 %+v", got)
	}
	t.Logf("退化可见成立：兜底归并打 WARN 且带 run/node/merged_by_where 字段")
}

// TestTL1440_EchoStillPrimaryNoNoise 反向口径：复核按契约 echo 序号时走既有
// 同序号回填，不触发兜底、不打 WARN（退化日志只留给真退化，避免正常路径
// 噪音）。
func TestTL1440_EchoStillPrimaryNoNoise(t *testing.T) {
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	scan := `{"findings":[` + tl1440Finding("a.go:1", "原话甲", "e1") + `],"count":1,"coverage":"c"}`
	// echo 序号（gapBFindingWithID）+ 措辞改写：tl1326 B1 既有形态。
	review := `{"confirmed":[` + gapBFindingWithID("a.go:1", "改写甲", "scanner#1") + `],"rejected":[]}`
	runner := &gapBRunner{byNode: map[string]string{
		"scanner":  scan,
		"reviewer": review,
	}}
	doc := `{
  "version": 1,
  "name": "tl1440-echo-primary",
  "nodes": [
    {"id":"scanner","type":"subagent","prompt":"scan",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"reviewer","type":"subagent","prompt":"复核：{{scanner.findings}}",
     "result":[{"name":"confirmed","type":"array"}],
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

	if logs := buf.String(); logs != "" {
		t.Fatalf("echo 契约达成时不应有任何退化 WARN，实际：%q", logs)
	}
	got := run.bag.snapshot()
	if len(got) != 1 || got[0].What != "改写甲" {
		t.Fatalf("echo 回填应仍 1 条且措辞取后写，实际 %+v", got)
	}
	t.Logf("echo 主路径不受影响：同序号回填零退化日志")
}
