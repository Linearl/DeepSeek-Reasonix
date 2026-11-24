package main

// tl1787 续作（tl1674 阶段二定案，用户 20261124 拍板）：pre-repair 分叉的
// **后台自愈**——idle 期元信息拼接。
//
// 触发面：挂 events_idle_sweep 的 tick（Task 945 已有 retire+compact 后台
// pass+park 机制）——每 tick 在既有 pass 之外跑一遍独立拼接 pass：判据通过
// 的分叉把拼接点/段边界/校验值登记进 <stem>.splices.json 档案袋（agent 层，
// 纯元信息，投影内容一个字节不动、零模型调用）。失败不阻塞既有 sweep：
// 单条失败记日志进下一 tick 的 skip 桶，不告警不重试风暴。
//
// 判据（note 三块①）：lineage（面板枚举同源的 owner↔备份同族关系）+ 改写
// 点时间戳（repairlog 的 BackupDir 命中=改写锚点；备份目录 stamp 先于锚点
// =时间线一致）+ 内容连续性（agent.CheckPreRepairSplice：covered/分叉长链/
// 折叠续接三分）。链预览选 HEAD 是 tl1674 手动合并入口的交互件；自动路径
// 的 HEAD 规则固定=新链（主线）为准——旧链以元信息登记为历史，绝不倒灌。
//
// 开关：experimental_prepair_autosplice（实验档，铁律 2 默认关）——off 时
// 本 pass 不跑，登记面与引入前逐字节一致。

import (
	"log/slog"
	"os"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/secrets"
)

// preRepairAutospliceMaxPerPass 是每 tick 的登记上限：登记是快照读+一次
// 小文件原子写，串行限量防止升级后首扫面对大批量存量分叉时堆叠 IO
// （eventsIdleSweepMaxPerPass 同一纪律）。
const preRepairAutospliceMaxPerPass = 4

// prepairAutospliceLive 是开关读缝（var 仅为测试缝——workspaceTrashPlatform
// 同一先例）：生产=config.PrepairAutospliceLive 的只读活读。
var prepairAutospliceLive = config.PrepairAutospliceLive

// runPreRepairAutoSplicePass 跑一遍拼接 pass：对每个活宿主备份跑判据，通过
// 者登记档案袋。enabled=false 直接返回（开关关=零动作）。busy 由调用方注
// 入（与 events sweep tick 同一判定源），单条失败只记日志——永不阻塞 sweep。
func runPreRepairAutoSplicePass(enabled bool, views []PreRepairBackupView, busy func(ownerPath string) bool, now time.Time) (registered, skipped int) {
	if !enabled {
		return 0, 0
	}
	for _, view := range views {
		if registered >= preRepairAutospliceMaxPerPass {
			break
		}
		// 判据前置闸：只拼活宿主（孤儿备份无可回主线；已归档宿主先从回收
		// 站还原对话——与手动合并桥同一目标闸）。无转录的空壳备份跳过。
		if view.OwnerPath == "" || view.OwnerArchived || view.Transcript == "" {
			skipped++
			continue
		}
		owner := view.OwnerPath
		if busy != nil && busy(owner) {
			skipped++
			continue
		}
		// 幂等闸：同一备份只登记一次（档案袋已有记录的跳过，不重写）。
		if agent.HasPreRepairSplice(owner, view.Dir) {
			skipped++
			continue
		}
		// 改写锚点：repairlog 的 BackupDir 命中=这条备份就是那次改写的改写
		// 前状态（lineage 强锚点）。时间线校验：备份 stamp 必须先于锚点。
		repairAt := int64(0)
		if records, err := agent.ListSessionRepairRecordsFor(owner); err == nil {
			for _, rec := range records {
				if rec.BackupDir == "" || rec.BackupDir != view.Dir {
					continue
				}
				if at, perr := time.Parse(time.RFC3339, rec.At); perr == nil {
					repairAt = at.Unix()
				}
				break
			}
		}
		if repairAt > 0 {
			if stampTime, perr := time.Parse("20060102T150405", view.Stamp); perr == nil && stampTime.After(time.Unix(repairAt, 0)) {
				slog.Warn("desktop: pre-repair autosplice skipped (backup newer than its repair record)",
					"backup", view.Dir, "owner", owner)
				skipped++
				continue
			}
		}
		plan, err := agent.CheckPreRepairSplice(owner, view.Transcript, repairAt)
		if err != nil {
			slog.Warn("desktop: pre-repair autosplice check failed (will retry next tick)",
				"backup", view.Dir, "owner", owner, "err", secrets.RedactError(err))
			skipped++
			continue
		}
		if !agent.PreRepairSplicePlanSplicable(plan) {
			// 诚实跳过桶：covered/无锚点等形态不是失败，不进 Warn。
			skipped++
			continue
		}
		entry, err := agent.RegisterPreRepairSplice(owner, agent.PreRepairSpliceRequest{
			BackupDir:        view.Dir,
			BackupTranscript: view.Transcript,
			Stamp:            view.Stamp,
			Plan:             plan,
			RepairAt:         repairAt,
			Source:           agent.PreRepairSpliceSourceAuto,
		})
		if err != nil {
			// 失败不阻塞 sweep（non-goal 纪律：静默记日志，下一 tick 自然
			// 重试一次——档案袋幂等闸防重试风暴）。
			slog.Warn("desktop: pre-repair autosplice register failed (will retry next tick)",
				"backup", view.Dir, "owner", owner, "err", secrets.RedactError(err))
			skipped++
			continue
		}
		registered++
		slog.Info("desktop: pre-repair fork spliced into the ledger (metadata-only, auto)",
			"backup", entry.BackupDir, "owner", owner, "kind", entry.Kind, "source", entry.Source,
			"segment", entry.SegmentEnd-entry.SegmentStart, "splicePoint", entry.SplicePoint)
	}
	return registered, skipped
}

