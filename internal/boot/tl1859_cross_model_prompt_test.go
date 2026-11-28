package boot

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
)

// tl1859（方案A）验收③：池键放宽为 root-only 后，跨模型复用的 prompt 必须与
// 同模型冷建逐字相等——persona 是唯一已知的模型维度分支，由复用路径上的
// strip+prepend（ApplyOfficialDeepSeekV4ProPersona）重贴；除此之外不允许任何
// per-model 或 per-复用轮次的分叉（残留风险①的断言化：未知分叉在此红，按任务
// 书降级条款处理）。同时钉验收②的 boot 层标记（boot: assembly reuse
// prompt=true）与多轮复用不累积（同段重复追加即红）。
//
// 跨 persona 边界双向验证：ds/deepseek-v4-pro（official 端点，persona 生效）
// ⇄ prov-b/model-b1（第三方端点，persona 不生效）。
//
// tl1859 撞面记录：真实链路的 stored assembly.SystemPrompt 已含 collab block
// 与 persona（boot.go 存的是最终版），复用路径曾无条件再追加一次 collab
// block → 每复用一次多一份（多轮累积）——本测试正是抓这个的钉，修复=追加点
// 幂等化（strip 旧段再追加，与 persona strip+prepend 同构）。

// tl1859CrossModelTestConfig：双 provider（official deepseek + 第三方），全部
// 走 boot-token-profile-test mock kind——kind 决定 provider 构造，base_url 决定
// official 判定（officialProviderKind 看 host）。
const tl1859CrossModelTestConfig = `
default_model = "ds/deepseek-v4-pro"

[agent]
system_prompt = "BASE"

[[providers]]
name = "ds"
kind = "boot-token-profile-test"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-pro"

[[providers]]
name = "prov-b"
kind = "boot-token-profile-test"
base_url = "https://b.example.com"
model = "model-b1"
`

func tl1859PromptOf(t *testing.T, res *BuildResult) string {
	t.Helper()
	if res.Snapshot == nil {
		t.Fatal("build returned nil Snapshot; nothing to compare")
	}
	p := res.Snapshot.SystemPrompt()
	if strings.TrimSpace(p) == "" {
		t.Fatal("build produced an empty system prompt; nothing to compare")
	}
	return p
}

func TestTL1859CrossModelReusePromptMatchesColdBuild(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("tl1859-cross-model", testutil.Turn{Text: "ok"}))
	writeFile(t, dir, "reasonix.toml", tl1859CrossModelTestConfig)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	baseOpts := func(model string) Options {
		return Options{Model: model, Sink: event.Discard, MCPHostProfile: plugin.HostProfileInteractive}
	}

	// round1：A（persona 生效）冷建——池条目的来源。
	resA, err := BuildRuntime(context.Background(), baseOpts("ds/deepseek-v4-pro"))
	if err != nil {
		t.Fatalf("round1 cold build (A): %v", err)
	}
	t.Cleanup(resA.Controller.Close)
	promptA := tl1859PromptOf(t, resA)
	if !strings.HasPrefix(promptA, config.OfficialDeepSeekV4ProPersona) {
		t.Fatal("round1 (official deepseek-v4-pro) must carry the persona as the first paragraph; boundary fixture broken")
	}
	if resA.Assembly == nil || strings.TrimSpace(resA.Assembly.SystemPrompt) == "" {
		t.Fatal("round1 returned no assembly to pool")
	}

	// round2：复用 A 的池条目建 B（persona 不生效）——root-only 键下「换模型
	// 打开对话」走的就是这条路径。
	logBuf.Reset()
	optsB := baseOpts("prov-b/model-b1")
	optsB.RuntimeReload = RuntimeReload{ReuseAssembly: resA.Assembly, PreviousPlan: FullReusePlan()}
	resB, err := BuildRuntime(context.Background(), optsB)
	if err != nil {
		t.Fatalf("round2 reuse build (B from A's assembly): %v", err)
	}
	t.Cleanup(resB.Controller.Close)
	logs2 := logBuf.String()
	// 验收②（boot 层）：复用命中必须自报 prompt=true。
	if !strings.Contains(logs2, "boot: assembly reuse") {
		t.Fatalf("round2 missing the boot: assembly reuse line:\n%s", logs2)
	}
	if !strings.Contains(logs2, "prompt=true") {
		t.Fatalf("round2 reuse line must report prompt=true (the stored prompt is served):\n%s", logs2)
	}
	promptB := tl1859PromptOf(t, resB)

	// round3：B 冷建——逐字基准。
	resColdB, err := BuildRuntime(context.Background(), baseOpts("prov-b/model-b1"))
	if err != nil {
		t.Fatalf("round3 cold build (B): %v", err)
	}
	t.Cleanup(resColdB.Controller.Close)
	coldB := tl1859PromptOf(t, resColdB)

	if promptB != coldB {
		t.Fatalf("cross-model reuse prompt diverged from the cold build beyond the persona segment\n"+
			"reuse len=%d cold len=%d\nreuse head=%q\ncold  head=%q\n"+
			"(first diff at byte %d)",
			len(promptB), len(coldB), firstN(promptB, 200), firstN(coldB, 200), firstDiff(promptB, coldB))
	}

	// 反向跨边界：B 的产物复用建 A——prepend 方向。
	resColdB2, err := BuildRuntime(context.Background(), baseOpts("prov-b/model-b1"))
	if err != nil {
		t.Fatalf("reverse round cold build (B): %v", err)
	}
	t.Cleanup(resColdB2.Controller.Close)
	optsA := baseOpts("ds/deepseek-v4-pro")
	optsA.RuntimeReload = RuntimeReload{ReuseAssembly: resColdB2.Assembly, PreviousPlan: FullReusePlan()}
	resA2, err := BuildRuntime(context.Background(), optsA)
	if err != nil {
		t.Fatalf("reverse round reuse build (A from B's assembly): %v", err)
	}
	t.Cleanup(resA2.Controller.Close)
	promptA2 := tl1859PromptOf(t, resA2)
	if promptA2 != promptA {
		t.Fatalf("reverse reuse prompt (B→A) diverged from the A cold build\n"+
			"reuse len=%d cold len=%d (first diff at byte %d)",
			len(promptA2), len(promptA), firstDiff(promptA2, promptA))
	}

	// 多轮复用不累积：round2 的产物（复用轮自己发布的 assembly）再复用一轮，
	// prompt 必须仍与冷建逐字相等——同段重复追加（历史缺陷形态）在此红。
	optsB2 := baseOpts("prov-b/model-b1")
	optsB2.RuntimeReload = RuntimeReload{ReuseAssembly: resB.Assembly, PreviousPlan: FullReusePlan()}
	resB2, err := BuildRuntime(context.Background(), optsB2)
	if err != nil {
		t.Fatalf("round4 reuse build (B from B's reused assembly): %v", err)
	}
	t.Cleanup(resB2.Controller.Close)
	if got := tl1859PromptOf(t, resB2); got != coldB {
		t.Fatalf("second-generation reuse prompt diverged from the cold build (segment accumulation?)\n"+
			"reuse len=%d cold len=%d (first diff at byte %d)",
			len(got), len(coldB), firstDiff(got, coldB))
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func firstDiff(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}
