package boot

import (
	"context"
	"runtime/debug"

	"reasonix/internal/baseproc"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// startBaseClient is the resident-base consumption point (design §10): boot
// asks for the client once per build and hands the result to the executor
// agent (base-toolcall gate), the controller (tool-catalog gate) and the
// cleanup chain.
//
// 任务 785（2026-10-11 用户拍板）：experimental_base_process 开关已退役，
// 常驻底座子进程永不 spawn。Enabled 恒为 false，Start 每次都返回 bare
// inline client（R1/R4：零副作用、pre-S1 行为逐字节一致），两个 gate
// （base_toolcall / base_gate）结构性地走本地分支。baseproc 实现代码按
// 最小退役档保留作废——它被主链路结构性 import（BaseClient 贯穿
// boot→agent→control，pidalive 还被 collabinbox 使用），彻底摘除的风险
// 大于留一段永不激活的 fail-closed 代码。
func startBaseClient(ctx context.Context, cfg *config.Config, reg *tool.Registry) baseproc.BaseClient {
	opts := baseproc.Options{
		// 785: the switch is retired — never spawn the resident base.
		Enabled:       false,
		ServerVersion: baseServerVersion(),
		Surface:       &baseproc.RegistrySurface{Reg: reg},
	}
	// R4 default: the bare inline client, never a manager.
	return baseproc.Start(ctx, opts)
}

// baseServerVersion is the local build identity an inline hello reports. The
// remote side reports the subprocess's own CLI version (ldflags-injected,
// passed through `base serve`); boot cannot reach the cli package's version
// var (import direction), so it falls back to module build info, then "dev".
func baseServerVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
