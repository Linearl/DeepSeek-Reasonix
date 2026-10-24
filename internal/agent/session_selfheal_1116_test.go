package agent

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 1116：超大上下文卡死根治——repair 告警闭合（件 1）、启动 Diet-only
// 修复的文件级安全（件 2 依赖的 DietSessionMessages 契约）、回归防假绿（件 3
// 5c：图占 95% 的投影必须剥图触发且剥后达标，逐 case 断言精确动作/次数）。

// pngHeaderBase64 is the real 8-byte PNG magic so the 1025 resolver can sniff
// dimensions; a bare string of 'A's cannot be sniffed and would fall back to
// the flat price, which is not the field shape (33.7MB data URLs sniff fine).
func pngHeaderBase64(t *testing.T, width, height int, totalBytes int) string {
	t.Helper()
	raw := make([]byte, 0, totalBytes)
	raw = append(raw, 0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n')
	raw = append(raw, make([]byte, 16)...) // IHDR length + type padding
	raw[12] = 0
	raw[13] = 0
	raw[14] = 0
	raw[15] = 13
	raw = append(raw, []byte("IHDR")...)
	raw = append(raw,
		byte(width>>24), byte(width>>16), byte(width>>8), byte(width),
		byte(height>>24), byte(height>>16), byte(height>>8), byte(height))
	for len(raw) < totalBytes {
		raw = append(raw, 'P')
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

// imageDominantSession builds a DAG session whose projection is dominated by
// embedded images (~95% of estimated tokens) with a small text tail — the
// 新9 incident shape (26.8MB images / 35 messages vs a few KB of text each).
func imageDominantSession(t *testing.T, dir, name string, windowTokens int) string {
	t.Helper()
	path := dir + string(os.PathSeparator) + name
	base := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	// 3 张 1568×1080 大图（约 2.2MB base64 每张，剥图前估算 ~1.66M tokens）。
	for i := 0; i < 3; i++ {
		m := dagMsg(provider.RoleUser, fmt.Sprintf("screenshot-%d", i), fmt.Sprintf("IMG%d", i))
		m.Images = []string{pngHeaderBase64(t, 1568, 1080, 3_500_000)}
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second)))
		parent = m.ID
	}
	// 少量文本消息（< 5% 的估算）——长到剥图后仍占预算大头，使 fold 也必须
	// 触发（新9 形态：剥图后 projection 仍贴顶 94%，折叠是第二道收敛）。
	for i := 0; i < 20; i++ {
		m := dagMsg(provider.RoleAssistant, strings.Repeat("t", 40000), fmt.Sprintf("TXT%d", i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(10+i)*time.Second)))
		parent = m.ID
	}
	dagAppend(t, path, entries...)
	return path
}

