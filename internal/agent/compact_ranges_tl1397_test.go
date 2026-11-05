package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1397 迭代一:ranges 小段增量压缩(实验档,默认关)。
//
// 现状(全量压缩):顶线后把整段折叠区重写成一条滚动摘要——下一请求前缀
// 全断(1396 态①豁免的必然),且摘要输入随历史线性涨(「越压越大」)。
// 目标(ranges):低于全量触发线的主动小折通道,把「上一摘要边界→活跃轮」
// 的未折叠增量折成一条追加式小摘要;保头前缀逐字节不动;tl1396 态③红线
// (部分压缩 ≥95%)在装前实测把关,破线拒装。
//
// 本文件钉住四条验收:
//  1. 摘要输入只含增量段,不随历史长度线性涨(验收①);
//  2. 保头前缀逐字节一致 + 装后实测命中 ≥ 95% 绝对线(验收②,态③由真实
//     实现在运行时过判据;膨胀摘要破线必须拒装);
//  3. 开关默认关 = Prepare 低于触发线零动作,与引入前逐字节一致(铁律 2);
//  4. 与全量压缩并存:小折可连续追加,manual 全量折叠收回单摘要,canonical
//     全程不变(验收③)。

// rangesScriptProvider 按次序弹出摘要回复,并记录每个请求——摘要输入边界
// 与「不重读历史」断言的取证面。
type rangesScriptProvider struct {
	mu      sync.Mutex
	replies []string
	got     []provider.Request
}

func (p *rangesScriptProvider) Name() string { return "ranges-script" }

func (p *rangesScriptProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *rangesScriptProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.got = append(p.got, req)
	reply := ""
	if len(p.replies) > 0 {
		reply = p.replies[0]
		p.replies = p.replies[1:]
	}
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: reply}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func (p *rangesScriptProvider) requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.got...)
}

// rangesEventSink 收事件,断言遥测观测面(ranges_fold 字段)用。
type rangesEventSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *rangesEventSink) Emit(e event.Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *rangesEventSink) telemetryNotices() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.events {
		if e.Kind == event.Notice && strings.Contains(e.Text, "compaction telemetry") {
			out = append(out, e.Detail)
		}
	}
	return out
}

// isolateRangesConfig 把 REASONIX_HOME 指到临时目录并按 needOn 落开关,
// 隔离宿主真实配置;返回后配置即下一次折叠活读到的值。
func isolateRangesConfig(t *testing.T, needOn bool) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if needOn {
		if err := cfg.SetExperimentalRangesCompact(true); err != nil {
			t.Fatalf("SetExperimentalRangesCompact: %v", err)
		}
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
}

// rangesSeedSession: 固定 system 头 + seedTurns 轮 user/assistant(带
// seedmarker 标记,断言「不重读历史」用)。
func rangesSeedSession(seedTurns int) *Session {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("S", 4000)},
	}
	for range seedTurns {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleUser, Content: "seedmarker question " + strings.Repeat("q", 600)},
			provider.Message{Role: provider.RoleAssistant, Content: "seedmarker answer " + strings.Repeat("a", 600)},
		)
	}
	return &Session{Messages: msgs}
}

// addRangesDelta 追加 deltaExchanges 轮新工作(每轮 ~2.4KB ≈ 600 计费 token),
// 让未折叠增量跨过 ranges 的触发下限。
func addRangesDelta(sess *Session, deltaExchanges int) {
	for range deltaExchanges {
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "delta turn " + strings.Repeat("d", 1200)})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "delta work " + strings.Repeat("w", 1200)})
	}
}

func countRangesSummaries(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		if isCompactionSummary(m) {
			n++
		}
	}
	return n
}

func requestInputTokens(req provider.Request) int {
	return estimateMessagesTokens(req.Messages)
}