// preRepairSpliceBusyFor 与 events sweep tick 的 busy 判定同一语义：真在
// 工作（运行中 turn/待答 prompt）或刚活跃（同一让路窗口）的本进程 tab 会
// 话让路；外部 lease 持有即 busy。判定失败保守让路。
func (a *App) preRepairSpliceBusyFor(ownerPath string, now time.Time) bool {
	if _, ctrl := a.controllerForSessionPath(ownerPath); ctrl != nil {
		activeWork, readable := false, false
		if rs, ok := ctrl.(control.RuntimeStateReader); ok {
			readable = true
			activeWork = rs.RuntimeStateSnapshot().ActiveWork()
		}
		lastActivity := ownerActivityAt(ownerPath, now)
		return eventsIdleSweepBusyFor(true, activeWork, lastActivity, now, readable)
	}
	if leaseInfo, leaseErr := agent.LoadSessionLeaseInfo(ownerPath); leaseErr == nil && leaseInfo != nil {
		return true
	}
	return false
}

// ownerActivityAt 是 busy 判定的「最近活跃」读数：转录 mtime（活动写面）。
func ownerActivityAt(ownerPath string, now time.Time) time.Time {
	if info, err := os.Stat(ownerPath); err == nil {
		return info.ModTime()
	}
	return now
}

// runPreRepairAutoSpliceTick 是 sweep tick 的拼接段：读开关→枚举→pass。
// 任何错误都折进计数与日志，绝不向上抛（失败不阻塞既有 sweep）。
func (l *eventsIdleSweepLoop) runPreRepairAutoSpliceTick(now time.Time) {
	if !prepairAutospliceLive() {
		return
	}
	views := preRepairBackupViewsIn(preRepairScanRoots())
	if len(views) == 0 {
		return
	}
	registered, skipped := runPreRepairAutoSplicePass(true, views, func(owner string) bool {
		return l.a.preRepairSpliceBusyFor(owner, now)
	}, now)
	if registered > 0 || skipped > 0 {
		slog.Info("desktop: pre-repair autosplice pass done",
			"candidates", len(views), "registered", registered, "skipped", skipped)
	}
}
