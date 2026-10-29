//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// tl1257 — 实验室（设置→实验室）打开慢的真机归因探针。
//
// 生产 desktop.log（tl1098 分块计时）已实锤：Settings() 的 view_build 块慢例
// 9–14.1s、sandbox_shell_scan 块恒 265–1108ms；两块的共同落点是
// desktop.sandboxViewFor → 本包 shell inventory 的发现链。本探针在同一台真机
// 上直接计量该链的冷/热成本，验证「30s TTL 过期 → 下一次设置页打开同步整轮
// 重建」这一根因，并给修复（ForView stale-while-revalidate）留前后对照读数。
//
// 与任务 1090 探针同款纪律：设 REASONIX_BENCH_SHELL_INVENTORY=1 才跑真实
// 测量（CI 无真实 shell 环境也跑不了子进程探测），未设时显式 skip——路径
// 显式给出，非静默 SKIP。
func TestShellInventoryRealMachineProbe1257(t *testing.T) {
	if os.Getenv("REASONIX_BENCH_SHELL_INVENTORY") != "1" {
		t.Skip("real-machine probe: set REASONIX_BENCH_SHELL_INVENTORY=1 to measure this host (CI has no real shell environment)")
	}

	// ① 冷：整轮发现（LookPath+注册表+安装根+bash 探测子进程）。
	InvalidateShellInventory()
	start := time.Now()
	ShellCapabilitiesForConfig("", "")
	coldShells := time.Since(start)

	// ② 冷：git 发现（同一快照上的独立探测轮）。
	start = time.Now()
	GitCapabilityForConfig("", "")
	coldGit := time.Since(start)

	// ③ 热读：同快照内重复（设置页 30s 内二次打开的路径）。
	var warmMax time.Duration
	for range 3 {
		start = time.Now()
		ShellCapabilitiesForConfig("", "")
		GitCapabilityForConfig("", "")
		ResolveShell("", "", nil)
		if d := time.Since(start); d > warmMax {
			warmMax = d
		}
	}

	t.Logf("PROBE1257 cold-shells=%s cold-git=%s warm-3x-max=%s", coldShells, coldGit, warmMax)

	// ④ TTL 过期后的同步读（修复前设置页打开的真实路径）：先睡过 30s TTL，
	// 再走非 stale-ok 读——这正是 desktop.log 里 view_build 9–14s 的复现。
	time.Sleep(shellInventoryTTL + 500*time.Millisecond)
	start = time.Now()
	ShellCapabilitiesForConfig("", "")
	expiredSync := time.Since(start)
	t.Logf("PROBE1257 expired-sync-read=%s (pre-fix settings-open path)", expiredSync)

	// ⑤ TTL 过期后的 stale-ok 读（修复后设置页路径）：同样过期状态，
	// ForView 读必须立即返回旧快照、后台重建，不阻塞。
	time.Sleep(shellInventoryTTL + 500*time.Millisecond)
	start = time.Now()
	ShellCapabilitiesForView("", "")
	staleOkRead := time.Since(start)
	t.Logf("PROBE1257 expired-staleok-read=%s (post-fix settings-open path)", staleOkRead)

	// ⑥ 同一过期窗口内的完整展示面读链（= sandboxViewFor 等价序列：
	// ResolveShellForView + ForView 能力 + ForView git），给设置页打开
	// 总耗时里 shell 段的改后占比。
	start = time.Now()
	ResolveShellForView("", "", nil)
	ShellCapabilitiesForView("", "")
	GitCapabilityForView("", "")
	fullViewRead := time.Since(start)
	t.Logf("PROBE1257 expired-fullview-read=%s (sandboxViewFor equivalent, post-fix)", fullViewRead)

	// 给后台重建一点时间落盘，再读确认最终一致（新快照已发布）。
	time.Sleep(3 * time.Second)
	start = time.Now()
	ShellCapabilitiesForView("", "")
	rebuiltWarm := time.Since(start)
	t.Logf("PROBE1257 rebuilt-warm-read=%s", rebuiltWarm)

	// ⑦ 噪声归因：热态下同一展示面读链 ×10，报 min/max。min 为机制真值
	// （全缓存命中应为 µs-ms 级）；max-min 差即本机调度/争用墙钟噪声，
	// 对应生产 desktop.log 里 ⑦ 块恒 265–1108ms 的波动。
	var minRead, maxRead time.Duration = 0, 0
	for range 10 {
		start = time.Now()
		ResolveShellForView("", "", nil)
		ShellCapabilitiesForView("", "")
		GitCapabilityForView("", "")
		d := time.Since(start)
		if minRead == 0 || d < minRead {
			minRead = d
		}
		if d > maxRead {
			maxRead = d
		}
	}
	t.Logf("PROBE1257 warmview-10x min=%s max=%s (min=mechanism truth, max-min=host contention noise)", minRead, maxRead)

	// ⑧ 三连读分解：哪个成员承担热成本。exec.LookPath 独立计量——决策表
	// 每次调用现跑 LookPath（不进快照缓存），若它是热成本大头，则是快照
	// 缓存漏了一项发现数据。
	start = time.Now()
	ResolveShellForView("", "", nil)
	resolveOnly := time.Since(start)
	start = time.Now()
	ShellCapabilitiesForView("", "")
	capsOnly := time.Since(start)
	start = time.Now()
	GitCapabilityForView("", "")
	gitOnly := time.Since(start)
	start = time.Now()
	_, err := exec.LookPath("bash")
	lookPathErr := err
	lookPathOnly := time.Since(start)
	t.Logf("PROBE1257 warm-split resolve=%s caps=%s git=%s lookpath-bash(err=%v)=%s", resolveOnly, capsOnly, gitOnly, lookPathErr, lookPathOnly)

	// ⑨ 全热微诊断：⑧ 已把 lookup/probe 缓存打满，第二次 resolve 应为 µs。
	// 分步计量 lookup("bash") 与 probe(命中路径)，定位残留成本成员。
	ResolveShellForView("", "", nil)
	start = time.Now()
	ResolveShellForView("", "", nil)
	secondResolve := time.Since(start)
	snap := defaultShellInventory.snapshotOpts(runtime.GOOS, "", "", true)
	start = time.Now()
	bashPath, _ := snap.lookup("bash")
	lookupOnly := time.Since(start)
	start = time.Now()
	snap.probe(bashPath)
	probeOnly := time.Since(start)
	t.Logf("PROBE1257 warm-second resolve=%s lookup-bash=%s probe(%s)=%s", secondResolve, lookupOnly, bashPath, probeOnly)
}
