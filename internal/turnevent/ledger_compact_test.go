package turnevent

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/store"
)

// 任务 1172 剩余部分：CompactIdleThrough 的验收测试。
// 逐跳背景见 ledger_compact.go 头注：既有 checkpoint 截断被「ack 供给」卡死，
// 本原语让持有盘上投影证据的调用方显式确认消费义务解除。

// runThreeUnackedTurns writes three completed, unacknowledged turns and
// returns their terminal sequences in order.
func runThreeUnackedTurns(t *testing.T, l *Ledger) []uint64 {
	t.Helper()
	seqs := make([]uint64, 0, 3)
	for i := 0; i < 3; i++ {
		if _, err := l.Begin(); err != nil {
			t.Fatalf("Begin %d: %v", i, err)
		}
		if _, ok, err := l.Append(event.Event{Kind: event.Text, Text: "body-of-turn"}, event.TurnInProgress); err != nil || !ok {
			t.Fatalf("text %d: ok=%v err=%v", i, ok, err)
		}
		done, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted)
		if err != nil || !ok {
			t.Fatalf("terminal %d: ok=%v err=%v", i, ok, err)
		}
		seqs = append(seqs, done.Sequence)
	}
	return seqs
}

func TestLedgerCompactIdleThroughAdvancesPastMissingAcks(t *testing.T) {
	path := testSessionPath(t)
	l := openTestLedger(t, path, "session")
	l.RequireProjectionAck(true)
	seqs := runThreeUnackedTurns(t, l)

	// 逐跳实证：无 ack 时既有 Compact 的 cutoff 卡在 0 —— 这是生产 9.07MB
	// ledger 的原形态（checkpoint 机制健在，触发链断在 ack 供给）。
	if err := l.Compact(); err != nil {
		t.Fatalf("Compact without acks: %v", err)
	}
	if pending := l.PendingProjections(); len(pending) != 3 {
		t.Fatalf("pending after bare Compact = %d, want 3 (nothing may be dropped without proof)", len(pending))
	}

	before, after, err := l.CompactIdleThrough(seqs[len(seqs)-1])
	if err != nil {
		t.Fatalf("CompactIdleThrough: %v", err)
	}
	if after <= 0 || after >= before {
		t.Fatalf("truncation did not shrink the log: before=%d after=%d", before, after)
	}
	if pending := l.PendingProjections(); len(pending) != 0 {
		t.Fatalf("pending after confirmed compaction = %d, want 0", len(pending))
	}
	data, err := os.ReadFile(store.SessionTurnEventLog(path))
	if err != nil {
		t.Fatalf("read compacted ledger: %v", err)
	}
	if !bytes.Contains(data, []byte(`"recordType":"checkpoint"`)) {
		t.Fatalf("compacted ledger lost its checkpoint record: %s", data)
	}
	if bytes.Contains(data, []byte("body-of-turn")) {
		t.Fatalf("confirmed-prefix event bodies survived the checkpoint: %s", data)
	}

	// 读侧契约（上游原生，零改动）：floor 之下重置、floor 之上正常分页。
	replay, err := l.Replay(0)
	if err != nil {
		t.Fatalf("Replay(0): %v", err)
	}
	if !replay.ResetRequired {
		t.Fatalf("ResetRequired = false, want true after checkpoint truncation")
	}
	if replay.FloorSequence != seqs[len(seqs)-1]+1 || replay.NextAfterSequence != seqs[len(seqs)-1] {
		t.Fatalf("floor contract = floor:%d next:%d, want floor:%d next:%d",
			replay.FloorSequence, replay.NextAfterSequence, seqs[len(seqs)-1]+1, seqs[len(seqs)-1])
	}

	// 幂等：同值重复确认不再重写。
	_, afterAgain, err := l.CompactIdleThrough(seqs[len(seqs)-1])
	if err != nil {
		t.Fatalf("repeat CompactIdleThrough: %v", err)
	}
	if afterAgain != after {
		t.Fatalf("repeat compaction rewrote the log: %d -> %d", after, afterAgain)
	}
}

