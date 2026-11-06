package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// tl1456 资源管理器数据面测试：装配纯函数（主行/子进程排序封顶/子代理投影）
// + 活体方法契约（只读、行不撒谎、JSON 可序列化）。测量诚实性断言贯穿：
// 平台没答上来的计数器必须 Measured=false，绝不允许 0 冒充测量值。

func TestAssembleResourceSnapshotMainRowAndRuntime(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	bootAt := now.Add(-90 * time.Minute)
	counters := procCounters{
		WorkingSetBytes: 3 << 30,
		PrivateBytes:    2 << 30,
		Handles:         1200,
		Threads:         42,
		Available:       true,
	}
	view := assembleResourceSnapshot(now, bootAt, counters,
		ResourceRuntimeMemory{HeapAllocBytes: 1 << 30, Goroutines: 77, NumGC: 5},
		ResourceSystemMemory{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, Available: true},
		nil, nil)

	if view.Main.PID != os.Getpid() {
		t.Fatalf("main pid = %d, want %d", view.Main.PID, os.Getpid())
	}
	if view.Main.Kind != "main" {
		t.Fatalf("main kind = %q, want main", view.Main.Kind)
	}
	if !view.Main.Measured || view.Main.MemoryBytes != 3<<30 {
		t.Fatalf("main row lost the OS working set: measured=%v bytes=%d", view.Main.Measured, view.Main.MemoryBytes)
	}
	if view.UptimeSeconds < 5399 || view.UptimeSeconds > 5401 {
		t.Fatalf("uptime = %.1f, want ~5400 (90min)", view.UptimeSeconds)
	}
	if view.Runtime.Goroutines != 77 || view.Runtime.HeapAllocBytes != 1<<30 {
		t.Fatalf("runtime view not passed through: %+v", view.Runtime)
	}
	if !view.System.Available || view.System.TotalBytes != 16<<30 {
		t.Fatalf("system memory not passed through: %+v", view.System)
	}
	// 空子进程/子代理也必须是 []（Wails 序列化 null → 前端崩）——表列已初始化。
	if view.Children == nil || view.Subagents == nil {
		t.Fatalf("children/subagents must be initialized slices, got %v/%v", view.Children, view.Subagents)
	}
	if len(view.Children) != 0 || len(view.Subagents) != 0 {
		t.Fatalf("expected empty tables, got %d children / %d subagents", len(view.Children), len(view.Subagents))
	}
	if _, err := time.Parse(time.RFC3339Nano, view.TS); err != nil {
		t.Fatalf("ts not RFC3339Nano: %q (%v)", view.TS, err)
	}
}

func TestAssembleResourceSnapshotUnmeasuredStaysHonest(t *testing.T) {
	now := time.Now()
	// 平台回退（counters.Available=false）：主行必须在、Measured=false、
	// bytes=0 是「没测」而不是「测得 0」。
	view := assembleResourceSnapshot(now, now.Add(-time.Minute), procCounters{},
		ResourceRuntimeMemory{}, ResourceSystemMemory{}, nil, nil)
	if view.Main.PID != os.Getpid() || view.Main.Kind != "main" {
		t.Fatalf("portable fallback lost the main row: %+v", view.Main)
	}
	if view.Main.Measured {
		t.Fatal("unmeasured platform must not claim Measured=true")
	}
	if view.System.Available {
		t.Fatal("unimplemented system memory must not claim Available=true")
	}
}

