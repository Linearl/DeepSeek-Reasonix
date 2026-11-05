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
// this controller did not create — never armed. tl1390: arms join a FIFO
// queue instead of overwriting the pending pointer — a fork off an already
// forked copy queues behind the first promotion (oldest copy settles onto the
// true main first; later entries retarget to the settled main), so the true
// main is never abandoned and copies converge instead of accumulating.
func (c *Controller) armPendingRecoveryPromotion(originalPath string, info agent.RecoveryBranchInfo) {
	if c.executor == nil || strings.TrimSpace(originalPath) == "" || strings.TrimSpace(info.Path) == "" || info.Path == originalPath {
		return
	}
	if info.Existing {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pending := range c.pendingRecoveryPromotions {
		if pending.copyPath == info.Path {
			return // 同一副本的登记已在队（fork 重试/重复登记），不重复入队
		}
	}
	// tl1390：从已在队的副本上再 fork（副本链）时，新登记直接指向链条根主
	// 路径——2026-11-04 事故的 copy2→copy1 转正把 recovery 名当成了 main，
	// 真主被弃、双副本并存。转正是副本字节整体上调（fork 前向复制），落点
	// 必须始终是主名；旧登记保留在队（其副本可能仍有独有字节，由换道守卫
	// 逐周期出队），孤儿副本交由既有清理通路收敛。
	for _, existing := range c.pendingRecoveryPromotions {
		if existing.copyPath == originalPath {
			originalPath = existing.mainPath
			break
		}
	}
	c.pendingRecoveryPromotions = append(c.pendingRecoveryPromotions, &recoveryPromotion{
		mainPath: originalPath,
		copyPath: info.Path,
		forkedAt: time.Now(),
	})
}

// applyPendingRecoveryPromotion settles the oldest armed promotion when its
// main file has stayed quiet since the fork. It reports whether the controller
// now lives on a main path again (callers re-read c.sessionPath).
func (c *Controller) applyPendingRecoveryPromotion() bool {
	c.mu.Lock()
	if len(c.pendingRecoveryPromotions) == 0 {
		c.mu.Unlock()
		return false
	}
	pending := c.pendingRecoveryPromotions[0]
	currentPath := c.sessionPath
	c.mu.Unlock()
	if pending == nil || c.executor == nil {
		return false
	}
	if currentPath != pending.copyPath {
		// 已被其他通路换道（brake adopt / quota adopt / 手动切会话）：队首
		// 登记作废，下个保存周期结算下一条。
		c.mu.Lock()
		c.pendingRecoveryPromotions = c.pendingRecoveryPromotions[1:]
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
	// tl1390：quiesce 本控制器的账本写手句柄再搬侧车。活跃 turn 期间账本
	// 长期持有绑定会话的 *.turns.jsonl 句柄，源或目标任一侧落在绑定会话上
	// 时 rename 必败（Windows 无 FILE_SHARE_DELETE）——2026-11-04 事故永久
	// declined 的直接机制。quiesce 关句柄且拦住并发 append（fn 在 l.mu 内），
	// 写手直写无 bufio 零丢失；decline 后下次 append 经 ensureWriterLocked
	// 原路径重开，成功后 adoptDiskSession 会 rebindTurnEvents 到新主路径。
	ledger := c.turnEventLedger()
	var archived string
	promote := func() error {
		var err error
		archived, err = agent.PromoteRecoveryBranchToMain(pending.copyPath, pending.mainPath)
		return err
	}
	var err error
	if ledger != nil {
		err = ledger.WithWriterQuiesced(promote)
	} else {
		err = promote()
	}
	if err != nil {
		// 锁忙/搬运失败：保留登记，下一保存周期重试。
		slog.Warn("controller: deferred recovery promotion declined this cycle; staying on the recovery copy",
			"main", pending.mainPath, "recovery", pending.copyPath, "err", err)
		return false
	}
	// tl1390：链式结算——队列里以本副本为 mainPath 的后继登记重定向到刚
	// 落定的主路径（copy2→copy1 变 copy2→trueMain），forkedAt 同步重置：
	// 主文件刚换了身份（= 刚结算的副本字节），「fork 后保持安静」的判定
	// 基准从重定向此刻起算，否则旧基准早于主文件最新写入，后继永远无法
	// 满足安静条件。
	c.mu.Lock()
	retargeted := c.pendingRecoveryPromotions[:0]
	for _, later := range c.pendingRecoveryPromotions[1:] {
		if later.mainPath == pending.copyPath {
			later.mainPath = pending.mainPath
			later.forkedAt = time.Now()
		}
		retargeted = append(retargeted, later)
	}
	c.pendingRecoveryPromotions = retargeted
	c.mu.Unlock()
	// 回绑主路径：从盘上重载（字节刚由副本 rename 而来，与内存转录逐字一致），
	// 以主路径为基线重建干净的 CAS 账本，投影/检查点/任务面同步翻转。
	if !c.adoptDiskSession(pending.mainPath) {
		// 转正已落地但回绑失败（重载磁盘错误）：下一次保存会按外部移除通路
		// 自救；这里留下失败痕迹即可。
		slog.Error("controller: recovery branch promoted but the main session failed to reload; next save recovers via the external-removal path",
			"main", pending.mainPath, "recovery", pending.copyPath)
		c.mu.Lock()
		c.pendingRecoveryPromotions = c.pendingRecoveryPromotions[1:]
		c.mu.Unlock()
		return false
	}
	c.mu.Lock()
	c.sessionPath = pending.mainPath
	c.guardianPath = guardian.PathFor(pending.mainPath)
	c.mu.Unlock()
	// commitRecoveredSession 曾把 in-flight turn 标记从主路径搬到副本；转正
	// 后搬回来，中断回合的恢复面跟随权威路径。
	c.transplantInFlightTurnMarker(pending.copyPath, pending.mainPath)
	appendSnapshotConflictDiagnostic(pending.mainPath, "promotion", "promoted_recovery_branch_to_main", nil, pending.mainPath, false)
	slog.Warn("controller: recovery branch promoted back to the main file after the main file stayed quiet",
		"main", pending.mainPath, "recovery", pending.copyPath, "archived", archived,
		"topic", agent.RecoveryTopicRootID(pending.mainPath))
	// tl1403: the host surface (tab SessionPath / lease / write authority /
	// frontend identity) was moved onto the copy at fork time by
	// OnSessionRecovered; the promotion just moved the writer back to the main
	// path. Without this notification the display face and the write face stay
	// split — the tab keeps watching the copy while every save lands on the
	// main file, which is exactly the watchdog early-stop loop. Fire-and-report:
	// the bytes already moved, so a handler failure never rolls the promotion
	// back; it logs and moves on.
	if promoted := c.sessionPromotedHandler(); promoted != nil {
		if err := promoted(SessionPromotionInfo{CopyPath: pending.copyPath, MainPath: pending.mainPath}); err != nil {
			slog.Warn("controller: recovery promotion host notification failed",
				"main", pending.mainPath, "recovery", pending.copyPath, "err", err)
		}
	}
	c.sink.Emit(sessionRecoveryNotice(event.NoticeCodeSessionRecoveryAdopted,
		"the conflict copy was folded back into this conversation's main file; the previous on-disk version is archived beside it"))
	return true
}
