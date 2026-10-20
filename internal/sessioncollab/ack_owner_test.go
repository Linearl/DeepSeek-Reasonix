package sessioncollab

// 任务1017 批C-E（2026-10-20）：Ack 归属方日志的契约测试。
// 现场缺口：Claim 非独占 + 先到先 Ack ⇒「已读未开工」时查不到由谁结算
// （原派单 msg_eb2afe706c017ffe 在 seen 里但接收方从未见过，零归属痕迹）。
// 钉死：归属标签逐路径可辨、游标语义不变、journal 失败不拖垮投递本身。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deliverForAck(t *testing.T, s *MailStore, from, to string) MailMessage {
	t.Helper()
	m, err := s.Deliver(context.Background(), MailMessage{From: from, To: to, Body: "ack-owner-probe"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestAckWithOwnerJournalsOwner：AckWithOwner 写一行归属（pid/owner/ids），
// 游标照常推进；旧 Ack 入口归为 unattributed；多次 Ack 按序累积。
func TestAckWithOwnerJournalsOwner(t *testing.T) {
	s := NewMailStore(t.TempDir())
	m1 := deliverForAck(t, s, "sc_from", "sc_me")
	m2 := deliverForAck(t, s, "sc_from", "sc_me")

	if err := s.AckWithOwner(context.Background(), AckOwnerPump, "sc_me", m1.ID); err != nil {
		t.Fatal(err)
	}
	if !s.Settled("sc_me", m1.ID) {
		t.Fatal("cursor semantics must be unchanged by the journal")
	}
	if err := s.Ack(context.Background(), "sc_me", m2.ID); err != nil {
		t.Fatal(err)
	}
	journal := s.AckJournal("sc_me")
	if len(journal) != 2 {
		t.Fatalf("two acks must leave two journal lines, got %d", len(journal))
	}
	if journal[0].Owner != AckOwnerPump || len(journal[0].IDs) != 1 || journal[0].IDs[0] != m1.ID {
		t.Fatalf("first entry must name the pump owner and the id, got %+v", journal[0])
	}
	if journal[1].Owner != AckOwnerUnattributed {
		t.Fatalf("the legacy Ack entry point must record unattributed, got %q", journal[1].Owner)
	}
	if journal[0].Pid != os.Getpid() {
		t.Fatalf("the journal must record the acking process pid, got %d", journal[0].Pid)
	}
	if !strings.Contains(journal[0].At, "T") {
		t.Fatalf("the journal timestamp must be RFC3339-like, got %q", journal[0].At)
	}
}

// TestDrainJournalsOwner：drain_inbox 的同锁批量结算必须带上自己的标签——
// 这是「泵 vs drain_inbox 谁先结算」现场的第一手判据。
func TestDrainJournalsOwner(t *testing.T) {
	s := NewMailStore(t.TempDir())
	m := deliverForAck(t, s, "sc_from", "sc_me")
	err := s.Drain(context.Background(), "sc_me", func(pending, refused []MailMessage) []string {
		return []string{m.ID}
	})
	if err != nil {
		t.Fatal(err)
	}
	journal := s.AckJournal("sc_me")
	if len(journal) != 1 || journal[0].Owner != AckOwnerDrainInbox {
		t.Fatalf("drain must journal its owner tag, got %+v", journal)
	}
}

// TestAckJournalFailureDoesNotFailDelivery：journal 是取证面，写失败（这里
// 让同路径变成目录）绝不能把 Ack 本身变成失败——游标已推进，回滚只会制造
// 重复投递风险。
func TestAckJournalFailureDoesNotFailDelivery(t *testing.T) {
	s := NewMailStore(t.TempDir())
	m := deliverForAck(t, s, "sc_from", "sc_me")
	if err := os.MkdirAll(filepath.Join(s.Dir(), "sc_me.ack.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.AckWithOwner(context.Background(), AckOwnerPump, "sc_me", m.ID); err != nil {
		t.Fatalf("a journal write failure must never fail the Ack, got %v", err)
	}
	if !s.Settled("sc_me", m.ID) {
		t.Fatal("the cursor must still advance when the journal cannot be written")
	}
}

// TestAckJournalReaderNeverFailsOnCorruptLine：读侧对缺行/坏行保持沉默——
// 取证面自己不能变成新的失败面。
func TestAckJournalReaderNeverFailsOnCorruptLine(t *testing.T) {
	s := NewMailStore(t.TempDir())
	m := deliverForAck(t, s, "sc_from", "sc_me")
	_ = s.AckWithOwner(context.Background(), AckOwnerPump, "sc_me", m.ID)
	f := s.ackJournalPath("sc_me")
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, append(b, []byte("not-json-at-all\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := s.AckJournal("sc_me")
	if len(journal) != 1 {
		t.Fatalf("a corrupt line must be skipped, got %d entries", len(journal))
	}
	if s.AckJournal("sc_absent") != nil {
		t.Fatal("an absent journal reads as empty, not as an error")
	}
}
