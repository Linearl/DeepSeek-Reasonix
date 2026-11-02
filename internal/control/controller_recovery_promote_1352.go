package control

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/guardian"
)

// tl1352 A：fork 副本延迟转正（副本转正回主文件）。
//
// fork 的既有契约是隔离——另一写者可能仍在写主文件，立刻把副本 rename 回去
// 会与它打乒乓（20261031 的并发契约测试也钉住了这一点）。但现场的主诉是：
// 幽灵写者消失后，副本继续承载活跃写入、主文件永久停更（停了两天），按主
// 文件恢复会丢掉全部后续工作。
//
// 所以转正是延迟的、带前提的：fork 时只登记（arm）；每个保存周期开头结算
// （apply）——主文件自 fork 起保持安静（mtime 未再前进 = 另一写者已死）才
// 把副本整体 rename 回主文件名并回绑主路径；主文件仍在被写则保留登记继续
// 隔离，绝不与活跃写者抢盘。字节级搬移与白名单归档在
// agent.PromoteRecoveryBranchToMain 内完成。

// promotionQuietEpsilon tolerates filesystem mtime granularity around the
// fork instant: a main-file write within this window right at the fork is
// treated as quiet (the bytes it would carry are archived by the promotion
// anyway, so the race costs nothing).
const promotionQuietEpsilon = time.Second

// recoveryPromotion is one armed fork promotion. Armed at fork time
// (armPendingRecoveryPromotion), settled at a later save cycle
// (applyPendingRecoveryPromotion).
type recoveryPromotion struct {
	mainPath string
	copyPath string
	forkedAt time.Time
}

// armPendingRecoveryPromotion registers the promotion for a freshly created
// fork. Pre-existing recovery copies (Existing=true) are an isolation state
// this controller did not create — never armed.
func (c *Controller) armPendingRecoveryPromotion(originalPath string, info agent.RecoveryBranchInfo) {
	if c.executor == nil || strings.TrimSpace(originalPath) == "" || strings.TrimSpace(info.Path) == "" || info.Path == originalPath {
		return
	}
	if info.Existing {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingRecoveryPromotion = &recoveryPromotion{
		mainPath: originalPath,
		copyPath: info.Path,
		forkedAt: time.Now(),
	}
}

// applyPendingRecoveryPromotion settles an armed promotion when the main file
// has stayed quiet since the fork. It reports whether the controller now lives
// on the main path again (callers re-read c.sessionPath).
func (c *Controller) applyPendingRecoveryPromotion() bool {
	c.mu.Lock()
	pending := c.pendingRecoveryPromotion
	currentPath := c.sessionPath
	c.mu.Unlock()
	if pending == nil || c.executor == nil {
		return false
	}
	if currentPath != pending.copyPath {
		// 已被其他通路换道（brake adopt / quota adopt / 手动切会话）：登记作废。
		c.mu.Lock()
		c.pendingRecoveryPromotion = nil
		c.mu.Unlock()
		return false
	}
	// 内存转录必须与副本盘上字节逐字稳定才可转正：转正会以副本字节重载会话
	// （adoptDiskSession），内存里未落盘的内容会在重载中丢失。未稳定就留给
	// 本次保存先落副本，下个周期再结算。
	if !c.executor.Session().SnapshotUpToDateForPath(pending.copyPath) {
		return false
	}
	if info, err := os.Stat(pending.mainPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false // 主文件状态不可判：下个保存周期再试
		}
		// 主文件已消失（外部移除现场）：转正即复活主文件，直接放行。
	} else if info.ModTime().After(pending.forkedAt.Add(promotionQuietEpsilon)) {
		return false // fork 后主文件仍在被写：另一写者活跃，保持隔离
	}
	archived, err := agent.PromoteRecoveryBranchToMain(pending.copyPath, pending.mainPath)
	if err != nil {
		// 锁忙/搬运失败：保留登记，下一保存周期重试。
		slog.Warn("controller: deferred recovery promotion declined this cycle; staying on the recovery copy",
			"main", pending.mainPath, "recovery", pending.copyPath, "err", err)
		return false
	}
	// 回绑主路径：从盘上重载（字节刚由副本 rename 而来，与内存转录逐字一致），
	// 以主路径为基线重建干净的 CAS 账本，投影/检查点/任务面同步翻转。
	if !c.adoptDiskSession(pending.mainPath) {
		// 转正已落地但回绑失败（重载磁盘错误）：下一次保存会按外部移除通路
		// 自救；这里留下失败痕迹即可。
		slog.Error("controller: recovery branch promoted but the main session failed to reload; next save recovers via the external-removal path",
			"main", pending.mainPath, "recovery", pending.copyPath)
		c.mu.Lock()
		c.pendingRecoveryPromotion = nil
		c.mu.Unlock()
		return false
	}
	c.mu.Lock()
	c.sessionPath = pending.mainPath
	c.guardianPath = guardian.PathFor(pending.mainPath)
	c.pendingRecoveryPromotion = nil
	c.mu.Unlock()
	// commitRecoveredSession 曾把 in-flight turn 标记从主路径搬到副本；转正
	// 后搬回来，中断回合的恢复面跟随权威路径。
	c.transplantInFlightTurnMarker(pending.copyPath, pending.mainPath)
	appendSnapshotConflictDiagnostic(pending.mainPath, "promotion", "promoted_recovery_branch_to_main", nil, pending.mainPath, false)
	slog.Warn("controller: recovery branch promoted back to the main file after the main file stayed quiet",
		"main", pending.mainPath, "recovery", pending.copyPath, "archived", archived,
		"topic", agent.RecoveryTopicRootID(pending.mainPath))
	c.sink.Emit(sessionRecoveryNotice(event.NoticeCodeSessionRecoveryAdopted,
		"the conflict copy was folded back into this conversation's main file; the previous on-disk version is archived beside it"))
	return true
}
