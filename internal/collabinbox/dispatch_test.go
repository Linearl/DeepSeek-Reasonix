package collabinbox

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// sendFixture 记一条发件日志（RecordSent，task 175 的写入口）。
func sendFixture(t *testing.T, mail *sessioncollab.MailStore, msg sessioncollab.MailMessage) sessioncollab.MailMessage {
	t.Helper()
	out, err := mail.Deliver(context.Background(), msg)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	mail.RecordSent(context.Background(), out, "")
	return out
}

// TestSentDispatchFourStates 逐态钉死映射（task 1696 验收②）：
// 等待（无回执/queued_followup）·进行中（injected/failed_retrying）·
// 已回信（threadId 回信链）·已拒绝（refused_* 终局）。
func TestSentDispatchFourStates(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()

	// 等待：无回执（还没赶上第一次 pump）。
	waiting := sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_bob", Body: "waiting body", At: 1000,
	})
	// 等待：queued_followup（降级排队）。
	queued := sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_bob", Body: "queued body", At: 2000,
	})
	// 进行中：injected（对方已收、尚未回信）。
	injected := sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_carol", Body: "injected body", At: 3000,
	})
	// 进行中：failed_retrying（投递失败仍在重试，非终局）。
	retrying := sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_bob", Body: "retrying body", At: 4000,
	})
	// 已拒绝：refused_provenance（终局）。
	refused := sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_bob", To: "sc_alice", Body: "refused body", At: 5000,
	})
	// 已回信：carol 对 injected 的信回了（threadId == 原消息 ID）。
	replied := sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_carol", Body: "replied body", At: 6000,
	})

	mustRecord := func(id, outcome string, at int64) {
		t.Helper()
		if err := mail.RecordDeliveryReceipt(ctx, sessioncollab.DeliveryReceipt{
			MessageID: id, To: "sc_bob", Outcome: outcome, At: at,
		}); err != nil {
			t.Fatalf("record receipt %s: %v", id, err)
		}
	}
	mustRecord(queued.ID, sessioncollab.ReceiptQueuedFollowup, 2100)
	mustRecord(injected.ID, sessioncollab.ReceiptInjected, 3100)
	mustRecord(retrying.ID, sessioncollab.ReceiptFailedRetrying, 4100)
	mustRecord(refused.ID, sessioncollab.ReceiptRefusedProvenance, 5100)
	// 回信：Deliver 一封 threadId 指回 replied 的信（进 carol 的 inbox）。
	deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_carol", To: "sc_alice", Body: "here is the answer",
		ThreadID: replied.ID, At: 6100,
	})

	snap, err := store.SentDispatch(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Degraded {
		t.Fatal("healthy store must not read degraded")
	}
	if snap.Total != 6 {
		t.Fatalf("total = %d, want 6", snap.Total)
	}
	statusOf := func(id string) SentDispatchRow {
		t.Helper()
		for _, r := range snap.Rows {
			if r.ID == id {
				return r
			}
		}
		t.Fatalf("row %s missing from dispatch view", id)
		return SentDispatchRow{}
	}
	cases := []struct {
		id   string
		want string
	}{
		{waiting.ID, DispatchWaiting},
		{queued.ID, DispatchWaiting},
		{injected.ID, DispatchInFlight},
		{retrying.ID, DispatchInFlight},
		{refused.ID, DispatchRefused},
		{replied.ID, DispatchReplied},
	}
	for _, tc := range cases {
		if got := statusOf(tc.id).Status; got != tc.want {
			t.Errorf("status(%s) = %q, want %q", tc.id, got, tc.want)
		}
	}
	repliedRow := statusOf(replied.ID)
	if !strings.Contains(repliedRow.ReplyPreview, "here is the answer") {
		t.Errorf("reply preview = %q, want the answer text", repliedRow.ReplyPreview)
	}
	if repliedRow.ReplyAt != 6100 {
		t.Errorf("replyAt = %d, want 6100", repliedRow.ReplyAt)
	}
	// Newest first（验收②：时间戳逐行可见，最新在上）。
	if snap.Rows[0].ID != replied.ID {
		t.Errorf("first row = %s, want newest (%s)", snap.Rows[0].ID, replied.ID)
	}
}

