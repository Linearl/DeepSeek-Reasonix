package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"reasonix/internal/provider"
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
//
// tl1083 §5.2-P0①：前置门槛改绑。原门槛「DAG 在位」（conversation.Head()）
// 随 tl1173 P1（新会话全走 schema-1，defaultNewSessionDAGWriter=false）与
// tl1227（存量 DAG 日志一次性迁移 schema-1、读侧单路径化）双双退役——
// 对迁移后的所有会话 Diet 结构性 return，1114 的零模型救命机制整机不可达
// （实例①正是该形态）。裁决 2 选 A：改绑「存在可校验的物化转录」——
// schema-1 的 SaveRewriteCompact 与 DAG 同享 CAS 语义（盘上 revision/digest
// 账本 + SessionSnapshotConflictError），重写管线原样复用，不新增通路。
// 判据 fail-safe：校验失败即拒绝重写（与 1116「无备份不动手」同一纪律），
// ERROR 留排查痕迹；备份白名单先行顺序不变（BackupSessionFiles 先于任何
// 落笔）；重写产物逐字校验（verifyRewrittenTranscript）落 repair 记录。

// kickOverflowDiet arms one automatic overflow diet for a projection-fallback
// episode whose canonical transcript sits at or above the hard input ceiling.
// Safe to call from the maintenance path: it never blocks and never runs
// inline — the diet (and its disk-bound transcript verification) happens on a
// deduplicated background goroutine.
func (a *Agent) kickOverflowDiet(reason string, est int) {
	if a == nil || a.sess.conversation == nil || a.sess.path == "" {
		return
	}
	if est < a.hardInputCeiling() {
		// 未达「发不出去」量级：投影失效交给 kickProjectionRebuild 的摘要重建，
		// 不为可发送的会话做本地重写。
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

// verifyMaterializedTranscript reports why the session's materialized
// transcript cannot be verified for a CAS rewrite (nil when it can). The
// tl1083 replacement for the retired DAG gate: the transcript must exist as
// bytes on disk this build owns (never a foreign file, never a future
// schema), and the CAS ledger must be readable when artifacts carry content —
// an unreadable ledger means no writer here can prove what it is about to
// rewrite, so the diet refuses. Purely read-side: any healing of a torn tail
// stays the save path's job.
func verifyMaterializedTranscript(path string) error {
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return fmt.Errorf("event log probe: %w", err)
	}
	if probe.futureSchema {
		return fmt.Errorf("event log schema %d is newer than this build supports", probe.schemaVersion)
	}
	if !probe.native && !probe.dag {
		return fmt.Errorf("event log is a foreign file this build does not own")
	}
	if !sessionArtifactsHaveContent(path) {
		return fmt.Errorf("no materialized transcript bytes on disk")
	}
	if _, _, err := sessionContentRevision(path); err != nil {
		return fmt.Errorf("CAS ledger unreadable: %w", err)
	}
	return nil
}

// dietMessageFingerprint hashes the provider-visible bytes of one message —
// role, content, reasoning, tool linkage and every image/tool-call payload —
// so a rewrite product can be verified verbatim against the folded
// transcript the diet intended to install.
func dietMessageFingerprint(m provider.Message) string {
	h := sha256.New()
	fmt.Fprintf(h, "role=%q\x00content=%d:%s\x00", m.Role, len(m.Content), m.Content)
	fmt.Fprintf(h, "reasoning=%d:%s\x00", len(m.ReasoningContent), m.ReasoningContent)
	fmt.Fprintf(h, "tool_call_id=%q\x00name=%q\x00", m.ToolCallID, m.Name)
	for _, img := range m.Images {
		fmt.Fprintf(h, "image=%d:%s\x00", len(img), img)
	}
	for _, tc := range m.ToolCalls {
		fmt.Fprintf(h, "toolcall=%q|%q|%d:%s\x00", tc.ID, tc.Name, len(tc.Arguments), tc.Arguments)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// verifyRewrittenTranscript reads the persisted transcript back and compares
// it verbatim against the folded transcript the diet rewrote toward: same
// message count and byte-identical provider-visible payloads. Any drift is a
// data-integrity failure, never a retry trigger — the originals are already
// in the repair backup, so the caller only surfaces the mismatch.
func verifyRewrittenTranscript(path string, want []provider.Message) error {
	s, err := LoadSession(path)
	if err != nil {
		return fmt.Errorf("reload for verification: %w", err)
	}
	got := s.Snapshot()
	if len(got) != len(want) {
		return fmt.Errorf("rewritten transcript has %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if dietMessageFingerprint(got[i]) != dietMessageFingerprint(want[i]) {
			return fmt.Errorf("message %d diverges after the rewrite (role=%s)", i, want[i].Role)
		}
	}
	return nil
}

// runOverflowDiet executes one verify → backup → strip+fold → CAS rewrite →
// re-verify pass on the live session. Ordering follows the 1011 discipline —
// the originals are in the backup directory before any byte is rewritten —
// and the backup uses the 1116 allowlist, so runtime locks (the 新9 blocker)
// cannot fail it. The tl1083 gate sits first: a transcript that cannot be
// verified on disk refuses the whole pass (fail-safe, same discipline as the
// 1116 no-backup-no-diet rule).
func (a *Agent) runOverflowDiet(s *Session, reason string, est int) {
	path := a.sess.path
	if err := verifyMaterializedTranscript(path); err != nil {
		// tl1083：可校验判据 fail-safe——校验失败即拒绝重写。ERROR 留排查
		// 痕迹，不静默降级（同 1116 备份失败的处置形态）。
		slog.Error("session: overflow diet refused — materialized transcript failed verification",
			"path", filepath.Base(path), "reason", reason, "err", err)
		return
	}
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
	// 重写产物逐字校验（§6.1 验收）：盘上落地的字节必须与折叠意图一致。
	// mismatch 不触发重试——原件已在备份里，只需失败可见。
	verifyStatus := "ok"
	if err := verifyRewrittenTranscript(path, folded); err != nil {
		verifyStatus = "mismatch"
		slog.Error("session: overflow diet rewrite product failed verbatim verification",
			"path", filepath.Base(path), "reason", reason, "backup", backupDir, "err", err)
	}
	names := make([]string, 0, len(actions))
	for _, act := range actions {
		if act.Status == "done" {
			names = append(names, act.Action)
		}
	}
	WriteSelfHealRepairRecord(path, backupDir, names,
		fmt.Sprintf("auto_overflow_diet reason=%s estimate_tokens=%d window_tokens=%d verify=%s", reason, est, window, verifyStatus))
	slog.Warn("session: overflow diet applied after projection fallback",
		"path", filepath.Base(path), "reason", reason, "actions", strings.Join(names, "+"),
		"estimate_tokens_before", est, "estimate_tokens_after", estimateProjectionTokens(folded),
		"window_tokens", window, "verify", verifyStatus, "backup", backupDir)
}
