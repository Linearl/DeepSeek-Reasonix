package collabinbox

// 任务 980 ③ 验收（数据层）：详情弹窗的全文读口——
//   ① 正常读：按 id 取回完整正文（多行不截断），元数据与列表行一致，preview
//      仍是首行速览（列表速览语义不变）；
//   ② 诚实失败：未知 id 报错（不是空正文）；空 id 报错；
//   ③ 降级诚实：mail 锁繁忙（History 降级空行集）→ 报错而非「not found」——
//      弹窗必须把「读不到」与「没有这封信」区分开（与 587 空/败分离同口径）。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/sessioncollab"
)

func TestReadMailReturnsCompleteBody(t *testing.T) {
	store, mail := fixtureStore(t)
	now := store.now()
	body := "第一行速览\n\n第二段正文（preview 取不到的部分）\n" + strings.Repeat("详情必须看到这一段。", 20)
	got := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_bob", Body: body, At: now,
	})

	detail, err := store.ReadMail(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("ReadMail: %v", err)
	}
	if detail.ID != got.ID || detail.From != "sc_alice" || detail.To != "sc_bob" || detail.At != got.At {
		t.Fatalf("detail metadata must mirror the list entry: %+v", detail)
	}
	if detail.Body != body {
		t.Fatalf("body must be the COMPLETE original text (got %d runes, want %d)", len(detail.Body), len(body))
	}
	if !strings.Contains(detail.Preview, "第一行速览") {
		t.Fatalf("preview stays the triage first line: %q", detail.Preview)
	}
	if strings.Contains(detail.Preview, "第二段正文") {
		t.Fatalf("preview must NOT carry the full body — the list stays triage-only: %q", detail.Preview)
	}
}

func TestReadMailUnknownAndEmptyIDError(t *testing.T) {
	store, _ := fixtureStore(t)
	if _, err := store.ReadMail(context.Background(), "m_missing"); err == nil {
		t.Fatal("reading an unknown id must error — an empty detail would render as empty body in the popup")
	}
	if _, err := store.ReadMail(context.Background(), ""); err == nil {
		t.Fatal("empty id must error")
	}
}

func TestReadMailLockBusyErrorsInsteadOfNotFound(t *testing.T) {
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_me", Body: "real data on disk"})

	// 楔住 .mail.lock（独占）：inbox 锁健康，只有传输层读降级为空行集。
	wedge, err := filelock.TryAcquire(filepath.Join(store.mailDir, ".mail.lock"))
	if err != nil {
		t.Fatalf("wedge the mail lock: %v", err)
	}
	defer wedge()

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	_, err = store.ReadMail(ctx, "any_id")
	if err == nil {
		t.Fatal("a lock-busy transport read must surface as an error, not as not-found/empty body")
	}
	if !strings.Contains(err.Error(), "busy") {
		t.Fatalf("the degraded error must say the store is busy (retryable), got: %v", err)
	}
}
