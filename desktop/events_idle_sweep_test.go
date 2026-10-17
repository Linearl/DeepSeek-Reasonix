package main

import (
	"errors"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// 任务 921 验收④：配置门是纯函数——只有 auto+cap 开启 sweep；off/manual/
// 无 cap 的 auto / nil 配置全部零行为（333 设计：manual 的清理入口留在存储
// 面板，sweep 不越权）。
func TestEventsIdleSweepFromConfig(t *testing.T) {
	if got := eventsIdleSweepFromConfig(nil); got.enabled {
		t.Fatalf("nil config must keep the sweep off: %+v", got)
	}
	manual := &config.Config{}
	if got := eventsIdleSweepFromConfig(manual); got.enabled {
		t.Fatalf("default (manual) must keep the sweep off: %+v", got)
	}
	off := &config.Config{EventsAutoRotation: "off"}
	if got := eventsIdleSweepFromConfig(off); got.enabled {
		t.Fatalf("off mode must keep the sweep off: %+v", got)
	}
	autoNoCap := &config.Config{EventsAutoRotation: "auto"}
	if got := eventsIdleSweepFromConfig(autoNoCap); got.enabled {
		t.Fatalf("auto without cap must keep the sweep off (save path already covers factor-only): %+v", got)
	}
	auto := &config.Config{EventsAutoRotation: "auto", EventsRotationFactor: 3, EventsRotationCapMB: 50}
	got := eventsIdleSweepFromConfig(auto)
	if !got.enabled || got.factor != 3 || got.capMB != 50 {
		t.Fatalf("auto+cap must enable with the configured thresholds: %+v", got)
	}
}

// 任务 921 验收④：决策矩阵——门/超限/忙/park 四道判定各守各的；park 重入
// 信号是「再涨 10% 或新活动」，与 710 的重警增长同一语义。
func TestEventsIdleSweepDecision(t *testing.T) {
	const mb = int64(1) << 20
	activity := int64(1790600000000)
	parked := &eventsIdleSweepPark{attemptSize: 65 * mb, activityAt: activity}

	cases := []struct {
		name       string
		enabled    bool
		overLimit  bool
		busy       bool
		size       int64
		park       *eventsIdleSweepPark
		activityAt int64
		wantDo     bool
		wantWhy    string
	}{
		{"switch off is zero behaviour", false, true, false, 100 * mb, nil, activity, false, "auto rotation off"},
		{"under limit skipped", true, false, false, 40 * mb, nil, activity, false, "under limit"},
		{"busy skipped", true, true, true, 100 * mb, nil, activity, false, "session busy"},
		{"eligible: over limit, no prior sweep", true, true, false, 100 * mb, nil, activity, true, "eligible"},
		{"parked: swept to 65MB, no regrowth yet", true, true, false, 70 * mb, parked, activity, false, "parked: awaiting regrowth or new activity"},
		{"re-arm: regrowth past 10%", true, true, false, 72 * mb, parked, activity, true, "eligible"},
		{"re-arm: new activity stamp", true, true, false, 70 * mb, parked, activity + 1, true, "eligible"},
	}
	for _, tc := range cases {
		ok, why := eventsIdleSweepDecision(tc.enabled, tc.overLimit, tc.busy, tc.size, tc.park, tc.activityAt)
		if ok != tc.wantDo || why != tc.wantWhy {
			t.Fatalf("%s: got (%v, %q), want (%v, %q)", tc.name, ok, why, tc.wantDo, tc.wantWhy)
		}
	}
}

// sweepPassHarness drives runEventsIdleSweepPass with an injected compact that
// replays a scripted before→after per path and records which paths were asked.
type sweepPassHarness struct {
	byPath map[string][2]int64 // path -> [before, after] the compact lands at
	errFor map[string]error
	asked  []string
}

func (h *sweepPassHarness) compact(path string) (int64, int64, error) {
	h.asked = append(h.asked, path)
	if err := h.errFor[path]; err != nil {
		return 0, 0, err
	}
	pair := h.byPath[path]
	return pair[0], pair[1], nil
}

var sweepKnobs = eventsIdleSweepConfig{enabled: true, factor: 3, capMB: 50}

// 任务 921 验收①（死会话形态，对应现场 20260922：104MB events / 40MB live）：
// 兜底重写把日志压回 cap 内；随后 park——同一活动期、未再涨 10% 的第二趟
// pass 不得再清一次（防「清完又清」的重写循环）；再涨 10% 后重新放行。
func TestEventsIdleSweepPassDeadSessionBackUnderCap(t *testing.T) {
	const mb = int64(1) << 20
	activity := int64(1790600000000)
	cands := []eventsIdleSweepCandidate{
		{Path: "dead.jsonl", EventsBytes: 104 * mb, LiveBytes: 40 * mb, LastActivityAt: activity},
	}
	park := map[string]eventsIdleSweepPark{}
	h := &sweepPassHarness{byPath: map[string][2]int64{"dead.jsonl": {104 * mb, 45 * mb}}}

	results := runEventsIdleSweepPass(cands, sweepKnobs, park, nil, h.compact)
	if len(h.asked) != 1 || h.asked[0] != "dead.jsonl" {
		t.Fatalf("first pass must compact the dead session exactly once: %v", h.asked)
	}
	if len(results) != 1 || results[0].Freed != 59*mb {
		t.Fatalf("first pass result = %+v, want one row freeing 59MB", results)
	}
	if p, ok := park["dead.jsonl"]; !ok || p.attemptSize != 45*mb || p.activityAt != activity {
		t.Fatalf("park must record the landed size and activity stamp: %+v ok=%v", p, ok)
	}

	// 第二趟：磁盘上已是清后水位（下一 tick 重新 stat）、活动未变 → park
	// 拦下，不得再问 compact。
	swept := []eventsIdleSweepCandidate{
		{Path: "dead.jsonl", EventsBytes: 45 * mb, LiveBytes: 40 * mb, LastActivityAt: activity},
	}
	runEventsIdleSweepPass(swept, sweepKnobs, park, nil, h.compact)
	if len(h.asked) != 1 {
		t.Fatalf("parked session must not be re-compactd without regrowth: asked=%v", h.asked)
	}

	// 再涨 10%+（45×1.1=49.5 → 50.5）：重新放行。
	grew := []eventsIdleSweepCandidate{
		{Path: "dead.jsonl", EventsBytes: 51 * mb, LiveBytes: 40 * mb, LastActivityAt: activity},
	}
	runEventsIdleSweepPass(grew, sweepKnobs, park, nil, h.compact)
	if len(h.asked) != 2 {
		t.Fatalf("10%% regrowth must re-arm the sweep: asked=%v", h.asked)
	}
}

// 任务 921 验收①（真超限形态，对应现场 20260927：102MB events / 64MB live）：
// 兜底重写砍掉尾巴（102→65MB）但压不进 cap——仍要 park，第二趟不得对同一
// 水位重复重写（每次重写都是纯 IO）。
func TestEventsIdleSweepPassTrueOverLimitParksAfterOneRewrite(t *testing.T) {
	const mb = int64(1) << 20
	activity := int64(1790600000000)
	cands := []eventsIdleSweepCandidate{
		{Path: "live-over-cap.jsonl", EventsBytes: 102 * mb, LiveBytes: 64 * mb, LastActivityAt: activity},
	}
	park := map[string]eventsIdleSweepPark{}
	h := &sweepPassHarness{byPath: map[string][2]int64{"live-over-cap.jsonl": {102 * mb, 65 * mb}}}

	results := runEventsIdleSweepPass(cands, sweepKnobs, park, nil, h.compact)
	if len(results) != 1 || results[0].Freed != 37*mb {
		t.Fatalf("one rewrite must free the non-live tail (37MB): %+v", results)
	}
	// 仍超 cap，但 park 记录水位后（下一 tick stat 到清后大小）第二趟必须静默。
	swept := []eventsIdleSweepCandidate{
		{Path: "live-over-cap.jsonl", EventsBytes: 65 * mb, LiveBytes: 64 * mb, LastActivityAt: activity},
	}
	runEventsIdleSweepPass(swept, sweepKnobs, park, nil, h.compact)
	if len(h.asked) != 1 {
		t.Fatalf("still-over-cap session must park after one rewrite: asked=%v", h.asked)
	}
}

// 每趟预算 + 大者优先：5 个超限候选只清 3 个，且是最大的三个（预算存在
// 的意义是防止升级后首扫一次性堆叠百 MB 级重写）。
func TestEventsIdleSweepPassBudgetLargestFirst(t *testing.T) {
	const mb = int64(1) << 20
	activity := int64(1790600000000)
	var cands []eventsIdleSweepCandidate
	h := &sweepPassHarness{byPath: map[string][2]int64{}}
	for i, size := range []int64{60, 70, 80, 90, 100} {
		path := string(rune('a'+i)) + ".jsonl"
		cands = append(cands, eventsIdleSweepCandidate{
			Path: path, EventsBytes: size * mb, LiveBytes: 10 * mb, LastActivityAt: activity,
		})
		h.byPath[path] = [2]int64{size * mb, (size - 20) * mb}
	}
	runEventsIdleSweepPass(cands, sweepKnobs, map[string]eventsIdleSweepPark{}, nil, h.compact)
	if len(h.asked) != eventsIdleSweepMaxPerPass {
		t.Fatalf("pass budget must cap rewrites at %d: asked=%v", eventsIdleSweepMaxPerPass, h.asked)
	}
	want := []string{"e.jsonl", "d.jsonl", "c.jsonl"} // 100/90/80MB
	for i, path := range want {
		if h.asked[i] != path {
			t.Fatalf("budget must land largest-first: asked=%v, want %v", h.asked, want)
		}
	}
}

// 忙碌与失败的边界：busy 候选不进 compact；lease 竞争失败（另一 runtime 抢先
// 拿到 lease）标 Skipped 且不 park——下一趟重试；其他失败同样不 park（瞬态
// IO），下一趟重试。
func TestEventsIdleSweepPassBusyAndFailureRetry(t *testing.T) {
	const mb = int64(1) << 20
	activity := int64(1790600000000)
	leaseErr := &agent.SessionLeaseError{}
	cands := []eventsIdleSweepCandidate{
		{Path: "busy.jsonl", EventsBytes: 90 * mb, LiveBytes: 10 * mb, LastActivityAt: activity, Busy: true},
		{Path: "raced.jsonl", EventsBytes: 80 * mb, LiveBytes: 10 * mb, LastActivityAt: activity},
		{Path: "broken.jsonl", EventsBytes: 70 * mb, LiveBytes: 10 * mb, LastActivityAt: activity},
	}
	park := map[string]eventsIdleSweepPark{}
	h := &sweepPassHarness{
		byPath: map[string][2]int64{},
		errFor: map[string]error{"raced.jsonl": leaseErr, "broken.jsonl": errors.New("disk full")},
	}

	results := runEventsIdleSweepPass(cands, sweepKnobs, park, nil, h.compact)
	if len(h.asked) != 2 {
		t.Fatalf("busy candidate must never reach compact: asked=%v", h.asked)
	}
	if len(park) != 0 {
		t.Fatalf("failed/skipped attempts must not park: %+v", park)
	}
	var raced, broken *eventsIdleSweepResult
	for i := range results {
		switch results[i].Path {
		case "raced.jsonl":
			raced = &results[i]
		case "broken.jsonl":
			broken = &results[i]
		}
	}
	if raced == nil || !raced.Skipped {
		t.Fatalf("lease race must be marked Skipped: %+v", results)
	}
	if broken == nil || broken.Skipped || broken.Error == "" {
		t.Fatalf("transient failure must carry Error without Skipped: %+v", results)
	}

	// 下一趟：两个失败会话都仍在候选里 → 重试（compact 再被问到）。
	h.errFor = map[string]error{}
	runEventsIdleSweepPass(cands[1:], sweepKnobs, park, nil, h.compact)
	if len(h.asked) != 4 {
		t.Fatalf("failed attempts must retry on the next pass: asked=%v", h.asked)
	}
}

// under-limit 候选是 pass 的绝大多数（每次 tick 全库扫描）：不进结果、不进
// compact，避免每 30 分钟给结果切片灌噪声行。
func TestEventsIdleSweepPassUnderLimitSilent(t *testing.T) {
	const mb = int64(1) << 20
	cands := []eventsIdleSweepCandidate{
		{Path: "small.jsonl", EventsBytes: 20 * mb, LiveBytes: 18 * mb, LastActivityAt: 1},
	}
	h := &sweepPassHarness{byPath: map[string][2]int64{}}
	results := runEventsIdleSweepPass(cands, sweepKnobs, map[string]eventsIdleSweepPark{}, nil, h.compact)
	if len(h.asked) != 0 || len(results) != 0 {
		t.Fatalf("under-limit session must be silent: asked=%v results=%+v", h.asked, results)
	}
}

// 任务 945 验收②（pass 级联动）：将被重写的多头候选先 retire 陈旧并发头、
// 再 compact（顺序即语义——rotation 只丢不可达节点，并发头 retire 后其独占
// 字节才不可达）；单头候选不问 retire（sidecar 预筛挡掉绝大多数 replay）；
// retire 失败不阻断 compact（没有它就退化为今天的清法，不该升级为新的失败面）。
func TestEventsIdleSweepPassRetiresStaleHeadsBeforeCompact(t *testing.T) {
	const mb = int64(1) << 20
	activity := int64(1790600000000)
	cands := []eventsIdleSweepCandidate{
		{Path: "multi.jsonl", EventsBytes: 100 * mb, LiveBytes: 40 * mb, LastActivityAt: activity, MultiHead: true},
		{Path: "single.jsonl", EventsBytes: 80 * mb, LiveBytes: 30 * mb, LastActivityAt: activity},
		{Path: "racy.jsonl", EventsBytes: 70 * mb, LiveBytes: 20 * mb, LastActivityAt: activity, MultiHead: true},
	}
	var retiredAsked []string
	retire := func(path string) ([]string, error) {
		retiredAsked = append(retiredAsked, path)
		if path == "racy.jsonl" {
			return nil, errors.New("replay torn")
		}
		return []string{"hdead"}, nil
	}
	h := &sweepPassHarness{byPath: map[string][2]int64{
		"multi.jsonl":  {100 * mb, 45 * mb},
		"single.jsonl": {80 * mb, 35 * mb},
		"racy.jsonl":   {70 * mb, 30 * mb},
	}}
	park := map[string]eventsIdleSweepPark{}

	results := runEventsIdleSweepPass(cands, sweepKnobs, park, retire, h.compact)
	if len(retiredAsked) != 2 || retiredAsked[0] != "multi.jsonl" || retiredAsked[1] != "racy.jsonl" {
		t.Fatalf("only multi-head candidates may cost a retire probe: %v", retiredAsked)
	}
	// retire 失败不阻断：三个候选都完成了 compact。
	if len(h.asked) != 3 {
		t.Fatalf("retire failure must not block the rewrite: asked=%v", h.asked)
	}
	if len(results) != 3 {
		t.Fatalf("every eligible candidate must land a result row: %+v", results)
	}
	for _, res := range results {
		if res.Error != "" {
			t.Fatalf("compact outcomes must be unchanged by retire: %+v", res)
		}
	}

	// 顺序：每个会话 retire 先于 compact（用共享序列验证）。
	// （顺序验证在下面的顺序专用测试中做——此处先验证集合并存。）

	// park 语义不受 retire 影响：parked 的多头会话不再问 retire 也不再 compact。
	swept := []eventsIdleSweepCandidate{
		{Path: "multi.jsonl", EventsBytes: 45 * mb, LiveBytes: 40 * mb, LastActivityAt: activity, MultiHead: true},
	}
	retiredAsked = nil
	runEventsIdleSweepPass(swept, sweepKnobs, park, retire, h.compact)
	if len(retiredAsked) != 0 || len(h.asked) != 3 {
		t.Fatalf("parked session must skip retire and compact alike: retire=%v compact=%v", retiredAsked, h.asked)
	}
}

// 任务 945 验收②（顺序）：同一会话内 retire 严格先于 compact——反过来就没有
// 意义（先 compact 清不到 retire 之后才不可达的字节）。
func TestEventsIdleSweepPassRetireRunsBeforeCompactPerSession(t *testing.T) {
	const mb = int64(1) << 20
	var seq []string
	retire := func(path string) ([]string, error) {
		seq = append(seq, "retire:"+path)
		return nil, nil
	}
	compact := func(path string) (int64, int64, error) {
		seq = append(seq, "compact:"+path)
		return 90 * mb, 40 * mb, nil
	}
	cands := []eventsIdleSweepCandidate{
		{Path: "m.jsonl", EventsBytes: 90 * mb, LiveBytes: 40 * mb, LastActivityAt: 1, MultiHead: true},
	}
	runEventsIdleSweepPass(cands, sweepKnobs, map[string]eventsIdleSweepPark{}, retire, compact)
	if len(seq) != 2 || seq[0] != "retire:m.jsonl" || seq[1] != "compact:m.jsonl" {
		t.Fatalf("retire must precede compact within one session: %v", seq)
	}
}
