package main

import (
	"reflect"
	"testing"
	"time"
)

// replaceBootTimeline swaps the process-wide collector for a fresh one and
// returns a restore func. 测试隔离：包级单例按测试重置，互不串扰。
func replaceBootTimeline(t *testing.T) *bootTimeline {
	t.Helper()
	old := bootTimelineObs
	fresh := &bootTimeline{startedAt: time.Now()}
	bootTimelineObs = fresh
	t.Cleanup(func() { bootTimelineObs = old })
	return fresh
}

func TestPercentileNearestRank(t *testing.T) {
	values := []int64{10, 20, 30, 40, 100}
	if got := percentile(values, 50); got != 30 {
		t.Fatalf("p50 = %d, want 30", got)
	}
	if got := percentile(values, 95); got != 100 {
		t.Fatalf("p95 = %d, want 100", got)
	}
	if got := percentile(values, 0); got != 10 {
		t.Fatalf("p0 = %d, want 10", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Fatalf("empty p50 = %d, want 0", got)
	}
	if got := percentile([]int64{7}, 95); got != 7 {
		t.Fatalf("single p95 = %d, want 7", got)
	}
	// 输入切片不得被改动。
	if values[0] != 10 || values[4] != 100 {
		t.Fatalf("input slice mutated: %v", values)
	}
}

func TestParseBootStageSummary(t *testing.T) {
	got := parseBootStageSummary("total=2740ms config=12ms mcp=1800ms prompt=95ms agent:tools=700ms")
	want := map[string]int64{"total": 2740, "config": 12, "mcp": 1800, "prompt": 95, "agent:tools": 700}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %v, want %v", got, want)
	}
	if parseBootStageSummary("") != nil {
		t.Fatal("empty summary should parse to nil")
	}
	if parseBootStageSummary("garbage") != nil {
		t.Fatal("unparsable summary should parse to nil")
	}
}

func TestParseKeyValueFields(t *testing.T) {
	got := parseKeyValueFields("kind=hydrate tab=t1 atMs=1234 hydrateMs=210")
	want := map[string]string{"kind": "hydrate", "tab": "t1", "atMs": "1234", "hydrateMs": "210"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %v, want %v", got, want)
	}
}

