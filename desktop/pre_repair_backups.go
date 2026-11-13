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

	"reasonix/internal/config"
	"reasonix/internal/store"
)

// PreRepairBackupView 是一个改写前备份目录的读数（只读，纯目录 stat 级，
// 与 ListRecoveryCopyGroups 同一成本哲学：绝不打开转录）。
type PreRepairBackupView struct {
	// Dir 是备份目录完整路径（<dir>/<stem>.pre-repair-<stamp>）。
	Dir string `json:"dir"`
	// Transcript 是目录内的主线转录副本（<stem>.jsonl）；备份失败/被清空时为空。
	Transcript string `json:"transcript,omitempty"`
	// OwnerPath 是旁侧现行会话转录（备份的宿主）；宿主已删除时为空——这种
	// 备份是宿主仅存的完整副本，面板照常列出。
	OwnerPath string `json:"ownerPath,omitempty"`
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
			// 宿主现行会话 = 旁侧 <stem>.jsonl。
			owner := filepath.Join(parent, stem+store.SessionTranscriptSuffix)
			if info, err := os.Stat(owner); err == nil && !info.IsDir() {
				view.OwnerPath = owner
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
			out = append(out, view)
			// 整棵备份树是一个单元：不再下探。
			return filepath.SkipDir
		})
	}
	// 新的在前（修复现场通常只关心最近一次）。主键=目录名里的 stamp（一次
	// 修复一个目录，语义时间，不受目录 mtime 秒级粒度限制）；解析不了的排
	// 最后，同 stamp 按路径稳定排序。
	stampTime := func(view PreRepairBackupView) time.Time {
		parsed, err := time.Parse("20060102T150405", view.Stamp)
		if err != nil {
			return time.Time{}
		}
		return parsed
	}
	sort.Slice(out, func(i, j int) bool {
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