// TestCompactSessionLedgerWithProvesOnlyTheConfirmedPrefix drives the
// sweep-facing entry: the proof callback confirms the first two pending turns
// and withholds the third, so the compaction must stop exactly there and a
// rerun with full proof truncates the rest.
func TestCompactSessionLedgerWithProvesOnlyTheConfirmedPrefix(t *testing.T) {
	path := testSessionPath(t)
	l := openTestLedger(t, path, "session")
	l.RequireProjectionAck(true)
	runThreeUnackedTurns(t, l)
	turnIDs, err := l.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	confirmed := map[string]bool{}
	for i, rec := range turnIDs {
		if rec.Status.Terminal() && i < 4 { // the first two turns' terminal records
			confirmed[rec.TurnID] = true
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	proved := 0
	before, after, err := CompactSessionLedgerWith(path, "session", func(turnID string) bool {
		proved++
		return confirmed[turnID]
	})
	if err != nil {
		t.Fatalf("CompactSessionLedgerWith: %v", err)
	}
	if proved != 3 {
		t.Fatalf("proof callback consulted %d times, want 3 (two confirmations plus the refusal that stops the walk)", proved)
	}
	if after <= 0 {
		t.Fatalf("compaction lost the log: before=%d after=%d", before, after)
	}
	data, err := os.ReadFile(store.SessionTurnEventLog(path))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if !bytes.Contains(data, []byte(`"recordType":"checkpoint"`)) {
		t.Fatalf("compacted ledger lost its checkpoint: %s", data)
	}

	// Full proof: rerun confirms the remaining turn and the tail goes too.
	before2, after2, err := CompactSessionLedgerWith(path, "session", func(string) bool { return true })
	if err != nil {
		t.Fatalf("full-proof rerun: %v", err)
	}
	if after2 >= before2 {
		t.Fatalf("full-proof rerun did not truncate the tail: before=%d after=%d", before2, after2)
	}
	if before3, after3, err := CompactSessionLedgerWith(path, "session", func(string) bool { return false }); err != nil || after3 != after2 || before3 != after2 {
		t.Fatalf("idempotent rerun = (%d,%d,%v), want (x,%d,nil)", before3, after3, err, after2)
	}
}

func TestLedgerCompactIdleThroughKeepsUnconfirmedTurnsBelowThrough(t *testing.T) {
	path := testSessionPath(t)
	l := openTestLedger(t, path, "session")
	l.RequireProjectionAck(true)
	seqs := runThreeUnackedTurns(t, l)

	// 调用方只证明到第二个 turn：第三个 turn 的消费义务未解除，其事件必须
	// 原样保留在盘上（红线：未确认的一条都不能丢）。
	if _, _, err := l.CompactIdleThrough(seqs[1]); err != nil {
		t.Fatalf("CompactIdleThrough(seqs[1]): %v", err)
	}
	pending := l.PendingProjections()
	if len(pending) != 1 || pending[0].TurnID == "" {
		t.Fatalf("pending after partial confirmation = %+v, want exactly the third turn", pending)
	}
	data, err := os.ReadFile(store.SessionTurnEventLog(path))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if bytes.Contains(data, []byte(`"recordType":"checkpoint"`)) && bytes.Count(data, []byte("body-of-turn")) != 1 {
		t.Fatalf("unconfirmed turn records were not retained: %s", data)
	}
}

// TestLedgerCompactIdleThroughShrinksPastNonTerminalRecord pins the guard that
// keeps a mid-history non-terminal turn on disk: the cutoff rewrite drops
// everything at or below the cutoff, so an unshrunk through would discard a
// turn still waiting on the user. The Open path converts orphans to
// interrupted before any compaction can run, so the raw shape is only
// reachable by constructing the state directly (package-internal test).
func TestLedgerCompactIdleThroughShrinksPastNonTerminalRecord(t *testing.T) {
	l := &Ledger{projectionCommittedThrough: 0, compactBytes: 1, compactEvents: 1}
	l.records = []Envelope{
		{Sequence: 1, TurnID: "a", Status: event.TurnCompleted},
		{Sequence: 2, TurnID: "b", Status: event.TurnWaitingUser},
		{Sequence: 3, TurnID: "b", Status: event.TurnWaitingUser},
		{Sequence: 4, TurnID: "c", Status: event.TurnCompleted},
	}
	if got := l.shrinkThroughPastNonTerminal(4); got != 1 {
		t.Fatalf("shrink(4) = %d, want 1 (turn b's first record sits at 2)", got)
	}
	if got := l.shrinkThroughPastNonTerminal(1); got != 1 {
		t.Fatalf("shrink(1) = %d, want 1 (only the terminal turn is under the range)", got)
	}
	l.records = []Envelope{{Sequence: 2, TurnID: "b", Status: event.TurnWaitingUser}}
	if got := l.shrinkThroughPastNonTerminal(2); got != 1 {
		t.Fatalf("shrink(2) = %d, want 1", got)
	}
}

// TestLedgerCompactIdleThroughConvertsOrphanThenCompacts walks the on-disk
// shape a crashed writer leaves behind: an orphaned non-terminal turn in the
// middle of the log. Open must convert it to a terminal interrupted record
// first; the confirmed compaction may then treat it like any other terminal
// turn under the confirmed range.
func TestLedgerCompactIdleThroughConvertsOrphanThenCompacts(t *testing.T) {
	path := testSessionPath(t)
	l := openTestLedger(t, path, "session")
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	done, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted)
	if err != nil || !ok {
		t.Fatalf("terminal: ok=%v err=%v", ok, err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ledgerPath := store.SessionTurnEventLog(path)
	f, err := os.OpenFile(ledgerPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open ledger file: %v", err)
	}
	orphan := `{"schemaVersion":2,"recordType":"event","sessionId":"session","turnId":"turn_orphan","seq":` + strconv.Itoa(int(done.Sequence)+1) + `,"kind":"text","status":"in_progress","createdAt":1,"event":{"kind":"text","text":"half-written"}}` + "\n"
	if _, err := f.WriteString(orphan); err != nil {
		t.Fatalf("append orphan record: %v", err)
	}
	_ = f.Close()

	reopened := openTestLedger(t, path, "session")
	seqs := []uint64{}
	for _, rec := range func() []Envelope {
		recs, err := reopened.EventsAfter(0)
		if err != nil {
			t.Fatalf("EventsAfter: %v", err)
		}
		return recs
	}() {
		seqs = append(seqs, rec.Sequence)
	}
	if len(seqs) < 3 {
		t.Fatalf("records = %v, want the interrupted recovery plus the orphan", seqs)
	}
	before, after, err := reopened.CompactIdleThrough(seqs[len(seqs)-1])
	if err != nil {
		t.Fatalf("CompactIdleThrough: %v", err)
	}
	if after <= 0 {
		t.Fatalf("compaction lost the checkpointed log: before=%d after=%d", before, after)
	}
	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if strings.Contains(string(data), "half-written") {
		t.Fatalf("converted orphan survived the confirmed compaction: %s", data)
	}
}

