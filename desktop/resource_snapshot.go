package main

import (
	"os"
	"runtime"
	"sort"
	"time"
)

// tl1456 资源管理器（命令面板 → 工具与诊断）的数据面：一次按需读内存总览
// （Go runtime memstats + OS 系统内存）+ 本产品真实形态的进程表。
//
// 形态对齐（任务书口径）：参照 zcode Resource Manager 的诉求，但按我方
// Wails 单进程 + 进程内子代理的真实形态设计，不是 zcode 的多进程模型——
//  - 进程表 = 本进程一行 + 本进程直接派生的 OS 子进程（仅父子关系过滤，
//    不枚举全系统进程：非本产品职责，也避免杀软对全量枚举的误报）；
//  - 运行中子代理（进程内 goroutine，无独立 PID/内存可分摊）单独成段，
//    复用既有 RunningSubagents 注册表（task 557），不新造登记。
//
// 只读契约（task 182/338 同款）：本文件不启动任何采样器、goroutine 或定时
// 器——面板开着才轮询（前端 2-5s 可配），关了就是零成本；所有读数都是
// 调用瞬间的现场。
//
// 测量诚实性（task 184 纪律）：平台没答上来的计数器一律 Measured/Available
// =false，绝不用 0 冒充测量值。

// resourceMaxChildren bounds the child table: our own children are a handful
// (terminal shells, helper processes); the cap only stops a pathological
// process tree from flooding one snapshot.
const resourceMaxChildren = 32

// ResourceProcessRow is one table row. MemoryBytes is the OS working set —
// the number the user can cross-check in Task Manager. Measured=false means
// the platform did not answer for this row (the UI shows "—" instead of a
// fake 0).
type ResourceProcessRow struct {
	Name          string  `json:"name"`
	PID           int     `json:"pid"`
	MemoryBytes   uint64  `json:"memoryBytes"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
	// Kind is "main" for this process, "child" for a directly spawned one.
	Kind string `json:"kind"`
	// Measured reports whether the OS actually reported the memory counter.
	Measured bool `json:"measured"`
}

// ResourceRuntimeMemory is the Go runtime's own view (runtime.ReadMemStats) —
// the "how much is our heap holding" side of the overview, distinct from the
// OS working set which includes non-heap pages.
type ResourceRuntimeMemory struct {
	HeapAllocBytes uint64  `json:"heapAllocBytes"`
	HeapSysBytes   uint64  `json:"heapSysBytes"`
	HeapObjects    uint64  `json:"heapObjects"`
	SysBytes       uint64  `json:"sysBytes"`
	Goroutines     int     `json:"goroutines"`
	NumGC          uint32  `json:"numGC"`
	LastGCPauseMs  float64 `json:"lastGCPauseMs"`
}

// ResourceSystemMemory is the machine-level view (OS API). Available=false on
// platforms where the answer is not implemented — never zeroes-as-data.
type ResourceSystemMemory struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
	Available      bool   `json:"available"`
}

// ResourceSubagentRow is one in-process running sub-agent (foreground
// registry). Sub-agents in this product share the main process — there is no
// per-subagent PID or memory to report, so the row carries identity and
// elapsed time only.
type ResourceSubagentRow struct {
	Ref         string `json:"ref"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// Origin is the owning tab's display title (same fallback as the capsule).
	Origin    string `json:"origin,omitempty"`
	StartedAt int64  `json:"startedAt"` // unix milliseconds
}

// ResourceSnapshotView is the whole panel payload for one refresh tick.
type ResourceSnapshotView struct {
	TS            string                `json:"ts"`
	UptimeSeconds float64               `json:"uptimeSeconds"`
	Main          ResourceProcessRow    `json:"main"`
	Children      []ResourceProcessRow  `json:"children"`
	Runtime       ResourceRuntimeMemory `json:"runtime"`
	System        ResourceSystemMemory  `json:"system"`
	Subagents     []ResourceSubagentRow `json:"subagents"`
}

