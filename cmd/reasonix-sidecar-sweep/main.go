// Command reasonix-sidecar-sweep 是 tl1806 的孤儿 sidecar 清扫 CLI：
// 缺省只读扫描会话目录产清单（零写盘）；--confirm 才把清单里的孤儿移入
// 各会话目录的 _recycle-YYYYMMDD/（回收语义：真删是之后另一次用户确认）。
//
// 硬约束（任务书定稿）：清单 → 用户确认 → 回收；禁批量哨删；执行前清单先出。
// 因此本工具的一切盘面变更都挂在显式旗后面：
//
//	(无旗)            只读扫描，清单打 stdout，不写任何盘面
//	--confirm         执行回收搬运（数据面孤儿 → _recycle-YYYYMMDD/）
//	--include-runtime 连运行时句柄（无主锁/lease）一起搬（缺省不动）
//	--include-titles  连 .titles.json 悬空键一起清（原表备份进回收区）
//	--home PATH       覆盖 reasonix 状态根（缺省自动发现 projects/*/sessions）
//	--dir PATH        显式指定会话目录（可重复；指定后跳过自动发现）
//	--json            stdout 出机器可读 JSON（供桌面端未来接线）
//
// 退出码：0=成功（含「清单干净」）；1=搬运有失败或扫描出错；2=用法错误。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/sidecarsweep"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func run(args []string) int {
	fs := flag.NewFlagSet("reasonix-sidecar-sweep", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var dirs multiFlag
	home := fs.String("home", "", "reasonix 状态根覆盖（缺省自动发现）")
	confirm := fs.Bool("confirm", false, "执行回收搬运（缺省只读出清单，零写盘）")
	includeRuntime := fs.Bool("include-runtime", false, "连同无主运行时句柄一起搬（需 --confirm）")
	includeTitles := fs.Bool("include-titles", false, "连 .titles.json 悬空键一起清（需 --confirm）")
	asJSON := fs.Bool("json", false, "stdout 输出 JSON")
	fs.Var(&dirs, "dir", "显式会话目录（可重复）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "多余参数: %v\n", fs.Args())
		return 2
	}

	roots, err := resolveRoots(*home, dirs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "error: 未发现任何会话目录（检查 --home/--dir）")
		return 2
	}

	rep, err := sidecarsweep.ScanRoots(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if *confirm {
		rep.Mode = "confirm"
		rc := sidecarsweep.Recycle(rep, sidecarsweep.RecycleOptions{
			IncludeRuntime: *includeRuntime,
			IncludeTitles:  *includeTitles,
		})
		emit(rep, rc, *asJSON, *confirm)
		if rc.Failed > 0 {
			return 1
		}
		return 0
	}
	emit(rep, nil, *asJSON, false)
	return 0
}