// 任务 824 验收：五点打点链路存在性——note 进收集器、汇总行带齐各段字段、
// 幂等、窗关闭后 note 静默。
func TestBootTimelineCollectsAndEmits(t *testing.T) {
	obs := replaceBootTimeline(t)

	bootTimelineNoteP1("load_tabs_file", 3*time.Millisecond)
	bootTimelineNoteP1("config_load", 8*time.Millisecond)
	bootTimelineNoteTabs(2)
	bootTimelineNoteLeg("tab-a", "ok", 2700*time.Millisecond, "total=2700ms config=10ms mcp=1800ms prompt=95ms agent:tools=700ms")
	bootTimelineNoteLeg("tab-b", "ok", 300*time.Millisecond, "total=300ms config=5ms mcp=100ms prompt=20ms agent:tools=150ms")
	bootTimelineNoteLeg("tab-c", "error", 120*time.Millisecond, "")
	bootTimelineNoteFrontendEvent("kind=hydrate tab=tab-a atMs=1157 hydrateMs=900")
	bootTimelineNoteFrontendEvent("kind=ui atMs=890 split=open sidebarCollapsed=0")

	if len(obs.legs) != 3 {
		t.Fatalf("legs = %d, want 3", len(obs.legs))
	}
	if len(obs.arrivals) != 2 {
		t.Fatalf("arrivals = %d, want 2", len(obs.arrivals))
	}
	if obs.arrivals[0].Kind != "hydrate" || obs.arrivals[0].TabID != "tab-a" || obs.arrivals[0].AtMs != 1157 || obs.arrivals[0].HydrateMs != 900 {
		t.Fatalf("hydrate arrival = %+v", obs.arrivals[0])
	}
	if obs.arrivals[1].Kind != "ui" || obs.arrivals[1].AtMs != 890 {
		t.Fatalf("ui arrival = %+v", obs.arrivals[1])
	}

	attrs := func() map[any]any {
		obs.mu.Lock()
		defer obs.mu.Unlock()
		out := make(map[any]any)
		kv := bootSummaryAttrsLocked(obs)
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}()
	wantKeys := []any{
		"tabs", 2,
		"p1", "load_tabs_file=3ms config_load=8ms",
		"legs", 2, "leg_errors", 1,
		"leg_total_p50_ms", int64(300), "leg_total_p95_ms", int64(2700),
		"mcp_p50_ms", int64(100), "mcp_p95_ms", int64(1800),
		"prompt_p50_ms", int64(20), "prompt_p95_ms", int64(95),
		"agent_tools_p50_ms", int64(150), "agent_tools_p95_ms", int64(700),
		"hydrate_tabs", 1, "hydrate_at_p50_ms", int64(1157),
		"ui_at_ms", int64(890),
	}
	if len(attrs) == 0 {
		t.Fatal("summary attrs empty")
	}
	for i := 0; i+1 < len(wantKeys); i += 2 {
		key := wantKeys[i]
		wantVal := wantKeys[i+1]
		gotVal, ok := attrs[key]
		if !ok {
			t.Fatalf("summary missing key %v (attrs=%v)", key, attrs)
		}
		if !reflect.DeepEqual(gotVal, wantVal) {
			t.Fatalf("summary[%v] = %v, want %v", key, gotVal, wantVal)
		}
	}

	// 幂等：第一个触发发出，第二个触发 no-op；窗关闭后 note 全部静默。
	if !bootTimelineEmitSummary("test-first") {
		t.Fatal("first emit should report true")
	}
	if bootTimelineEmitSummary("test-second") {
		t.Fatal("second emit should be a no-op")
	}
	bootTimelineNoteP1("late", time.Millisecond)
	bootTimelineNoteLeg("late", "ok", time.Millisecond, "total=1ms")
	bootTimelineNoteFrontendEvent("kind=hydrate tab=late atMs=1 hydrateMs=1")
	bootTimelineNoteTabs(99)
	obs.mu.Lock()
	legs, arrivals, tabs := len(obs.legs), len(obs.arrivals), obs.tabs
	obs.mu.Unlock()
	if legs != 3 || arrivals != 2 || tabs != 2 {
		t.Fatalf("notes after emit leaked: legs=%d arrivals=%d tabs=%d", legs, arrivals, tabs)
	}
}

// 损坏的前端事件不进收集器也不 panic——诊断通道绝不能反噬调用方。
func TestBootTimelineFrontendEventToleratesGarbage(t *testing.T) {
	obs := replaceBootTimeline(t)
	bootTimelineNoteFrontendEvent("")
	bootTimelineNoteFrontendEvent("not-kv-at-all")
	bootTimelineNoteFrontendEvent("kind=hydrate atMs=notanumber")
	bootTimelineNoteFrontendEvent("atMs=12")
	if len(obs.arrivals) != 0 {
		t.Fatalf("garbage arrivals = %d, want 0", len(obs.arrivals))
	}
}

// P1 行渲染：无段时返回空串（不空打一行），有段时按键序拼接。
func TestLogBootP1StagesRenders(t *testing.T) {
	obs := replaceBootTimeline(t)
	if summary := renderP1Locked(obs); summary != "" {
		t.Fatalf("empty p1 render = %q", summary)
	}
	bootTimelineNoteP1("topic_reconcile", 2*time.Millisecond)
	bootTimelineNoteP1("load_tabs_file", 5*time.Millisecond)
	obs.mu.Lock()
	summary := renderP1Locked(obs)
	obs.mu.Unlock()
	if summary != "topic_reconcile=2ms load_tabs_file=5ms" {
		t.Fatalf("p1 render = %q", summary)
	}
}
