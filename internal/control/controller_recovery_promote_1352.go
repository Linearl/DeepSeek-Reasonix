package control

import (
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/guardian"
)

// tl1352 A：fork 副本转正的 controller 侧接线。
//
// recoverSnapshotConflict fork 落地后调用本方法：两条保存锁空闲即把副本整体
// rename 回主文件名（字节级搬移在 agent.PromoteRecoveryBranchToMain 内完成，
// 主文件现存字节先白名单归档），随后把 controller 的全部绑定面翻回主路径——
// 与 commitRecoveredSession 的正向翻转互为镜像。任何一步失败都留在副本上
// （fork 仍是兜底），只留 WARN 排查痕迹。

// promoteRecoveryBranchToMain reports whether the promotion landed and the
// controller now writes to originalPath again.
func (c *Controller) promoteRecoveryBranchToMain(originalPath string, info agent.RecoveryBranchInfo) bool {
	if c.executor == nil || strings.TrimSpace(originalPath) == "" || strings.TrimSpace(info.Path) == "" || info.Path == originalPath {
		return false
	}
	// 只转正本次新建的副本：Existing=true 的副本身份与外部引用关系不明，不动。
	if info.Existing {
		return false
	}
	archived, err := agent.PromoteRecoveryBranchToMain(info.Path, originalPath)
	if err != nil {
		slog.Warn("controller: recovery branch promotion declined; active writes stay on the recovery copy",
			"main", originalPath, "recovery", info.Path, "err", err)
		return false
	}
	// 会话回绑主路径：从盘上重载（字节刚由副本 rename 而来，与内存转录逐字
	// 一致），以主路径为基线重建干净的 CAS 账本，投影/检查点/任务面同步翻转。
	if !c.adoptDiskSession(originalPath) {
		// 转正已落地但回绑失败（重载磁盘错误）：下一次保存会按外部移除通路
		// 自救；这里留下失败痕迹即可。
		slog.Error("controller: recovery branch promoted but the main session failed to reload; next save recovers via the external-removal path",
			"main", originalPath, "recovery", info.Path)
		return false
	}
	c.mu.Lock()
	c.sessionPath = originalPath
	c.guardianPath = guardian.PathFor(originalPath)
	c.mu.Unlock()
	// commitRecoveredSession 曾把 in-flight turn 标记从主路径搬到副本；转正
	// 后搬回来，中断回合的恢复面跟随权威路径。
	c.transplantInFlightTurnMarker(info.Path, originalPath)
	slog.Info("controller: recovery branch promoted; displaced main bytes archived",
		"main", originalPath, "recovery", info.Path, "archived", archived)
	return true
}
