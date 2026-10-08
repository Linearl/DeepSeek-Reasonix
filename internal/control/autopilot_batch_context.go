// Task 394: compact batch-round context header. When an autopilot session
// opens a new unattended round (goal continuation or background-job wake) and
// the experimental dial is on, the round input is prefixed with a bounded
// header: batch id, the previous round's hash-chain tail, and the open
// threads — sourced from the configured batch plan file plus the session's
// own schema-2 chain position. No new state storage: the plan file is read at
// injection time and never cached; the chain tail is the live head position.
// The "full" level additionally carries the plan's conventions and acceptance
// reminders (交付约定与验收提醒). Empty return = no injection, which is the
// dial-off default and the attended-session answer in every state.

package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/config"
)

const (
	// batchContextManifestOpen / batchContextManifestClose delimit the
	// lightweight manifest section inside a batch plan file. An HTML comment
	// is invisible in rendered markdown, so the plan stays human-readable
	// with the machine-readable section embedded:
	//
	//   <!-- reasonix-batch-manifest
	//   batch: 400s-batch-2
	//   open: 394 注入头 @wt-394; 401 卡片 @wt-401
	//   in-flight: 402 面板 @wt-402
	//   done: 400 批量派单
	//   conventions: 交付回执写 docs/report/zcode交付/；commit 用「394：」前缀
	//   acceptance: 开关关态等价断言；大小有界断言
	//   -->
	batchContextManifestOpen  = "<!-- reasonix-batch-manifest"
	batchContextManifestClose = "-->"

	// batchContextCap bounds the header body. The wrapper adds a fixed few
	// dozen bytes on top, so the whole header stays well under one prompt
	// percent even for a pathological plan file.
	batchContextCap = 4096

	// batchContextIDChars is the short form for chain head/leaf ids in the
	// header — long enough to pin the exact position in the schema-2 log,
	// short enough to keep the line readable.
	batchContextIDChars = 12
)

// batchManifest is the parsed reasonix-batch-manifest section of a batch plan
// file: open/done/in-flight entries (each may carry a @thread-id annotation)
// plus optional conventions and acceptance lines.
type batchManifest struct {
	batch       string
	open        []string
	inFlight    []string
	done        []string
	conventions string
	acceptance  string
}

// parseBatchManifest extracts the manifest section. It requires a CLOSED
// block: an accidentally unclosed comment must not swallow the rest of the
// plan prose into the header. No section (or an unclosed one) reads as "no
// manifest" — the header then degrades to the in-process chain tail.
func parseBatchManifest(content string) (batchManifest, bool) {
	start := strings.Index(content, batchContextManifestOpen)
	if start < 0 {
		return batchManifest{}, false
	}
	rest := content[start+len(batchContextManifestOpen):]
	end := strings.Index(rest, batchContextManifestClose)
	if end < 0 {
		return batchManifest{}, false
	}
	var m batchManifest
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch key {
		case "batch":
			m.batch = value
		case "open":
			m.open = append(m.open, splitBatchManifestEntries(value)...)
		case "in-flight", "inflight":
			m.inFlight = append(m.inFlight, splitBatchManifestEntries(value)...)
		case "done":
			m.done = append(m.done, splitBatchManifestEntries(value)...)
		case "conventions":
			m.conventions = joinBatchManifestLine(m.conventions, value)
		case "acceptance":
			m.acceptance = joinBatchManifestLine(m.acceptance, value)
		}
	}
	return m, true
}

