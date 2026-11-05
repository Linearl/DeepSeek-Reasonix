package agent

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1396 三态红线测试。判据设计与任务书一致：
//
//	态① 全量压缩豁免绝对线（重写投影必然断前缀，理论必然不作 defect）；
//	态② 全量压缩相对下降线（hitAfter ≥ hitBefore × 场景系数）；
//	态③ 部分压缩绝对线（小段 ranges 增量压缩 ≥95%，tl1397 落地时生效）。
//
// 全量变换由真实投影构建器 buildVisibleCompressionProjection 驱动——判据
// 钉住的是压缩链的实际产出，不是它的一个模型。部分变换按 tl1397 的形态
// 建模（保头逐字节保留 + 中后段小折），tl1397 落地后其真实实现须过同一判据。

// synCompactionSession 生成一段合成会话：固定 system 头 + n 轮
// user/assistant 交错（约 1/3 轮带 toolcall+toolresult）。所有尺寸随机但
// 由 seed 锚定，保证判据读数可复现。
func synCompactionSession(rng *rand.Rand) []provider.Message {
	n := 10 + rng.IntN(30) // 10..39 轮
	text := func(lo, hi int) string {
		return strings.Repeat(string(rune('a'+rng.IntN(26))), lo+rng.IntN(hi-lo+1))
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("S", 1800+rng.IntN(800))},
	}
	for range n {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: text(100, 600)})
		if rng.IntN(3) == 0 {
			msgs = append(msgs,
				provider.Message{
					Role: provider.RoleAssistant,
					ToolCalls: []provider.ToolCall{{
						ID:        fmt.Sprintf("call_%d", rng.Int64N(1<<62)),
						Name:      "read_file",
						Arguments: `{"path":"` + text(8, 40) + `"}`,
					}},
				},
				provider.Message{Role: provider.RoleTool, ToolCallID: "x", Content: text(200, 800)})
		} else {
			msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: text(100, 600)})
		}
	}
	return msgs
}

// fullFoldProjection 走压缩链的真实投影构建器，把 visible[head:start] 折成
// 一条 summary（summary 尺寸按折叠内容的 foldRatio 近似典型压缩率）。
func fullFoldProjection(visible []provider.Message, start int, foldRatio float64) []provider.Message {
	fold := visible[1:start]
	foldChars := charsOfMessages(fold)
	summary := strings.Repeat("s", max(200, int(float64(foldChars)*foldRatio)))
	plan := visibleCompressionPlan{firstFold: 1}
	plan.foldMask = make([]bool, len(visible))
	for i := 1; i < start; i++ {
		plan.foldMask[i] = true
	}
	// buildVisibleCompressionProjection 自己包 formatSummaryMessage，这里传裸文本。
	return buildVisibleCompressionProjection(visible, plan, summary)
}

// partialFoldProjection 是 tl1397 小段增量压缩的形态模型：保头 [0:k]
// 逐字节保留，仅把 [k:end] 折成一条小 summary，尾部原样保留。
func partialFoldProjection(visible []provider.Message, k, end int, foldRatio float64) []provider.Message {
	foldChars := charsOfMessages(visible[k:end])
	summary := strings.Repeat("s", max(50, int(float64(foldChars)*foldRatio)))
	out := append(append([]provider.Message(nil), visible[:k]...), formatSummaryMessage(summary))
	return append(out, visible[end:]...)
}

// 标定池样本：before/cached（上一请求已缓存的前缀）压缩计划齐备。
type calibSample struct {
	turns      int
	startRatio float64 // fold 起点在会话消息中的位置
	foldRatio  float64 // 折叠压缩率
	hitBefore  float64
	hitAfter   float64
	ratio      float64 // hitAfter / hitBefore
}

func buildCalibSample(rng *rand.Rand) calibSample {
	visible := synCompactionSession(rng)
	n := len(visible)
	start := 1 + int(float64(n-1)*(0.3+rng.Float64()*0.5)) // fold 起点 30%..80%
	s := calibSample{
		turns:      n,
		startRatio: float64(start) / float64(n),
		foldRatio:  0.10 + rng.Float64()*0.20, // 典型压缩率 10%..30%
	}
	// cached = 上一请求的前缀（before 去掉最后一轮，即 2 条消息）。
	cached := visible[:max(1, n-2)]
	s.hitBefore = PrefixCacheHitRate(cached, visible)
	projected := fullFoldProjection(visible, start, s.foldRatio)
	s.hitAfter = PrefixCacheHitRate(cached, projected)
	if s.hitBefore <= 0 {
		s.hitBefore = 1e-9
	}
	s.ratio = s.hitAfter / s.hitBefore
	return s
}

// TestFullCompressionCacheRedline 态②门禁本体：合成池每个样本经真实投影
// 构建器做全量压缩，相对下降不得破线。系数是标定测试实测的保守下界
// （向下取 0.05 网格），标定测试与门禁共用同一个池，系数不可能漂移到
// 实测值之上。
func TestFullCompressionCacheRedline(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x71396, 0x1396))
	for i := range 240 {
		s := buildCalibSample(rng)
		if err := CheckCompressCacheRedline(CompressScenarioFull, s.hitBefore, s.hitAfter); err != nil {
			t.Fatalf("sample %d (turns=%d start=%.2f fold=%.2f): %v", i, s.turns, s.startRatio, s.foldRatio, err)
		}
	}
}

