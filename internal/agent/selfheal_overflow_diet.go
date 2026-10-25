package agent

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// 任务 1114：投影失效 × 物理超窗的自动应急减肥（失效自动 Diet）。
//
// 现场（20261024 新9 二次复发，desktop.log.11）：05:12 写下的投影在当晚构建
// 更新后因 covered 前缀内容漂移被判失效（08:45:08 warn reason=content_mismatch，
// UI 徽标「投影失效 · 全量回退中」），视图退化为服务全量 canonical
// （3,025,467 / 1M 窗口）；彼时唯一能给 canonical 减肥的 Diet 通路又被备份
// 自锁挡死（tl 1116 已修白名单），且触发点只在打开/切 tab——会话在失效
// 现场没有任何自动收敛手段，压缩循环对 3M token 的摘要请求反复 400/超时
// 直到人工干预（23:38 手动 compact 才落地）。
//
// 本件把打开时自愈（SelfHealOpenSession，control 层）已验证的同一条
// 「备份 → 剥图+折叠 → CAS 重写」通路接到失效现场：当 fallback 观测真的
// 发出警告（warnProjectionFallback，自带 5 分钟节流）且物理占用 ≥ 硬输入
// 上限时，后台跑一次 Diet。它不新增任何投影写入路径——DietSessionMessages
// / Rewrite / SaveRewriteCompact / BackupSessionFiles 全是已装机原语；
// 1116 调研的竞品对照（借鉴排序 #1：codex 跳过模型重装窗口 / mimo
// stripMedia）也把「零模型兜底自动化」列为成本最低、收益最高的收口。
//
// 与 kickProjectionRebuild 的分工：canonical 没过硬顶时，后台摘要重建是
// 又快又省的正道（813 的分段重建）；只有「失效 + 物理超窗」这个发不出请求
// 的量级才先 Diet（本地零模型、必然成功）再让重建在小转录上跑完——
// 两者用各自的 CAS 位去重，写路径都经 SaveRewriteCompact 的 CAS 保护。

// kickOverflowDiet arms one automatic overflow diet for a projection-fallback
// episode whose canonical transcript sits at or above the hard input ceiling.
// Safe to call from the maintenance path: it never blocks and never runs
// inline — the diet happens on a deduplicated background goroutine.
func (a *Agent) kickOverflowDiet(reason string, est int) {
	if a == nil || a.sess.conversation == nil || a.sess.path == "" {
		return
	}
	if est < a.hardInputCeiling() {
		// 未达「发不出去」量级：投影失效交给 kickProjectionRebuild 的摘要重建，
		// 不为可发送的会话做本地重写。
		return
	}
	if _, dag := a.sess.conversation.Head(); !dag {
		// 非 DAG 会话没有 CAS 重写通路（与 SelfHealOpenSession 同一门槛）。
		return
	}
	if !a.sess.dietPending.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.sess.dietPending.Store(false)
		s := a.sess.session()
		if s == nil {
			return
		}
		a.runOverflowDiet(s, reason, est)
	}()
}

// runOverflowDiet executes one backup → strip+fold → CAS rewrite pass on the
// live session. Ordering follows the 1011 discipline — the originals are in
// the backup directory before any byte is rewritten — and the backup uses the
// 1116 allowlist, so runtime locks (the 新9 blocker) cannot fail it.
func (a *Agent) runOverflowDiet(s *Session, reason string, est int) {
	path := a.sess.path
	window := a.effectiveContextWindow()
	folded, actions, changed := DietSessionMessages(s.Snapshot(), window)
	if !changed {
		slog.Info("session: overflow diet skipped (nothing to strip or fold)",
			"path", filepath.Base(path), "reason", reason,
			"estimate_tokens", est, "window_tokens", window)
		return
	}
	backupDir, err := BackupSessionFiles(path)
	if err != nil {
		// 协调线 1556 同一语义：无备份不动手。ERROR 留排查痕迹，不静默降级。
		slog.Error("session: overflow diet backup failed; refusing to rewrite without a backup",
			"path", path, "reason", reason, "err", err)
		return
	}
	s.Rewrite(folded, "selfheal_diet")
	if err := s.SaveRewriteCompact(path); err != nil {
		slog.Warn("session: overflow diet rewrite failed",
			"path", path, "reason", reason, "err", err)
		return
	}
	names := make([]string, 0, len(actions))
	for _, act := range actions {
		if act.Status == "done" {
			names = append(names, act.Action)
		}
	}
	WriteSelfHealRepairRecord(path, backupDir, names,
		fmt.Sprintf("auto_overflow_diet reason=%s estimate_tokens=%d window_tokens=%d", reason, est, window))
	slog.Warn("session: overflow diet applied after projection fallback",
		"path", filepath.Base(path), "reason", reason, "actions", strings.Join(names, "+"),
		"estimate_tokens_before", est, "estimate_tokens_after", estimateProjectionTokens(folded),
		"window_tokens", window, "backup", backupDir)
}
