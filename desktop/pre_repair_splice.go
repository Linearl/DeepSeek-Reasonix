package main

// tl1674 阶段二（用户 20261124 拍板转正）：pre-repair 分叉的**手动合并入口**
// ——自动自愈（tl1787 续作 events_idle_sweep 拼接 pass）之外的人工路径。
//
// 与自动路径完全同源：同一 agent.CheckPreRepairSplice 判据 + 同一
// SessionSpliceLedger 档案袋（<stem>.splices.json），HEAD 语义一致=新链（主
// 线）为准——旧链以元信息登记为历史，绝不倒灌。合并是**纯元信息操作**：主
// 线转录与事件日志逐字节不动、零模型调用，登记只写档案袋（校验值本地零额
// 度重算）；写原子（tmp+rename），失败即无登记，主线无损。
//
// 面三件事：
//  1. 链预览（PreviewPreRepairSplice）：判据纯读数+分段结构（旧链公共前缀/
//     丢失段/主线续段）供用户确认——自动路径没有的交互件，本入口的核心价值；
//  2. 手动登记（MergePreRepairSplice）：以判据为权威在登记时重跑，登记带
//     manual 来源标记（与 auto 可观测区分，见 agent 层来源闸）；
//  3. 健康闸提示替代硬拦（tl1787 遗留诉求在本面的覆盖）：拼接是纯元信息操
//     作，宿主不健康不危及数据（写入面只有档案袋），故体检不过只作提示
//     （面板弹窗披露 codes + 建议先修复），不做删除闸式的硬拦——「不放松
//     健康闸」指的是删除/改写面的既有保护，本入口不新增保护也不削它。
//
// 闸面与 MergePreRepairBackup 同一纪律：备份形态校验 + knownSessionDirs 包
// 含校验 + 目标闸（孤儿备份无可回主线、宿主已归档先还原再拼）。锁面与合并
// 桥同款（runtime 变更互斥 + sessionRemovalMu，与删除/归档链不穿插）。
//
// non-goals：不改投影（转录/事件日志零触碰——本面连 catalog 对账与
// session:consolidated 事件都不发，那是内容变更面的收尾，元信息登记无内容
// 可刷）；不做自动触发（那是 1787 的 pass）；不动健康闸既有保护。

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// PreRepairSplicePreviewView 是手动拼接的链预览：判据读数 + 分段结构（链预
// 览交互件的数据面）+ 健康提示读数。Splicable=false 时 Reason 给人话原因、
// 确认弹窗只保留关闭。
type PreRepairSplicePreviewView struct {
	Splicable bool   `json:"splicable"`
	Reason    string `json:"reason,omitempty"`
	// Kind/分段边界与判据同口径（trim 后的消息索引）。
	Kind            string `json:"kind,omitempty"`
	SharedPrefix    int    `json:"sharedPrefix,omitempty"`
	SplicePoint     int    `json:"splicePoint,omitempty"`
	SegmentStart    int    `json:"segmentStart,omitempty"`
	SegmentEnd      int    `json:"segmentEnd"`
	SegmentMessages int    `json:"segmentMessages"`
	// 两侧（trim 后）总条数——分段结构的对屏读数（主线 N 条 / 备份 M 条）。
	OwnerMessages  int `json:"ownerMessages"`
	BackupMessages int `json:"backupMessages"`
	// RepairAt 是改写记录锚点（unix 秒；0=无 repairlog 命中）——预览披露改
	// 写依据（时间线一致才可拼，见闸）。
	RepairAt int64 `json:"repairAt,omitempty"`
	// Degraded 是判据的降级读侧标记（tl1674c 折叠态降级）：空=两侧严格快照；
	// 非空=有侧跑在 load 时修复后的视图上（"owner"/"backup"/"owner backup"）
	// ——降级放行不是无声的，预览与回执都如实披露。
	Degraded string `json:"degraded,omitempty"`
	// OwnerHealthy/HealthCodes 是健康闸**提示**读数（不硬拦）：体检不过时
	// 弹窗披露 codes 并建议先修复，拼接本身照常可用（纯元信息操作）。
	OwnerHealthy bool     `json:"ownerHealthy"`
	HealthCodes  []string `json:"healthCodes,omitempty"`
	// AlreadyRegistered 报告该备份是否已在档案袋（幂等：已登记行入口隐藏，
	// 这里是预览层的重复防线）。
	AlreadyRegistered bool   `json:"alreadyRegistered,omitempty"`
	OwnerPath         string `json:"ownerPath"`
	BackupDir         string `json:"backupDir"`
}

