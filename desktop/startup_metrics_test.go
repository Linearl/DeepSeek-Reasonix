package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tl 1163（启动链分阶段打点）：主链里程碑、前端 epoch 墙钟桥接、startup-metrics
// 落盘的验收测试。全部纯收集器/纯渲染断言——打点件本身零行为，测试也不碰
// 启动路径。

// TestBootTimelineMarksFirstArrivalWins：里程碑首到生效（重复打点不覆盖最早
// 读数）、按到达序渲染、emitted 后静默。
func TestBootTimelineMarksFirstArrivalWins(t *testing.T) {
	obs := replaceBootTimeline(t)

	bootTimelineNoteMarkAt("main_enter", 2*time.Millisecond)
	bootTimelineNoteMarkAt("pre_wails_done", 48*time.Millisecond)
	// 重复同名打点必须被忽略——里程碑是时间点，不是累加器。
	bootTimelineNoteMarkAt("main_enter", 999*time.Millisecond)
	bootTimelineNoteMarkAt("startup_cb", 52*time.Millisecond)

	obs.mu.Lock()
	got := renderMarksLocked(obs)
	obs.mu.Unlock()
	if got != "main_enter=2ms pre_wails_done=48ms startup_cb=52ms" {
		t.Fatalf("marks render = %q", got)
	}

	if !bootTimelineEmitSummary("marks-test") {
		t.Fatal("first emit should report true")
	}
	bootTimelineNoteMarkAt("dom_ready", time.Millisecond)
	obs.mu.Lock()
	after := renderMarksLocked(obs)
	obs.mu.Unlock()
	if strings.Contains(after, "dom_ready") {
		t.Fatalf("mark after emit leaked: %q", after)
	}
}

// TestBootTimelineMarksInSummaryAttrs：marks 串进汇总 attrs（slog 行与落盘行
// 的单一来源）。
func TestBootTimelineMarksInSummaryAttrs(t *testing.T) {
	obs := replaceBootTimeline(t)
	bootTimelineNoteMarkAt("main_enter", 2*time.Millisecond)
	bootTimelineNoteMarkAt("window_shown", 700*time.Millisecond)

	obs.mu.Lock()
	defer obs.mu.Unlock()
	kv := bootSummaryAttrsLocked(obs)
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == "marks" {
			if kv[i+1] != "main_enter=2ms window_shown=700ms" {
				t.Fatalf("marks attr = %v", kv[i+1])
			}
			return
		}
	}
	t.Fatalf("summary attrs missing marks: %v", kv)
}

// TestBootTimelineFrontendEventEpochBridge：带 epochMs 的前端事件映射到进程
// 时间轴（AtGoMs = epochMs - startedAt.UnixMilli）；不带 epochMs 的旧格式保持
// 旧形状（AtGoValid=false，汇总不输出 _go 字段）。
func TestBootTimelineFrontendEventEpochBridge(t *testing.T) {
	obs := replaceBootTimeline(t)
	startEpoch := obs.startedAt.UnixMilli()

	bootTimelineNoteFrontendEvent("kind=hydrate tab=t1 atMs=1157 hydrateMs=900 epochMs=" + itoa64(startEpoch+620))
	bootTimelineNoteFrontendEvent("kind=ui atMs=890 epochMs=" + itoa64(startEpoch+730))
	bootTimelineNoteFrontendEvent("kind=fcp atMs=540 epochMs=" + itoa64(startEpoch+541))
	// 旧格式（无 epochMs）：824 时代的上报照旧入收集器。
	bootTimelineNoteFrontendEvent("kind=hydrate tab=t2 atMs=1500 hydrateMs=400")
	// 损坏 epochMs：与缺失同义。
	bootTimelineNoteFrontendEvent("kind=ui atMs=900 epochMs=notanumber")

	obs.mu.Lock()
	defer obs.mu.Unlock()
	arrivals := obs.arrivals
	if len(arrivals) != 5 {
		t.Fatalf("arrivals = %d, want 5", len(arrivals))
	}
	if !arrivals[0].AtGoValid || arrivals[0].AtGoMs != 620 {
		t.Fatalf("hydrate bridge = %+v", arrivals[0])
	}
	if !arrivals[1].AtGoValid || arrivals[1].AtGoMs != 730 {
		t.Fatalf("ui bridge = %+v", arrivals[1])
	}
	if !arrivals[2].AtGoValid || arrivals[2].AtGoMs != 541 {
		t.Fatalf("fcp bridge = %+v", arrivals[2])
	}
	if arrivals[3].AtGoValid {
		t.Fatalf("legacy arrival must not claim a Go-axis reading: %+v", arrivals[3])
	}
	if arrivals[4].AtGoValid {
		t.Fatalf("garbage epochMs must not claim a Go-axis reading: %+v", arrivals[4])
	}

	kv := kvMap(bootSummaryAttrsLocked(obs))
	// _go 字段只在有桥接读数时出现；同批混合新旧上报时取有效桥接集合。
	if got := kv["hydrate_at_go_p50_ms"]; got != int64(620) {
		t.Fatalf("hydrate_at_go_p50_ms = %v, want 620", got)
	}
	if got := kv["ui_at_go_ms"]; got != int64(730) {
		t.Fatalf("ui_at_go_ms = %v, want 730", got)
	}
	if got := kv["fcp_at_go_ms"]; got != int64(541) {
		t.Fatalf("fcp_at_go_ms = %v, want 541", got)
	}
	if got := kv["fcp_at_ms"]; got != int64(540) {
		t.Fatalf("fcp_at_ms = %v, want 540", got)
	}
}

