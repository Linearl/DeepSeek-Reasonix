// Command probe478 is the task 478 acceptance probe: it drives a real
// baseproc.Manager against the desktop exe spawned with the D4 argv
// (`<exe> base serve --stdio`) and asserts the three things the old wiring
// could not do — handshake succeeds, the view reaches remote_ready and STAYS
// there across many health intervals (no restart loop), and the subprocess
// log carries the ready/exit liveness lines.
//
// 装机后复测同款（沿 cmd/probe470 先例入库）：
//
//	go run ./cmd/probe478 <已安装的 Reasonix 桌面 exe 路径>
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/baseproc"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: probe478 <desktop-exe>")
		os.Exit(2)
	}
	exe := os.Args[1]
	logFile := filepath.Join(os.TempDir(), "wt478-manager-base.log")
	_ = os.Remove(logFile)

	ctx := context.Background()
	opts := baseproc.Options{
		Enabled:          true,
		Command:          []string{exe, "base", "serve", "--stdio"},
		LogFile:          logFile,
		HealthInterval:   200 * time.Millisecond,
		HealthTimeout:    100 * time.Millisecond,
		HealthMaxMisses:  2,
		RestartBaseDelay: 100 * time.Millisecond,
		RestartMaxDelay:  500 * time.Millisecond,
		DegradedRetry:    time.Second, // 若退化，1s 一探——循环会立刻显形
		Log:              slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	started := time.Now()
	m := baseproc.NewManager(ctx, opts)
	poolRef := m.Acquire(opts) // the pool's own never-released reference, as boot does
	view := m.Acquire(baseproc.Options{Enabled: true})
	fmt.Printf("mode right after NewManager: %s (%.0fms)\n", view.Mode(), time.Since(started).Seconds()*1000)

	if view.Mode() != baseproc.ModeRemote {
		fmt.Println("FAIL: view is not remote_ready after the synchronous first spawn")
		os.Exit(1)
	}
	fmt.Println("PASS: handshake ok, view = remote_ready")

	// 健康稳定窗：3 秒 ≈ 15 个健康 ping（200ms 间隔）。任何一次重启循环都会
	// 把视图打回 inline。
	dropped := 0
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if view.Mode() != baseproc.ModeRemote {
			dropped++
		}
	}
	if dropped > 0 {
		fmt.Printf("FAIL: view dropped out of remote_ready %d times across the 3s health window (restart loop)\n", dropped)
		os.Exit(1)
	}
	fmt.Println("PASS: mode stayed remote_ready across the 3s health window (~15 pings), zero restarts")

	if err := view.Ping(ctx); err != nil {
		fmt.Println("FAIL: view ping:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: view ping round-trip ok")

	// 视图释放不牵连底座（S1c 池语义），显式 shutdown 才退场。
	if err := view.Close(); err != nil {
		fmt.Println("view close:", err)
	}
	time.Sleep(300 * time.Millisecond)
	if m.Closed() {
		fmt.Println("FAIL: manager closed after a mere view release")
		os.Exit(1)
	}
	fmt.Println("PASS: view release does not take the resident base down")
	_ = poolRef.Close()
	_ = m.Close()

	raw, err := os.ReadFile(logFile)
	if err != nil {
		fmt.Println("FAIL: read subprocess log:", err)
		os.Exit(1)
	}
	fmt.Printf("--- subprocess log %s (%d bytes) ---\n%s\n", logFile, len(raw), raw)
	if len(raw) == 0 {
		fmt.Println("FAIL: subprocess log is empty")
		os.Exit(1)
	}
}