// TestSentDispatchUnknownOutcomeFailsSafeToWaiting：未知 outcome 绝不判成
// 已拒绝——拒绝是对另一方的断言，词表拼错不得制造它（receipt.go fail-safe
// 方向同源：静默侧可以多收，断言侧不许多判）。
func TestSentDispatchUnknownOutcomeFailsSafeToWaiting(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	msg := sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_bob", Body: "x", At: 1000,
	})
	err := mail.RecordDeliveryReceipt(ctx, sessioncollab.DeliveryReceipt{
		MessageID: msg.ID, To: "sc_bob", Outcome: "some_future_outcome", At: 1100,
	})
	// 第一道防线：写入边界拒绝未知 outcome（ValidDeliveryReceiptOutcome）。
	// 若未来词表放宽放它进来，下面第二道防线（SentDispatch fail-safe 到
	// waiting）也必须成立，绝不 manufacture 一个「已拒绝」。
	if err == nil {
		snap, derr := store.SentDispatch(ctx, 0)
		if derr != nil {
			t.Fatal(derr)
		}
		for _, r := range snap.Rows {
			if r.ID == msg.ID && r.Status != DispatchWaiting {
				t.Errorf("unknown outcome status = %q, want %q", r.Status, DispatchWaiting)
			}
		}
	}
	snap, err := store.SentDispatch(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Rows {
		if r.ID == msg.ID && r.Status != DispatchWaiting {
			t.Errorf("unknown outcome status = %q, want %q", r.Status, DispatchWaiting)
		}
	}
}

// TestSentDispatchRolesResolveAtReadTime：角色字段在读取时解析
// （human/main/sub/heartbeat/system 五值集），未知 contact 诚实缺席。
func TestSentDispatchRolesResolveAtReadTime(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, func(contact string) string {
		switch contact {
		case "sc_main":
			return sessioncollab.IdentityMain
		case "sc_sub":
			return sessioncollab.IdentitySub
		default:
			return ""
		}
	})
	mail := sessioncollab.NewMailStore(dir)
	sendFixture(t, mail, sessioncollab.MailMessage{
		From: "sc_main", To: "sc_sub", Body: "dispatch", At: 1000,
	})
	snap, err := store.SentDispatch(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(snap.Rows))
	}
	row := snap.Rows[0]
	if row.FromRole != sessioncollab.IdentityMain || row.ToRole != sessioncollab.IdentitySub {
		t.Errorf("roles = %q→%q, want main→sub", row.FromRole, row.ToRole)
	}
}

// TestSentDispatchLimitAndTruncation：limit 页纪律（默认 50、硬顶 500、
// truncated 标记）与 AllSent 的 newest-first 排序一致。
func TestSentDispatchLimitAndTruncation(t *testing.T) {
	store, mail := fixtureStore(t)
	for i := 0; i < 5; i++ {
		sendFixture(t, mail, sessioncollab.MailMessage{
			From: "sc_alice", To: "sc_bob", Body: "m", At: int64(1000+i),
		})
	}
	snap, err := store.SentDispatch(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 5 || snap.Returned != 3 || !snap.Truncated {
		t.Fatalf("total=%d returned=%d truncated=%v, want 5/3/true",
			snap.Total, snap.Returned, snap.Truncated)
	}
	if snap.Rows[0].At != 1004 {
		t.Errorf("first row at=%d, want 1004 (newest first)", snap.Rows[0].At)
	}
}

// TestSentDispatchEmptyStoreIsEmpty：无发件记录 = 诚实空态（零行、零标记），
// 前端据此渲染空态而非报错。
func TestSentDispatchEmptyStoreIsEmpty(t *testing.T) {
	store, _ := fixtureStore(t)
	snap, err := store.SentDispatch(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 0 || snap.Returned != 0 || len(snap.Rows) != 0 || snap.Degraded || snap.Truncated {
		t.Fatalf("empty store snapshot = %+v, want zero-value rows", snap)
	}
}

// TestValidDispatchStatus：四态词表封闭——前端徽标只渲染这四个值。
func TestValidDispatchStatus(t *testing.T) {
	for _, s := range []string{DispatchWaiting, DispatchInFlight, DispatchReplied, DispatchRefused} {
		if !ValidDispatchStatus(s) {
			t.Errorf("ValidDispatchStatus(%q) = false", s)
		}
	}
	for _, s := range []string{"", "pending", "PENDING", "refused_provenance"} {
		if ValidDispatchStatus(s) {
			t.Errorf("ValidDispatchStatus(%q) = true, want false", s)
		}
	}
}

// TestAllSentAggregatesEverySender：AllSent 聚合所有发送方的 sent 日志
// （History 的发件侧对偶），legacy 行（无 fromContactId 字段）归文件名属主。
func TestAllSentAggregatesEverySender(t *testing.T) {
	_, mail := fixtureStore(t)
	sendFixture(t, mail, sessioncollab.MailMessage{From: "sc_alice", To: "sc_bob", Body: "a1", At: 1000})
	sendFixture(t, mail, sessioncollab.MailMessage{From: "sc_bob", To: "sc_alice", Body: "b1", At: 2000})
	sendFixture(t, mail, sessioncollab.MailMessage{From: "sc_alice", To: "sc_carol", Body: "a2", At: 3000})
	sent, degraded := mail.AllSent(context.Background())
	if degraded {
		t.Fatal("healthy store must not read degraded")
	}
	if len(sent) != 3 {
		t.Fatalf("len = %d, want 3", len(sent))
	}
	if sent[0].At != 3000 {
		t.Errorf("first at = %d, want 3000 (newest first)", sent[0].At)
	}
}
