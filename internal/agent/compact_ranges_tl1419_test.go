package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl1419 (迭代三): model-driven ranges folds — 叠加不替代 (前置评估定案):
// the model is a new trigger SOURCE over the tl1397 transaction; the经济判据
// (delta floor / foldEconomics / savings floor) and the tl1396 state-③ red
// line stay hard gates for every source; the whole-range compress cooldown is
// replaced by the flood gate (savings floor + sliding window).
//
// 本文件钉住六条验收:
//  1. 模型发起单次压缩过限成功(entry 全链:开关→窗口→事务→回执翻译);
//  2. 低于单次收益限拒,拒因带计费尺读数(below-savings-floor);
//  3. 滑动窗口频次超限拒(frequency-window),窗口滑动后恢复;
//  4. 开关门:模型开关关 / ranges 通道开关关 → 可读拒因零动作;
//  5. tl1396 态③红线对模型触发源照常触发(节省过限但命中破线 → 拒);
//  6. 遥测观测面:telemetry 同行带 trigger=model_ranges + ranges_fold 读数。
//
// 冷却不适用(约束①)由构造保证:本路径从不触碰 lastExplicitFoldAt /
// foldCooldownInterval(全量 compress 工具的间隔闸),验收③的窗口连折测试
// 即反证——冷却若在,第二次连折会被 10min 间隔拦下而非窗口拦下。

// isolateModelRangesConfig 把 REASONIX_HOME 指到临时目录,落两道开关
// (模型开关 + ranges 通道开关)与洪水闸窗口参数;测试需要精确窗口行为。
func isolateModelRangesConfig(t *testing.T, windowMinutes, windowMax int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.SetExperimentalRangesCompact(true); err != nil {
		t.Fatalf("SetExperimentalRangesCompact: %v", err)
	}
	if err := cfg.SetExperimentalModelRangesCompact(true); err != nil {
		t.Fatalf("SetExperimentalModelRangesCompact: %v", err)
	}
	if err := cfg.SetModelRangesFoldWindowMinutes(windowMinutes); err != nil {
		t.Fatalf("SetModelRangesFoldWindowMinutes: %v", err)
	}
	if err := cfg.SetModelRangesFoldWindowMax(windowMax); err != nil {
		t.Fatalf("SetModelRangesFoldWindowMax: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
}

// newModelRangesAgent 造一个已装全量基线摘要、带 deltaExchanges 轮未折叠
// 增量的会话(迭代一 harness 同参数:窗口 50k,基线摘要 30000 字符保头)。
func newModelRangesAgent(t *testing.T, sink event.Sink, noteReplies ...string) *Agent {
	t.Helper()
	replies := append([]string{strings.Repeat("D", 30000)}, noteReplies...)
	prov := &rangesScriptProvider{replies: replies}
	sess := rangesSeedSession(40)
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 50_000, CompactRatio: 0.8, RecentKeep: 2,
	}, sink)
	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("baseline compact: %v", err)
	}
	return a
}

