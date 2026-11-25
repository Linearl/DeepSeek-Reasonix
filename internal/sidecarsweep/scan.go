package sidecarsweep

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/pathidentity"
	"reasonix/internal/store"
)

// metaLockHashFor 计算会话路径的 meta 保存锁哈希——键形与 agent.save_locks.go
// sessionMetaLockPath 严格一致：sha256(pathidentity.Canonical(会话路径)) 前 8
// 字节的 hex。canonical 化用 pathidentity.Canonical 单一来源（含 Windows 小写
// 折叠与符号链接物理解析），不另造第二份规范化。
func metaLockHashFor(sessionPath string) (string, error) {
	canonical := pathidentity.Canonical(sessionPath)
	if canonical == "" {
		return "", fmt.Errorf("empty canonical path")
	}
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:8]), nil
}

// Row 是清单的一行：一个被判定为孤儿（或需复核/defer）的目录条目。
type Row struct {
	// Name 是条目在会话目录里的名字（basename）。
	Name string `json:"name"`
	// Kind 是形态种类（branch-meta / event-log / guardian-family …）。
	Kind string `json:"kind"`
	// Class 是处置面：data / runtime / mechanism / unknown。
	Class string `json:"class"`
	// Size 是条目字节数（目录为其一层平铺和，与 desktop 枚举同口径）。
	Size int64 `json:"size"`
	// ModTime 是条目最后修改时间（RFC3339）。
	ModTime string `json:"mtime"`
	// Owner 是期望配对的 owner 转录名（孤儿判据的一部分）。
	Owner string `json:"owner,omitempty"`
	// MasterStem 是所属会话 stem。
	MasterStem string `json:"master_stem"`
	// Evidence 是孤儿判据的逐条注明（构建自路径事实，不解读内容）。
	Evidence string `json:"evidence"`
	// MasterInTrash 是「master 躺在 .trash 回收站桶」的证据（桶名；空=未见）。
	MasterInTrash string `json:"master_in_trash,omitempty"`
	// Action 是建议处置：recycle（--confirm 默认搬）/ review-only（人工复核）/
	// defer（机制持有，不搬）/ runtime（--include-runtime 才搬）。
	Action string `json:"action"`
	// IsDir 标记目录工件。
	IsDir bool `json:"is_dir,omitempty"`
}

// DirReport 是一个会话目录的扫描结果。
type DirReport struct {
	// Dir 是会话目录绝对路径。
	Dir string `json:"dir"`
	// Masters 是活主转录数（plain + zst 档案）。
	Masters int `json:"masters"`
	// EmptyMasters 是 0 字节主转录数（空壳信息项，tl1793 同族形态；仅提示，
	// 不入任何搬运面——空壳的处置权在桌面端 UI/用户）。
	EmptyMasters int `json:"empty_masters,omitempty"`
	// Orphans 是孤儿行（含 runtime 处置面；按 class/action 过滤搬运面）。
	Orphans []Row `json:"orphans"`
	// Unknown 是形态表外条目（永远 review-only，--confirm 不碰）。
	Unknown []Row `json:"unknown,omitempty"`
	// Deferred 是让位给桌面端补收机制的残留（cleanup-pending 标记所在 stem）。
	Deferred []Row `json:"deferred,omitempty"`
	// TitlesDangling 是 .titles.json 里指向不存在会话的键（悬空指针形态）。
	TitlesDangling []string `json:"titles_dangling,omitempty"`
	// TrashLoose 是 .trash 顶层的散文件（桶语义外形态；仅提示，desktop 领地）。
	TrashLoose []string `json:"trash_loose,omitempty"`
	// Errors 是本目录扫描过程中的读错误（逐条可见，不静默吞）。
	Errors []string `json:"errors,omitempty"`
}

// Report 是一次扫描的整体回执。
type Report struct {
	// GeneratedAt 是扫描时间（RFC3339）。
	GeneratedAt string `json:"generated_at"`
	// Mode 是 scan（缺省只读）或 confirm（已执行回收搬运）。
	Mode string `json:"mode"`
	// Dirs 是逐目录结果（按目录名排序）。
	Dirs []DirReport `json:"dirs"`
	// Totals 是全局汇总。
	Totals Totals `json:"totals"`
}

