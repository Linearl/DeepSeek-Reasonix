package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
// 与「不重读历史」断言的取证面。队列弹尽后回落到 fallback(供收敛循环里
// 的多次全量折叠取同一回复)。
type rangesScriptProvider struct {
	mu       sync.Mutex
	replies  []string
	fallback string
	got      []provider.Request
}

func (p *rangesScriptProvider) Name() string { return "ranges-script" }

func (p *rangesScriptProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *rangesScriptProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.got = append(p.got, req)
	reply := p.fallback
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

// rangesSeedSession: 固定 system 头 + seedTurns 轮 user/assistant。前
// oldMarkerTurns 轮带 oldmarker(全量基线必把它们折掉),其余带 seedmarker
// (保留尾/增量合法携带)——「不重读已折叠历史」的断言标的就是 oldmarker。
func rangesSeedSession(seedTurns int) *Session {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("S", 4000)},
	}
	for i := range seedTurns {
		marker := "seedmarker"
		if i < seedTurns/2+2 {
			marker = "oldmarker"
		}
		msgs = append(msgs,
			provider.Message{Role: provider.RoleUser, Content: marker + " question " + strings.Repeat("q", 600)},
			provider.Message{Role: provider.RoleAssistant, Content: marker + " answer " + strings.Repeat("a", 600)},
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
	// 输入里既没有全量基线摘要,也没有「已被基线折叠」的早期历史——只读
	// 增量段(保留尾与 delta 是未折叠增量,合法入段;oldmarker 只存在于
	// 基线折叠区)。
	if strings.Contains(bigInput, strings.Repeat("D", 1000)) {
		t.Fatal("ranges summarizer re-read the prior digest (input must be the segment only)")
	}
	if strings.Contains(bigInput, "oldmarker") {
		t.Fatal("ranges summarizer re-read already-folded history (input must be the un-folded segment only)")
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
		if isCompactionSummary(m) {
			idx = i // 取最新一条(追加的小段摘要在保头前缀的末端)
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
	prov := &rangesScriptProvider{
		replies: []string{
			strings.Repeat("D", 30000),
			strings.Repeat("n", 600),
			strings.Repeat("n", 600),
		},
		fallback: strings.Repeat("M", 800), // 收尾 manual 全量(收敛循环复用;摘要须比被折区域小才能过装门)
	}
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
	// 全量路径并存(验收③):ranges 相位之后 manual 全量照常落地——换成
	// 更小的滚动摘要、投影不增大;force manual 每次只折半区,旧摘要的完全
	// 合并由既有全量语义增量完成,这里钉的是「可回收、无错、canonical 不动」。
	estBefore := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("closing manual compact: %v", err)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got > 3 {
		t.Fatalf("manual compact grew the summary count to %d", got)
	}
	if estAfter := a.estimatedVisibleRequestTokens(a.modelVisibleMessages()); estAfter > estBefore {
		t.Fatalf("manual compact grew the projection: %d -> %d tokens", estBefore, estAfter)
	}
	if len(sess.Messages) == 0 || len(sess.Messages) < canonicalBefore {
		t.Fatalf("canonical transcript mutated: before=%d after=%d", canonicalBefore, len(sess.Messages))
	}
}

// 验收①对照面:同一构造的长历史下,旧全量路径与新 ranges 路径的摘要请求
// 规模直接对照(「压缩耗时不随历史长度线性涨」的两路径读数)。
//
// 构造边界:钉内无网络,provider 秒回;真实压缩耗时的大头是 provider 对摘
// 要输入的预填充/计费,随输入线性涨——故主判据=摘要请求输入规模(计费尺,
// requestInputTokens),墙钟只含本地组装+fake 流耗,作为参考读数 t.Logf 输
// 出、不设硬阈值(防 flaky)。两 arm 共用同一 seed 历史(120 轮),靠 est 与
// 触发线的相对位置分道(与真实生产的分道几何一致):增量推过 0.8 触发线 →
// 旧全量通道(摘要输入=整段折叠区,O(历史));增量停在触发线下但跨 delta
// 下限 → 新 ranges 通道(笔记输入=增量段+窗口恒定尾,O(窗口) 不是
// O(历史))。首跑读数(2026-11-09):full=57295 / ranges=19242 冷尺 tokens
// (比值 3.0x;full 侧已被 preFold 预算 39181 钳住,seed 再大也不再涨——
// 钳前它要付 O(历史) 的 elide/drop 工作,钳后输入=窗口预算,历史长度仍
// 决定「多少工作被丢弃」)。断言 ranges < full 的 1/2(实测 33%,留 17pt
// 余量;更紧的系数会擦边)。
func TestRangesFoldVersusFullCompactionScaling(t *testing.T) {
	isolateRangesConfig(t, true)
	run := func(deltaExchanges int) (inputTokens int, wall time.Duration) {
		// 回复按 arm 次序供给(队列按请求次序弹出):第二条 = 本 arm 接管通道
		// 自己的摘要回复——全量 arm 用 F8000(压力摘要),ranges arm 用 n600
		// (小段笔记);错位会让笔记吃到全量回复而膨胀破线(首跑实测)。
		second := strings.Repeat("F", 8000) // 压力全量摘要(比折叠区小才能过装门)
		if deltaExchanges < 10 {
			second = strings.Repeat("n", 600) // ranges 小段笔记
		}
		prov := &rangesScriptProvider{replies: []string{
			strings.Repeat("D", 30000), // 基线摘要(几何门要保头前缀占主导)
			second,
		}}
		sess := rangesSeedSession(120)
		a := New(prov, tool.NewRegistry(), sess, Options{
			ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
		}, event.Discard)
		if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
			t.Fatalf("baseline compact: %v", err)
		}
		addRangesDelta(sess, deltaExchanges)
		start := time.Now()
		if err := a.PrepareContext(context.Background()); err != nil {
			t.Fatalf("PrepareContext: %v", err)
		}
		wall = time.Since(start)
		reqs := prov.requests()
		if len(reqs) < 2 {
			t.Fatalf("expected baseline digest + maintenance requests, got %d", len(reqs))
		}
		return requestInputTokens(reqs[len(reqs)-1]), wall
	}

	fullTokens, fullWall := run(55)    // 增量推过触发线(基线后 est≈11.6k+55×~0.6k≈45k ∈ [fold 40k, hard 50k]):旧全量通道接管
	rangesTokens, rangesWall := run(2) // 增量停在触发线下(est≈12.9k):新 ranges 通道接管
	if rangesTokens >= fullTokens/2 {
		t.Fatalf("ranges summarizer input not bounded by the segment: ranges=%d tokens vs full=%d tokens", rangesTokens, fullTokens)
	}
	t.Logf("same-seed(120 turns) scaling: full-path input=%d tokens (wall %v), ranges-path input=%d tokens (wall %v)",
		fullTokens, fullWall, rangesTokens, rangesWall)
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
