//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
)

// tl1456 资源管理器的 Linux 数据源：/proc/meminfo（系统内存）+ /proc 扫描
// 按父 PID 过滤的直接子进程。同样是只认自家孩子、不做全系统枚举的口径。

// readSystemMemory parses MemTotal/MemAvailable out of /proc/meminfo.
// MemAvailable (3.14+) is the honest "can still be allocated" number; on old
// kernels it falls back to MemFree and keeps Available=true — a coarse but
// real answer beats a missing one.
func readSystemMemory() ResourceSystemMemory {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ResourceSystemMemory{}
	}
	var total, avail uint64
	haveAvail := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = kb * 1024
		case strings.HasPrefix(line, "MemAvailable:"):
			avail = kb * 1024
			haveAvail = true
		case strings.HasPrefix(line, "MemFree:") && !haveAvail:
			avail = kb * 1024
		}
	}
	if total == 0 {
		return ResourceSystemMemory{}
	}
	return ResourceSystemMemory{TotalBytes: total, AvailableBytes: avail, Available: true}
}

// listChildProcesses scans /proc/[pid]/stat for entries whose PPID is this
// process. comm may contain spaces/parens, so the numeric tail after the last
// ')' is the parse surface (state ppid ...). Memory comes from statm's RSS
// (pages of 4KiB on the kernels this fork targets); start time (field 22,
// clock ticks since boot) + /proc/uptime give the row's uptime.
func listChildProcesses() []ResourceProcessRow {
	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	clockTicks := 100.0 // USER_HZ is 100 on every Linux the desktop targets
	systemUptime := linuxSystemUptime()
	pageBytes := uint64(os.Getpagesize())

	children := make([]ResourceProcessRow, 0, 8)
	for _, entry := range procEntries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		statData, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		text := string(statData)
		close := strings.LastIndex(text, ")")
		if close < 0 || close+2 >= len(text) {
			continue
		}
		fields := strings.Fields(text[close+2:])
		if len(fields) < 20 {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid != self {
			continue
		}
		startTicks, err := strconv.ParseFloat(fields[19], 64)
		if err != nil {
			startTicks = 0
		}
		row := ResourceProcessRow{
			Name: strings.TrimSpace(text[strings.Index(text, "(")+1 : close]),
			PID:  pid,
			Kind: "child",
			// Measured: /proc always answers for a live process — the row is
			// only appended when its stat was readable.
			Measured: true,
		}
		if systemUptime > 0 {
			row.UptimeSeconds = systemUptime - startTicks/clockTicks
			if row.UptimeSeconds < 0 {
				row.UptimeSeconds = 0
			}
		}
		if statmData, err := os.ReadFile("/proc/" + entry.Name() + "/statm"); err == nil {
			statmFields := strings.Fields(string(statmData))
			if len(statmFields) >= 2 {
				rssPages, err := strconv.ParseUint(statmFields[1], 10, 64)
				if err == nil {
					row.MemoryBytes = rssPages * pageBytes
				}
			}
		}
		children = append(children, row)
	}
	return children
}

// linuxSystemUptime reads /proc/uptime (seconds); 0 = unreadable.
func linuxSystemUptime() float64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return uptime
}
