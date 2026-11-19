package main

// tl1684 R7 回归钉（读侧）：checkpoint 截断会物理删除 turn 的事件行，而
// 首响配对只能读事件行——不聚合 checkpoint 留存样本时，被截断 turn 的
// 首响（往往是长尾最坏样本）从 P95/Max 里消失（tl1684 归因 C5/C6：三期
// 「单调恶化」与「P50 反降」都是这个截断的度量假象）。
//
// 本钉用真实 turnevent.Ledger 写四个 turn → ack → 强制 Compact（正文删光）
// → 再写一个 turn 留在 live 区 → scanLedgers 断言四个样本全部可见、极值
// 不丢、Compact 前后读数连续。测试只使用基点（bc3d5a8c1f）就存在的
// API 与字段，因此可以在基点上直接跑出「修前红」：旧读数逻辑跳过
// checkpoint 行，被截断的三个样本丢失，断言 len(Turns)=4 失败。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/store"
	"reasonix/internal/turnevent"
)

// msOf 取样本 MS 列表。
func msOf(turns []turnSample) []int64 {
	out := make([]int64, len(turns))
	for i, t := range turns {
		out[i] = t.MS
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestScanLedgersCheckpointKeepsExtremes(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	l, err := turnevent.Open(sessionPath, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	runPinnedTurn := func(respondAfter time.Duration) string {
		t.Helper()
		id, err := l.Begin()
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged, Status: event.TurnInProgress}, event.TurnInProgress); err != nil || !ok {
			t.Fatalf("status append: ok=%v err=%v", ok, err)
		}
		if respondAfter > 0 {
			time.Sleep(respondAfter)
		}
		if _, ok, err := l.Append(event.Event{Kind: event.Text, Text: "answer"}, event.TurnInProgress); err != nil || !ok {
			t.Fatalf("text append: ok=%v err=%v", ok, err)
		}
		if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
			t.Fatalf("terminal append: ok=%v err=%v", ok, err)
		}
		if err := l.AcknowledgeProjection(id); err != nil {
			t.Fatalf("ack: %v", err)
		}
		return id
	}

	// 三个已终结 turn，首响各约 25/60/40ms（中间那个是「极值」）。
	runPinnedTurn(25 * time.Millisecond)
	runPinnedTurn(60 * time.Millisecond)
	runPinnedTurn(40 * time.Millisecond)

	// 截断前基线：live 事件行还在，样本应齐全。
	now := time.Now()
	before, err := scanLedgers([]string{dir}, 168*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Turns) != 3 {
		t.Fatalf("pre-compaction turns = %d, want 3", len(before.Turns))
	}
	want := msOf(before.Turns)
	if want[len(want)-1] < 40 {
		t.Fatalf("pre-compaction max = %dms, want the ~60ms extreme", want[len(want)-1])
	}

	// 强制 checkpoint 截断：三个 turn 的事件行物理删除。
	if err := l.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	data, err := os.ReadFile(store.SessionTurnEventLog(sessionPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[0] != byte('{') {
		t.Fatalf("compacted ledger missing checkpoint head: %q", data[:min(len(data), 64)])
	}
	for _, body := range []string{"answer"} {
		if strings.Contains(string(data), body) {
			t.Fatalf("event lines survived compaction; the truncation scenario is not exercised: %s", data)
		}
	}

	// 第四个 turn 留在 live 区（不终结、不 Compact），制造「留存池 + live
	// 配对」两源并存的聚合现场。
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin live turn: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged, Status: event.TurnInProgress}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("live status append: ok=%v err=%v", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.Text, Text: "live answer"}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("live text append: ok=%v err=%v", ok, err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	after, err := scanLedgers([]string{dir}, 168*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	// 修前红：旧口径跳过 checkpoint 行 → 只剩 live 区 1 样本。
	// 修后绿：留存池聚合 → 4 样本齐全。
	if len(after.Turns) != 4 {
		t.Fatalf("post-compaction turns = %d, want 4 (3 checkpoint-retained + 1 live); checkpoint truncation dropped historical samples", len(after.Turns))
	}
	got := msOf(after.Turns)
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("sample %dms visible before compaction disappeared after it (reading discontinuity): before=%v after=%v", w, want, got)
		}
	}
	// 极值不丢：截断后的 Max 必须 ≥ 截断前的 Max（新增 live 样本只会更多）。
	if got[len(got)-1] < want[len(want)-1] {
		t.Fatalf("max shrank across compaction: before=%d after=%d (worst sample lost)", want[len(want)-1], got[len(got)-1])
	}
	// P95（nearest-rank，n=4 → 取第 4 位 = Max）跨截断连续。
	if p95Before, p95After := percentileInt64(want, 0.95), percentileInt64(got, 0.95); p95After < p95Before {
		t.Fatalf("P95 shrank across compaction: before=%d after=%d", p95Before, p95After)
	}
	// 去重：live 区没有与留存池重叠的 turn（四个 turn 各自独立），如果有
	// 重复聚合，样本数会超过 4——上面 len==4 已隐式钉住。
}
