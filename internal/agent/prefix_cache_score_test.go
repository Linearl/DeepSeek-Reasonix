package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func scoreMsg(role provider.Role, text string) provider.Message {
	return provider.Message{Role: role, Content: text}
}

// 同一列表与纯追加不动前缀：命中率必须恒等 1。
func TestPrefixCacheHitRateIdentityAndAppend(t *testing.T) {
	base := []provider.Message{
		scoreMsg(provider.RoleSystem, strings.Repeat("S", 500)),
		scoreMsg(provider.RoleUser, strings.Repeat("U", 500)),
		scoreMsg(provider.RoleAssistant, strings.Repeat("A", 500)),
	}
	if got := PrefixCacheHitRate(base, base); got != 1 {
		t.Fatalf("identity: hit = %v, want 1", got)
	}
	appended := append(append([]provider.Message(nil), base...),
		scoreMsg(provider.RoleUser, strings.Repeat("N", 200)))
	// 口径：hit = 公共前缀/请求总长。追加的尾部是新 token，本身不在缓存里，
	// 所以 hit = 缓存流/新请求流 < 1——前缀未被破坏，只是请求变长了。
	// 精确读数：base 流 1530 / appended 流 1740。
	if got := PrefixCacheHitRate(base, appended); got <= 0.85 || got >= 0.91 {
		t.Fatalf("append: hit = %v, want ≈ 0.88 (uncached new tail dilutes, the prefix itself is intact)", got)
	}
}

// 中段折叠（头部保留）：命中率 = 保头流占请求总流的比例。
func TestPrefixCacheHitRateHeadRetainedFold(t *testing.T) {
	head := []provider.Message{
		scoreMsg(provider.RoleSystem, strings.Repeat("S", 1000)),
		scoreMsg(provider.RoleUser, strings.Repeat("U", 1000)),
	}
	tail := []provider.Message{scoreMsg(provider.RoleAssistant, strings.Repeat("A", 1000))}
	before := append(append([]provider.Message(nil), head...),
		scoreMsg(provider.RoleUser, strings.Repeat("M", 1000)), tail[0])
	after := append(append([]provider.Message(nil), head...),
		scoreMsg(provider.RoleUser, "summary"), tail[0])

	// cached = before 全量（上一请求即缓存的情形）。
	got := PrefixCacheHitRate(before, after)
	// 公共前缀 = 头两条（2000 content runes + 每条哨兵/role 头）；
	// after 总流 = 头两条 + summary 条 + 保留尾条（1000 runes）。
	want := 2018.0 / 3045.0
	if diff := got - want; diff > 0.02 || diff < -0.02 {
		t.Fatalf("fold: hit = %v, want ≈ %v (retained head over the post-compression total)", got, want)
	}
}

// 消息边界哨兵：公共前缀恰好止于内容分歧处，不得跨消息桥接。
func TestPrefixCacheHitRateMessageBoundary(t *testing.T) {
	cached := []provider.Message{
		scoreMsg(provider.RoleUser, "turn one"),
		scoreMsg(provider.RoleUser, "turn two"),
	}
	// 请求删掉了 "turn one"。缓存首条是 "turn one"，请求首条是 "turn two"：
	// 公共前缀 = 哨兵+role 头（10 runes）+ "turn "（5 runes），在 o/w 分叉；
	// 若实现跨消息桥接（对上缓存第二条 "turn two" 的完整流），读数会更高。
	request := []provider.Message{scoreMsg(provider.RoleUser, "turn two")}
	want := 15.0 / 18.0 // 哨兵(5)+role(4)+分隔(1)+共内容(5) ／ 同头(10)+"turn two"(8)
	if got := PrefixCacheHitRate(cached, request); got != want {
		t.Fatalf("boundary: hit = %v, want exactly %v (divergence must stop at the content, never bridge into a later cached message)", got, want)
	}
}

// LocalOnly / RawContent 不上车：与缓存口径无关的字段不进流。
func TestPrefixCacheHitRateIgnoresLocalFields(t *testing.T) {
	base := []provider.Message{scoreMsg(provider.RoleUser, "visible content")}
	local := provider.Message{Role: provider.RoleUser, Content: "visible content", LocalOnly: true, RawContent: "raw"}
	if got := PrefixCacheHitRate(base, []provider.Message{local}); got != 1 {
		t.Fatalf("local fields: hit = %v, want 1 (LocalOnly/RawContent never reach the provider)", got)
	}
}

