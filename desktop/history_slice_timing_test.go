package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// 任务 451 分相打点的单元测试：trace 的累计/格式化/阈值行为，以及
// pageHistorySliceSource 在挂上 trace 时真的记录 page-* 各相。

func TestHistorySliceTraceNilReceiver(t *testing.T) {
	var tr *historySliceTrace
	// nil trace 必须等价于"不记录"：照常执行 fn、不 panic、不输出。
	called := false
	tr.run("phase", func() { called = true })
	if !called {
		t.Fatal("nil trace run() skipped the wrapped function")
	}
	if err := tr.runErr("phase", func() error { return nil }); err != nil {
		t.Fatalf("nil trace runErr() returned %v", err)
	}
	tr.step("phase", time.Millisecond)
	tr.markSource("index")
	tr.emit("tab")
	if tr.elapsed() != 0 {
		t.Fatalf("nil trace elapsed = %d, want 0", tr.elapsed())
	}
	// 任务 560：nil trace 的阶段发布/清理同样必须无操作、不 panic。
	tr.publishPhase("cold-eventlog")
	tr.clearPhase()
}

func TestHistorySliceTraceRecordsPhases(t *testing.T) {
	tr := newHistorySliceTrace("tab-records")
	value := 0
	tr.run("a", func() { value = 1 })
	if value != 1 {
		t.Fatal("run() did not execute the wrapped function")
	}
	if err := tr.runErr("b", func() error { value = 2; return nil }); err != nil || value != 2 {
		t.Fatalf("runErr() = %v value=%d", err, value)
	}
	tr.step("c", 3*time.Millisecond)
	tr.markSource("index")
	// parts 布局：名字=毫秒；emit 再用空格连接成单行。
	if len(tr.parts) != 3 {
		t.Fatalf("parts = %v, want 3 entries", tr.parts)
	}
	if !strings.HasPrefix(tr.parts[0], "a=") || !strings.HasPrefix(tr.parts[1], "b=") {
		t.Fatalf("parts[0:2] = %v, want a=/b= prefixes", tr.parts[0:2])
	}
	if tr.parts[2] != "c=3ms" {
		t.Fatalf("parts[2] = %q, want c=3ms", tr.parts[2])
	}
	if tr.source != "index" {
		t.Fatalf("source = %q, want index", tr.source)
	}
}

func TestHistorySliceTraceEmitThreshold(t *testing.T) {
	// 阈值必须与前端 tab switch 时机线一致（150ms），慢切片才值得一行日志。
	if historySliceSlowLogMs != slowTabSwitchLogMs {
		t.Fatalf("historySliceSlowLogMs = %d, want to match slowTabSwitchLogMs %d", historySliceSlowLogMs, slowTabSwitchLogMs)
	}
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	slow := &historySliceTrace{startedAt: time.Now().Add(-300 * time.Millisecond)}
	slow.step("page-convert", 250*time.Millisecond)
	slow.markSource("index")
	slow.emit("tab-1")
	out := buf.String()
	if !strings.Contains(out, "desktop: history slice timing") ||
		!strings.Contains(out, "page-convert=250ms") ||
		!strings.Contains(out, "source=index") || !strings.Contains(out, "tab=tab-1") {
		t.Fatalf("slow emit line missing fields, got: %s", out)
	}

	buf.Reset()
	fast := newHistorySliceTrace("tab-fast")
	fast.step("page-fetch", time.Millisecond)
	fast.emit("tab-2")
	if out := buf.String(); !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "history slice timing") {
		t.Fatalf("fast emit should log at debug, got: %s", out)
	}
}

func TestPageHistorySliceSourceRecordsTracePhases(t *testing.T) {
	app := historySliceTestApp(t)
	msgs := []provider.Message{
		historySliceUser(0, "phase probe turn"),
		historySliceAssistant(0, "answer"),
	}
	src := newInMemoryHistorySliceSource("phase-trace", msgs, func(s string) string { return s }, agent.PersistedState{}, false)
	tr := newHistorySliceTrace("tab-page")
	src.trace = tr
	req := HistorySliceRequest{Turns: 12, Entries: 50, Bytes: 512 << 10}
	page, err := app.pageHistorySliceSource(src, req, func(s string) string { return s }, nil, nil, "")
	if err != nil || len(page.Entries) == 0 {
		t.Fatalf("page = entries:%d err:%v", len(page.Entries), err)
	}
	// 窗口读取与行转换两相必须出现（todo/planner 相只在相应内容存在时出现）。
	joined := strings.Join(tr.parts, " ")
	if !strings.Contains(joined, "page-fetch=") || !strings.Contains(joined, "page-convert=") {
		t.Fatalf("trace parts missing page phases: %v", tr.parts)
	}
	// 转换相耗时应被真实计时（>=0ms 的格式化值）。
	for _, part := range tr.parts {
		if !strings.HasSuffix(part, "ms") {
			t.Fatalf("trace part %q is not a ms duration", part)
		}
	}
}

// 任务 560：HistoryLoadPhase 轮询口的发布/覆盖/清理语义。只发布既有阶段名，
// 纯内存注册表，无任何 IO。
func TestHistoryLoadPhasePublishesExistingStageNames(t *testing.T) {
	app := &App{}
	if got := app.HistoryLoadPhase("tab-x"); got != "" {
		t.Fatalf("idle HistoryLoadPhase = %q, want empty", got)
	}

	tr := newHistorySliceTrace("tab-x")
	inside := ""
	tr.run("cold-eventlog", func() { inside = app.HistoryLoadPhase("tab-x") })
	if inside != "cold-eventlog" {
		t.Fatalf("phase during cold-eventlog run = %q, want cold-eventlog", inside)
	}
	tr.clearPhase()
	if got := app.HistoryLoadPhase("tab-x"); got != "" {
		t.Fatalf("phase after clearPhase = %q, want empty", got)
	}

	// 嵌套 run：注册表始终指向最近开始的阶段（外层 live-source 被内层
	// live-index-load 覆盖），这正是前端想要的「正在执行」语义。
	nested := newHistorySliceTrace("tab-y")
	inner := ""
	nested.run("live-source", func() {
		nested.run("live-index-load", func() { inner = app.HistoryLoadPhase("tab-y") })
	})
	if inner != "live-index-load" {
		t.Fatalf("nested phase = %q, want live-index-load", inner)
	}
	nested.clearPhase()
	if got := app.HistoryLoadPhase("tab-y"); got != "" {
		t.Fatalf("phase after nested clearPhase = %q, want empty", got)
	}

	// 空 tabID 的 trace 不发布：注册表无键可写。
	anonymous := newHistorySliceTrace("")
	anonymous.run("cold-eventlog", func() {
		if got := app.HistoryLoadPhase(""); got != "" {
			t.Fatalf("anonymous trace published a phase: %q", got)
		}
	})
	anonymous.clearPhase()

	// 未知 tabID 与带空白的 tabID 都安全返回空串。
	if got := app.HistoryLoadPhase("never-registered"); got != "" {
		t.Fatalf("unknown tab HistoryLoadPhase = %q, want empty", got)
	}
	if got := app.HistoryLoadPhase("  tab-x  "); got != "" {
		t.Fatalf("padded tab HistoryLoadPhase = %q, want empty", got)
	}
}
