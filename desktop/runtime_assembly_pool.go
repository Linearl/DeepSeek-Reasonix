package main

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"

	"reasonix/internal/boot"
)

// runtimeAssemblyPool is the task-363A reuse pool. tl1859（方案A）：键放宽为
// root-only——一个 root 一条 ReusedAssembly，同项目换模型/effort 打开对话不再
// 全额冷建 prompt/skills/commands/hooks 发现。依据（启动耗时根因诊断报告
// §七）：复用产物全部按 root 构建（base prompt/rules/memory/skills），模型维度
// 的唯一已知分支 persona 由 boot 复用后 strip+prepend 重贴
// （deepseek_v4_pro_persona.go），effort 不进任何复用内容；跨维度复用已有两条
// 生产先例链（切模型 app_model.go / 切 effort app_effort.go 用出向键取池）。
// tl1741x：experimental_runtime_reuse 门随撤开关删除（tl1741 主件已撤
// runtimeReuseEnabled 配置门）——池无条件启用，helper 不再有 off 分支；池冷时
// acquire 返回 nil、构建路径退化为 boot.Build 同形。
//
// Lifecycle is last-writer-wins per key: store overwrites the entry for that
// key and acquire never mutates. Pooled assemblies share the exact objects
// wired into the live controller — treat them as immutable. Stale entries are
// bounded by the number of distinct roots the user actually opens, and each is
// GC-able once overwritten or dropped by DropRuntimeAssemblyPools.
type runtimeAssemblyPool struct {
	mu         sync.Mutex
	entries    map[string]*boot.ReusedAssembly
	generation uint64
}

var runtimeAssemblies = &runtimeAssemblyPool{entries: map[string]*boot.ReusedAssembly{}}

// runtimeAssemblyKey fingerprints the reuse identity. tl1859（方案A）：只有
// root 参与指纹——model/effort 维度随键移除（跨维度复用的正确性由 boot 的
// persona 重贴与 memory 指纹回退保证，不靠键隔离）。
func runtimeAssemblyKey(root string) string {
	digest := sha256.Sum256([]byte(root))
	return hex.EncodeToString(digest[:8])
}

// tl1741：runtimeReuseEnabled 配置门随撤开关删除——运行时复用池无条件启用
// （键退役，复用/存储路径只受池冷热影响，语义不变）。

// acquireRuntimeAssembly returns the pooled assembly for the key, or nil when
// the pool is cold. Read-only: no reference counting.
func (a *App) acquireRuntimeAssembly(key string) *boot.ReusedAssembly {
	runtimeAssemblies.mu.Lock()
	defer runtimeAssemblies.mu.Unlock()
	assembly := runtimeAssemblies.entries[key]
	if assembly != nil {
		slog.Info("desktop: runtime assembly hit", "key", key)
	}
	return assembly
}

// storeRuntimeAssembly publishes a freshly built assembly under the key
// (last-writer-wins). A no-op for a nil assembly.
func (a *App) storeRuntimeAssembly(key string, assembly *boot.ReusedAssembly) {
	if assembly == nil {
		return
	}
	runtimeAssemblies.mu.Lock()
	defer runtimeAssemblies.mu.Unlock()
	runtimeAssemblies.entries[key] = assembly
	runtimeAssemblies.generation++
	slog.Info("desktop: runtime assembly stored", "key", key, "pool_size", len(runtimeAssemblies.entries))
}

// DropRuntimeAssemblyPools empties the pool (tests). Stale entries were never
// reachable through a cold key, so this is hygiene rather than correctness.
func DropRuntimeAssemblyPools() {
	runtimeAssemblies.mu.Lock()
	defer runtimeAssemblies.mu.Unlock()
	runtimeAssemblies.entries = map[string]*boot.ReusedAssembly{}
	runtimeAssemblies.generation++
}

// runtimeAssemblyKeyForTab derives the pool key for a tab snapshot in one
// place so acquire/store always agree on the identity. tl1859（方案A）：签名
// 收窄为 root-only——编译期堵死「某条链私自加回 model/effort 维度」的回归面。
func runtimeAssemblyKeyForTab(workspaceRoot string) string {
	return runtimeAssemblyKey(strings.ToLower(strings.TrimSpace(workspaceRoot)))
}