func requestInputText(req provider.Request) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// 验收①主流程:全量建立基线 → 增量小折只读增量段,输入不随历史长度涨。
func TestRangesFoldSummarizerInputBoundedBySegment(t *testing.T) {
	isolateRangesConfig(t, true)
	run := func(seedTurns int) (inputTokens int, input string, summaries int) {
		prov := &rangesScriptProvider{replies: []string{
			strings.Repeat("D", 30000), // 全量基线摘要(大:几何门要保头前缀占主导)
			strings.Repeat("n", 600),   // ranges 小段笔记
		}}
		sess := rangesSeedSession(seedTurns)
		a := New(prov, tool.NewRegistry(), sess, Options{
			ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
		}, event.Discard)

		// 基线:manual force 全量压缩装一条滚动摘要。
		if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
			t.Fatalf("baseline compact: %v", err)
		}
		if got := countRangesSummaries(a.modelVisibleMessages()); got != 1 {
			t.Fatalf("baseline summaries = %d, want 1", got)
		}
		// 增量:跨过 delta 触发下限,走低于触发线的 ranges 通道。
		addRangesDelta(sess, 2)
		if err := a.PrepareContext(context.Background()); err != nil {
			t.Fatalf("PrepareContext (ranges lane): %v", err)
		}
		summaries = countRangesSummaries(a.modelVisibleMessages())
		reqs := prov.requests()
		if len(reqs) < 2 {
			t.Fatalf("expected digest + ranges summary requests, got %d", len(reqs))
		}
		last := reqs[len(reqs)-1]
		return requestInputTokens(last), requestInputText(last), summaries
	}

	smallTokens, _, smallSummaries := run(30)
	if smallSummaries != 2 {
		t.Fatalf("ranges fold did not append a segment summary: summaries = %d, want 2 (digest + ranges)", smallSummaries)
	}
	bigTokens, bigInput, bigSummaries := run(120)
	if bigSummaries != 2 {
		t.Fatalf("ranges fold did not append a segment summary (big seed): summaries = %d, want 2", bigSummaries)
	}
	// 历史翻倍,小折输入不得跟随涨(输入只含 system+增量段+指令)。
	if bigTokens > smallTokens*2 {
		t.Fatalf("ranges summarizer input grew with history: seed30=%d tokens, seed120=%d tokens", smallTokens, bigTokens)
	}
	// 输入里既没有全量基线摘要,也没有更早的 seed 历史——只读增量段。
	if strings.Contains(bigInput, strings.Repeat("D", 1000)) {
		t.Fatal("ranges summarizer re-read the prior digest (input must be the segment only)")
	}
	if strings.Contains(bigInput, "seedmarker") {
		t.Fatal("ranges summarizer re-read pre-digest history (input must be the segment only)")
	}
	t.Logf("ranges input: seed30=%d tokens, seed120=%d tokens (history doubled, input flat)", smallTokens, bigTokens)
}

// 验收②:保头前缀逐字节一致 + 装后实测命中过 95% 绝对线。
func TestRangesFoldKeepsHeadPrefixByteIdentical(t *testing.T) {
	isolateRangesConfig(t, true)
	prov := &rangesScriptProvider{replies: []string{
		strings.Repeat("D", 30000),
		strings.Repeat("n", 600),
	}}
	sess := rangesSeedSession(40)
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
	}, event.Discard)
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("baseline compact: %v", err)
	}
	addRangesDelta(sess, 2)
	pre := a.modelVisibleMessages()
	if err := a.PrepareContext(context.Background()); err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	post := a.modelVisibleMessages()
	idx := -1
	for i, m := range post {
		if isCompactionSummary(m) && i > 0 {
			idx = i
			break
		}
	}
	if idx <= 0 {
		t.Fatalf("no appended range summary found in the view (summaries=%d)", countRangesSummaries(post))
	}
	if len(pre) < idx {
		t.Fatalf("view shrank before the fold point: pre=%d post=%d", len(pre), idx)
	}
	for i := 0; i < idx; i++ {
		if post[i].Role != pre[i].Role || post[i].Content != pre[i].Content {
			t.Fatalf("head prefix byte-shifted at %d: pre(%s,%.40q) post(%s,%.40q)", i, pre[i].Role, pre[i].Content, post[i].Role, post[i].Content)
		}
	}
	hit := PrefixCacheHitRate(pre, post)
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, hit); err != nil {
		t.Fatalf("real ranges fold broke the state-3 red line: %v", err)
	}
	if a.rangesFoldIndex.Load() != 1 {
		t.Fatalf("rangesFoldIndex = %d, want 1", a.rangesFoldIndex.Load())
	}
}

