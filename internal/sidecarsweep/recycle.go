package sidecarsweep

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"reasonix/internal/store"
)

// RecycleOptions 控制 --confirm 通道的搬运面。
type RecycleOptions struct {
	// IncludeRuntime 放开运行时句柄（锁/lease）的搬运（缺省不动）。
	IncludeRuntime bool
	// IncludeTitles 放开 .titles.json 悬空键清理（缺省只列不动）。
	IncludeTitles bool
	// Now 供测试注入（回收目录日期与备份时间戳）；nil = time.Now。
	Now func() time.Time
}

// RecycleRow 是搬运回执的一行。
type RecycleRow struct {
	Name   string `json:"name"`
	Status string `json:"status"` // moved / skipped-revived / skipped-gone / failed / titles-rewritten / titles-failed
	Dest   string `json:"dest,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// RecycleReceipt 是 --confirm 通道的回执：搬了什么、跳过什么、为何失败。
// Mode 语义与防呆：本函数只做「移入回收区」一种动作——真删是之后另一次用户
// 确认的事（_recycle 语义铁律），任何代码路径都不得在本包内删除条目。
type RecycleReceipt struct {
	ExecutedAt string       `json:"executed_at"`
	RecycleDir string       `json:"-"` // 每目录一个，逐行记录在 rows 的 Dest 里
	Rows       []RecycleRow `json:"rows"`
	Moved      int          `json:"moved"`
	Failed     int          `json:"failed"`
	Skipped    int          `json:"skipped"`
}

// recycleDirName 返回回收区目录名（本地日期，全局工作区 _recycle-YYYYMMDD
// 同款命名）。
func recycleDirName(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return "_recycle-" + now().Format("20060102")
}

// Recycle 把报告里处置面命中 --confirm 旗的孤儿行移入各自会话目录的
// _recycle-YYYYMMDD/（保留原名；同名碰撞加 -dup<N> 后缀），并逐行追加
// manifest.tsv（回收名 TAB 原路径 TAB 种类 TAB 字节数）作为还原依据。
//
// TOCTOU 防复活闸：每一行搬前重查 master liveness——扫描与搬运之间 master
// 若被运行时重建（会话复活/新建），该行跳过不搬（skipped-revived）。
// 单行失败不拖累其余，逐行可归因；任何失败让 CLI 退出码非零。
func Recycle(rep *Report, opts RecycleOptions) *RecycleReceipt {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	rc := &RecycleReceipt{ExecutedAt: opts.Now().Format(time.RFC3339)}
	for i := range rep.Dirs {
		dir := rep.Dirs[i].Dir
		if len(rep.Dirs[i].Orphans) == 0 && !(opts.IncludeTitles && len(rep.Dirs[i].TitlesDangling) > 0) {
			continue
		}
		rc.recycleDir(dir, &rep.Dirs[i], opts)
	}
	return rc
}

// recycleDir 对单目录执行搬运（含标题悬空键清理）。manifest 原子性说明：
// manifest.tsv 是 O_APPEND 追加写——搬运成功一行记一行，进程中断后 manifest
// 仍如实反映已搬条目（宁可记录少一行，不可记录多一行）。
func (rc *RecycleReceipt) recycleDir(dir string, dr *DirReport, opts RecycleOptions) {
	// 资格预检：过滤 runtime 旗面后无可搬行且无 titles 工作 → 不建回收区目录
	// （--confirm 也不留空目录）。
	eligible := 0
	for _, row := range dr.Orphans {
		if row.Class == "runtime" && !opts.IncludeRuntime {
			continue
		}
		eligible++
	}
	if eligible == 0 && !(opts.IncludeTitles && len(dr.TitlesDangling) > 0) {
		return
	}
	target := filepath.Join(dir, recycleDirName(opts.Now))
	if err := os.MkdirAll(target, 0o755); err != nil {
		rc.failAll(dr.Orphans, "recycle-dir-mkdir: "+err.Error())
		return
	}
	manifest, err := os.OpenFile(filepath.Join(target, "manifest.tsv"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		rc.failAll(dr.Orphans, "manifest-open: "+err.Error())
		return
	}
	defer manifest.Close()

	for _, row := range dr.Orphans {
		if row.Class == "runtime" && !opts.IncludeRuntime {
			continue // 未放开：留在原地，回执不记（清单里已有该行）
		}
		status, dest, detail := rc.moveOne(dir, target, row)
		rc.Rows = append(rc.Rows, RecycleRow{Name: row.Name, Status: status, Dest: dest, Detail: detail})
		switch {
		case status == "moved":
			rc.Moved++
			fmt.Fprintf(manifest, "%s\t%s\t%s\t%s\t%d\n", filepath.Base(dest), filepath.Join(dir, row.Name), row.Kind, row.MasterStem, row.Size)
		case status == "failed":
			rc.Failed++
		default:
			rc.Skipped++
		}
	}

	if opts.IncludeTitles && len(dr.TitlesDangling) > 0 {
		if err := rewriteTitlesWithoutDangling(dir, target, dr.TitlesDangling, opts.Now); err != nil {
			rc.Rows = append(rc.Rows, RecycleRow{Status: "titles-failed", Detail: err.Error()})
			rc.Failed++
		} else {
			rc.Rows = append(rc.Rows, RecycleRow{
				Status: "titles-rewritten",
				Dest:   target,
				Detail: fmt.Sprintf("移除悬空键 %d 个；原表已备份进回收区（%s%s…）", len(dr.TitlesDangling), titlesBackupPrefix, opts.Now().Format("150405")),
			})
			rc.Moved++
		}
	}
}

// moveOne 搬一行：TOCTOU 防复活 → 条目仍在 → 原名入回收区（碰撞加后缀）→
// os.Rename 同卷原子移动。移动是本包唯一的盘面变更动作。
func (rc *RecycleReceipt) moveOne(dir, target string, row Row) (status, dest, detail string) {
	// 防复活：master 重新出现（会话被还原/重建）→ 侧车不再是孤儿，跳过。
	if masterPresent(dir, row.MasterStem) {
		return "skipped-revived", "", "master 已重现，条目回到活家族，不搬"
	}
	src := filepath.Join(dir, row.Name)
	if _, err := os.Lstat(src); err != nil {
		return "skipped-gone", "", "条目已不存在（被他链搬走）: " + err.Error()
	}
	dst := filepath.Join(target, row.Name)
	if final, err := uniqueDest(dst); err == nil {
		dst = final
	} else {
		return "failed", "", "dest-resolve: " + err.Error()
	}
	if err := os.Rename(src, dst); err != nil {
		return "failed", "", "rename: " + err.Error()
	}
	return "moved", dst, ""
}

func (rc *RecycleReceipt) failAll(rows []Row, detail string) {
	for _, row := range rows {
		if row.Class == "runtime" {
			continue
		}
		rc.Rows = append(rc.Rows, RecycleRow{Name: row.Name, Status: "failed", Detail: detail})
		rc.Failed++
	}
}

// uniqueDest 在同名碰撞时生成 <name>-dup<N> 后缀的目标名（永不覆盖已有条目）。
func uniqueDest(dst string) (string, error) {
	if _, err := os.Lstat(dst); os.IsNotExist(err) {
		return dst, nil
	}
	for i := 1; i < 1000; i++ {
		cand := dst + "-dup" + strconv.Itoa(i)
		if _, err := os.Lstat(cand); os.IsNotExist(err) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("no free recycle slot for %s", filepath.Base(dst))
}

// masterPresent 重查 master liveness（plain / zst 档案任一）。
func masterPresent(dir, stem string) bool {
	if stem == "" {
		return false
	}
	if _, err := os.Lstat(filepath.Join(dir, stem+store.SessionTranscriptSuffix)); err == nil {
		return true
	}
	_, err := os.Lstat(filepath.Join(dir, stem+store.SessionTranscriptSuffix+store.SessionTranscriptZstSuffix))
	return err == nil
}

// ── titles 悬空键清理（--include-titles 放开）────────────────────────────

const titlesBackupPrefix = ".titles.json.backup-"

// decodeTitles 解码 .titles.json 字节（map[string]string；非表形态=失败）。
func decodeTitles(b []byte) (map[string]string, bool) {
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, true
}

// rewriteTitlesWithoutDangling 把 .titles.json 中的悬空键移除：原表先整份
// 备份进回收区（字节级 copy，绝不改写备份），再 temp+rename 原子替换。
// 并发披露：桌面端在飞时存在 last-writer-wins 窗口——悬空键惰性无害（不指
// 向任何活会话），窗口内桌面端新写会盖掉本次清理，属可接受降级（回执注明）。
func rewriteTitlesWithoutDangling(dir, recycleTarget string, dangling []string, now func() time.Time) error {
	path := filepath.Join(dir, ".titles.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read titles: %w", err)
	}
	m, ok := decodeTitles(b)
	if !ok {
		return fmt.Errorf("titles map undecodable; refusing to rewrite")
	}
	// 备份进回收区（copy 原字节）。
	if now == nil {
		now = time.Now
	}
	backup := filepath.Join(recycleTarget, titlesBackupPrefix+now().Format("150405"))
	if err := copyFile(path, backup); err != nil {
		return fmt.Errorf("backup titles: %w", err)
	}
	// 过滤悬空键（保序无关：map 语义）。
	remove := map[string]bool{}
	for _, k := range dangling {
		remove[k] = true
	}
	for k := range remove {
		delete(m, k)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode titles: %w", err)
	}
	out = append(out, '\n')
	tmp := path + ".tmp-sidecarsweep"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("write titles temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace titles: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