// splitBatchManifestEntries splits one manifest line into entries on ";".
func splitBatchManifestEntries(value string) []string {
	var entries []string
	for _, entry := range strings.Split(value, ";") {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// joinBatchManifestLine folds repeated key lines into one "; "-joined value.
func joinBatchManifestLine(current, value string) string {
	if current == "" {
		return value
	}
	return current + "; " + value
}

// readBatchManifest loads and parses the plan file. A missing or unreadable
// file is not an error — the header proceeds without it (same contract as the
// task-388 proxy manifest: judge without it).
func readBatchManifest(path string) (batchManifest, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return batchManifest{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return batchManifest{}, false
	}
	return parseBatchManifest(string(raw))
}

// autopilotBatchContextHeader composes the header injected when an autopilot
// session opens a new unattended round. Empty return = no injection: the dial
// is off (default), the config is unreadable, or the session is attended —
// 非 autopilot 会话零变更 in every dial state.
func (c *Controller) autopilotBatchContextHeader() string {
	if c == nil || !c.unattendedRun() {
		return ""
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return ""
	}
	level := cfg.AutopilotBatchContextLevel()
	if level == "" {
		return ""
	}
	planPath := strings.TrimSpace(cfg.Desktop.AutopilotBatchPlan)
	manifest, manifestOK := readBatchManifest(planPath)

	var lines []string
	batchID := manifest.batch
	if batchID == "" && planPath != "" {
		batchID = strings.TrimSuffix(filepath.Base(planPath), filepath.Ext(planPath))
	}
	if batchID != "" {
		lines = append(lines, "batch: "+batchID)
	}
	if tail := c.batchChainTail(); tail != "" {
		lines = append(lines, "prev-round chain tail: "+tail)
	}
	if manifestOK {
		if len(manifest.open) > 0 {
			lines = append(lines, "open threads: "+strings.Join(manifest.open, "; "))
		}
		if len(manifest.inFlight) > 0 {
			lines = append(lines, "in-flight: "+strings.Join(manifest.inFlight, "; "))
		}
	}
	if level == "full" {
		if manifestOK {
			if len(manifest.done) > 0 {
				lines = append(lines, fmt.Sprintf("done: %d entries already completed", len(manifest.done)))
			}
			if manifest.conventions != "" {
				lines = append(lines, "conventions: "+manifest.conventions)
			}
			if manifest.acceptance != "" {
				lines = append(lines, "acceptance: "+manifest.acceptance)
			}
		}
		if planPath != "" {
			if manifestOK {
				lines = append(lines, "plan (delivery conventions pointer): "+planPath)
			} else {
				lines = append(lines, "plan: "+planPath+" (unreadable or without a reasonix-batch-manifest section — proceed without it)")
			}
		}
	}

	body := truncateOldestFirst(strings.Join(lines, "\n"), batchContextCap)
	return "<batch-context> (task 394 experimental; continuity header for this batch round; @id = collab thread id)\n" +
		body + "\n</batch-context>"
}

// batchChainTail reports the session's current hash-chain position — the tail
// the previous round wrote. Head/leaf short ids pin the exact chain position
// in the schema-2 log (per-entry sha256 digests hang off the leaf), which is
// what lets the next round (or an auditor) verify continuity without any new
// state storage.
func (c *Controller) batchChainTail() string {
	if c == nil || c.executor == nil {
		return ""
	}
	ref, ok := c.executor.Session().Head()
	if !ok {
		return ""
	}
	return "head=" + batchShortID(ref.HeadID) + " leaf=" + batchShortID(ref.LeafID)
}

// batchShortID returns the bounded short form of a chain id.
func batchShortID(id string) string {
	if len(id) <= batchContextIDChars {
		return id
	}
	return id[:batchContextIDChars]
}

// truncateOldestFirst caps content at limit bytes keeping the TAIL (the
// newest entries), cutting at a line boundary so no entry is half-presented.
// Content at or under the limit returns unchanged. Shared with the task-388
// proxy manifest, which had this loop inline.
func truncateOldestFirst(content string, limit int) string {
	if len(content) <= limit {
		return content
	}
	lines := strings.Split(content, "\n")
	total := 0
	start := len(lines)
	for start > 0 {
		need := len(lines[start-1]) + 1
		if total+need > limit && total > 0 {
			break
		}
		start--
		total += need
	}
	kept := strings.Join(lines[start:], "\n")
	if len(kept) > limit {
		// A single line longer than the cap: hard-clip from the left so the
		// newest tail text survives.
		kept = "…" + kept[len(kept)-limit+1:]
	}
	return "… (older entries truncated)\n" + kept
}