// 验收②运行时门:几何门放行但真实摘要膨胀破线 → 拒装( summaries 不变)。
func TestRangesFoldRedlineRejectsBloatedNote(t *testing.T) {
	isolateRangesConfig(t, true)
	prov := &rangesScriptProvider{replies: []string{
		strings.Repeat("D", 30000),
		strings.Repeat("n", 200000), // 膨胀笔记:比折叠段还大,命中率必破 95%
	}}
	sess := rangesSeedSession(40)
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
	}, event.Discard)
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("baseline compact: %v", err)
	}
	addRangesDelta(sess, 2)
	if err := a.PrepareContext(context.Background()); err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 1 {
		t.Fatalf("bloated note was installed: summaries = %d, want 1 (red-line reject)", got)
	}
	if a.rangesFoldIndex.Load() != 0 {
		t.Fatalf("rangesFoldIndex = %d, want 0 (reject must not count as a fold)", a.rangesFoldIndex.Load())
	}
}

// 铁律 2:开关默认关,低于触发线的 Prepare 零动作。
func TestRangesFoldDisabledByDefault(t *testing.T) {
	isolateRangesConfig(t, false)
	prov := &rangesScriptProvider{replies: []string{strings.Repeat("D", 30000)}}
	sess := rangesSeedSession(40)
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
	}, event.Discard)
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("baseline compact: %v", err)
	}
	addRangesDelta(sess, 2)
	if err := a.PrepareContext(context.Background()); err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 1 {
		t.Fatalf("switch off: summaries = %d, want 1 (below-trigger Prepare must be a no-op)", got)
	}
	if a.rangesFoldIndex.Load() != 0 {
		t.Fatalf("switch off: rangesFoldIndex = %d, want 0", a.rangesFoldIndex.Load())
	}
	if len(prov.requests()) != 1 {
		t.Fatalf("switch off: extra summarizer requests made (%d total), want only the baseline digest", len(prov.requests()))
	}
}

// 验收③:与全量压缩并存——小折连续追加,manual 全量收回单摘要,canonical 不动。
func TestRangesFoldCoexistsWithFullCompression(t *testing.T) {
	isolateRangesConfig(t, true)
	prov := &rangesScriptProvider{replies: []string{
		strings.Repeat("D", 30000),
		strings.Repeat("n", 600),
		strings.Repeat("n", 600),
		strings.Repeat("M", 5000), // 收尾 manual 全量
	}}
	sess := rangesSeedSession(40)
	canonicalBefore := len(sess.Messages)
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
	}, event.Discard)
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("baseline compact: %v", err)
	}
	addRangesDelta(sess, 2)
	if err := a.PrepareContext(context.Background()); err != nil {
		t.Fatalf("first PrepareContext: %v", err)
	}
	addRangesDelta(sess, 2)
	if err := a.PrepareContext(context.Background()); err != nil {
		t.Fatalf("second PrepareContext: %v", err)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 3 {
		t.Fatalf("after two ranges folds summaries = %d, want 3 (digest + n1 + n2)", got)
	}
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("closing manual compact: %v", err)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 1 {
		t.Fatalf("after the manual full fold summaries = %d, want 1 (rolling digest restored)", got)
	}
	if len(sess.Messages) == 0 || len(sess.Messages) < canonicalBefore {
		t.Fatalf("canonical transcript mutated: before=%d after=%d", canonicalBefore, len(sess.Messages))
	}
}

// 观测面(三约束③预留):ranges 折叠的遥测必须带 ranges_fold 读数,
// 「频次×缓存抖动」联合观测由此面出数。
func TestRangesFoldTelemetryObservationSurface(t *testing.T) {
	isolateRangesConfig(t, true)
	prov := &rangesScriptProvider{replies: []string{
		strings.Repeat("D", 30000),
		strings.Repeat("n", 600),
	}}
	sess := rangesSeedSession(40)
	sink := &rangesEventSink{}
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
	}, sink)
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("baseline compact: %v", err)
	}
	addRangesDelta(sess, 2)
	if err := a.PrepareContext(context.Background()); err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	notices := sink.telemetryNotices()
	found := false
	for _, d := range notices {
		if strings.Contains(d, "ranges_fold=1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ranges telemetry observation missing ranges_fold=1 in %v", notices)
	}
}
