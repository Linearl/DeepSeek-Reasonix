package pendingcards

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 验收锚（任务 408）：入队即持久、三态闭环、TTL 超时、容量封顶、损坏自愈。

func testCard(id string) Card {
	return Card{ID: id, Session: "s.jsonl", CreatedAt: time.Now(), Kind: KindAsk, Summary: "选哪个库", TurnID: "t1"}
}

func TestEnqueuePersistsAndReloadSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	q, err := Open(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testCard("p1")); err != nil {
		t.Fatal(err)
	}
	// 重启等价：重新 Open 读到同一张卡（不随 turn 滚动丢失）。
	reopened, err := Open(session)
	if err != nil {
		t.Fatal(err)
	}
	pending := reopened.Pending(time.Now(), time.Hour)
	if len(pending) != 1 || pending[0].ID != "p1" || pending[0].Kind != KindAsk {
		t.Fatalf("pending after reopen = %+v", pending)
	}
	if pending[0].State != StatePending {
		t.Fatalf("state = %q, want pending", pending[0].State)
	}
}

func TestEnqueueSameIDIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testCard("p1")); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testCard("p1")); err != nil {
		t.Fatal(err)
	}
	if got := q.CountPending(); got != 1 {
		t.Fatalf("pending = %d, want 1 (dedup by prompt id)", got)
	}
}

func TestThreeStateClosure(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := q.Enqueue(testCard(id)); err != nil {
			t.Fatal(err)
		}
	}
	// 批完
	if err := q.Settle("a", StateResolved, "answered"); err != nil {
		t.Fatal(err)
	}
	// 撤回
	if err := q.Settle("b", StateWithdrawn, "user_withdrew"); err != nil {
		t.Fatal(err)
	}
	// 超时
	if err := q.Settle("c", StateTimeout, "wait_expired"); err != nil {
		t.Fatal(err)
	}
	if got := q.CountPending(); got != 0 {
		t.Fatalf("pending = %d, want 0 after three-state closure", got)
	}
	states := map[string]string{}
	for _, c := range q.All() {
		states[c.ID] = c.State + ":" + c.Outcome
	}
	if states["a"] != StateResolved+":answered" || states["b"] != StateWithdrawn+":user_withdrew" || states["c"] != StateTimeout+":wait_expired" {
		t.Fatalf("closure states = %v", states)
	}
	// 首次结算获胜：迟到重复结算不得覆盖（批完后撤回不成立）。
	if err := q.Settle("a", StateWithdrawn, "late"); err != nil {
		t.Fatal(err)
	}
	for _, c := range q.All() {
		if c.ID == "a" && (c.State != StateResolved || c.Outcome != "answered") {
			t.Fatalf("first settlement was overwritten: %+v", c)
		}
	}
	// 未知 id
	if err := q.Settle("zz", StateResolved, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("settle unknown = %v, want ErrNotFound", err)
	}
	// 非法终态
	if err := q.Settle("a", StatePending, "x"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("settle pending = %v, want ErrInvalidState", err)
	}
}

func TestSweepExpiredMarksTimeout(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	old := testCard("old")
	old.CreatedAt = time.Now().Add(-2 * time.Hour)
	if err := q.Enqueue(old); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testCard("new")); err != nil {
		t.Fatal(err)
	}
	n, err := q.SweepExpired(time.Now(), 30*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want 1", n, err)
	}
	pending := q.Pending(time.Now(), 30*time.Minute)
	if len(pending) != 1 || pending[0].ID != "new" {
		t.Fatalf("pending after sweep = %+v", pending)
	}
	for _, c := range q.All() {
		if c.ID == "old" && (c.State != StateTimeout || c.Outcome != "ttl_expired") {
			t.Fatalf("expired card = %+v", c)
		}
	}
}

func TestCapacityDropsOldestSettledFirst(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// 填满：DefaultMaxCards 张，其中最早的 10 张已结算。
	for i := 0; i < DefaultMaxCards; i++ {
		id := string(rune('a'+i%26)) + time.Duration(i).String()
		if err := q.Enqueue(Card{ID: id, Kind: KindApproval, CreatedAt: time.Now().Add(time.Duration(i) * time.Second), Summary: "s"}); err != nil {
			t.Fatal(err)
		}
		if i < 10 {
			if err := q.Settle(id, StateResolved, "answered"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 再入队 1 张：应挤掉最老的已结算卡而不是拒绝。
	if err := q.Enqueue(Card{ID: "fresh", Kind: KindAsk, CreatedAt: time.Now(), Summary: "s"}); err != nil {
		t.Fatalf("enqueue over settled cards: %v", err)
	}
	if got := len(q.All()); got > DefaultMaxCards {
		t.Fatalf("cards = %d, want <= %d", got, DefaultMaxCards)
	}
	found := false
	for _, c := range q.All() {
		if c.ID == "fresh" {
			found = true
		}
	}
	if !found {
		t.Fatal("fresh card was dropped")
	}
}

func TestCapacityRefusesWhenAllPending(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < DefaultMaxCards; i++ {
		if err := q.Enqueue(Card{ID: time.Duration(i).String(), Kind: KindApproval, CreatedAt: time.Now(), Summary: "s"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Enqueue(Card{ID: "overflow", Kind: KindAsk, CreatedAt: time.Now(), Summary: "s"}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("enqueue = %v, want ErrCapacity", err)
	}
}

func TestCorruptFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(FileNameFor(session), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := Open(session)
	if err != nil {
		t.Fatalf("corrupt file must not fail open: %v", err)
	}
	if q.CountPending() != 0 {
		t.Fatalf("corrupt file left cards behind")
	}
	// 且可继续写入（自愈）。
	if err := q.Enqueue(testCard("p1")); err != nil {
		t.Fatal(err)
	}
}

func TestNewerSchemaIgnored(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	data := `{"schemaVersion":99,"cards":[{"id":"x","kind":"ask","state":"pending","createdAt":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(FileNameFor(session), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := Open(session)
	if err != nil {
		t.Fatal(err)
	}
	if q.CountPending() != 0 {
		t.Fatal("newer schema must be ignored, not misread")
	}
}

func TestEmptySessionPathRefused(t *testing.T) {
	if _, err := Open("  "); !errors.Is(err, ErrNoSession) {
		t.Fatalf("open empty = %v, want ErrNoSession", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := time.Duration(i).String()
			_ = q.Enqueue(Card{ID: id, Kind: KindAsk, CreatedAt: time.Now(), Summary: "s"})
			_ = q.Settle(id, StateResolved, "answered")
			_ = q.Pending(time.Now(), time.Hour)
			_ = q.CountPending()
		}(i)
	}
	wg.Wait()
	if got := len(q.All()); got != 16 {
		t.Fatalf("cards = %d, want 16 (no lost updates)", got)
	}
}
