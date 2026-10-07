package collabinbox

import (
	"errors"
	"log/slog"
	"os"

	"reasonix/internal/baseproc/pidalive"
)

// 任务511 复发断根（2026-10-07）——stale 锁持有者诊断自动回收。
//
// 事故形态：实例 A 持锁后死亡，OS 层锁随进程终结即时释放（Windows LockFileEx /
// POSIX flock 都绑定句柄生命周期），但 holder 侧车 .collab-inbox.lock.holder
// 是普通文件、内容刻意在 release 后存活（「informative, never authoritative」
// 契约），于是「pid=26048 held_since=10-05 23:45」残留 1.5 天——排障者据此追
// 僵尸、误判「锁永远 busy」，511 当晨与复发两轮排查都被它带偏。
//
// 回收语义（防误杀是硬边界）：
//   - 只删「指认死进程」的侧车诊断文件，绝不触碰锁文件本身与 OS 锁——
//     死进程的锁由内核回收，无需也无法代劳；
//   - 侧车 pid 存活（或 pid 复用后看似存活）⇒ 一字不动。宁可留下陈旧诊断，
//     绝不误清存活持有者的记录——与 baseproc lease C4 同一保守取向；
//   - pid 缺失/不可解析 ⇒ 无法判定 ⇒ 不动（never guess）；
//   - 清除动作本身带 INFO 留痕，指认被清的完整侧车内容。
//
// 竞态窗口分析（完整版见任务 511 复发报告 §竞态）：本函数在锁外运行，唯一的
// 竞态对象是「另一进程恰在本进程读侧车与 os.Remove 之间完成新的独占加锁并
// 重写侧车」。后果仅是那一份新鲜诊断被误删——锁语义零影响（删除的是诊断
// 文件，不是锁文件），新持有者的下一次独占加锁会重新盖章。诊断可再生的
// 丢失换不来任何正确性风险，故不加锁、不嵌套。

// clearStaleHolderIfDead applies the recycle contract above. It returns the
// cleared sidecar content ("" when the sidecar was absent, live-owned, or
// left alone as unjudgeable).
func (s *Store) clearStaleHolderIfDead() string {
	info := s.LockHolderInfo()
	if info == "" {
		return ""
	}
	pid := pidalive.ParseHolderPid(info)
	if pid <= 0 {
		return "" // unparsable: cannot judge — leave it, never guess
	}
	if pidalive.Alive(pid) {
		return "" // live holder (or pid reuse): NEVER touch a live holder's record
	}
	if err := os.Remove(s.lockHolderPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("collab inbox: stale lock holder sidecar removal failed",
			"path", s.lockHolderPath(), "stale_holder", info, "pid", pid, "err", err)
		return ""
	}
	slog.Info("collab inbox: stale lock holder sidecar cleared (recorded holder process is gone; the OS lock died with it)",
		"stale_holder", info, "pid", pid)
	return info
}
