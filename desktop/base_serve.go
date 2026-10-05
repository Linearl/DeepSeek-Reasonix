package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"reasonix/internal/baseproc"
)

// 任务 478：S1 底座消费面接线——桌面 exe 分发 `base serve` 子命令。
//
// baseproc 管理器默认把「当前可执行文件 + base serve --stdio」当作底座子进程
// spawn（internal/baseproc/manager.go serveArgv，决策 D4：同一二进制，无新构建
// /签名面）。桌面 exe 此前不认这段 argv：被 spawn 后照常走 GUI——Wails 单实例
// 锁把既有窗口拉到前台（experimental_base_process=true 下每 5min degraded 探测
// 重 spawn 一次的副作用），握手失败循环，base.log 0 字节（P21/X7/497 核查结
// 论）。本文件让桌面 exe 对这段 argv 给出与 `reasonix base serve`
// （internal/cli/base.go）完全相同的应答：同一 RunStdioServer、同一用法错误
// 契约。
//
// 拦截时机（main.go 最前部）是正确性的一部分：
//   - 必须在 installDesktopLogging 之前——fd 2 此刻仍是父进程传入的 base.log
//     句柄（F2），desktop.log 不能截走子进程诊断；
//   - 必须在任何 GUI/单实例机制之前——底座子进程绝不触碰窗口与单实例锁。
//
// 开关语义（铁律 2）：experimental_base_process 默认 false，父进程不会 spawn，
// 这段分发自然不运行；普通 GUI 启动的 argv 不含 "base" 前缀，只多一次字符串
// 比较。关闭时逐字节零行为。

// classifyBaseServeArgs 判定一段 argv 是否应按 base 子命令分发。serve=true
// 表示进入 stdio 服务循环；serve=false 且 handled=true 表示用法错误（与
// cli.baseCommand 相同：usage 行 + 退出码 2）；handled=false 表示与本分发
// 无关（GUI 正常启动路径）。usage 为 nil 时落到 os.Stderr。
func classifyBaseServeArgs(args []string, usage io.Writer) (serve, handled bool) {
	if usage == nil {
		usage = os.Stderr
	}
	if len(args) == 0 || args[0] != "base" {
		return false, false
	}
	rest := args[1:]
	if len(rest) == 0 || rest[0] != "serve" {
		fmt.Fprintln(usage, "usage: reasonix base serve --stdio")
		return false, true
	}
	fs := flag.NewFlagSet("base serve", flag.ContinueOnError)
	fs.SetOutput(usage)
	stdio := fs.Bool("stdio", false, "serve the base protocol on stdin/stdout (the only v1 transport)")
	if err := fs.Parse(rest[1:]); err != nil {
		fmt.Fprintln(usage, "usage: reasonix base serve --stdio")
		return false, true
	}
	if !*stdio {
		fmt.Fprintln(usage, "reasonix base serve: --stdio is required (the only v1 transport)")
		return false, true
	}
	return true, true
}

// runBaseServe 是分发体本身；独立成函数让测试可以在进程内用管道驱动同一段
// stdio 服务循环，而不必真的 spawn 二进制。
func runBaseServe(ctx context.Context, in io.Reader, out, errw io.Writer) int {
	return baseproc.RunStdioServer(ctx, version, in, out, errw)
}

// maybeRunBaseServe 是 main 的前置分发：handled=true 时调用方必须以返回的
// 退出码立刻退出，绝不继续 GUI 启动。
func maybeRunBaseServe(args []string) (int, bool) {
	serve, handled := classifyBaseServeArgs(args, os.Stderr)
	if !handled {
		return 0, false
	}
	if !serve {
		return 2, true
	}
	// 与 cli.baseCommand 相同：Ctrl-C 映射为干净的 serve 返回；孤儿路径是
	// stdin EOF（父进程死亡），RunStdioServer 在 Serve 内部处理。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runBaseServe(ctx, os.Stdin, os.Stdout, os.Stderr), true
}