// resolveRoots 解析扫描根：--dir 显式优先；--home 覆盖状态根下的
// projects/*/sessions；缺省走 config.AllProjectSessionDirs()（与桌面端同一
// 发现机制，不另造第二份目录清单）。
func resolveRoots(home string, dirs []string) ([]string, error) {
	if len(dirs) > 0 {
		out := make([]string, 0, len(dirs))
		for _, d := range dirs {
			abs, err := filepath.Abs(d)
			if err != nil {
				return nil, err
			}
			if info, err := os.Stat(abs); err != nil || !info.IsDir() {
				return nil, fmt.Errorf("--dir %s 不是可读目录", d)
			}
			out = append(out, abs)
		}
		return out, nil
	}
	if strings.TrimSpace(home) != "" {
		projDir := filepath.Join(home, "projects")
		entries, err := os.ReadDir(projDir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", projDir, err)
		}
		var out []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sessions := filepath.Join(projDir, e.Name(), "sessions")
			if info, err := os.Stat(sessions); err == nil && info.IsDir() {
				out = append(out, sessions)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	return config.AllProjectSessionDirs(), nil
}

// emit 把扫描（与可选搬运回执）写 stdout。
func emit(rep *sidecarsweep.Report, rc *sidecarsweep.RecycleReceipt, asJSON, confirmed bool) {
	if asJSON {
		payload := struct {
			Report  *sidecarsweep.Report         `json:"report"`
			Receipt *sidecarsweep.RecycleReceipt `json:"receipt,omitempty"`
		}{Report: rep, Receipt: rc}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return
	}
	printHuman(rep, rc, confirmed)
}

func printHuman(rep *sidecarsweep.Report, rc *sidecarsweep.RecycleReceipt, confirmed bool) {
	mode := "只读扫描（未动任何盘面）"
	if confirmed {
		mode = "已执行回收搬运（--confirm；真删候再次确认）"
	}
	fmt.Printf("reasonix-sidecar-sweep · tl1806 孤儿 sidecar 清单\n模式：%s\n时间：%s\n\n",
		mode, rep.GeneratedAt)
	for _, d := range rep.Dirs {
		fmt.Printf("== %s\n", d.Dir)
		fmt.Printf("   活主转录 %d（空壳 %d）｜孤儿 data %d｜孤儿 runtime %d｜不明 %d｜defer %d｜悬空标题键 %d\n",
			d.Masters, d.EmptyMasters, countClass(d.Orphans, "data"), countClass(d.Orphans, "runtime"),
			len(d.Unknown), len(d.Deferred), len(d.TitlesDangling))
		for _, r := range d.Orphans {
			flagNote := ""
			if r.Class == "runtime" {
				flagNote = "  [--include-runtime 才搬]"
			}
			trashNote := ""
			if r.MasterInTrash != "" {
				trashNote = fmt.Sprintf("  [master 在桶 %s]", r.MasterInTrash)
			}
			fmt.Printf("   ORPHAN %-8s %-24s %s (%d B)%s%s\n      判据: %s\n",
				r.Class, r.Kind, r.Name, r.Size, trashNote, flagNote, r.Evidence)
		}
		for _, r := range d.Unknown {
			fmt.Printf("   REVIEW %-8s %s (%d B)\n      %s\n", r.Kind, r.Name, r.Size, r.Evidence)
		}
		for _, r := range d.Deferred {
			fmt.Printf("   DEFER  %-8s %s\n      %s\n", r.Kind, r.Name, r.Evidence)
		}
		for _, k := range d.TitlesDangling {
			fmt.Printf("   TITLE-DANGLING %s\n      判据: .titles.json 键指向的会话文件不在活目录\n", k)
		}
		for _, k := range d.TrashLoose {
			fmt.Printf("   TRASH-LOOSE %s\n      判据: .trash 顶层散文件（桶语义外；desktop 领地，仅提示）\n", k)
		}
		for _, e := range d.Errors {
			fmt.Printf("   ERROR %s\n", e)
		}
	}
	t := rep.Totals
	fmt.Printf("\n汇总：目录 %d｜活主 %d（空壳 %d）｜孤儿 data %d（%d B）｜runtime %d｜不明 %d｜defer %d｜悬空标题键 %d\n",
		t.Dirs, t.Masters, t.EmptyMasters, t.DataOrphans, t.DataBytes, t.RuntimeOrphans, t.Unknown, t.Deferred, t.TitlesDangling)
	if confirmed && rc != nil {
		fmt.Printf("搬运回执：moved %d｜skipped %d｜failed %d\n", rc.Moved, rc.Skipped, rc.Failed)
		for _, r := range rc.Rows {
			fmt.Printf("   %-16s %s %s\n", r.Status, r.Name, r.Detail)
		}
		fmt.Println("回收语义：条目已移入 <会话目录>/_recycle-YYYYMMDD/（含 manifest.tsv 还原依据）；真删需再次确认。")
	} else {
		fmt.Println("下一步：核对清单后加 --confirm 执行回收（缺省永不写盘）。")
	}
}

func countClass(rows []sidecarsweep.Row, class string) int {
	n := 0
	for _, r := range rows {
		if r.Class == class {
			n++
		}
	}
	return n
}