// 空请求视作全命中（无可破坏的前缀），除数为零不炸。
func TestPrefixCacheHitRateEmptyRequest(t *testing.T) {
	if got := PrefixCacheHitRate([]provider.Message{scoreMsg(provider.RoleUser, "x")}, nil); got != 1 {
		t.Fatalf("empty request: hit = %v, want 1", got)
	}
	if got := PrefixCacheHitRate(nil, nil); got != 1 {
		t.Fatalf("both empty: hit = %v, want 1", got)
	}
}

// 三态判定器：态①全量豁免绝对线；态②全量相对下降；态③部分绝对线。
func TestCheckCompressCacheRedlineThreeStates(t *testing.T) {
	// 态①: 全量压缩合法地低于 95% 绝对线而不算 defect（豁免钉子）。
	if err := CheckCompressCacheRedline(CompressScenarioFull, 1.0, 0.30); err != nil {
		t.Fatalf("state① exemption: full scenario at hit=0.30 must pass without consulting the absolute line: %v", err)
	}
	// 同一数值走部分压缩口径必须红——两条线互不误伤。
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, 0.30); err == nil {
		t.Fatal("state③: partial scenario at hit=0.30 must break the absolute line")
	}
	// 态②: 相对下降达标/破线。
	if err := CheckCompressCacheRedline(CompressScenarioFull, 0.8, 0.8*fullCompressionCacheFloorRatio); err != nil {
		t.Fatalf("state②: hit exactly at before×coefficient must pass: %v", err)
	}
	if err := CheckCompressCacheRedline(CompressScenarioFull, 1.0, fullCompressionCacheFloorRatio-0.01); err == nil {
		t.Fatal("state②: hit below before×coefficient must break the relative line")
	}
	// 态③: 绝对线边界。
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, PartialCompressCacheFloorRatio); err != nil {
		t.Fatalf("state③: hit exactly at the absolute line must pass: %v", err)
	}
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, PartialCompressCacheFloorRatio-0.001); err == nil {
		t.Fatal("state③: hit below the absolute line must break it")
	}
	// 未知场景必须显式报错，不得静默放行。
	if err := CheckCompressCacheRedline("tail-truncate", 1.0, 1.0); err == nil {
		t.Fatal("unknown scenario must error, not pass")
	}
}

// 判定器读数必须与打分器口径一致（同一函数链，门禁与测试不分叉）。
func TestRedlineReadsScorerUnits(t *testing.T) {
	system := scoreMsg(provider.RoleSystem, strings.Repeat("S", 500))
	before := []provider.Message{
		system,
		scoreMsg(provider.RoleUser, strings.Repeat("U", 500)),
		scoreMsg(provider.RoleAssistant, strings.Repeat("A", 500)),
	}
	// 正常形态：head(system 2000) + 主体(20000) 折成 summary 3000 + 保留尾 4000。
	// hit ≈ head/after 总 ≈ 2000/9000+，高于相对系数、远低于 95% 绝对线——
	// 同一读数上两条线给出相反裁决，正是三态设计的意义。
	system = scoreMsg(provider.RoleSystem, strings.Repeat("S", 2000))
	before = []provider.Message{
		system,
		scoreMsg(provider.RoleUser, strings.Repeat("U", 10000)),
		scoreMsg(provider.RoleAssistant, strings.Repeat("A", 10000)),
	}
	normal := []provider.Message{
		system,
		scoreMsg(provider.RoleUser, strings.Repeat("s", 3000)),
		scoreMsg(provider.RoleAssistant, strings.Repeat("A", 4000)),
	}
	hitNormal := PrefixCacheHitRate(before, normal)
	if err := CheckCompressCacheRedline(CompressScenarioFull, 1.0, hitNormal); err != nil {
		t.Fatalf("full scenario should accept a normal head-retained fold (hit=%v): %v", hitNormal, err)
	}
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, hitNormal); err == nil {
		t.Fatalf("partial scenario must reject the same fold (hit=%v below the 95%% absolute line)", hitNormal)
	}
	// 劣化形态：system 也被重写、大摘要顶到头部——全量/部分两条线都必须红。
	degraded := []provider.Message{
		scoreMsg(provider.RoleSystem, "rewritten system"),
		scoreMsg(provider.RoleUser, strings.Repeat("s", 2000)),
	}
	hitDegraded := PrefixCacheHitRate(before, degraded)
	if err := CheckCompressCacheRedline(CompressScenarioFull, 1.0, hitDegraded); err == nil {
		t.Fatalf("full scenario must reject a fold that rewrites the fixed head (hit=%v)", hitDegraded)
	}
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, hitDegraded); err == nil {
		t.Fatalf("partial scenario must reject a fold that rewrites the fixed head (hit=%v)", hitDegraded)
	}
}