// TestFullCompressionExemptFromAbsoluteLine 态①豁免钉子：全量压缩的合法
// 样本普遍跌破 95% 绝对线——豁免是判据成立的必要条件，不是放水。同时
// 验证判定器对同一读数在两种场景下给出相反裁决。
func TestFullCompressionExemptFromAbsoluteLine(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x71396, 0x1396))
	belowAbsolute := 0
	for range 60 {
		s := buildCalibSample(rng)
		if s.hitAfter < PartialCompressCacheFloorRatio {
			belowAbsolute++
		}
		if err := CheckCompressCacheRedline(CompressScenarioFull, s.hitBefore, s.hitAfter); err != nil {
			t.Fatalf("full sample inside its relative budget must pass: %v", err)
		}
		if err := CheckCompressCacheRedline(CompressScenarioPartial, s.hitBefore, s.hitAfter); err == nil && s.hitAfter < PartialCompressCacheFloorRatio {
			t.Fatalf("the same reading under the partial scenario must break the absolute line (hit=%v)", s.hitAfter)
		}
	}
	if belowAbsolute == 0 {
		t.Fatal("calibration: no full-compression sample landed below the 95% absolute line — the exemption premise is not exercised by reality, re-check the pool")
	}
	t.Logf("exemption evidence: %d/60 full-compression samples sit below the 95%% absolute line (as theory requires) and all pass the full scenario", belowAbsolute)
}

// TestFullCompressionCacheCalibration 场景系数标定（验收③，首版数据入库）：
// 同一合成池实测 hitAfter/hitBefore，输出 min/mean/p10，并断言入库系数
// 不高于实测保守下界。改压缩链后重跑此测试即可复现标定。
func TestFullCompressionCacheCalibration(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x71396, 0x1396))
	ratios := make([]float64, 0, 240)
	minS := calibSample{}
	for i := range 240 {
		s := buildCalibSample(rng)
		ratios = append(ratios, s.ratio)
		if i == 0 || s.ratio < minS.ratio {
			minS = s
		}
	}
	sum := 0.0
	for _, r := range ratios {
		sum += r
	}
	// p10（保守分位）。
	sorted := append([]float64(nil), ratios...)
	slicesSortFloat(sorted)
	p10 := sorted[len(sorted)*10/100]
	t.Logf("calibration pool: n=%d min=%.4f (turns=%d start=%.2f fold=%.2f hitBefore=%.4f hitAfter=%.4f) mean=%.4f p10=%.4f",
		len(ratios), minS.ratio, minS.turns, minS.startRatio, minS.foldRatio, minS.hitBefore, minS.hitAfter, sum/float64(len(ratios)), p10)
	if minS.ratio < fullCompressionCacheFloorRatio {
		t.Fatalf("calibration: measured lower bound %.4f < committed coefficient %.2f — the stored coefficient is not a conservative bound; re-measure and re-commit it",
			minS.ratio, fullCompressionCacheFloorRatio)
	}
}

func slicesSortFloat(xs []float64) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// TestPartialCompressionAbsoluteLine 态③（tl1397 预留）：小段 ranges 增量
// 压缩必须保住 95% 前缀。合法样本 = 折叠起点在会话尾部 4% 之内的小折；
// 破线样本 = 折叠点前移（吃掉大段前缀）。tl1397 落地时其真实实现须过同
// 一判据——本测试钉住的是判据本身，不是某个候选实现。
func TestPartialCompressionAbsoluteLine(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x71397, 0x1397))
	for i := range 40 {
		visible := synCompactionSession(rng)
		n := len(visible)
		// 合法：折叠起点在尾部 3% 之内、只折到末尾（小段），压缩率任意。
		// 口径上 cached=before 全量（hitBefore=1 基线，比真实缓存更保守），
		// 保头部分必须贡献 ≥95% 的下一请求 token。
		k := n * 97 / 100
		partial := partialFoldProjection(visible, k, n, 0.1+rng.Float64()*0.5)
		hit := PrefixCacheHitRate(visible, partial)
		if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, hit); err != nil {
			t.Fatalf("legal partial sample %d (n=%d k=%d) must stay above the absolute line: %v", i, n, k, err)
		}
	}
	// 破线：同一会话把折叠点移到中段（50%），前缀大段失效。
	visible := synCompactionSession(rand.New(rand.NewPCG(0x71397, 0x1)))
	n := len(visible)
	k := n / 2
	partial := partialFoldProjection(visible, k, k+max(1, n/10), 0.3)
	hit := PrefixCacheHitRate(visible, partial)
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, hit); err == nil {
		t.Fatalf("mid-context partial fold must break the absolute line (hit=%v)", hit)
	}
	// 另一破线形态：折叠点够靠后但摘要比折叠段还大（负压缩），请求膨胀
	// 使命中率跌破 95% 口径。
	k = n * 97 / 100
	bloated := partialFoldProjection(visible, k, n, 3.0)
	hit = PrefixCacheHitRate(visible, bloated)
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, hit); err == nil {
		t.Fatalf("bloated partial fold must break the absolute line (hit=%v)", hit)
	}
}