// Totals 汇总各处置面的行数与字节数。
type Totals struct {
	Dirs           int   `json:"dirs"`
	Masters        int   `json:"masters"`
	EmptyMasters   int   `json:"empty_masters"`
	DataOrphans    int   `json:"data_orphans"`
	DataBytes      int64 `json:"data_bytes"`
	RuntimeOrphans int   `json:"runtime_orphans"`
	Unknown        int   `json:"unknown"`
	Deferred       int   `json:"deferred"`
	TitlesDangling int   `json:"titles_dangling"`
}

// ScanRoots 扫描一组会话目录（缺省只读，零写盘）。dirs 为空时返回空报告。
func ScanRoots(dirs []string) (*Report, error) {
	rep := &Report{GeneratedAt: time.Now().Format(time.RFC3339), Mode: "scan"}
	roots := append([]string(nil), dirs...)
	sort.Strings(roots)
	for _, dir := range roots {
		dr, err := ScanDir(dir)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", dir, err)
		}
		rep.Dirs = append(rep.Dirs, *dr)
	}
	recomputeTotals(rep)
	return rep, nil
}

// ScanDir 只读扫描一个会话目录，产出孤儿清单。全程只做 ReadDir/Stat 读操作，
// 不写任何盘面（零写盘钉：ScanDir* 系列测试对目录树做前后快照比对）。
//
// 判定主轴：master liveness 唯一判据 = <stem>.jsonl 或 <stem>.jsonl.zst 存在。
// 每个非结构条目按形态表归位后，master 不在 → 孤儿；master 在 → 活家族（含
// 未知新形态——stem 前缀 catch-all 与删除链同款纪律，活主的新 sidecar 形态
// 天然配对，不误报）；归属解析不出 → 不明（review-only）。
// cleanup-pending 标记所在 stem 整族 defer（桌面端补收机制持有）。
func ScanDir(dir string) (*DirReport, error) {
	dr := &DirReport{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// 第一遍：master 存在性表（plain 与 zst 档案同权）+ cleanup-pending stem
	// 集 + .trash 桶名表 + 空壳计数 + meta 保存锁哈希配对表。
	masterAlive := map[string]bool{}  // stem -> plain/zst 任一存在
	masterPlain := map[string]bool{}  // stem -> plain .jsonl 存在（titles 键判据）
	pendingStems := map[string]bool{} // cleanup-pending 标记所在 stem
	trashBuckets := []string{}        // .trash 顶层条目名（桶目录为主）
	// metaLockByHash：活主转录的 meta 保存锁哈希 → stem。键形与 save_locks.go
	// sessionMetaLockPath 一致：sha256(pathidentity.Canonical(会话路径))[:8字节]
	// 的 hex——键形单一来源复用，不另造规范化。
	metaLockByHash := map[string]string{}
	type entryInfo struct {
		name    string
		isDir   bool
		size    int64
		modTime time.Time
	}
	byName := map[string]entryInfo{}
	for _, e := range entries {
		name := e.Name()
		info, err := e.Info()
		if err != nil {
			dr.Errors = append(dr.Errors, fmt.Sprintf("stat %s: %v", name, err))
			continue
		}
		size := int64(0)
		if !info.IsDir() && info.Mode().IsRegular() {
			size = info.Size()
		} else if info.IsDir() {
			size = flatDirBytes(filepath.Join(dir, name))
		}
		byName[name] = entryInfo{name: name, isDir: info.IsDir(), size: size, modTime: info.ModTime()}

		c := classifyName(name)
		if c.class == classMaster {
			dr.Masters++
			stem := c.masterStem
			masterAlive[stem] = true
			if c.kind == "transcript" {
				masterPlain[stem] = true
				if size == 0 {
					dr.EmptyMasters++
				}
			}
			if hash, err := metaLockHashFor(filepath.Join(dir, stem+store.SessionTranscriptSuffix)); err == nil {
				metaLockByHash[hash] = stem
			}
			continue
		}
		if c.class == classMechanism {
			if c.masterStem != "" {
				pendingStems[c.masterStem] = true
			}
			continue
		}
		if name == ".trash" && info.IsDir() {
			sub, err := os.ReadDir(filepath.Join(dir, name))
			if err != nil {
				dr.Errors = append(dr.Errors, fmt.Sprintf("read .trash: %v", err))
				continue
			}
			for _, t := range sub {
				trashBuckets = append(trashBuckets, t.Name())
				if !t.IsDir() {
					dr.TrashLoose = append(dr.TrashLoose, t.Name())
				}
			}
		}
	}

	// 第二遍：逐条目归位。
	for _, e := range entries {
		name := e.Name()
		ei, ok := byName[name]
		if !ok {
			continue // stat 失败已记录
		}
		c := classifyName(name)
		switch c.class {
		case classMaster, classStructural:
			continue
		case classMechanism:
			// 标记本体不入清单（机制状态，桌面端补收持有）。
			continue
		case classUnknown:
			dr.Unknown = append(dr.Unknown, Row{
				Name: name, Kind: c.kind, Class: "unknown",
				Size: ei.size, ModTime: ei.modTime.Format(time.RFC3339),
				Evidence: "形态表外条目：不解读内容、不自动处置（--confirm 永不碰）；人工判读后可另行走桌面端删除面",
				Action:   "review-only", IsDir: ei.isDir,
			})
			continue
		}
		// 有归属形态：孤儿判据 = master 不在活目录。
		row := Row{
			Name: name, Kind: c.kind, Size: ei.size,
			ModTime: ei.modTime.Format(time.RFC3339),
			Owner:   c.owner, MasterStem: c.masterStem, IsDir: ei.isDir,
		}
		// meta 保存锁：按活主哈希配对（needsHashPair）。配对上=活家族；
		// 配不上=陈旧锁（会话已不在），runtime 处置面默认不搬。
		if c.needsHashPair {
			if stem, hit := metaLockByHash[c.hashKey]; hit {
				_ = stem
				continue
			}
			row.Class = "runtime"
			row.Evidence = fmt.Sprintf("meta 跨进程保存锁（哈希 %s）；判据：本目录活主转录的锁哈希均不匹配——持锁会话已不在活目录，属陈旧句柄；运行时句柄默认不搬（--include-runtime 才搬）",
				c.hashKey)
			row.Action = "runtime"
			dr.Orphans = append(dr.Orphans, row)
			continue
		}
		if masterAlive[c.masterStem] {
			continue // 活家族成员（含未知新形态：stem 前缀天然配对）
		}
		if c.masterStem == "" {
			// 无归属键的运行时瞬态（保存临时文件等）：只进 runtime 面，永不
			// 冒充数据孤儿。
			row.Class = "runtime"
			row.Evidence = fmt.Sprintf("运行时瞬态 %s（保存临时文件形态）；判据：无归属键（CreateTemp 随机后缀），进程中断残留；默认不搬（--include-runtime 才搬）", c.kind)
			row.Action = "runtime"
			dr.Orphans = append(dr.Orphans, row)
			continue
		}
		row.Class = className(c.class)
		row.MasterInTrash = trashEvidence(c.masterStem, trashBuckets)
		ev := fmt.Sprintf("sidecar 形态 %s（期望配对 %s）；判据：主转录 <stem>.jsonl 与 <stem>.jsonl.zst 均不在活目录",
			c.kind, displayOrDash(c.owner))
		if row.MasterInTrash != "" {
			ev += fmt.Sprintf("；master 躺在 .trash 桶「%s」（历史删除链漏搬形态，tl1442 清单收敛前产物）", row.MasterInTrash)
		} else {
			ev += "；.trash 未见同 key 桶"
		}
		row.Evidence = ev
		switch {
		case pendingStems[c.masterStem]:
			// 同 stem 有 cleanup-pending 标记：桌面端补收机制会重试清理，
			// 本包让位（「先用现有机制」；搬走残留反而让补收面扑空）。
			row.Action = "defer"
			row.Evidence += "；该 stem 存在 .cleanup-pending.json 标记——桌面端补收机制持有，本扫描让位不搬"
			dr.Deferred = append(dr.Deferred, row)
		case c.class == classRuntime:
			row.Action = "runtime"
			row.Evidence += "；运行时句柄（锁/lease）有独立生命周期，默认不搬"
			dr.Orphans = append(dr.Orphans, row)
		default:
			row.Action = "recycle"
			dr.Orphans = append(dr.Orphans, row)
		}
	}

	// 悬空指针形态：.titles.json 键指向不存在的会话 basename。
	dr.TitlesDangling = danglingTitleKeys(dir, masterPlain)

	sortRows(dr.Orphans)
	sortRows(dr.Unknown)
	sortRows(dr.Deferred)
	sort.Strings(dr.TitlesDangling)
	sort.Strings(dr.TrashLoose)
	return dr, nil
}

// flatDirBytes 是目录工件的体量读数（一层平铺 stat 和，preRepairBackupBytes
// 同一口径；不递归——口径与桌面端枚举一致且只读）。
func flatDirBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// trashEvidence 查 master 是否躺在 .trash 桶里。桶名两代形态：
// 旧=<key>（<stem>.jsonl），现=<stem>.jsonl-deleted-<nano>-<n>
// （reserveUniqueSessionTrashItemDir）。名字证据，不进桶解读内容。
func trashEvidence(stem string, buckets []string) string {
	key := stem + store.SessionTranscriptSuffix
	deletedPrefix := key + "-deleted-"
	for _, b := range buckets {
		if b == key || strings.HasPrefix(b, deletedPrefix) {
			return b
		}
	}
	return ""
}

// danglingTitleKeys 读 .titles.json（缺省/损坏=空表，loadSessionTitlesWithError
// 同语义），返回指向不存在会话的键。会话存在判据与 master 一致：键名 plain
// 或其 .jsonl.zst 孪生任一存在。读失败如实上抛路径错误（不静默吞）。
func danglingTitleKeys(dir string, masterPlain map[string]bool) []string {
	b, err := os.ReadFile(filepath.Join(dir, ".titles.json"))
	if err != nil {
		return nil // 缺省=无覆盖；损坏表交给桌面端既有读边界报错，不越界判定
	}
	m, ok := decodeTitles(b)
	if !ok {
		return nil
	}
	var out []string
	for k := range m {
		stem := strings.TrimSuffix(k, store.SessionTranscriptSuffix)
		if stem == k {
			continue // 键不是 <stem>.jsonl 形态：不在本判据内（保守不判）
		}
		if masterPlain[stem] {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, k+store.SessionTranscriptZstSuffix)); err == nil {
			continue
		}
		out = append(out, k)
	}
	return out
}

func className(c kindClass) string {
	switch c {
	case classData:
		return "data"
	case classRuntime:
		return "runtime"
	case classMechanism:
		return "mechanism"
	case classUnknown:
		return "unknown"
	default:
		return "other"
	}
}

func sortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].MasterStem != rows[j].MasterStem {
			return rows[i].MasterStem < rows[j].MasterStem
		}
		return rows[i].Name < rows[j].Name
	})
}

func displayOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func recomputeTotals(rep *Report) {
	rep.Totals = Totals{}
	for _, d := range rep.Dirs {
		rep.Totals.Dirs++
		rep.Totals.Masters += d.Masters
		rep.Totals.EmptyMasters += d.EmptyMasters
		rep.Totals.Unknown += len(d.Unknown)
		rep.Totals.Deferred += len(d.Deferred)
		rep.Totals.TitlesDangling += len(d.TitlesDangling)
		for _, r := range d.Orphans {
			if r.Class == "runtime" {
				rep.Totals.RuntimeOrphans++
			} else {
				rep.Totals.DataOrphans++
				rep.Totals.DataBytes += r.Size
			}
		}
	}
}
