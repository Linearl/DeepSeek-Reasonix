// tl1485b 存量盘点工具：枚举会话边车，比较 UpdatedAt 与转录消息级最新
// CreatedAt。防呆钉死：校正/判定基准只用消息级 CreatedAt，禁止用文件
// mtime——2026-10-29 维护波已实测 mtime 基准会把 95/261 会话误抬（防
// #8694 复发），本工具与库内 repair 同判据。
//
// 用法：
//
//	go run ./cmd/sessionactivityscan                 # 只读盘点，出偏新清单与分布
//	go run ./cmd/sessionactivityscan -v             # 附全部会话明细
//	go run ./cmd/sessionactivityscan -apply         # 对偏新行逐条调用既有修复链自愈
//	go run ./cmd/sessionactivityscan -dir <目录>     # 只扫指定目录（可多次）
//
// 只处理会话目录顶层行（catalog 发现面同界，ListSessionOrder 不递归）；
// 子目录里的历史备份副本（.pre-repair-* 等）只盘点不修。
// --apply 不做任何平行修法：逐行调用 agent.RepairSessionListingProjection，
// 与库内 catalog repair 队列完全同源（锁/CAS/让位语义一致）。
// 保留复用：后续任何「边车时间戳 vs 转录真相」审计可直接重跑。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// scanLineLimit 与 agent 侧 sessionDisplayIndexMaxLineBytes 同量级：附件行
// 可以很大，超限行跳过（时间戳审计宁可漏读也不 OOM）。
const scanLineLimit = 16 << 20

type row struct {
	path      string
	updatedAt time.Time
	trueMax   time.Time
	inSubdir  bool
	fresh     bool
}

func (r row) delta() time.Duration {
	if r.trueMax.IsZero() {
		return 0
	}
	return r.updatedAt.Sub(r.trueMax)
}

func (r row) flagged(threshold time.Duration) bool {
	return !r.trueMax.IsZero() && r.delta() > threshold
}

type stampLine struct {
	CreatedAt int64 `json:"createdAt"`
}

type sliceFlags []string

func (s *sliceFlags) String() string { return strings.Join(*s, ",") }
func (s *sliceFlags) Set(v string) error {
	*s = append(*s, v)
	return nil
}

type inventory struct {
	rows           []row
	noMeta         int
	noTimestamps   int
	decodeFailures []string
}

func main() {
	threshold := flag.Duration("threshold", time.Hour, "偏新判定阈值（UpdatedAt 超过消息级真相该时长才计入）")
	apply := flag.Bool("apply", false, "对偏新行逐条调用既有修复链自愈（repair 同源，不做平行修法）")
	verbose := flag.Bool("v", false, "输出全部会话明细（默认只出偏新清单）")
	var dirs sliceFlags
	flag.Var(&dirs, "dir", "扫描目录（可多次；缺省=全局会话目录+全部项目会话目录）")
	flag.Parse()

	scanDirs := dirs
	if len(scanDirs) == 0 {
		scanDirs = defaultScanDirs()
	}
	inv := scanDirs2Inventory(scanDirs)

	var live, backup []row
	for _, r := range inv.rows {
		if !r.flagged(*threshold) {
			continue
		}
		if r.inSubdir {
			backup = append(backup, r)
		} else {
			live = append(live, r)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].delta() > live[j].delta() })
	sort.Slice(backup, func(i, j int) bool { return backup[i].delta() > backup[j].delta() })

	fmt.Printf("扫描目录 %d 个；顶层会话+子目录备份共 %d 行（无 meta %d，无消息级时间戳 %d，解码失败 %d），阈值 %v\n",
		len(scanDirs), len(inv.rows), inv.noMeta, inv.noTimestamps, len(inv.decodeFailures), *threshold)
	fmt.Printf("偏新会话：顶层 %d，子目录备份 %d（备份只盘点不修）\n", len(live), len(backup))
	printBuckets("顶层偏新分布", live)
	if len(inv.decodeFailures) > 0 {
		fmt.Println("解码失败清单：")
		for _, p := range inv.decodeFailures {
			fmt.Printf("  %s\n", p)
		}
	}
	fmt.Println()
	for label, list := range map[string][]row{"顶层": live, "备份": backup} {
		if len(list) == 0 {
			continue
		}
		fmt.Printf("%s偏新清单（delta 降序）：path | UpdatedAt | 消息级最新 | 偏新量 | 投影fresh\n", label)
		for _, r := range list {
			fmt.Printf("  %s | %s | %s | %v | %v\n",
				r.path, r.updatedAt.Format(time.RFC3339), r.trueMax.Format(time.RFC3339),
				r.delta().Round(time.Minute), r.fresh)
		}
		fmt.Println()
	}
	if *verbose {
		fmt.Println("全部会话明细（未偏新部分）：")
		for _, r := range inv.rows {
			if r.flagged(*threshold) {
				continue
			}
			maxAt := "-"
			if !r.trueMax.IsZero() {
				maxAt = r.trueMax.Format(time.RFC3339)
			}
			fmt.Printf("  %s | %s | %s | fresh=%v\n", r.path, r.updatedAt.Format(time.RFC3339), maxAt, r.fresh)
		}
	}

	if !*apply {
		return
	}
	fmt.Println()
	fmt.Printf("开始自愈顶层 %d 行（走 agent.RepairSessionListingProjection）...\n", len(live))
	healed := 0
	for _, r := range live {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		result, err := agent.RepairSessionListingProjection(ctx, r.path)
		cancel()
		status := string(result.Status)
		switch {
		case err != nil:
			status = "error: " + err.Error()
		case result.Status == agent.SessionListingRepairApplied, result.Status == agent.SessionListingRepairAlreadyCurrent:
			healed++
		}
		fmt.Printf("  %s -> %s\n", r.path, status)
	}
	fmt.Printf("自愈完成：applied/already_current %d / %d\n", healed, len(live))
}

