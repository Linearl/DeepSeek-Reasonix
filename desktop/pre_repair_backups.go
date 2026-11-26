package main

// tl1572: 自愈改写前备份（*.pre-repair-*）的可见面。
//
// 一次修复/瘦身（Diet）把会话的全部数据侧车整体备份到会话文件旁的
// <stem>.pre-repair-<UTC 紧凑时间> 目录（internal/agent.BackupSessionFiles），
// 之后 Migration/DAG 扫描等一切会话枚举都刻意排除这些树（dagMigration.go
// Backups 计数的注释）。排除是对的——备份树永不作为会话加载——但排除的
// 一并副作用是版本与副本面板看不见它们：Diet 通知承诺「可在面板中查看」，
// 面板却没有任何入口（用户实测 17 个 pre-repair 目录全不可见 = 文案假承诺）。
//
// 本文件只做枚举（只读目录读数，零重放零写入）；「查看」复用既有的
// PreviewRecoveryChain（前端以备份内转录自身为主线参数调用，其成员校验
// 对 main==chain 直通），adopt/切回不在本面承诺——备份树不在 catalog
// lineage 里，SetActiveSessionVersion 会按既有校验拒绝，通知文案同步收口
// 为「查看」。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// PreRepairBackupView 是一个改写前备份目录的读数（只读，纯目录 stat 级，
// 与 ListRecoveryCopyGroups 同一成本哲学：绝不打开转录）。
//
// tl1646（2026-11-16 用户裁决）：tl1603 的 Deletable/BlockReason 列表初判
// 字段退役——「宿主不在=仅存副本=禁删」的 E5 闸①被推翻，孤儿副本允许删除
// （前端确认弹窗警示不可恢复），列表不再给行标不可删态；删除时仍保留健康
// 闸（pre_repair_cleanup.go，列表期无法便宜判定）。
type PreRepairBackupView struct {
	// Dir 是备份目录完整路径（<dir>/<stem>.pre-repair-<stamp>）。
	Dir string `json:"dir"`
	// Transcript 是目录内的主线转录副本（<stem>.jsonl）；备份失败/被清空时为空。
	Transcript string `json:"transcript,omitempty"`
	// OwnerPath 是旁侧现行会话转录（备份的宿主）；宿主已删除时为空——孤儿
	// 备份，面板照常列出（行内标注「原对话已不存在」），删除入口可用
	// （tl1646：确认弹窗明示唯一副本不可恢复后放行）。
	OwnerPath string `json:"ownerPath,omitempty"`
	// OwnerArchived（tl1787）表示宿主转录躺在 .trash 回收站桶里（归档链把
	// 宿主与备份一起搬进桶的形态）：ownerPath 非空但那是归档工件而非活会话。
	// 行内标注「原对话已归档」，删除放行（健康闸跳过归档态，活宿主保护不变）。
	OwnerArchived bool `json:"ownerArchived,omitempty"`
	// Spliced（tl1787 续作）表示该备份已登记进宿主的拼接档案袋
	// （<stem>.splices.json，后台自愈或手动合并的元信息拼接记录）——行内
	// 标注「已登记拼接」，长链可经档案袋查询重建。
	Spliced bool `json:"spliced,omitempty"`
	// SplicedBy（tl1674b 阶段二）是登记来源：auto=后台自愈 / manual=手动合
	// 并入口——面板行内标注按它区分两种触发（历史档案袋无来源字段时为空，
	// 徽标维持素形态）。
	SplicedBy string `json:"splicedBy,omitempty"`
	// SplicedDegraded（tl117A/包117-A）是该条登记的降级读侧标记（tl1674c
	// 口径："owner"/"backup"/"owner backup"，空=两侧严格快照）——行内徽标
	// 据它如实披露「已登记但投影未生成」（降级登记=纯元信息操作、不重生成
	// 任何投影），并明示降级只可登记不可重复合并。
	SplicedDegraded string `json:"splicedDegraded,omitempty"`
	// Label 是展示名：备份内 meta sidecar 的会话标题优先，回退 stem。
	Label string `json:"label"`
	// Stamp 是目录名携带的 UTC 紧凑时间戳（20060102T150405）。
	Stamp string `json:"stamp"`
	// Bytes 是目录内全部常规文件的字节和。
	Bytes int64 `json:"bytes"`
	// Files 是目录内常规文件数。
	Files int `json:"files"`
	// ModifiedAt 是目录 mtime（RFC3339）。
	ModifiedAt string `json:"modifiedAt"`
}

// preRepairStemOf 拆出 "<stem>.pre-repair-<stamp>" 目录名的 stem 与 stamp。
// 不是备份目录形态时 ok=false。
func preRepairStemOf(name string) (stem, stamp string, ok bool) {
	idx := strings.LastIndex(name, store.SessionPreRepairBackupSuffix)
	if idx <= 0 {
		return "", "", false
	}
	return name[:idx], name[idx+len(store.SessionPreRepairBackupSuffix):], true
}