func TestSortResourceChildrenOrderCapAndSelfFilter(t *testing.T) {
	self := os.Getpid()
	rows := []ResourceProcessRow{
		{Name: "small.exe", PID: 101, MemoryBytes: 10 << 20},
		{Name: "huge.exe", PID: 102, MemoryBytes: 900 << 20},
		{Name: "mid.exe", PID: 103, MemoryBytes: 300 << 20},
		{Name: "self-echo", PID: self, MemoryBytes: 999 << 20}, // 自身误入必须被滤掉
		{Name: "dead.exe", PID: 0, MemoryBytes: 50 << 20},      // 非法 PID 滤掉
		{Name: "tie-a.exe", PID: 205, MemoryBytes: 300 << 20},
		{Name: "tie-b.exe", PID: 204, MemoryBytes: 300 << 20},
	}
	sorted := sortResourceChildren(rows)
	if len(sorted) != 5 {
		t.Fatalf("sorted len = %d, want 5 (self + pid0 filtered)", len(sorted))
	}
	if sorted[0].Name != "huge.exe" {
		t.Fatalf("heaviest child must sort first, got %s", sorted[0].Name)
	}
	if sorted[1].Name != "mid.exe" || sorted[2].Name != "tie-b.exe" || sorted[3].Name != "tie-a.exe" || sorted[4].Name != "small.exe" {
		t.Fatalf("order wrong: %v", []string{sorted[1].Name, sorted[2].Name, sorted[3].Name, sorted[4].Name})
	}
	for _, row := range sorted {
		if row.Kind != "child" {
			t.Fatalf("row %s kind = %q, want child (set by assembler)", row.Name, row.Kind)
		}
	}
	// 封顶：resourceMaxChildren+10 行只留前 resourceMaxChildren（最重的）。
	flood := make([]ResourceProcessRow, 0, resourceMaxChildren+10)
	for i := 0; i < resourceMaxChildren+10; i++ {
		flood = append(flood, ResourceProcessRow{PID: 1000 + i, MemoryBytes: uint64(i) << 20})
	}
	if got := sortResourceChildren(flood); len(got) != resourceMaxChildren {
		t.Fatalf("cap broken: got %d rows, want %d", len(got), resourceMaxChildren)
	}
}

func TestResourceSubagentRowsOldestFirst(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	subs := []ForegroundSubagentView{
		{Ref: "late", Name: "task", StartedAt: base.Add(time.Minute).UnixMilli(), Title: "Tab B"},
		{Ref: "early", Description: "查找引用", StartedAt: base.UnixMilli()},
	}
	rows := resourceSubagentRows(subs)
	if len(rows) != 2 || rows[0].Ref != "early" || rows[1].Ref != "late" {
		t.Fatalf("oldest-first broken: %+v", rows)
	}
	if rows[0].Description != "查找引用" || rows[0].Origin != "" {
		t.Fatalf("projection lost description/origin: %+v", rows[0])
	}
	if rows[1].Origin != "Tab B" {
		t.Fatalf("projection lost tab title: %+v", rows[1])
	}
}

func TestBaseNameForSeparators(t *testing.T) {
	cases := map[string]string{
		`C:\dir\reasonix.exe`: "reasonix.exe",
		"/usr/bin/reasonix":   "reasonix",
		"plain.exe":           "plain.exe",
		`C:\dir\sub\`:         "",
	}
	for in, want := range cases {
		if got := baseNameFor(in); got != want {
			t.Fatalf("baseNameFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResourceSnapshotLiveContract: 活体方法只读、字段齐、JSON 全序列化、
// runtime 读数是真测量（本测试进程内 ReadMemStats 必然有真实值）。
func TestResourceSnapshotLiveContract(t *testing.T) {
	app := &App{}
	view := app.ResourceSnapshot()

	if view.Main.Kind != "main" || view.Main.PID != os.Getpid() {
		t.Fatalf("live main row wrong: %+v", view.Main)
	}
	if strings.TrimSpace(view.Main.Name) == "" {
		t.Fatal("main row name must not be empty")
	}
	// runtime 面：heap 系统读数在真实进程里不可能全零。
	if view.Runtime.HeapAllocBytes == 0 || view.Runtime.SysBytes == 0 {
		t.Fatalf("runtime memstats look fake: %+v", view.Runtime)
	}
	if view.Runtime.Goroutines <= 0 {
		t.Fatalf("goroutine count must be positive in a live process: %d", view.Runtime.Goroutines)
	}
	// 子代理段初始化（空=注册表无运行项，属正常现场）。
	if view.Subagents == nil || view.Children == nil {
		t.Fatalf("live tables must be initialized: %v/%v", view.Subagents, view.Children)
	}
	// JSON 全字段可序列化（桥契约）——children/subagents 出来必须是 [] 非 null。
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"ts", "uptimeSeconds", "main", "children", "runtime", "system", "subagents"} {
		if _, ok := back[key]; !ok {
			t.Fatalf("json payload missing %q", key)
		}
	}
	if back["children"] == nil || back["subagents"] == nil {
		t.Fatal("empty tables must serialize as [] not null")
	}
}