func defaultScanDirs() []string {
	var out []string
	if dir := config.SessionDir(); dir != "" {
		out = append(out, dir)
	}
	base := config.MemoryUserDir()
	if base != "" {
		projects := filepath.Join(base, "projects")
		if entries, err := os.ReadDir(projects); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				out = append(out, filepath.Join(projects, entry.Name(), "sessions"))
			}
		}
	}
	return out
}

func scanDirs2Inventory(dirs []string) inventory {
	var inv inventory
	for _, dir := range dirs {
		sub := scanSessionDir(dir, false)
		inv.rows = append(inv.rows, sub.rows...)
		inv.noMeta += sub.noMeta
		inv.noTimestamps += sub.noTimestamps
		inv.decodeFailures = append(inv.decodeFailures, sub.decodeFailures...)
	}
	return inv
}

// scanSessionDir 单层枚举 + 一层子目录（备份副本）。与 catalog 发现面同界：
// agent.ListSessionOrder 不递归，顶层行才是 catalog 会话行。
func scanSessionDir(dir string, inSubdir bool) inventory {
	var inv inventory
	entries, err := os.ReadDir(dir)
	if err != nil {
		return inv
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		if entry.IsDir() {
			if agent.SessionPathInTrash(path) || inSubdir {
				continue
			}
			sub := scanSessionDir(path, true)
			inv.rows = append(inv.rows, sub.rows...)
			inv.noMeta += sub.noMeta
			inv.noTimestamps += sub.noTimestamps
			inv.decodeFailures = append(inv.decodeFailures, sub.decodeFailures...)
			continue
		}
		if !store.IsSessionTranscriptName(name) {
			continue
		}
		meta, ok, err := agent.LoadBranchMeta(path)
		if err != nil || !ok || meta.UpdatedAt.IsZero() {
			inv.noMeta++
			continue
		}
		r := row{
			path:      path,
			updatedAt: meta.UpdatedAt,
			inSubdir:  inSubdir,
			fresh:     sessionOrderFresh(meta),
			trueMax:   transcriptMaxCreatedAt(path),
		}
		if r.trueMax.IsZero() {
			if transcriptNonEmpty(path) {
				// 有转录内容但读不到任何消息级时间戳：repair 同样缺证据不动。
				inv.noTimestamps++
			}
			continue
		}
		inv.rows = append(inv.rows, r)
	}
	return inv
}

func sessionOrderFresh(meta agent.BranchMeta) bool {
	info := agent.SessionOrderInfo{
		SchemaVersion:        meta.SchemaVersion,
		Turns:                meta.Turns,
		Revision:             meta.Revision,
		ListingRevision:      meta.ListingRevision,
		ContentDigest:        meta.ContentDigest,
		ListingContentDigest: meta.ListingContentDigest,
	}
	return info.ListingProjectionFresh()
}

// transcriptMaxCreatedAt 全量解码兼容转录（保存与 repair 回放都会维护该文件），
// 取最新非零 CreatedAt——与 agent.SessionListingActivityTime 同语义。
func transcriptMaxCreatedAt(path string) time.Time {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), scanLineLimit)
	var newest int64
	for scanner.Scan() {
		var line stampLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if line.CreatedAt > newest {
			newest = line.CreatedAt
		}
	}
	if newest <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(newest)
}

func transcriptNonEmpty(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func printBuckets(label string, flagged []row) {
	if len(flagged) == 0 {
		return
	}
	buckets := []struct {
		name string
		lo   time.Duration
		hi   time.Duration
	}{{"阈值~6h", 0, 6 * time.Hour}, {"6h~24h", 6 * time.Hour, 24 * time.Hour}, {"1d~7d", 24 * time.Hour, 7 * 24 * time.Hour}, {"7d+", 7 * 24 * time.Hour, 1 << 62}}
	fmt.Printf("%s：\n", label)
	for _, b := range buckets {
		count := 0
		for _, r := range flagged {
			d := r.delta()
			if d > b.lo && d <= b.hi {
				count++
			}
		}
		if count > 0 {
			fmt.Printf("  %-8s %d\n", b.name, count)
		}
	}
	fmt.Printf("  最小偏新 %v，最大偏新 %v\n", flagged[len(flagged)-1].delta().Round(time.Minute), flagged[0].delta().Round(time.Minute))
}
