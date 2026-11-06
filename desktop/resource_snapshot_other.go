//go:build !windows && !linux

package main

// tl1456 资源管理器的 portable 回退（darwin 等）：进程表只保留本进程一行，
// 系统内存暂无实现——照 task 184 的测量诚实性纪律，答不上来就 Available=
// false，前端显示「平台未提供」而不是冒充 0。后续需要 darwin 时补
// sysctl(hw.memsize) + host_statistics64，接口不变。
//
// 本进程行不受影响：working set 走 procCounters 的 portable 回退（Available
// =false，行在、读数诚实），Go runtime memstats 照常。

func readSystemMemory() ResourceSystemMemory {
	return ResourceSystemMemory{}
}

func listChildProcesses() []ResourceProcessRow {
	return nil
}