// Test1116SelfHealWarnsWhenHydrateRefilledButDietSkipped is 件 1's acceptance:
// hydrate=done + diet=not-run + re-inspection still over-window must surface
// the overflow warning on the report AND persist it into the repair log —
// the 20261024 新9 shape (repair silently delivered a 33.7MB projection).
func Test1116SelfHealWarnsWhenHydrateRefilledButDietSkipped(t *testing.T) {
	path := selfHealFixture(t) // dominated head + watermark gap ⇒ hydrate is actionable
	const tinyWindow = 1_000_000

	report, err := SelfHealSession(path, SelfHealOptions{Hydrate: true, WindowTokens: tinyWindow})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatalf("expected a repair: %+v", report)
	}
	// hydrate=done, diet 未开（opts.Diet=false ⇒ 报告里根本没有 strip/fold 动作）。
	if act, ok := SelfHealActionByName(report.Actions, HealActionHydrate); !ok || act.Status != "done" {
		t.Fatalf("hydrate must be done: %+v", report.Actions)
	}
	if _, ok := SelfHealActionByName(report.Actions, HealActionStripImages); ok {
		t.Fatalf("diet must not have run (opts.Diet=false): %+v", report.Actions)
	}
	// 注意：这个 fixture 修完后 head_lag 仍在（hydrate-only 不 promote head），
	// 所以 report.Healthy=false 是预期；Warning 不得命中——因为超窗判据
	// （projection_overflow）本来就不在 issue 列表里（估算远低于窗口）。
	if report.Healthy {
		t.Fatalf("hydrate-only on a dominated fixture must keep head_lag: %+v", report)
	}
	for _, issue := range report.BeforeAfter {
		if issue.Code == HealthProjectionOverflow {
			t.Fatalf("hydrate-only fixture must not be over-window: %+v", report.BeforeAfter)
		}
	}
	if report.Warning != nil {
		t.Fatalf("non-over-window session must not warn: %+v", report.Warning)
	}

	// 端到端：把一个超窗的、已经 hydrate 过的会话用 Diet:false 的选项再修一遍，
	// 期望 Warning 命中（before 不 healthy，hydrate 不改体积，after 仍不健康）。
	// 注意：hydrate-only 会同时留下 head_lag（这里不 promote），所以 after 不
	// healthy；Warning 的判据只看 hydrate=done + diet 未跑 + 仍超窗三件事。
	warnPath := imageDominantSession(t, t.TempDir(), "warn.jsonl", tinyWindow)
	before := InspectSessionHealth(warnPath, HealthInspectOptions{WindowTokens: tinyWindow})
	if before.Healthy {
		t.Fatalf("image-dominant fixture must overflow: %+v", before)
	}
	// Diet:false ⇒ 报告里没有 strip/fold 动作；hydrate 在这个 DAG 上照常 done。
	warnReport, err := SelfHealSession(warnPath, SelfHealOptions{Hydrate: true, WindowTokens: tinyWindow})
	if err != nil {
		t.Fatal(err)
	}
	if !warnReport.Repaired {
		t.Fatalf("expected repair: %+v", warnReport)
	}
	if act, ok := SelfHealActionByName(warnReport.Actions, HealActionHydrate); !ok || act.Status != "done" {
		t.Fatalf("hydrate must be done on the image fixture: %+v", warnReport.Actions)
	}
	if _, ok := SelfHealActionByName(warnReport.Actions, HealActionStripImages); ok {
		t.Fatalf("diet must not have run (opts.Diet=false): %+v", warnReport.Actions)
	}
	if warnReport.Healthy {
		t.Fatalf("image-dominant session must stay over-window after hydrate-only: %+v", warnReport)
	}
	if warnReport.Warning == nil {
		t.Fatalf("hydrate done + diet not-run + still over-window must warn: %+v", warnReport)
	}
	if warnReport.Warning.Code != SelfHealWarningHydrateStillOverWindow {
		t.Fatalf("warning code = %q, want %q", warnReport.Warning.Code, SelfHealWarningHydrateStillOverWindow)
	}
	if !warnReport.Warning.HydrateActionDone || !warnReport.Warning.DietActionNotRun {
		t.Fatalf("warning flags wrong: %+v", warnReport.Warning)
	}
	if warnReport.Warning.SuggestedAction != "diet" {
		t.Fatalf("warning must point at diet: %+v", warnReport.Warning)
	}

	// 修复日志里必须带同一条 Warning（归因链：面板 ↔ repairlog 一致）。
	records, err := ListSessionRepairRecordsFor(warnPath)
	if err != nil || len(records) == 0 {
		t.Fatalf("repair records missing: %d err=%v", len(records), err)
	}
	last := records[len(records)-1]
	if last.Warning == nil || last.Warning.Code != SelfHealWarningHydrateStillOverWindow {
		t.Fatalf("repair log must persist the warning: %+v", last)
	}
}

