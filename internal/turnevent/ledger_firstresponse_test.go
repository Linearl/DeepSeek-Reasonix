package turnevent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/store"
)

// tl1684 R7 回归钉（写侧）：checkpoint 截断物理删除 turn 事件行，首响样本
// 若不随 checkpoint 留存，历史最坏样本就对 benchmarks/experience 的周读数
// 不可见（tl1684 归因 C5：公布 Max 160s，真实 Max ≥180s）。本文件钉住留存
// 池的四条语义：截断携带、跨代继承、去重与有界、Open 回填。

// runTurn 开一个 turn：turn_status（起点）→ sleep → text（首响）→ 终结，
// 返回 (turnID, 首响实测 ms)。
func runTurn(t *testing.T, l *Ledger, respondAfter time.Duration) (string, int64) {
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
	fr := findSample(l.firstResponses, id)
	if fr.TurnID == "" {
		t.Fatalf("turn %s produced no first-response sample", id)
	}
	return id, fr.MS
}

// firstResponsesOf 从盘上 checkpoint 行解析留存池（读侧视角的字段形状）。
func firstResponsesOf(t *testing.T, path string) []firstResponseStat {
	t.Helper()
	data, err := os.ReadFile(store.SessionTurnEventLog(path))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		var header struct {
			RecordType string `json:"recordType"`
		}
		if json.Unmarshal([]byte(line), &header) != nil || header.RecordType != "checkpoint" {
			continue
		}
		var ck struct {
			FirstResponses []firstResponseStat `json:"firstResponses"`
		}
		if err := json.Unmarshal([]byte(line), &ck); err != nil {
			t.Fatalf("checkpoint line: %v", err)
		}
		return ck.FirstResponses
	}
	return nil
}

func findSample(pool []firstResponseStat, turnID string) firstResponseStat {
	for _, fr := range pool {
		if fr.TurnID == turnID {
			return fr
		}
	}
	return firstResponseStat{}
}

// TestLedgerCheckpointCarriesFirstResponseSamples：截断携带 + 跨代继承。
// 三个 turn 各自终结并 ack 后强制 Compact——正文事件行被删，但首响样本必须
// 出现在 checkpoint 行里；重开后继续跑第四个 turn 再 Compact，前三个样本
// 必须仍随新 checkpoint 留存（池跨 checkpoint 继承，不是每代重建）。
func TestLedgerCheckpointCarriesFirstResponseSamples(t *testing.T) {
	path := testSessionPath(t)
	l := openTestLedger(t, path, "session")

	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		id, _ := runTurn(t, l, 20*time.Millisecond)
		ids = append(ids, id)
		if err := l.AcknowledgeProjection(id); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}
	if err := l.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 正文事件行已被截断（"answer" 只存在于被删行中）。
	data, err := os.ReadFile(store.SessionTurnEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("answer")) {
		t.Fatalf("event lines survived compaction; the truncation scenario is not exercised: %s", data)
	}

	pool := firstResponsesOf(t, path)
	if len(pool) != 3 {
		t.Fatalf("checkpoint retained %d first-response samples, want 3: %s", len(pool), data)
	}
	for _, id := range ids {
		fr := findSample(pool, id)
		if fr.TurnID == "" {
			t.Fatalf("turn %s lost its first-response sample through compaction", id)
		}
		if fr.StartedAt <= 0 || fr.MS < 0 {
			t.Fatalf("turn %s sample malformed: %+v", id, fr)
		}
		// 首响 = text 行 createdAt − 首条行 createdAt；sleep 20ms 后应落在
		// [15, 5000]ms（Windows timer 粗精度下限宽松，上限防异常值）。
		if fr.MS < 15 || fr.MS > 5000 {
			t.Fatalf("turn %s first-response ms = %d, want ~20ms", id, fr.MS)
		}
	}

	// 跨代继承：重开后池恢复，第四个 turn 再 Compact，前三个样本不丢。
	l2, err := Open(path, "session")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = l2.Close() })
	if len(l2.firstResponses) != 3 {
		t.Fatalf("pool after reopen = %d, want 3 (checkpoint inheritance)", len(l2.firstResponses))
	}
	id4, _ := runTurn(t, l2, 20*time.Millisecond)
	if err := l2.AcknowledgeProjection(id4); err != nil {
		t.Fatalf("ack 4: %v", err)
	}
	if err := l2.Compact(); err != nil {
		t.Fatalf("Compact gen2: %v", err)
	}
	pool2 := firstResponsesOf(t, path)
	if len(pool2) != 4 {
		t.Fatalf("pool after second compaction = %d, want 4 (earlier generations must be inherited)", len(pool2))
	}
	for _, id := range ids {
		if findSample(pool2, id).TurnID == "" {
			t.Fatalf("generation-1 turn %s dropped by the second compaction", id)
		}
	}
	if findSample(pool2, id4).TurnID == "" {
		t.Fatalf("generation-2 turn %s missing from the pool", id4)
	}
}

