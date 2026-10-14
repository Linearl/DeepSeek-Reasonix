package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSessionFixture writes one session's file family under root/projects/<dir>,
// reusing the same live-growth rules the sentinel must honor (.trash skipped,
// *-recovery-* excluded, transcript vs events classified by store helpers).
func writeSessionFixture(t *testing.T, root, dir string, files map[string]int) {
	t.Helper()
	for name, size := range files {
		p := filepath.Join(root, "projects", dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 任务886 ①：同一遍 walk 必须点名最大 transcript 与最大 events 文件——
// 哨兵的全部价值在「是哪个会话」，总量口径给不出这个答案（新6 事故即此）。
// .trash 与 *-recovery-* 是既有 live-growth 规则，不得被 top 统计漏掉。
func TestWalkProjectsTopsNamesLargestSessionFiles(t *testing.T) {
	root := t.TempDir()
	writeSessionFixture(t, root, "sessions", map[string]int{
		"20261010-collab.jsonl":          70 << 20,  // 70MB transcript → 超 64MB 哨兵线
		"20261010-collab.events.jsonl":   120 << 20, // 120MB events → 超 100MB 哨兵线
		"20261010-collab.turns.jsonl":    1 << 10,
		"20261010-collab.guardian.jsonl": 1 << 10,
	})
	writeSessionFixture(t, root, "sessions", map[string]int{
		"20260927-collab.jsonl":        40 << 20, // 更小的 transcript，不得夺冠
		"20260927-collab.events.jsonl": 50 << 20,
	})
	// 淘汰件：.trash 与 recovery 副本比所有活件都大，必须被排除。
	writeSessionFixture(t, root, "sessions/.trash", map[string]int{
		"dead.jsonl":        300 << 20,
		"dead.events.jsonl": 300 << 20,
	})
	writeSessionFixture(t, root, "sessions", map[string]int{
		"old-recovery-abcdef12.jsonl":        200 << 20,
		"old-recovery-abcdef12.events.jsonl": 200 << 20,
	})

	_, _, tops := walkProjectsBytesMB(root)
	if !strings.HasSuffix(tops.transcriptPath, filepath.Join("sessions", "20261010-collab.jsonl")) {
		t.Fatalf("transcript top = %q, want 20261010-collab.jsonl (.trash/recovery 小件漏排即此错)", tops.transcriptPath)
	}
	if got := tops.transcriptBytes >> 20; got != 70 {
		t.Fatalf("transcript top bytes = %dMB, want 70MB", got)
	}
	if !strings.HasSuffix(tops.eventsPath, filepath.Join("sessions", "20261010-collab.events.jsonl")) {
		t.Fatalf("events top = %q, want 20261010-collab.events.jsonl", tops.eventsPath)
	}
	if got := tops.eventsBytes >> 20; got != 120 {
		t.Fatalf("events top bytes = %dMB, want 120MB", got)
	}
}

// 任务886 ②：阈值下静默、阈值上 WARN 且带 path+remedy（转线建议）——
// 「触发且说清下一步」是哨兵的验收本体，缺 remedy 等于只报不导。
func TestWarnSessionSizesThresholds(t *testing.T) {
	m := &perfMonitor{warnGate: newPerfWarnGate()}

	capture := func(mutate func()) string {
		t.Helper()
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		defer slog.SetDefault(prev)
		mutate()
		m.warnSessionSizes(time.Now())
		return buf.String()
	}

	// 阈值下：静默。
	m.sizeTops = projectFileTops{
		transcriptPath:  filepath.Join("s", "ok.jsonl"),
		transcriptBytes: 63 << 20,
		eventsPath:      filepath.Join("s", "ok.events.jsonl"),
		eventsBytes:     99 << 20,
	}
	if out := capture(func() {}); out != "" {
		t.Fatalf("阈值下不应告警，got:\n%s", out)
	}

	// transcript 超限：WARN 且指名文件 + 转线 remedy；events 不重复报。
	m.sizeTops = projectFileTops{
		transcriptPath:  filepath.Join("s", "new6.jsonl"),
		transcriptBytes: 87 << 20,
		eventsPath:      filepath.Join("s", "new6.events.jsonl"),
		eventsBytes:     50 << 20,
	}
	out := capture(func() {})
	for _, want := range []string{"sessionTranscriptMb", "new6.jsonl", "转线", "64"} {
		if !strings.Contains(out, want) {
			t.Fatalf("transcript 超限告警缺 %q，got:\n%s", want, out)
		}
	}

	// events 超限：WARN 且指名 events 文件。
	m.sizeTops = projectFileTops{
		transcriptPath:  filepath.Join("s", "ok.jsonl"),
		transcriptBytes: 10 << 20,
		eventsPath:      filepath.Join("s", "new6.events.jsonl"),
		eventsBytes:     238 << 20,
	}
	out = capture(func() {})
	for _, want := range []string{"sessionEventsFileMb", "new6.events.jsonl", "events_auto_rotation"} {
		if !strings.Contains(out, want) {
			t.Fatalf("events 超限告警缺 %q，got:\n%s", want, out)
		}
	}
}

// 任务886 ③：同一 metric 15 分钟冷却内重复超限被 warnGate 抑制，
// 且超限值继续恶化（≥1.1×）可重开——沿用298 的防刷屏契约，哨兵不破例。
func TestWarnSessionSizesRateLimitedByGate(t *testing.T) {
	m := &perfMonitor{warnGate: newPerfWarnGate()}
	m.sizeTops = projectFileTops{
		transcriptPath:  filepath.Join("s", "new6.jsonl"),
		transcriptBytes: 87 << 20,
	}

	fire := func(now time.Time) string {
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		defer slog.SetDefault(prev)
		m.warnSessionSizes(now)
		return buf.String()
	}

	base := time.Now()
	first := fire(base)
	if !strings.Contains(first, "sessionTranscriptMb") {
		t.Fatalf("首次超限必须告警，got:\n%s", first)
	}
	if second := fire(base.Add(time.Minute)); second != "" {
		t.Fatalf("冷却期内重复超限应被抑制，got:\n%s", second)
	}
	// 恶化重开：+20% 超过 rearm 比例（1.1×）。
	m.sizeTops.transcriptBytes = 105 << 20
	if third := fire(base.Add(2 * time.Minute)); !strings.Contains(third, "sessionTranscriptMb") {
		t.Fatalf("恶化后的重复超限应重开告警，got:\n%s", third)
	}
}