// Test1116HydrateStillOverWindowCriterion is the 件 1 pure-function truth
// table — every combination that must (not) warn, with exact expectations.
func Test1116HydrateStillOverWindowCriterion(t *testing.T) {
	dietDone := []SelfHealAction{
		{Action: HealActionHydrate, Status: "done"},
		{Action: HealActionStripImages, Status: "done"},
	}
	dietSkipped := []SelfHealAction{
		{Action: HealActionHydrate, Status: "done"},
		{Action: HealActionStripImages, Status: "skipped"},
		{Action: HealActionFold, Status: "skipped"},
	}
	hydrateOnly := []SelfHealAction{{Action: HealActionHydrate, Status: "done"}}
	hydrateFailed := []SelfHealAction{{Action: HealActionHydrate, Status: "failed"}}

	cases := []struct {
		name            string
		actions         []SelfHealAction
		stillOverWindow bool
		want            bool
	}{
		{"hydrate done + diet skipped + over-window ⇒ warn", dietSkipped, true, true},
		{"hydrate done + no diet action + over-window ⇒ warn", hydrateOnly, true, true},
		{"hydrate done + diet done + over-window ⇒ no warn (diet ran)", dietDone, true, false},
		{"hydrate done + diet skipped + not over-window ⇒ no warn", dietSkipped, false, false},
		{"hydrate failed + over-window ⇒ no warn (hydrate not the culprit)", hydrateFailed, true, false},
	}
	for _, c := range cases {
		w, ok := detectHydrateStillOverWindow(c.actions, c.stillOverWindow, 1_000_000, 3_000_000)
		if ok != c.want {
			t.Fatalf("%s: warn=%t, want %t (warning=%+v)", c.name, ok, c.want, w)
		}
		if ok && w.Code != SelfHealWarningHydrateStillOverWindow {
			t.Fatalf("%s: code=%q", c.name, w.Code)
		}
		if ok && (w.ProjectionTokens != 3_000_000 || w.WindowTokens != 1_000_000) {
			t.Fatalf("%s: readings wrong: %+v", c.name, w)
		}
	}
}

// Test1116ImageDominantProjectionDietRegression is 件 3-5c's anti-fake-green
// regression: a projection dominated by images (~95% of estimated tokens,
// mirroring the 26.8MB / 33.7MB 新9 shape) must (a) trip the overflow
// criterion, (b) trigger DietSessionMessages, (c) strip every data-URL payload
// and fold the old text so the estimate lands under the window — with exact
// action statuses and counts, never a bare bool.
func Test1116ImageDominantProjectionDietRegression(t *testing.T) {
	const window = 100_000 // far below the image-dominated estimate
	path := imageDominantSession(t, t.TempDir(), "img95.jsonl", window)

	before := InspectSessionHealth(path, HealthInspectOptions{WindowTokens: window})
	if before.Healthy {
		t.Fatalf("image-dominant projection must overflow: %+v", before)
	}
	if before.ProjectionTokens < int(float64(window)*projectionOverflowRatio) {
		t.Fatalf("fixture estimate %d must breach the window %d", before.ProjectionTokens, window)
	}
	// 图占比 ≥ 90%（防假绿前提：确实由图撑爆，不是文本）。
	textOnly := estimateProjectionTokens(stripMessageImages(mustSnapshot(t, path)))
	if float64(textOnly) > 0.25*float64(before.ProjectionTokens) {
		t.Fatalf("fixture must be image-dominant: text-only %d vs full %d", textOnly, before.ProjectionTokens)
	}

	report, err := SelfHealSession(path, SelfHealOptions{Diet: true, WindowTokens: window})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatalf("expected repair: %+v", report)
	}
	// 精确动作次数断言（勿用 bool）：strip=done 且 fold=done，各恰好一次。
	status := map[string]int{}
	for _, a := range report.Actions {
		if a.Status == "done" {
			status[a.Action]++
		}
	}
	if status[HealActionStripImages] != 1 {
		t.Fatalf("strip_images done count = %d, want exactly 1: %+v", status[HealActionStripImages], report.Actions)
	}
	if status[HealActionFold] != 1 {
		t.Fatalf("fold done count = %d, want exactly 1: %+v", status[HealActionFold], report.Actions)
	}
	after := InspectSessionHealth(path, HealthInspectOptions{WindowTokens: window})
	if !after.Healthy {
		t.Fatalf("diet must bring the estimate under the window: %+v", after.Issues)
	}
	if after.ProjectionTokens >= int(float64(window)*projectionOverflowRatio) {
		t.Fatalf("post-diet estimate %d must sit below the overflow ratio", after.ProjectionTokens)
	}
	if report.Warning != nil {
		t.Fatalf("diet run must not warn: %+v", report.Warning)
	}
	// 剥图后零 data: payload 存留。
	for _, m := range mustSnapshot(t, path) {
		for _, img := range m.Images {
			if strings.HasPrefix(img, "data:") {
				t.Fatalf("embedded image survived the diet: %d bytes", len(img))
			}
		}
	}
}

func mustSnapshot(t *testing.T, path string) []provider.Message {
	t.Helper()
	s, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	return s.Snapshot()
}