// 验收①:模型发起单次压缩过限成功——ok 回执带计费尺读数,折叠真装上。
func TestModelRangesFoldSuccessThroughEntry(t *testing.T) {
	isolateModelRangesConfig(t, 5, 2)
	a := newModelRangesAgent(t, event.Discard, strings.Repeat("n", 600))
	addRangesDelta(a.sess.conversation, 2)

	res, err := a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil {
		t.Fatalf("RangesFoldCompact: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("status = %q reason = %q, want ok", res.Status, res.Reason)
	}
	if res.FoldIndex != 1 {
		t.Fatalf("fold_index = %d, want 1", res.FoldIndex)
	}
	if res.SourceTokens <= 0 || res.ProjectionTokens <= 0 || res.ProjectionTokens >= res.SourceTokens {
		t.Fatalf("billing readings not populated: source=%d proj=%d", res.SourceTokens, res.ProjectionTokens)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 2 {
		t.Fatalf("summaries = %d, want 2 (digest + ranges note)", got)
	}
	if a.rangesFoldIndex.Load() != 1 {
		t.Fatalf("rangesFoldIndex = %d, want 1", a.rangesFoldIndex.Load())
	}
	if len(a.modelRangesFoldTimes) != 1 {
		t.Fatalf("window records = %d, want 1 (install must feed the flood-gate window)", len(a.modelRangesFoldTimes))
	}
}

// 验收②:低于单次收益限拒——膨胀笔记让实测节省跌破地板,拒因带读数;
// 拒约不记窗口(窗口防的是「安装抖缓存」,失败尝试不改缓存面)。
func TestModelRangesFoldSavingsFloorRejection(t *testing.T) {
	isolateModelRangesConfig(t, 5, 2)
	// 200000 字符笔记:比折叠段大一个量级,实测节省必为负。
	a := newModelRangesAgent(t, event.Discard, strings.Repeat("n", 200000))
	addRangesDelta(a.sess.conversation, 2)

	res, err := a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil {
		t.Fatalf("RangesFoldCompact: %v", err)
	}
	if res.Status != "rejected" {
		t.Fatalf("status = %q, want rejected (savings below floor)", res.Status)
	}
	if !strings.Contains(res.Reason, "below-savings-floor") {
		t.Fatalf("reason missing the savings-floor gate: %q", res.Reason)
	}
	if !strings.Contains(res.Reason, "billed tokens") {
		t.Fatalf("reason missing the billing-ruler readings: %q", res.Reason)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 1 {
		t.Fatalf("thin fold was installed: summaries = %d, want 1", got)
	}
	if a.rangesFoldIndex.Load() != 0 {
		t.Fatalf("rangesFoldIndex = %d, want 0 (reject must not count as a fold)", a.rangesFoldIndex.Load())
	}
	if len(a.modelRangesFoldTimes) != 0 {
		t.Fatalf("window records = %d, want 0 (rejections must not consume the window)", len(a.modelRangesFoldTimes))
	}
}

// 验收③:滑动窗口频次超限拒;窗口滑过后恢复。第二次连折被窗口拦下(而非
// 冷却)即「冷却不适用」的反证——全量 compress 的 10min 冷却若在,连折会
// 被间隔闸拦下。
func TestModelRangesFoldWindowRejection(t *testing.T) {
	isolateModelRangesConfig(t, 1, 1) // 1 分钟窗口,窗口内最多 1 次
	a := newModelRangesAgent(t, event.Discard,
		strings.Repeat("n", 600),
		strings.Repeat("n", 600),
	)
	addRangesDelta(a.sess.conversation, 2)

	res, err := a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil || res.Status != "ok" {
		t.Fatalf("first fold: status=%q err=%v reason=%q, want ok", res.Status, err, res.Reason)
	}
	// 第二次:窗口内已有 1 次(上限 1)→ 拒,拒因带窗口读数。该拒发生在任何
	// 摘要请求之前(零花费)。
	res, err = a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil {
		t.Fatalf("second fold: %v", err)
	}
	if res.Status != "rejected" || !strings.Contains(res.Reason, "frequency-window") {
		t.Fatalf("second fold status=%q reason=%q, want frequency-window rejection", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "limit 1 per 1 minutes") {
		t.Fatalf("reason missing window parameters: %q", res.Reason)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 2 {
		t.Fatalf("window-exhausted fold was installed: summaries = %d, want 2", got)
	}
	// 窗口滑过(把记录回拨 2 分钟 > 1 分钟窗)→ 恢复可折。第一次折叠已把旧
	// 增量折掉,先补新工作再发起(段门要求有未折叠增量)。
	addRangesDelta(a.sess.conversation, 2)
	a.modelRangesFoldTimes[0] -= int64(2 * time.Minute)
	res, err = a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil || res.Status != "ok" {
		t.Fatalf("fold after window slide: status=%q err=%v reason=%q, want ok", res.Status, err, res.Reason)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 3 {
		t.Fatalf("post-slide fold not installed: summaries = %d, want 3", got)
	}
}

// 验收④:开关门——模型开关关(出厂默认)零动作;ranges 通道开关关同样
// 可读拒因。两次都不发生任何摘要请求。
func TestModelRangesFoldSwitchGates(t *testing.T) {
	// 出厂默认:两道开关全关 → 模型开关先拦。
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
	res, err := a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil {
		t.Fatalf("RangesFoldCompact: %v", err)
	}
	if res.Status != "rejected" || !strings.Contains(res.Reason, "experimental_model_ranges_compact") {
		t.Fatalf("model switch off: status=%q reason=%q, want rejection naming the switch", res.Status, res.Reason)
	}
	if len(prov.requests()) != 1 {
		t.Fatalf("switch off: summarizer requests = %d, want only the baseline digest (zero action)", len(prov.requests()))
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 1 {
		t.Fatalf("switch off: summaries = %d, want 1", got)
	}

	// ranges 通道开关关、模型开关开 → 通道门拦,拒因点名通道开关。
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.SetExperimentalModelRangesCompact(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	res, err = a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil {
		t.Fatalf("RangesFoldCompact (ranges lane off): %v", err)
	}
	if res.Status != "rejected" || !strings.Contains(res.Reason, "experimental_ranges_compact") {
		t.Fatalf("ranges lane off: status=%q reason=%q, want rejection naming the lane switch", res.Status, res.Reason)
	}
	if len(prov.requests()) != 1 {
		t.Fatalf("ranges lane off: summarizer requests = %d, want unchanged", len(prov.requests()))
	}
}

// 验收⑤:tl1396 态③红线对模型触发源照常触发——构造「节省过限但命中破线」
// 的折叠(6 轮增量 ≈ 3.6k token,4000 字符笔记 ≈ 34k 保头前缀的 ~11.7%,
// 命中 ~89% < 95% 线;节省 ≈ 2.6k > 400 地板),拒因必须点名红线。
func TestModelRangesFoldRedlineStillTriggers(t *testing.T) {
	isolateModelRangesConfig(t, 5, 2)
	a := newModelRangesAgent(t, event.Discard, strings.Repeat("n", 4000))
	addRangesDelta(a.sess.conversation, 6)

	res, err := a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil {
		t.Fatalf("RangesFoldCompact: %v", err)
	}
	if res.Status != "rejected" {
		t.Fatalf("status = %q reason = %q, want rejected by the red line", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "cache red line") {
		t.Fatalf("reason missing the tl1396 red line: %q", res.Reason)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 1 {
		t.Fatalf("red-line-breaking fold was installed: summaries = %d, want 1", got)
	}
	if a.rangesFoldIndex.Load() != 0 {
		t.Fatalf("rangesFoldIndex = %d, want 0", a.rangesFoldIndex.Load())
	}
}

// 验收⑥:遥测观测面——模型触发源的安装沿迭代一同一事务面出数:同一行
// telemetry 带 trigger=model_ranges 与 ranges_fold/preserved/hit 读数,
// 「频次×缓存」联合观测对模型发起同样成立。
func TestModelRangesFoldTelemetrySurface(t *testing.T) {
	isolateModelRangesConfig(t, 5, 2)
	sink := &rangesEventSink{}
	a := newModelRangesAgent(t, sink, strings.Repeat("n", 600))
	addRangesDelta(a.sess.conversation, 2)

	res, err := a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil || res.Status != "ok" {
		t.Fatalf("RangesFoldCompact: status=%q err=%v reason=%q", res.Status, err, res.Reason)
	}
	notices := sink.telemetryNotices()
	found := false
	for _, d := range notices {
		if strings.Contains(d, "trigger=model_ranges") && strings.Contains(d, "ranges_fold=1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("telemetry missing model_ranges fold line (want trigger=model_ranges + ranges_fold=1) in %v", notices)
	}
}
