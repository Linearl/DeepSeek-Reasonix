package main

// 任务620（立即清理）：面板「立即清理」按钮的后端契约——
//  1. 点击即执行一次完整写侧维护（保留期按龄 prune + 清理规则按会话存在性
//     prune），不走任务511的五分钟节流闸（面板打开路径可能被节流跳过，
//     按钮路径不允许——用户点了就要执行）；
//  2. 默认清理规则（never）下只有保留期半边生效；
//  3. 规则半边按当前所选规则执行（sender 规则 + 发方已删除 → 该清）；
//  4. 返回的快照是清理后的新默认视图（行数与磁盘一致）。

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

func TestCleanCollabMailNowRetention(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	deliver := func(msg sessioncollab.MailMessage) sessioncollab.MailMessage {
		t.Helper()
		out, err := mail.Deliver(context.Background(), msg)
		if err != nil {
			t.Fatalf("deliver %q: %v", msg.Body, err)
		}
		return out
	}
	deliver(sessioncollab.MailMessage{From: "sc_alice", To: "sc_main", Body: "fresh one"})
	deliver(sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "aged beyond the 7d retention",
		At: time.Now().Add(-8 * 24 * time.Hour).UnixMilli(),
	})

	res, err := app.CleanCollabMailNow()
	if err != nil {
		t.Fatalf("clean now: %v", err)
	}
	// 默认规则 never → 只保留期半边生效：恰好清掉超龄那封。
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want 1 (the aged entry)", res.Removed)
	}
	if len(res.Snapshot.Entries) != 1 || res.Snapshot.Entries[0].Preview != "fresh one" {
		t.Fatalf("snapshot entries = %d, want the single fresh entry", len(res.Snapshot.Entries))
	}
	// 物理 prune 已落盘（按钮路径与面板路径同一套写侧维护）。
	if rows, degraded := mail.History(context.Background()); degraded || len(rows) != 1 {
		t.Fatalf("history rows after clean = %d (degraded=%v), want 1", len(rows), degraded)
	}
}

func TestCleanCollabMailNowCleanupRule(t *testing.T) {
	isolateDesktopUserDirs(t)
	// 注入花名册：只有 sc_main 活着 —— sc_alice 对清理规则而言是「已删除」。
	restoreFn := identityScanFn
	restoreNow := identityScanNow
	restoreCache := identityScanCache
	restoreAt := identityScanAt
	t.Cleanup(func() {
		identityScanFn = restoreFn
		identityScanNow = restoreNow
		identityScanCache = restoreCache
		identityScanAt = restoreAt
	})
	identityScanFn = func() []sessioncollab.Identity {
		return []sessioncollab.Identity{{ContactID: "sc_main", SessionPath: "main.jsonl"}}
	}
	identityScanCache = nil
	identityScanAt = time.Time{}

	app := NewApp()
	// 先落规则再投信：SetCleanupRule 自带一次立即清理，先设规则才轮到按钮
	// 证明「点击即按所选规则执行」。
	if _, err := app.SetCollabMailCleanupRule("sender"); err != nil {
		t.Fatalf("set cleanup rule: %v", err)
	}
	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "mail from a deleted session",
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	res, err := app.CleanCollabMailNow()
	if err != nil {
		t.Fatalf("clean now: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want 1 (sender-deleted mail under the sender rule)", res.Removed)
	}
	if len(res.Snapshot.Entries) != 0 {
		t.Fatalf("snapshot entries = %d, want 0 after the rule fired", len(res.Snapshot.Entries))
	}
	if rows, degraded := mail.History(context.Background()); degraded || len(rows) != 0 {
		t.Fatalf("history rows after rule clean = %d (degraded=%v), want 0", len(rows), degraded)
	}
}
