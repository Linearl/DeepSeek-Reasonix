package main

// Task 409 群聊式协作视图（只读观察窗）的聚合面测试。三个口径：
//  1. 一次呈现 ≥3 个协作会话（409 验收 1）：三个真实身份夹具出现在同一张
//     overview 里，数据全部来自现有数据源——身份目录扫描 + 共享忙闲判定
//     （CollabStatusRecords，get_session_status 的同一函数）+ 信箱 History。
//  2. 状态映射 = get_session_status 的共享判定原样透传（409 不新造状态）：
//     running / queued / idle / unknown 四态逐一对上。
//  3. 最近一条往来只算已投递的信（与 320 契约 ② 同口径）；锁繁忙的 degraded
//     读返回空表，不用别会话的信冒充（任务 511 纪律）。
// 另有 collabViewCardFor 的选卡规则单测：进行中 > 排队 > 阻塞 > 已终结，
// 同级取最近更新；contact 优先、会话路径兜底。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

func collabViewFixture(t *testing.T, stem, title, contact, topic string) {
	t.Helper()
	p := filepath.Join(config.SessionDir(), stem+".jsonl")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(p, false, func(m *agent.BranchMeta) error {
		m.CustomTitle = title
		m.ContactID = contact
		m.TopicID = topic
		m.Purpose = "409 测试夹具"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGetCollabViewOverviewAggregatesRoster(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(config.SessionDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	collabViewFixture(t, "cv_a", "主对话甲", "sc_cv_a", "top_cv_a")
	collabViewFixture(t, "cv_b", "子对话乙", "sc_cv_b", "top_cv_b")
	collabViewFixture(t, "cv_c", "子对话丙", "sc_cv_c", "top_cv_c")

	// 目录扫描缓存复位：夹具在隔离目录里刚落盘，必须让这次 overview 读到它们。
	restoreCache, restoreAt, restoreFn, restoreNow := identityScanCache, identityScanAt, identityScanFn, identityScanNow
	restoreStatus := collabViewStatusOverride
	t.Cleanup(func() {
		identityScanCache, identityScanAt, identityScanFn, identityScanNow = restoreCache, restoreAt, restoreFn, restoreNow
		collabViewStatusOverride = restoreStatus
	})
	identityScanCache, identityScanAt = nil, time.Time{}

	// 共享忙闲判定（注入点即 a.collabSessionStatus 的替身）：甲在跑、乙有排队、
	// 丙空闲——四个会话状态词汇与 get_session_status 完全一致。
	collabViewStatusOverride = func(contactID string) (bool, int64, int, bool) {
		switch contactID {
		case "sc_cv_a":
			return true, 1700000000000, 0, true
		case "sc_cv_b":
			return false, 0, 2, true
		case "sc_cv_c":
			return false, 0, 0, true
		}
		return false, 0, 0, false
	}

	// 已投递往来：给甲发一封信（发给乙的留在队列里，不进往来列）。
	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_cv_b", To: "sc_cv_a", Body: "409 视图第一行\n第二行不进预览",
	}); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	overview, err := app.GetCollabViewOverview()
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.GeneratedAt <= 0 {
		t.Fatalf("generatedAt = %d, want a real timestamp", overview.GeneratedAt)
	}
	byContact := map[string]CollabViewSession{}
	for _, row := range overview.Sessions {
		byContact[row.ContactID] = row
	}
	for _, tc := range []struct {
		contact, title, state, purpose string
		unread                         int
		exchangeFrom                   string
	}{
		{"sc_cv_a", "主对话甲", "running", "409 测试夹具", 1, "sc_cv_b"},
		{"sc_cv_b", "子对话乙", "queued", "409 测试夹具", 2, "sc_cv_b"},
		{"sc_cv_c", "子对话丙", "idle", "409 测试夹具", 0, ""},
	} {
		row, ok := byContact[tc.contact]
		if !ok {
			t.Fatalf("contact %s missing from the overview (%d rows): %+v", tc.contact, len(overview.Sessions), overview.Sessions)
		}
		if row.Title != tc.title || row.State != tc.state || row.Purpose != tc.purpose {
			t.Fatalf("%s = title %q state %q purpose %q, want %q/%q/%q", tc.contact, row.Title, row.State, row.Purpose, tc.title, tc.state, tc.purpose)
		}
		if row.UnreadInbox != tc.unread {
			t.Fatalf("%s unread = %d, want %d", tc.contact, row.UnreadInbox, tc.unread)
		}
		if tc.exchangeFrom == "" {
			if row.LastExchange != nil {
				t.Fatalf("%s should have no exchange, got %+v", tc.contact, row.LastExchange)
			}
			continue
		}
		if row.LastExchange == nil || row.LastExchange.From != tc.exchangeFrom {
			t.Fatalf("%s exchange = %+v, want from %s", tc.contact, row.LastExchange, tc.exchangeFrom)
		}
		if row.LastExchange.Preview != "409 视图第一行" {
			t.Fatalf("preview must be the first line only, got %q", row.LastExchange.Preview)
		}
	}
}

// 未知态绝不冒充空闲：本进程看不到的运行时（另一个测试 App 里没有控制器）
// 原样报 unknown，并保留 get_session_status 的自我解释语义。
func TestGetCollabViewOverviewUnknownNeverGuessesIdle(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(config.SessionDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	collabViewFixture(t, "cv_u", "看不见的会话", "sc_cv_u", "top_cv_u")
	restoreCache, restoreAt, restoreStatus := identityScanCache, identityScanAt, collabViewStatusOverride
	t.Cleanup(func() {
		identityScanCache, identityScanAt, collabViewStatusOverride = restoreCache, restoreAt, restoreStatus
	})
	identityScanCache, identityScanAt = nil, time.Time{}
	collabViewStatusOverride = nil // 生产探针：裸 App 没有控制器 → known=false

	app := NewApp()
	overview, err := app.GetCollabViewOverview()
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	for _, row := range overview.Sessions {
		if row.ContactID != "sc_cv_u" {
			continue
		}
		if row.State != "unknown" {
			t.Fatalf("state = %q, want unknown (never a guessed idle)", row.State)
		}
		return
	}
	t.Fatalf("contact sc_cv_u missing from %d rows: %+v", len(overview.Sessions), overview.Sessions)
}

func TestCollabViewCardForPicksCurrentCard(t *testing.T) {
	now := time.Now().UnixMilli()
	open := sessioncollab.Card{ID: "open", Title: "进行中的派单", Status: sessioncollab.StatusRunning, Assignee: "sc_x", UpdatedAt: now - 500}
	pending := sessioncollab.Card{ID: "pending", Title: "排队件", Status: sessioncollab.StatusPending, Assignee: "sc_x", UpdatedAt: now}
	done := sessioncollab.Card{ID: "done", Title: "已回执", Status: sessioncollab.StatusDone, Assignee: "sc_x", UpdatedAt: now}
	blockedOther := sessioncollab.Card{ID: "blocked", Title: "别人的阻塞件", Status: sessioncollab.StatusBlocked, Assignee: "sc_y", UpdatedAt: now}

	// 进行中的卡压过更新的已终结卡（终结 = 回执，不是当前任务）。
	if got := collabViewCardFor([]sessioncollab.Card{done, open}, "sc_x", ""); got == nil || got.ID != "open" {
		t.Fatalf("running must beat newer terminal, got %+v", got)
	}
	// 同为打开态：排队与阻塞按 rank，同级取最近更新。
	if got := collabViewCardFor([]sessioncollab.Card{open, pending}, "sc_x", ""); got == nil || got.ID != "open" {
		t.Fatalf("running must beat pending, got %+v", got)
	}
	if got := collabViewCardFor([]sessioncollab.Card{done, pending}, "sc_x", ""); got == nil || got.ID != "pending" {
		t.Fatalf("pending must beat terminal, got %+v", got)
	}
	// 会话路径兜底：任务卡还没有 contact_id（pre-contact 卡）也能按路径挂上。
	byPath := sessioncollab.Card{ID: "bypath", Title: "老卡", Status: sessioncollab.StatusRunning, SessionTo: `C:\s\a.jsonl`, UpdatedAt: now}
	if got := collabViewCardFor([]sessioncollab.Card{byPath}, "", `C:\s\a.jsonl`); got == nil || got.ID != "bypath" {
		t.Fatalf("session-path fallback must attach the card, got %+v", got)
	}
	// 别人的卡不冒充。
	if got := collabViewCardFor([]sessioncollab.Card{blockedOther}, "sc_x", ""); got != nil {
		t.Fatalf("another contact's card must not attach, got %+v", got)
	}
	if got := collabViewCardFor(nil, "sc_x", ""); got != nil {
		t.Fatalf("no cards must give no card, got %+v", got)
	}
}
