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

// PreRepairBlockReasonSoleCopy 是 E5 判据的拒绝令牌（wire 契约：前端按令牌
// 出三语文案，不翻译后缀细节）：宿主已不在、回收站里也没有它的家族成员
// （转录桶/随行备份桶都没有）——这份备份是该对话仅存的完整副本，删它就是
// 真丢内容（tl1116「无备份不减肥」的禁区）。
const PreRepairBlockReasonSoleCopy = "sole-copy"

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
	// Deletable 是 tl1603 E5 判据的**列表期初判**（纯 stat 级，零重放）：
	// 宿主在（活会话的改写前快照，内容另有正本）或宿主已走删除/归档链（内容
	// 在回收站桶里）= 非唯一退路，true；宿主不在且回收站无家族 = 仅存副本，
	// false。健康闸成本高（重放宿主日志），只在删除时执行（pre_repair_cleanup.go）
	// ——deletable=true 的行仍可能被删除桥以健康理由拒掉，那是诚实的拒删提示。
	Deletable bool `json:"deletable"`
	// BlockReason 是 deletable=false 时的稳定令牌（PreRepairBlockReasonSoleCopy）。
	BlockReason string `json:"blockReason,omitempty"`
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
	// tl1603: E5 列表期初判的回收站家族缓存（每个会话目录的 .trash 只走一遍，
	// 同目录多条孤儿备份共享读数）。nil 条目=该目录尚无回收站读数。
	trashFamilies := map[string]map[string]bool{}
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
			// tl1603 E5 初判（纯 stat，零重放）：宿主在=非唯一退路；宿主不在
			// 时看回收站里还有没有 stem 家族成员（删除/归档链 tl1442-ext 会把
			// 转录与随行备份一起搬进桶——桶里有=内容另有副本）。都没有=仅存
			// 副本，初判拒绝，删除桥以同一判据权威复验。
			if view.OwnerPath != "" {
				view.Deletable = true
			} else if preRepairTrashFamilyHit(parent, stem, trashFamilies) {
				view.Deletable = true
			} else {
				view.BlockReason = PreRepairBlockReasonSoleCopy
			}
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

// preRepairTrashFamilyHit 报告会话目录 dir 的回收站（<dir>/.trash/<桶>/）里
// 是否仍保有 stem 家族的成员——宿主转录本体（<stem>.jsonl 文件）或任一随行
// 的改写前备份目录（<stem>.pre-repair-*）。删除/归档链（tl1442-ext 全集清单）
// 把这两类成员随桶搬走，所以「桶里有」=宿主走的是显式删除/归档链、内容另有
// 副本=备份非唯一退路（E5 判据的后半）。桶名形态不做业务判断（主链桶=
// key、防撞桶带 -recovery-<stamp> 尾号、终端归档桶唯一名——各链各有命名），
// 只读桶内一层成员名，命名无关、覆盖全部链。
//
// mem 是同一会话目录的跨条目缓存（枚举一个目录几十条孤儿备份时 .trash 只走
// 一遍）；nil 条目=该目录尚无读数，命中表 lazy 填充。
func preRepairTrashFamilyHit(dir, stem string, mem map[string]map[string]bool) bool {
	hits, cached := mem[dir]
	if cached {
		return hits[stem]
	}
	hits = map[string]bool{}
	mem[dir] = hits
	buckets, err := os.ReadDir(filepath.Join(dir, ".trash"))
	if err != nil {
		return false
	}
	for _, bucket := range buckets {
		if !bucket.IsDir() {
			continue
		}
		members, err := os.ReadDir(filepath.Join(dir, ".trash", bucket.Name()))
		if err != nil {
			continue
		}
		for _, member := range members {
			name := member.Name()
			switch {
			case !member.IsDir() && strings.TrimSuffix(name, store.SessionTranscriptSuffix) != name:
				// 一个转录名文件（<stem>.jsonl）——它的 stem 入表。
				hits[strings.TrimSuffix(name, store.SessionTranscriptSuffix)] = true
			case member.IsDir() && strings.Contains(name, store.SessionPreRepairBackupSuffix):
				// 一个改写前备份目录——剥掉备份后缀即宿主 stem。
				if idx := strings.LastIndex(name, store.SessionPreRepairBackupSuffix); idx > 0 {
					hits[name[:idx]] = true
				}
			}
		}
	}
	return hits[stem]
}

// ListPreRepairBackups 列出全库的改写前备份目录，供版本与副本面板的
// 「改写前备份」分区展示。只读，绝不改动任何备份字节。
func (a *App) ListPreRepairBackups() ([]PreRepairBackupView, error) {
	return preRepairBackupViewsIn(preRepairScanRoots()), nil
}