// readRuntimeMemStats is a seam for tests (ReadMemStats stops the world).
var readRuntimeMemStats = func() ResourceRuntimeMemory {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	lastPauseMs := 0.0
	if n := mem.NumGC; n > 0 {
		idx := (n - 1) % uint32(len(mem.PauseNs))
		lastPauseMs = float64(mem.PauseNs[idx]) / 1e6
	}
	return ResourceRuntimeMemory{
		HeapAllocBytes: mem.HeapAlloc,
		HeapSysBytes:   mem.HeapSys,
		HeapObjects:    mem.HeapObjects,
		SysBytes:       mem.Sys,
		Goroutines:     runtime.NumGoroutine(),
		NumGC:          mem.NumGC,
		LastGCPauseMs:  lastPauseMs,
	}
}

// mainProcessName is the executable file name of this process (the row the
// user can match against Task Manager). Failure keeps the row honest with
// "reasonix" rather than an empty cell.
func mainProcessName() string {
	if path, err := os.Executable(); err == nil && path != "" {
		if base := baseNameFor(path); base != "" {
			return base
		}
	}
	return "reasonix"
}

// baseNameFor is the path→file-name tail shared by row assembly (kept separate
// from filepath.Base so the Windows unit test can exercise mixed separators).
func baseNameFor(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// assembleResourceSnapshot is the injectable core: pure assembly from the
// platform reads. Tests feed fixture counters/memory/children without
// touching a live process.
func assembleResourceSnapshot(now time.Time, bootAt time.Time, counters procCounters, runtimeView ResourceRuntimeMemory, sys ResourceSystemMemory, children []ResourceProcessRow, subagents []ForegroundSubagentView) ResourceSnapshotView {
	uptime := now.Sub(bootAt).Seconds()
	if uptime < 0 {
		uptime = 0
	}
	view := ResourceSnapshotView{
		TS:            now.UTC().Format(time.RFC3339Nano),
		UptimeSeconds: uptime,
		Main: ResourceProcessRow{
			Name:          mainProcessName(),
			PID:           os.Getpid(),
			MemoryBytes:   counters.WorkingSetBytes,
			UptimeSeconds: uptime,
			Kind:          "main",
			Measured:      counters.Available,
		},
		Runtime: runtimeView,
		System:  sys,
		Children: sortResourceChildren(children),
		Subagents: resourceSubagentRows(subagents),
	}
	return view
}

// sortResourceChildren orders the child table heaviest-memory first (the row
// the user is hunting for lands on top) and caps it.
func sortResourceChildren(children []ResourceProcessRow) []ResourceProcessRow {
	out := make([]ResourceProcessRow, 0, len(children))
	for _, row := range children {
		if row.PID <= 0 || row.PID == os.Getpid() {
			continue
		}
		row.Kind = "child"
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MemoryBytes != out[j].MemoryBytes {
			return out[i].MemoryBytes > out[j].MemoryBytes
		}
		return out[i].PID < out[j].PID
	})
	if len(out) > resourceMaxChildren {
		out = out[:resourceMaxChildren]
	}
	return out
}

// resourceSubagentRows projects the foreground registry, oldest-first so the
// long-runner sits at the top of the list.
func resourceSubagentRows(subagents []ForegroundSubagentView) []ResourceSubagentRow {
	out := make([]ResourceSubagentRow, 0, len(subagents))
	for _, sub := range subagents {
		out = append(out, ResourceSubagentRow{
			Ref:         sub.Ref,
			Name:        sub.Name,
			Description: sub.Description,
			Origin:      sub.Title,
			StartedAt:   sub.StartedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out
}

// ResourceSnapshot serves one panel refresh tick (tl1456). Read-only and
// on-demand: no sampler, no goroutine, no retained state — the panel polls
// at its own cadence while open.
func (a *App) ResourceSnapshot() ResourceSnapshotView {
	return assembleResourceSnapshot(
		time.Now(),
		bootTimelineStartedAt,
		readProcCounters(),
		readRuntimeMemStats(),
		readSystemMemory(),
		listChildProcesses(),
		a.RunningSubagents(),
	)
}