// TestLedgerFirstResponseRetentionRules：无首响不录、同 turn 去重、有界丢最老。
func TestLedgerFirstResponseRetentionRules(t *testing.T) {
	path := testSessionPath(t)
	l := openTestLedger(t, path, "session")

	// 无首响的 turn（直接终结）不入池。
	silent, err := l.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
		t.Fatalf("silent terminal: ok=%v err=%v", ok, err)
	}
	if findSample(l.firstResponses, silent).TurnID != "" {
		t.Fatalf("a turn without any model output must not produce a first-response sample")
	}

	// 同 turn 多次响应只录一条（首个）。
	dupe, err := l.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = l.Append(event.Event{Kind: event.TurnStatusChanged, Status: event.TurnInProgress}, event.TurnInProgress)
	if _, ok, err := l.Append(event.Event{Kind: event.Text, Text: "first"}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("first text: %v", err)
	}
	firstAt := l.records[len(l.records)-1].CreatedAt
	time.Sleep(15 * time.Millisecond)
	if _, ok, err := l.Append(event.Event{Kind: event.Text, Text: "second"}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("second text: %v", err)
	}
	secondAt := l.records[len(l.records)-1].CreatedAt
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
		t.Fatalf("terminal: %v", err)
	}
	fr := findSample(l.firstResponses, dupe)
	if fr.TurnID == "" {
		t.Fatalf("responding turn missing from pool")
	}
	want := firstAt - fr.StartedAt
	if fr.MS != want {
		t.Fatalf("sample kept the wrong response: ms=%d, want first-response delta %d (second text at %d)", fr.MS, want, secondAt)
	}

	// 有界：溢出丢最老（构造超过 firstResponseCap 的池；base=1 时再灌
	// cap-base+1 条，把原有的 dupe 与第一条 overflow 都挤出去）。
	base := len(l.firstResponses)
	last := firstResponseCap - base + 1
	for i := 0; i <= last; i++ {
		l.rememberFirstResponseLocked(fmt.Sprintf("overflow-%06d", i), 1000+int64(i), int64(i))
	}
	if len(l.firstResponses) != firstResponseCap {
		t.Fatalf("pool size = %d, want capped at %d", len(l.firstResponses), firstResponseCap)
	}
	if findSample(l.firstResponses, "overflow-000000").TurnID != "" || findSample(l.firstResponses, dupe).TurnID != "" {
		t.Fatalf("overflow must drop the oldest samples first")
	}
	if findSample(l.firstResponses, fmt.Sprintf("overflow-%06d", last)).TurnID == "" {
		t.Fatalf("newest overflow sample missing")
	}
}

// TestLedgerFirstResponseBackfillAtOpen：事件行仍在 live 区的已终结 turn，
// 重开后必须补录入池——它们的下一次 Compact 就会删掉这些行，回填是
// 「Open 之后才合入留存」场景下样本不丢的前提。
func TestLedgerFirstResponseBackfillAtOpen(t *testing.T) {
	path := testSessionPath(t)
	l := openTestLedger(t, path, "session")
	id, _ := runTurn(t, l, 15*time.Millisecond)
	// 不 Compact（事件行留在 live 区），直接重开。
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	l2, err := Open(path, "session")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = l2.Close() })
	fr := findSample(l2.firstResponses, id)
	if fr.TurnID == "" {
		t.Fatalf("terminal turn with live event lines must be backfilled into the pool at Open")
	}
	if fr.MS < 10 || fr.MS > 5000 {
		t.Fatalf("backfilled ms = %d, want ~15ms", fr.MS)
	}

	// 补录后立即 Compact：样本随 checkpoint 留存、正文删除。
	if err := l2.AcknowledgeProjection(id); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := l2.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if findSample(firstResponsesOf(t, path), id).TurnID == "" {
		t.Fatalf("backfilled sample lost by the very compaction that deleted its event lines")
	}
}
