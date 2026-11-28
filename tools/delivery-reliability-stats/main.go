// Command delivery-reliability-stats aggregates the desktop log's tl1747
// delivery-decision lines into the four readings the delivery-reliability
// observation task asks for: failed_retrying counts (by reason), exhaustion
// counts, the cancel-source distribution, and the retry recovery rate.
// Args accept files or directories (a directory contributes desktop.log and
// its .<n> rotations); no args means the live log dir.
//
// Usage:
//
//	go run ./tools/delivery-reliability-stats
//	go run ./tools/delivery-reliability-stats -since 2026-11-28
//	go run ./tools/delivery-reliability-stats /path/to/desktop.log
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

// decisionRe / reasonRe anchor on the logDeliveryDecision contract fields
// (slog TextHandler emits unquoted values for bare tokens).
var (
	decisionRe  = regexp.MustCompile(`\bdelivery_decision=(\w+)`)
	reasonRe    = regexp.MustCompile(`\bdecision_reason=([\w:]+)`)
	attemptRe   = regexp.MustCompile(`\bretry_attempt=(\d+)`)
	cancelSrcRe = regexp.MustCompile(`\bcancel_source=([\w:]+)`)
	timeDayRe   = regexp.MustCompile(`^time=(\d{4}-\d{2}-\d{2})`)
)

// counts is the aggregate over one scan window.
type counts struct {
	// failedRetryByReason counts delivery_decision=failed_retrying lines by
	// decision_reason (enqueue_error / contact_pass_failed / session_open_failed).
	failedRetryByReason map[string]int
	// exhausted counts decision_reason=open_retry_exhausted lines.
	exhausted int
	// cancelBySource counts delivery_decision=cancelled lines by cancel_source.
	cancelBySource map[string]int
	// recovered counts decision_reason=contact_retry_recovered lines —
	// the numerator of the retry recovery rate.
	recovered int
	// recoveredAttempts sums retry_attempt over recovered chains (info only).
	recoveredAttempts int
	// days remembers per-reason day attribution for the by-day table.
	failedRetryByDay map[string]int
	exhaustedByDay   map[string]int
	cancelByDay      map[string]int
}

func newCounts() *counts {
	return &counts{
		failedRetryByReason: map[string]int{},
		cancelBySource:      map[string]int{},
		failedRetryByDay:    map[string]int{},
		exhaustedByDay:      map[string]int{},
		cancelByDay:         map[string]int{},
	}
}

// scan reads one log stream into c. since ("" = everything) drops earlier
// days before they are counted.
func scan(r io.Reader, since string, c *counts) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		m := decisionRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		day := ""
		if dm := timeDayRe.FindStringSubmatch(line); dm != nil {
			day = dm[1]
		}
		if since != "" && day != "" && day < since {
			continue
		}
		reason := ""
		if rm := reasonRe.FindStringSubmatch(line); rm != nil {
			reason = rm[1]
		}
		switch m[1] {
		case "failed_retrying":
			if reason == "" {
				reason = "unknown"
			}
			c.failedRetryByReason[reason]++
			if day != "" {
				c.failedRetryByDay[day]++
			}
		case "cancelled":
			src := cancelSrcRe.FindStringSubmatch(line)
			key := "unknown"
			if src != nil {
				key = src[1]
			}
			// collab 条目的取消按会话带来 collab:<contact> 完整值；分布聚合
			// 到 collab 类别，避免每目标一列把表打碎。
			if strings.HasPrefix(key, "collab:") {
				key = "collab"
			}
			c.cancelBySource[key]++
			if day != "" {
				c.cancelByDay[day]++
			}
		case "delivered":
			if reason == "contact_retry_recovered" {
				c.recovered++
				if am := attemptRe.FindStringSubmatch(line); am != nil {
					c.recoveredAttempts += atoi(am[1])
				}
			}
		}
		if reason == "open_retry_exhausted" {
			c.exhausted++
			if day != "" {
				c.exhaustedByDay[day]++
			}
		}
	}
	return sc.Err()
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
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

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func main() {
	since := flag.String("since", "", "only count days >= YYYY-MM-DD")
	flag.Parse()

	files := expandArgs(flag.Args())
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no readable desktop.log found (pass a dir or file)")
		os.Exit(1)
	}
	c := newCounts()
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		err = scan(fh, *since, c)
		_ = fh.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
		}
	}

	fmt.Println("== tl1747 delivery reliability readings ==")
	failedTotal := 0
	for _, reason := range sortedKeys(c.failedRetryByReason) {
		failedTotal += c.failedRetryByReason[reason]
	}
	fmt.Printf("failed_retrying total: %d\n", failedTotal)
	for _, reason := range sortedKeys(c.failedRetryByReason) {
		fmt.Printf("  %s\t%d\n", reason, c.failedRetryByReason[reason])
	}
	fmt.Printf("exhausted (open_retry_exhausted): %d\n", c.exhausted)
	fmt.Println("== cancel source distribution ==")
	cancelTotal := 0
	for _, src := range sortedKeys(c.cancelBySource) {
		cancelTotal += c.cancelBySource[src]
	}
	fmt.Printf("cancelled total: %d\n", cancelTotal)
	for _, src := range sortedKeys(c.cancelBySource) {
		fmt.Printf("  %s\t%d\n", src, c.cancelBySource[src])
	}
	fmt.Println("== retry recovery ==")
	// 口径：分子=contact_retry_recovered（失败链被成功 pass 收口），
	// 分母=contact 级 failed_retrying（contact_pass_failed + session_open_failed）。
	// 恢复可能发生在失败后的任意 pass，事件数近似链数；0 分母输出 n/a。
	contactFailed := c.failedRetryByReason["contact_pass_failed"] + c.failedRetryByReason["session_open_failed"]
	rate := "n/a (no contact-level failures yet)"
	if contactFailed > 0 {
		rate = fmt.Sprintf("%.1f%% (%d/%d)", float64(c.recovered)/float64(contactFailed)*100, c.recovered, contactFailed)
	}
	fmt.Printf("retry recovery rate: %s\n", rate)
	fmt.Printf("recovered chains: %d (cumulative failed passes recovered from: %d)\n", c.recovered, c.recoveredAttempts)
	fmt.Println("== by day ==")
	for _, day := range sortedKeys(c.failedRetryByDay) {
		fmt.Printf("  %s\tfailed_retrying=%d exhausted=%d cancelled=%d\n", day, c.failedRetryByDay[day], c.exhaustedByDay[day], c.cancelByDay[day])
	}
}