// PreRepairSpliceReceiptView 是手动拼接登记回执：Registered=false 时 Reason
// 给人话原因（covered 等合法无事形态），其余为登记成功的对屏读数。
type PreRepairSpliceReceiptView struct {
	Registered      bool   `json:"registered"`
	Reason          string `json:"reason,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Source          string `json:"source,omitempty"`
	SplicePoint     int    `json:"splicePoint,omitempty"`
	SegmentMessages int    `json:"segmentMessages,omitempty"`
	OwnerMessages   int    `json:"ownerMessages,omitempty"`
	// Degraded 与判据同口径（tl1674c）：登记若有降级读侧，回执如实带同值。
	Degraded  string `json:"degraded,omitempty"`
	OwnerPath string `json:"ownerPath"`
	BackupDir string `json:"backupDir"`
}

// preRepairSpliceTarget 是手动拼接两桥共用的目标闸：备份形态校验 + 已知会
// 话目录包含校验（与删除/合并闸同一纪律）+ 备份内转录存在 + 目标闸（孤儿
// 无可回主线、宿主已归档先还原）。返回活宿主路径与备份内转录路径。
func (a *App) preRepairSpliceTarget(dir string) (ownerPath, transcript, stem, stamp string, err error) {
	backupDir := filepath.Clean(strings.TrimSpace(dir))
	if backupDir == "." || filepath.VolumeName(backupDir) == backupDir {
		return "", "", "", "", fmt.Errorf("no backup path given")
	}
	base := filepath.Base(backupDir)
	stem, stamp, ok := preRepairStemOf(base)
	if !ok {
		return "", "", "", "", fmt.Errorf("not a pre-repair backup directory: %s", base)
	}
	parent := filepath.Dir(backupDir)
	if !a.pathInsideKnownSessionDirs(parent, backupDir) {
		slog.Warn("desktop: pre-repair splice refused", "reason", "outside known session dirs", "path", backupDir)
		return "", "", "", "", fmt.Errorf("backup path outside known session directories")
	}
	// 备份内主线转录副本（枚举面同一来源：<stem>.jsonl）。
	candidate := filepath.Join(backupDir, stem+store.SessionTranscriptSuffix)
	if info, serr := os.Stat(candidate); serr != nil || info.IsDir() {
		return "", "", "", "", fmt.Errorf("this backup has no main transcript inside to splice: %s", base)
	}
	// 目标闸：只拼活宿主（与自动 pass 的候选闸同一语义：宿主不在=孤儿备份
	// 无可回主线；宿主躺进 .trash 桶=已归档，先还原对话再拼）。
	owner := filepath.Join(parent, stem+store.SessionTranscriptSuffix)
	if oinfo, oerr := os.Lstat(owner); oerr != nil || oinfo.IsDir() {
		return "", "", "", "", fmt.Errorf("the original conversation no longer exists — an orphan backup has no main line to splice into")
	}
	if preRepairOwnerArchived(owner) {
		return "", "", "", "", fmt.Errorf("the original conversation is archived (in the recoverable trash) — restore it from the trash first, then splice")
	}
	// 活宿主路径归一（会话目录映射优先，合并桥同一处理）。
	ownerPath = owner
	if _, sessionPath, serr := a.sessionDirForPath(owner); serr == nil {
		ownerPath = sessionPath
	}
	return ownerPath, candidate, stem, stamp, nil
}

// preRepairSpliceAnchor 是改写记录锚点读数（与自动 pass 同一来源）：
// repairlog 的 BackupDir 命中=这条备份就是那次改写前的状态（lineage 强锚
// 点）；返回首个命中的 unix 秒（0=无命中）。时间线校验（备份 stamp 是否晚
// 于锚点）留在调用方——两桥的失败话术不同。
func preRepairSpliceAnchor(ownerPath, backupDir string) int64 {
	records, err := agent.ListSessionRepairRecordsFor(ownerPath)
	if err != nil {
		return 0
	}
	for _, rec := range records {
		if rec.BackupDir == "" || rec.BackupDir != backupDir {
			continue
		}
		if at, perr := time.Parse(time.RFC3339, rec.At); perr == nil {
			return at.Unix()
		}
		break
	}
	return 0
}

// preRepairSpliceAnchorTimeline 报告备份 stamp 是否晚于改写锚点（时间线不
// 一致=true）：stamp 解析失败=无法证伪，放行（保守放行与自动 pass 的读数
// 缺失处理同一方向）。
func preRepairSpliceAnchorTimeline(stamp string, repairAt int64) bool {
	if repairAt <= 0 {
		return false
	}
	stampTime, perr := time.Parse("20060102T150405", stamp)
	if perr != nil {
		return false
	}
	return stampTime.After(time.Unix(repairAt, 0))
}

// PreviewPreRepairSplice 给出手动拼接的链预览（纯读数，零写入零模型）：
// 判据分段结构 + 健康提示读数 + 幂等读数，供面板确认弹窗渲染。
func (a *App) PreviewPreRepairSplice(dir string) (PreRepairSplicePreviewView, error) {
	ownerPath, transcript, _, stamp, err := a.preRepairSpliceTarget(dir)
	if err != nil {
		return PreRepairSplicePreviewView{}, err
	}
	backupDir := filepath.Clean(strings.TrimSpace(dir))
	view := PreRepairSplicePreviewView{
		OwnerHealthy: true,
		OwnerPath:    ownerPath,
		BackupDir:    backupDir,
	}
	view.AlreadyRegistered = agent.HasPreRepairSplice(ownerPath, backupDir)
	// 健康提示读数（提示替代硬拦）：体检不过只带 codes 给弹窗披露。
	if report := preRepairHealthInspect(ownerPath, agent.HealthInspectOptions{}); !report.Healthy {
		view.OwnerHealthy = false
		for _, issue := range report.Issues {
			view.HealthCodes = append(view.HealthCodes, issue.Code)
		}
	}
	// 改写锚点 + 时间线：备份晚于它自己的改写记录=时间线不一致，拒绝登记
	// （自动 pass 同一判死；预览层如实给 splicable=false + 原因）。
	repairAt := preRepairSpliceAnchor(ownerPath, backupDir)
	view.RepairAt = repairAt
	if preRepairSpliceAnchorTimeline(stamp, repairAt) {
		view.Reason = "this backup is newer than its own repair record — the timeline does not support a splice"
		return view, nil
	}
	plan, err := agent.CheckPreRepairSplice(ownerPath, transcript, repairAt)
	if err != nil {
		// 判据 fail-closed（快照读不了）：预览如实报不可拼（人话原样透传）。
		view.Reason = plan.Reason
		if view.Reason == "" {
			view.Reason = "content snapshot unavailable; the splice verdict needs both sides readable"
		}
		return view, nil
	}
	view.Splicable = agent.PreRepairSplicePlanSplicable(plan)
	view.Reason = plan.Reason
	view.Kind = string(plan.Kind)
	view.SharedPrefix = plan.SharedPrefix
	view.SplicePoint = plan.SplicePoint
	view.SegmentStart = plan.SegmentStart
	view.SegmentEnd = plan.SegmentEnd
	view.SegmentMessages = plan.SegmentMessages
	view.OwnerMessages = plan.OwnerMessages
	view.BackupMessages = plan.BackupMessages
	view.Degraded = plan.Degraded
	return view, nil
}

// MergePreRepairSplice 执行手动拼接登记（纯元信息）：目标闸→判据重跑（登
// 记时权威）→ 档案袋登记（source=manual）。主线转录与事件日志逐字节不动。
func (a *App) MergePreRepairSplice(dir string) (PreRepairSpliceReceiptView, error) {
	receipt := PreRepairSpliceReceiptView{}
	ownerPath, transcript, _, stamp, err := a.preRepairSpliceTarget(dir)
	if err != nil {
		return receipt, err
	}
	backupDir := filepath.Clean(strings.TrimSpace(dir))
	receipt.OwnerPath = ownerPath
	receipt.BackupDir = backupDir
	slog.Info("desktop: pre-repair manual splice begin", "backup", backupDir, "owner", ownerPath)
	// 锁面与合并桥同款（与删除/归档链不穿插）；判据与登记都在锁内——登记
	// 读数（边界指纹/规模）反映同一时刻的主线状态。
	registerErr := func() error {
		defer a.lockRuntimeMutation("pre-repair-splice")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()

		// 幂等闸（登记时权威；面板对已登记行隐藏入口，这里是竞态防线）。
		if agent.HasPreRepairSplice(ownerPath, backupDir) {
			return fmt.Errorf("this backup is already registered in the splice ledger (idempotent)")
		}
		repairAt := preRepairSpliceAnchor(ownerPath, backupDir)
		if preRepairSpliceAnchorTimeline(stamp, repairAt) {
			return fmt.Errorf("this backup is newer than its own repair record — the timeline does not support a splice")
		}
		plan, err := agent.CheckPreRepairSplice(ownerPath, transcript, repairAt)
		if err != nil {
			return fmt.Errorf("%s", plan.Reason)
		}
		if !agent.PreRepairSplicePlanSplicable(plan) {
			// covered / 无锚点等合法无事形态：回执如实带原因（非错误）。
			receipt.Reason = plan.Reason
			return nil
		}
		entry, err := agent.RegisterPreRepairSplice(ownerPath, agent.PreRepairSpliceRequest{
			BackupDir:        backupDir,
			BackupTranscript: transcript,
			Stamp:            stamp,
			Plan:             plan,
			RepairAt:         repairAt,
			Source:           agent.PreRepairSpliceSourceManual,
		})
		if err != nil {
			return err
		}
		receipt.Registered = true
		receipt.Reason = ""
		receipt.Kind = entry.Kind
		receipt.Source = entry.Source
		receipt.SplicePoint = entry.SplicePoint
		receipt.SegmentMessages = entry.SegmentEnd - entry.SegmentStart
		receipt.OwnerMessages = entry.OwnerMessages
		receipt.Degraded = entry.Degraded
		return nil
	}()
	if registerErr != nil {
		return receipt, friendlySessionFileError(registerErr)
	}
	if receipt.Registered {
		slog.Info("desktop: pre-repair fork manually spliced into the ledger (metadata-only, manual)",
			"backup", receipt.BackupDir, "owner", ownerPath, "kind", receipt.Kind,
			"source", receipt.Source, "segment", receipt.SegmentMessages,
			"splicePoint", receipt.SplicePoint)
	} else {
		slog.Info("desktop: pre-repair manual splice nothing to register", "backup", receipt.BackupDir, "reason", receipt.Reason)
	}
	return receipt, nil
}