// TestBootTimelineNoGoAxisFieldsWithoutEpoch：整批上报都不带 epochMs 时，
// 汇总不得出现 *_at_go_ms 字段（前端轴值绝不能冒充进程轴读数）。
func TestBootTimelineNoGoAxisFieldsWithoutEpoch(t *testing.T) {
	obs := replaceBootTimeline(t)
	bootTimelineNoteFrontendEvent("kind=hydrate tab=t1 atMs=1157 hydrateMs=900")
	bootTimelineNoteFrontendEvent("kind=ui atMs=890")
	bootTimelineNoteFrontendEvent("kind=fcp atMs=540")

	obs.mu.Lock()
	defer obs.mu.Unlock()
	kv := kvMap(bootSummaryAttrsLocked(obs))
	for _, key := range []string{"hydrate_at_go_p50_ms", "ui_at_go_ms", "fcp_at_go_ms"} {
		if _, ok := kv[key]; ok {
			t.Fatalf("key %q must be absent without epochMs bridge", key)
		}
	}
	if kv["ui_at_ms"] != int64(890) || kv["fcp_at_ms"] != int64(540) {
		t.Fatalf("front-end-axis fields changed: %v", kv)
	}
}

// TestStartupMetricsLineFormat：落盘行格式——头三字段 + attrs 单行渲染；含
// 空格的段串必须加引号（行内字段切分安全）。
func TestStartupMetricsLineFormat(t *testing.T) {
	obs := replaceBootTimeline(t)
	bootTimelineNoteMarkAt("main_enter", 2*time.Millisecond)
	bootTimelineNoteMarkAt("window_shown", 700*time.Millisecond)
	bootTimelineNoteP1("load_tabs_file", 3*time.Millisecond)
	bootTimelineNoteTabs(2)
	startEpoch := obs.startedAt.UnixMilli()
	bootTimelineNoteFrontendEvent("kind=fcp atMs=540 epochMs=" + itoa64(startEpoch+541))
	bootTimelineNoteFrontendEvent("kind=ui atMs=890 epochMs=" + itoa64(startEpoch+730))

	obs.mu.Lock()
	line := renderStartupMetricsLineLocked(obs, "frontend-ready+10s")
	obs.mu.Unlock()

	if !strings.HasPrefix(line, "[startup] ts=") {
		t.Fatalf("line prefix = %q", line)
	}
	for _, want := range []string{
		" trigger=frontend-ready+10s",
		" pid=",
		" version=",
		" channel=",
		" wall_ms=",
		" tabs=2",
		` marks="main_enter=2ms window_shown=700ms"`,
		// 单段列表不含空格，按渲染规则不加引号（首个 = 之后整体是值，
		// 字段切分仍安全）；多段（marks 两项）才带引号。
		" p1=load_tabs_file=3ms",
		" ui_at_ms=890",
		" ui_at_go_ms=730",
		" fcp_at_ms=540",
		" fcp_at_go_ms=541",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %q:\n%s", want, line)
		}
	}
	if after := strings.SplitN(line, "]", 2)[1]; strings.Count(after, " marks=") != 1 {
		t.Fatalf("marks field not exactly once: %s", line)
	}
}

// TestAppendStartupMetricsFile：追加语义（多次启动逐行累积）与写失败静默。
func TestAppendStartupMetricsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup-metrics.log")
	appendStartupMetricsFile(path, "line-one")
	appendStartupMetricsFile(path, "line-two")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "line-one\nline-two\n" {
		t.Fatalf("file = %q", string(data))
	}
	// 不可写目标：静默返回（目录不存在），绝不 panic。
	appendStartupMetricsFile(filepath.Join(path, "not-a-dir", "x.log"), "never")
}

// TestStartupMetricsPathIsCwdRelative：落盘路径锚定 cwd（tl 1163 正文硬约束：
// 不写用户数据目录）。
func TestStartupMetricsPathIsCwdRelative(t *testing.T) {
	if got := startupMetricsPath(); got != "startup-metrics.log" {
		t.Fatalf("path = %q, want cwd-relative startup-metrics.log", got)
	}
}

func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}

// kvMap flattens the summary attrs into a lookup map (attrs are k1,v1,k2,v2…).
func kvMap(kv []any) map[any]any {
	out := make(map[any]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}
