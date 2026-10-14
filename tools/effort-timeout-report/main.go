// Command effort-timeout-report aggregates the desktop log's task-421/889
// effort-read evidence into one view: timed-out counts by day and day+hour
// (the "下次爆表一键取数" entry for the recurring "effort read timed out"
// notice) plus the slowest reads' segment attribution from the task-889
// breakdown line. Args accept files or directories (a directory contributes
// desktop.log and its .<n> rotations); no args means the live log dir.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"reasonix/internal/config"
)

var (
	timePrefixRe  = regexp.MustCompile(`^time=(\d{4}-\d{2}-\d{2})T(\d{2}):`)
	totalMsRe     = regexp.MustCompile(`\btotal_ms=(\d+)`)
	reconcileMsRe = regexp.MustCompile(`\breconcile_ms=(\d+)`)
	snapshotMsRe  = regexp.MustCompile(`\bsnapshot_ms=(\d+)`)
	memoMsRe      = regexp.MustCompile(`\bmemo_ms=(\d+)`)
	walkMsRe      = regexp.MustCompile(`\breconcile_walk_ms=(\d+)`)
	tabRe         = regexp.MustCompile(`\btab=(\S+)`)
	afterTimeRe   = regexp.MustCompile(`\bafter_timeout=(\w+)`)
)

const (
	timeoutMsg   = `msg="desktop: effort read timed out`
	breakdownMsg = `msg="desktop: effort read breakdown"`
)

// slowRead is one breakdown line kept for attribution: when it ran, which
// tab, and the segment split the task-889 line carries.
type slowRead struct {
	at           string
	tab          string
	totalMs      int64
	reconcileMs  int64
	snapshotMs   int64
	memoMs       int64
	walkMs       int64
	afterTimeout bool
}

// report is the aggregate: timed-out counts per day and per hour of day
// (across all parsed days), plus the slowest breakdown lines (desc by total).
type report struct {
	days  map[string]int
	hours map[string]int
	slow  []slowRead
}

// scan reads one log stream, counting timeout lines by day and by day+hour
// and collecting breakdown lines at or above minMs. since ("" = everything)
// drops earlier days before they are counted.
func scan(r io.Reader, minMs int64, since string) (map[string]int, map[string]int, []slowRead, error) {
	days := map[string]int{}
	hours := map[string]int{}
	var slow []slowRead
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "time=") {
			continue
		}
		m := timePrefixRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		day, hour := m[1], m[2]
		if since != "" && day < since {
			continue
		}
		switch {
		case strings.Contains(line, timeoutMsg):
			days[day]++
			// Keyed by day+hour so the table keeps day attribution: rows
			// render grouped per day, hour-of-day patterns stay readable.
			hours[day+" "+hour]++
		case strings.Contains(line, breakdownMsg):
			if total := int64Attr(totalMsRe, line); total >= minMs {
				slow = append(slow, slowRead{
					at:           day + "T" + hour + ":" + attrAfter(timePrefixRe, line),
					tab:          strAttr(tabRe, line),
					totalMs:      total,
					reconcileMs:  int64Attr(reconcileMsRe, line),
					snapshotMs:   int64Attr(snapshotMsRe, line),
					memoMs:       int64Attr(memoMsRe, line),
					walkMs:       int64Attr(walkMsRe, line),
					afterTimeout: strAttr(afterTimeRe, line) == "true",
				})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return days, hours, slow, err
	}
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].totalMs > slow[j].totalMs })
	return days, hours, slow, nil
}

func int64Attr(re *regexp.Regexp, line string) int64 {
	m := re.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	var n int64
	for _, c := range m[1] {
		n = n*10 + int64(c-'0')
	}
	return n
}

func strAttr(re *regexp.Regexp, line string) string {
	if m := re.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// attrAfter keeps the clock portion (MM:SS.frac) that follows the
// day+hour prefix, so a slow read's row reads as a full timestamp.
func attrAfter(re *regexp.Regexp, line string) string {
	loc := re.FindStringIndex(line)
	if loc == nil {
		return ""
	}
	rest := line[loc[1]:]
	if end := strings.IndexByte(rest, ' '); end > 0 {
		rest = rest[:end]
	}
	return rest
}

// expandArgs turns file/dir arguments into the concrete log files to parse
// (a directory contributes desktop.log plus its desktop.log.<n> rotations);
// an empty list defaults to the live log directory.
func expandArgs(args []string) []string {
	if len(args) == 0 {
		args = []string{filepath.Join(config.MemoryUserDir(), "logs", "desktop")}
	}
	var out []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			out = append(out, arg)
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(arg, "desktop.log*"))
		for _, p := range matches {
			if fileExists(p) {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// Usage:
//
//	go run ./tools/effort-timeout-report
//	go run ./tools/effort-timeout-report -since 2026-10-09 -top 20
//	go run ./tools/effort-timeout-report /path/to/logs/desktop.log
//
// Exit code 1 only when nothing could be parsed.
func main() {
	since := flag.String("since", "", "only count days >= YYYY-MM-DD")
	minMs := flag.Int64("min-ms", 2000, "slow-read threshold for breakdown rows (ms)")
	top := flag.Int("top", 10, "how many slow reads to list")
	flag.Parse()

	files := expandArgs(flag.Args())
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no readable desktop.log found (pass a dir or file)")
		os.Exit(1)
	}
	days := map[string]int{}
	hours := map[string]int{}
	var slow []slowRead
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		d, h, s, err := scan(fh, *minMs, *since)
		_ = fh.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			continue
		}
		for k, v := range d {
			days[k] += v
		}
		for k, v := range h {
			hours[k] += v
		}
		slow = append(slow, s...)
	}
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].totalMs > slow[j].totalMs })

	fmt.Println("== effort read timed out: by day ==")
	for _, day := range sortedKeys(days) {
		fmt.Printf("%s\t%d\n", day, days[day])
	}
	fmt.Println("== effort read timed out: by day and hour ==")
	for _, hour := range sortedKeys(hours) {
		fmt.Printf("%s:00\t%d\n", hour, hours[hour])
	}
	fmt.Printf("== slow effort reads (total_ms >= %d), top %d ==\n", *minMs, *top)
	for i, r := range slow {
		if i >= *top {
			break
		}
		after := ""
		if r.afterTimeout {
			after = " after_timeout"
		}
		fmt.Printf("%s total=%d reconcile=%d (memo=%d walk=%d) snapshot=%d tab=%s%s\n",
			r.at, r.totalMs, r.reconcileMs, r.memoMs, r.walkMs, r.snapshotMs, r.tab, after)
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