// preRepairBackupViewsIn 枚举各根下全部 pre-repair 备份目录（递归，备份树
// 自身不再下探——整个树就是一个备份单元）。单个根读不了就跳过：面板的
// 其它分区照常渲染（ListRecoveryCopyGroups 同一原则）。
func preRepairBackupViewsIn(roots []string) []PreRepairBackupView {
	seen := map[string]struct{}{}
	out := []PreRepairBackupView{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if _, statErr := os.Stat(root); statErr != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d == nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			stem, stamp, ok := preRepairStemOf(d.Name())
			if !ok {
				return nil
			}
			// 备份目录按完整路径去重（多根可能重叠）。
			if _, dup := seen[path]; dup {
				return filepath.SkipDir
			}
			seen[path] = struct{}{}

			view := PreRepairBackupView{
				Dir:   path,
				Stamp: stamp,
			}
			parent := filepath.Dir(path)
			// 备份内主线转录副本 = <stem>.jsonl（备份白名单的核心内容）。
			candidate := filepath.Join(path, stem+store.SessionTranscriptSuffix)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				view.Transcript = candidate
			}
			// 宿主现行会话 = 旁侧 <stem>.jsonl。tl1787：宿主在 .trash 桶里 =
			// 已归档态（归档链把宿主与备份一起搬进桶），行内单独标注——
			// 前端确认弹窗对归档行给「随对话归档」披露而非孤儿警示。
			owner := filepath.Join(parent, stem+store.SessionTranscriptSuffix)
			if info, err := os.Stat(owner); err == nil && !info.IsDir() {
				view.OwnerPath = owner
				view.OwnerArchived = preRepairOwnerArchived(owner)
			}
			// tl1787 续作：拼接档案袋登记读数（后台自愈/手动合并的元信息
			// 拼接是否已覆盖这条备份）；tl1674b：登记来源一并读出（面板
			// 行内标注按它区分自动/手动触发；多条登记理论不存在——幂等
			// 闸同一备份只记一次，取首条来源即可）。
			//
			// tl117A（包117-A 读取链修复）：这段读数原在 os.Stat(owner) 成功
			// 分支内——宿主文件不在（孤儿/被移动）时 Spliced 永远不读，已登记
			// 状态在行上凭空消失（无徽标）。档案袋 <stem>.splices.json 与备份
			// 同目录、路径只由 parent+stem 推导，不依赖宿主文件在场，故移出
			// 条件：有无宿主都读，孤儿行同样显示「已登记拼接」。
			view.Spliced = agent.HasPreRepairSplice(owner, path)
			if view.Spliced {
				if entries, lerr := agent.ReadPreRepairSpliceLedger(owner); lerr == nil {
					for _, entry := range entries {
						if entry.BackupDir == path {
							view.SplicedBy = entry.Source
							view.SplicedDegraded = entry.Degraded
							break
						}
					}
				}
			}
			// 展示名：优先备份内 meta 的会话标题（备份时刻的标题），回退 stem。
			// meta 在备份内读（备份自含 sidecar）；宿主 meta 属现行状态，不读。
			labelSource := view.Transcript
			if labelSource == "" {
				labelSource = owner
			}
			view.Label = recoveryDisplayName(labelSource)
			if view.Label == "" {
				view.Label = stem
			}
			// 体量与文件数：纯 stat 遍历本目录一层（备份目录是平的）。
			entries, err := os.ReadDir(path)
			if err == nil {
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					info, err := e.Info()
					if err != nil || !info.Mode().IsRegular() {
						continue
					}
					view.Files++
					view.Bytes += info.Size()
				}
			}
			if info, err := d.Info(); err == nil {
				view.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
			}
			// tl1646：列表不再携带可删初判——孤儿副本（宿主不在）与活宿主备
			// 份同形态列出，删除入口一律可用；唯一副本的警示在前端确认弹窗
			// （settings.recoveryCopiesPreRepairSoleWarn），健康闸在删除桥。
			out = append(out, view)
			// 整棵备份树是一个单元：不再下探。
			return filepath.SkipDir
		})
	}
	// tl1603-r2④: 按体量从大到小（用户裁决的排序键）——最占空间的排最前，
	// 清理收益一目了然。并列体量内保留 tl1572 的「新的在前」（stamp 降序）
	// 作次键；解析不了的排最后，同 stamp 按路径稳定排序。
	stampTime := func(view PreRepairBackupView) time.Time {
		parsed, err := time.Parse("20060102T150405", view.Stamp)
		if err != nil {
			return time.Time{}
		}
		return parsed
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		ti, tj := stampTime(out[i]), stampTime(out[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		if ti.IsZero() != tj.IsZero() {
			// 一个可解析一个不可解析：可解析的在前。
			return tj.IsZero()
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// preRepairScanRoots 是备份可能出现的全部根：全局会话目录 + 各项目会话树
// （会话文件在哪，备份就在哪——backupSessionFiles 落在会话文件旁）。
func preRepairScanRoots() []string {
	return []string{
		config.SessionDir(),
		filepath.Join(config.MemoryUserDir(), "projects"),
	}
}

// ListPreRepairBackups 列出全库的改写前备份目录，供版本与副本面板的
// 「改写前备份」分区展示。只读，绝不改动任何备份字节。
func (a *App) ListPreRepairBackups() ([]PreRepairBackupView, error) {
	return preRepairBackupViewsIn(preRepairScanRoots()), nil
}
