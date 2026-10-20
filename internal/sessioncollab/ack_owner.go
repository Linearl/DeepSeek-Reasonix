package sessioncollab

// 任务1017 批C-E（2026-10-20）：Ack 归属方日志（ownership journal）。
//
// 2026-10-20 活体取证报告的机制级结论 #2：Claim 刻意非独占（two-phase
// Claim→Ack，at-least-once），「谁先 Ack」决定消息对所有其他消费者隐身——
// 原派单 msg_eb2afe706c017ffe 在 seen 游标里、接收方却从未见过，正是这个
// 前提的现场，而现场里没有任何「由谁结算」的痕迹可查。本切片让每次 Ack
// 留下归属方（进程 pid + 消费路径标签），「已读未开工」从此有了第一手
// forensic 证据；「被他人 Ack 时不得对本方隐藏」的读侧完整方案超出本切片，
// 等归属数据在真实现场积累后再定形态（先观测、后行为，与批B 同一纪律）。
//
// 形态：<contact>.ack.jsonl，一次 Ack 调用一行（批量 ids 合并一行）：
//   {"at":"2026-10-20T12:00:00.000+08:00","pid":123,"owner":"pump","ids":["msg_a"]}
// 与 seen 游标同锁追加（ackLocked，B1 的 same-lock 纪律不变）；追加失败只
// Warn 不回滚——游标才是投递契约，journal 是纯取证面，绝不让诊断写失败
// 把投递本身变成失败。目录扫描不受影响：既有的枚举都只认 .inbox.jsonl。

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Ack journal owner labels for the four production consumers. Distinct tags
// are the whole point: a stranded message's "already settled" state must be
// attributable to the exact path that settled it.
const (
	AckOwnerUnattributed     = "unattributed"       // legacy Ack() entry point
	AckOwnerPump             = "pump"               // desktop delivery pump (Claim→deliver→Ack)
	AckOwnerDrainInbox       = "drain_inbox"        // drain_inbox tool batch settle (same-lock Drain)
	AckOwnerAwaitReply       = "await_reply"        // AwaitReplyContext thread consumption
	AckOwnerCollabInboxIndex = "collab_inbox_index" // aggregate panel decide/clean path
)

// AckJournalEntry is one Ack call's ownership record.
type AckJournalEntry struct {
	At    string   `json:"at"`
	Pid   int      `json:"pid"`
	Owner string   `json:"owner"`
	IDs   []string `json:"ids"`
}

func (s *MailStore) ackJournalPath(contactID string) string {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		contactID = "unknown"
	}
	return filepath.Join(s.root, contactID+".ack.jsonl")
}

// AckWithOwner is Ack with the consuming path named. Same cursor semantics as
// Ack; the only addition is the ownership journal line.
func (s *MailStore) AckWithOwner(ctx context.Context, owner, contactID string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	if strings.TrimSpace(owner) == "" {
		owner = AckOwnerUnattributed
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.ackLockedWithOwner(contactID, owner, ids...)
}

// Ack advances the cursor for IDs whose delivery is settled — delivered
// successfully, or refused for good. Not acking leaves them for the next pass.
// The journal records the owner as "unattributed"; production consumers should
// prefer AckWithOwner so a 现场 can be attributed.
func (s *MailStore) Ack(ctx context.Context, contactID string, ids ...string) error {
	return s.AckWithOwner(ctx, AckOwnerUnattributed, contactID, ids...)
}

// ackLockedWithOwner is AckWithOwner's body under the caller's lock
// (B1: same-lock Claim+Ack). Cursor first (the contract), journal second
// (the forensic surface, best-effort by contract).
func (s *MailStore) ackLockedWithOwner(contactID, owner string, ids ...string) error {
	seen := s.readCursor(contactID)
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			seen[id] = true
			cleaned = append(cleaned, id)
		}
	}
	if err := s.writeCursor(contactID, seen); err != nil {
		return err
	}
	if len(cleaned) == 0 {
		return nil
	}
	entry := AckJournalEntry{
		At:    time.Now().Format("2006-01-02T15:04:05.000Z07:00"), // RFC3339 毫秒
		Pid:   os.Getpid(),
		Owner: owner,
		IDs:   cleaned,
	}
	if err := appendJSONL(s.ackJournalPath(contactID), entry); err != nil {
		// Diagnostic surface only: never fail the Ack over a journal write.
		slog.Warn("session collab mail: ack ownership journal append failed (cursor already advanced)",
			"path", s.ackJournalPath(contactID), "owner", owner, "ids", len(cleaned), "err", err)
	}
	return nil
}

// AckJournal reads one contact's ownership journal (oldest first). Purely
// diagnostic: an absent or unreadable file reads as an empty journal, and a
// corrupt line is skipped — the reader must never become a new failure surface.
func (s *MailStore) AckJournal(contactID string) []AckJournalEntry {
	b, err := os.ReadFile(s.ackJournalPath(contactID))
	if err != nil {
		return nil
	}
	var out []AckJournalEntry
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e AckJournalEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}
