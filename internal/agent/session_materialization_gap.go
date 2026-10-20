package agent

import (
	"bufio"
	"log/slog"
	"os"
	"time"
)

// 任务 1010 需求 3: 物化断层检测（只检测，不改数据）。
//
// 现场（20261019 ai-passport）：主 .jsonl 检查点与 meta 停在 10-16，而
// events 日志继续写到 10-19——两个水位之间的内容对主文件一侧的所有读者
// （导出、列表预览、手工修复工具）都不可见。本文件在打开会话时把两侧
// 水位读出来，超阈值就发一条可见的 HeadEventMaterializationLag 提示，
// 文案指向右键「修复对话」（修复动作属任务 1011）。
//
// 硬边界：这里的每一个函数都是只读的。绝不写、移动或截断任何会话文件；
// 1011 的手动修复是唯一允许改历史数据的入口。

const (
	// materializationGapNoticeThreshold is the minimum messages-between-
	// watermarks that surfaces the notice. Below it the two sides are ordinary
	// save-lag noise (a save may be mid-flight), not a断层.
	materializationGapNoticeThreshold = 50
	// materializationGapFreshWindow is how close the meta sidecar's UpdatedAt
	// must sit to the newest live-head activity for the session to count as
	// freshly materialized. Within the window the .jsonl scan is skipped
	// entirely: a healthy session pays no extra per-open cost (任务 1010 验收 5).
	materializationGapFreshWindow = time.Hour
)

// detectMaterializationGap compares the materialized .jsonl checkpoint with the
// events log's newest live chain. It is read-only by construction: one stat, one
// cached sidecar read, one line count. ok is false for every healthy or
// undecidable shape — no jsonl, a windowed (tail first-paint) or damaged
// replay, a fresh sidecar — so the notice only fires on the real断层 shape.
func detectMaterializationGap(sessionPath string, st *sessionDAGState) (HeadEvent, bool) {
	if sessionPath == "" || st == nil || st.damaged {
		return HeadEvent{}, false
	}
	// A tail-window first paint only replayed the trailing bytes: chain lengths
	// from a windowed graph undercount and would fabricate a gap.
	if st.windowStart != 0 {
		return HeadEvent{}, false
	}
	if _, err := os.Stat(sessionPath); err != nil {
		return HeadEvent{}, false
	}
	newestID, newestLen, newestAt := newestLiveChain(st)
	if newestID == "" || newestLen <= materializationGapNoticeThreshold {
		return HeadEvent{}, false
	}
	// Cheap freshness gate first (cached sidecar read): a meta written within
	// the window of the newest head activity means both sides advance together.
	meta, ok, err := loadBranchMetaCached(sessionPath)
	if err != nil || !ok {
		return HeadEvent{}, false
	}
	if meta.UpdatedAt.After(newestAt) || newestAt.Sub(meta.UpdatedAt) < materializationGapFreshWindow {
		return HeadEvent{}, false
	}
	materialized, err := countJSONLLines(sessionPath)
	if err != nil {
		return HeadEvent{}, false
	}
	gap := newestLen - materialized
	if gap <= materializationGapNoticeThreshold {
		return HeadEvent{}, false
	}
	slog.Warn("session: materialization gap detected",
		"path", sessionPath,
		"materialized_messages", materialized,
		"event_chain_messages", newestLen,
		"gap_messages", gap,
		"event_head", newestID,
		"event_last_activity", newestAt.UTC().Format(time.RFC3339),
		"meta_updated_at", meta.UpdatedAt.UTC().Format(time.RFC3339))
	return HeadEvent{
		Kind:          HeadEventMaterializationLag,
		HeadID:        newestID,
		Messages:      materialized,
		OtherMessages: newestLen,
	}, true
}

// newestLiveChain returns the longest live head's chain: the events side of the
// watermark comparison (任务 1010 验收 3 的「events 最新」读数).
func newestLiveChain(st *sessionDAGState) (id string, length int, lastActivity time.Time) {
	for _, live := range st.liveHeads() {
		h := st.heads[live]
		if h == nil {
			continue
		}
		n := len(st.chainIDs(live))
		if n > length || (n == length && h.lastActivity.After(lastActivity)) {
			id, length, lastActivity = live, n, h.lastActivity
		}
	}
	return id, length, lastActivity
}

// countJSONLLines counts transcript records in the materialized .jsonl
// checkpoint. Read-only; blank trailing lines are ignored.
func countJSONLLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		n++
	}
	return n, sc.Err()
}
