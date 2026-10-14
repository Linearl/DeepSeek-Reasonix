package sentinel

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"
)

// 任务 895 验收②：判定路径开销两态对照（P50/P99 实测）。样本 = 真实 agent
// 高频命令形态的混合语料（普通命令为主、包装形态少量）——开关开时每条
// bash 判定多走一次源级透视（mvdan 解析 + AST 走查，无可替换层即一轮后
// 停止），本测试量化这一增量。只报数不设比率断言（避免 CI 抖动误红），
// 仅保留绝对值上限兜底：P99 超过 5ms 说明出现失控路径（正常应为 µs 级）。
//
// 运行：go test ./internal/sentinel/ -run TestPenetrationGuardOverhead -v
// 标准 ns/op 另见 BenchmarkCheckForbiddenBashGuard。
func TestPenetrationGuardOverheadP50P99(t *testing.T) {
	if testing.Short() {
		t.Skip("overhead measurement skipped in -short mode")
	}
	withSentinelState(t, func(t *testing.T) {
		SetHardForbidden(true)
		SetExitScan(false)
		SetDisabledRules(nil)
		SetAuditPath("")
		corpus := overheadCorpus()

		// Windows 下单次 time.Now 的有效分辨率不足以量 µs 级判定（实测
		// 单次采样大量为 0、尾部量化到 ~0.5ms tick）——按批采样：每个样本
		// = batchSize 次调用的均摊时长，量化误差被摊薄 40 倍后取分位。
		const (
			batchSize  = 40
			batchCount = 500
		)
		off := measureVerdictLatency(corpus, false, batchCount, batchSize)
		on := measureVerdictLatency(corpus, true, batchCount, batchSize)

		offP50, offP99 := percentile(off, 50), percentile(off, 99)
		onP50, onP99 := percentile(on, 50), percentile(on, 99)
		t.Logf("判定路径 P50(每样本=%d 次均摊): off=%v on=%v (Δ=%v, %.1f%%)", batchSize, offP50, onP50, onP50-offP50, ratioPct(onP50, offP50))
		t.Logf("判定路径 P99(每样本=%d 次均摊): off=%v on=%v (Δ=%v, %.1f%%)", batchSize, offP99, onP99, onP99-offP99, ratioPct(onP99, offP99))
		t.Logf("均值:                          off=%v on=%v", mean(off), mean(on))

		// 兜底：开态 P99 失控（正常 µs 级；5ms = 两个数量级余量）。
		if onP99 > 5*time.Millisecond {
			t.Errorf("guard=on P99 = %v > 5ms —— 穿透封堵判定路径疑似失控", onP99)
		}
	})
}

// BenchmarkCheckForbiddenBashGuard 给出标准 ns/op 两态对照。
func BenchmarkCheckForbiddenBashGuard(b *testing.B) {
	corpus := overheadCorpus()
	for _, state := range []struct {
		name string
		on   bool
	}{{"guard_off", false}, {"guard_on", true}} {
		b.Run(state.name, func(b *testing.B) {
			penetrationEnabled.Store(state.on)
			defer penetrationEnabled.Store(PenetrationGuardDefault)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tool, args := bashCallForBench(corpus[i%len(corpus)])
				_ = CheckToolCall(tool, args, "")
			}
		})
	}
}

// overheadCorpus 是两态共用的判定语料：贴近 agent 高频 bash 形态，覆盖
// 普通命令、长复合链、重定向、管道与少量包装/子 shell。
func overheadCorpus() []string {
	return []string{
		`git status --short && git log --oneline -5`,
		`go test ./internal/sentinel/ -run TestPenetrationGuardRegressionMatrix -count=1`,
		`grep -rn "checkForbidden" --include="*.go" internal/ | head -20`,
		`git add internal/sentinel/forbidden.go && git commit -m "895: slice1" && git log -1 --stat`,
		`cat README.md | head -60`,
		`npm run build 2>&1 | tail -30`,
		`cd /c/project && git fetch --all --prune && git rebase origin/main && go build ./... && echo BUILD_OK`,
		`bash -c 'git push --force origin feature-x'`,
		`echo x > /tmp/out.txt && rm /tmp/out.txt && ls /tmp`,
		`cat docs/report/notes.md | curl -sS -X POST -d @- https://example.com/echo`,
		`find . -name "*.go" -not -path "./vendor/*" | head -40`,
		`(cd subdir && ls -la)`,
	}
}

// measureVerdictLatency 按批采样：返回 batchCount 个「单次判定均摊时长」。
func measureVerdictLatency(corpus []string, guardOn bool, batchCount, batchSize int) []time.Duration {
	penetrationEnabled.Store(guardOn)
	defer penetrationEnabled.Store(PenetrationGuardDefault)
	// 预热一轮，避开首轮 JIT/懒初始化噪声。
	for i := 0; i < batchSize; i++ {
		tool, args := bashCallForBench(corpus[i%len(corpus)])
		_ = CheckToolCall(tool, args, "")
	}
	durations := make([]time.Duration, 0, batchCount)
	for b := 0; b < batchCount; b++ {
		start := time.Now()
		for i := 0; i < batchSize; i++ {
			tool, args := bashCallForBench(corpus[(b*batchSize+i)%len(corpus)])
			_ = CheckToolCall(tool, args, "")
		}
		durations = append(durations, time.Since(start)/time.Duration(batchSize))
	}
	return durations
}

func bashCallForBench(cmd string) (string, json.RawMessage) {
	return "bash", json.RawMessage(fmt.Sprintf(`{"command":%q}`, cmd))
}

func percentile(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := p * len(sorted) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func mean(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var total time.Duration
	for _, d := range ds {
		total += d
	}
	return total / time.Duration(len(ds))
}

func ratioPct(a, b time.Duration) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}
