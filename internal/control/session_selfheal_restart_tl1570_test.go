package control

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1570 端到端：重启后打开会话的自愈按落盘实账判定。
//
// 现场（20261113 包90 装机）：重启 → 打开成熟大会话 → SelfHealOpenSession
// 首轮前必然无实账（任务 1183 锚纯内存）→ 超窗判据退计价估算 → 误判超窗 →
// Diet（剥图+折叠）→ 压缩失败连败 → 再 Diet → 裁剪最早历史。修复后锚随
// usage 采样落 .jsonl.meta，重启后（新 executor 实例）回读注入
// ActualContextTokens——本文件三颗钉：
//  ① 重启 + 落盘锚：按实账判，不再误判超窗、零 Diet；
//  ② 对照组（同 fixture 无锚）：估算确实判超窗并 Diet——钉住旧缺陷形状，
//     防fixture 松动后哨兵失效；
//  ③ 小会话 + 锚：healthy、零动作（零回退）。

const (
	// tl1570Window keeps the diet line near the fixture size: 5000×0.95=4750.
	tl1570Window = 5000
	// tl1570ProvName is the destination the fixture anchors are keyed to; the
	// provider-match gate must see it from the restarted executor too.
	tl1570ProvName = "tl1570-prov"
)

// tl1570BigTurns builds the transcript: each turn carries ~2500 chars of
// assistant text (~625 estimated tokens), so 8 turns estimate ≈11K tokens —
// far past the 4750 diet line — while its persisted anchor reads ≈3.1K.
func tl1570BigTurns(turns int) []provider.Message {
	big := strings.Repeat("word ", 500)
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "SYS"},
		{Role: provider.RoleUser, Content: "standing constraint: never change the public API"},
	}
	for range turns {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: big},
			provider.Message{Role: provider.RoleUser, Content: "continue"},
		)
	}
	return msgs
}

// tl1570ViewChars mirrors agent.msgChars for plain-content messages (no tool
// calls in this fixture), minus a 50-char safety margin so the anchor always
// sits under the live view — a delta of ≤50 chars prices at ≤13 fallback
// tokens and cannot cross the line on its own.
func tl1570ViewChars(msgs []provider.Message) int64 {
	n := int64(0)
	for _, m := range msgs {
		n += int64(len(m.Content))
	}
	return n - 50
}

// tl1570RestartedController seeds a transcript with the first process, then
// reopens it the way a relaunch does: fresh executor, LoadSession, Resume.
// The self-heal gate is held OFF through Resume (the real hook fires the heal
// in the background; these tests drive SelfHealOpenSession synchronously) and
// restored on cleanup — it is package-global state.
func tl1570RestartedController(t *testing.T, dir, name string, msgs []provider.Message) *Controller {
	t.Helper()
	savedGate := sessionSelfHealGate
	sessionSelfHealGate = nil
	t.Cleanup(func() { sessionSelfHealGate = savedGate })

	path := filepath.Join(dir, name)

	prov := &recordingProvider{name: tl1570ProvName}
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("SYS"), agent.Options{}, event.Discard)
	c := New(Options{Runner: exec, Executor: exec, SystemPrompt: "SYS", SessionDir: dir, SessionPath: path, Label: "seed", Sink: event.Discard})
	exec.Session().Messages = msgs
	if err := c.Snapshot(); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	prov2 := &recordingProvider{name: tl1570ProvName}
	exec2 := agent.New(prov2, tool.NewRegistry(), agent.NewSession("SYS"), agent.Options{}, event.Discard)
	c2 := New(Options{Runner: exec2, Executor: exec2, SystemPrompt: "SYS", SessionDir: dir, SessionPath: path, Label: "restart", Sink: event.Discard})
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	c2.Resume(loaded, path)
	return c2
}

func tl1570WriteAnchor(t *testing.T, path string, coveredChars int64) {
	t.Helper()
	err := agent.UpdateBranchMeta(path, false, func(m *agent.BranchMeta) error {
		m.ContextBaseline = &agent.ContextBaselineMeta{
			ProviderName:     tl1570ProvName,
			PromptTokens:     3000,
			CompletionTokens: 120,
			CoveredChars:     coveredChars,
			SampledAt:        time.Now().UTC(),
		}
		return nil
	})
	if err != nil {
		t.Fatalf("write anchor into meta sidecar: %v", err)
	}
}

func tl1570OpenSelfHeal(c *Controller) SelfHealOpenOutcome {
	// The fixture holds the gate off through Resume; flip it on for the
	// synchronous heal call (Cleanup restores the saved value).
	sessionSelfHealGate = func() bool { return true }
	return c.SelfHealOpenSession(tl1570Window)
}

// 钉①: restart + persisted anchor — the criterion judges on the provider's
// own accounting (usage_actual), the mature session stays healthy, nothing is
// rewritten, no backup grows.
func TestTL1570RestartJudgesByPersistedAnchor(t *testing.T) {
	msgs := tl1570BigTurns(8)
	c := tl1570RestartedController(t, t.TempDir(), "tl1570-big.jsonl", msgs)
	tl1570WriteAnchor(t, c.SessionPath(), tl1570ViewChars(msgs))

	out := tl1570OpenSelfHeal(c)
	if out.Dieted || len(out.Actions) > 0 || out.BackupDir != "" {
		t.Fatalf("persisted anchor did not stop the misjudgment diet: dieted=%v actions=%v backup=%q detail=%q",
			out.Dieted, out.Actions, out.BackupDir, out.Detail)
	}
	if !out.Healthy {
		t.Fatalf("session judged unhealthy with a fresh persisted anchor: detail=%q", out.Detail)
	}
}

// 钉②（对照组）: the identical fixture WITHOUT an anchor still walks the old
// path — estimate ≥95% window → diet. This pins the defect shape the fix
// removes; if it ever goes green without an anchor, the fixture no longer
// exercises the misjudgment and must be retightened, not celebrated.
func TestTL1570AnchorlessEstimateStillDietsTheFixture(t *testing.T) {
	msgs := tl1570BigTurns(8)
	c := tl1570RestartedController(t, t.TempDir(), "tl1570-noanchor.jsonl", msgs)

	out := tl1570OpenSelfHeal(c)
	if !out.Dieted {
		t.Fatalf("anchorless fixture stopped dieting — the tl1570 control pin is stale: detail=%q", out.Detail)
	}
}

// 钉③: small session + anchor — healthy with zero actions either way; the
// real-usage injection must never push a fitting session over the line.
func TestTL1570SmallSessionStaysHealthyZeroFallback(t *testing.T) {
	msgs := tl1570BigTurns(1)
	c := tl1570RestartedController(t, t.TempDir(), "tl1570-small.jsonl", msgs)
	tl1570WriteAnchor(t, c.SessionPath(), tl1570ViewChars(msgs))

	out := tl1570OpenSelfHeal(c)
	if !out.Healthy || out.Dieted || len(out.Actions) > 0 || out.BackupDir != "" {
		t.Fatalf("small session regressed under the persisted anchor: healthy=%v dieted=%v actions=%v backup=%q",
			out.Healthy, out.Dieted, out.Actions, out.BackupDir)
	}
}
