package main

// tl1447 缺陷①②的 desktop 面：活跃性判定（registry lastActivity——进度事
// 件刷新、无主 ref 不造行、视图暴露 LastActivityAt）与「继续」续轮桥
// （ContinueSubagentForSession——固定载荷、persist-first、不受消息开关管辖、
// 归属证明同一道闸）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/subagentmailbox"
)

// ① 活跃性：进度事件刷新既有行的 lastActivity；无主 ref 是 no-op（不造行）；
// 视图暴露 LastActivityAt（旧行退化为起点）。
func TestTL1447ActivityRefreshesRegistryLastActivity(t *testing.T) {
	app, tab := foregroundApp(t)

	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", false))
	views := app.RunningSubagents()
	if len(views) != 1 {
		t.Fatalf("registry must hold the running child: %+v", views)
	}
	if views[0].LastActivityAt != int64(1700000000000) {
		t.Fatalf("lifecycle stamps lastActivity with the start time: %d", views[0].LastActivityAt)
	}

	// 带该 ref 的进度事件 → lastActivity 刷新。
	tab.sink.Emit(event.Event{
		Kind: event.ToolProgress,
		Tool: event.Tool{ID: "child-call", Name: event.SubagentProgressStatusName, SubagentRef: "sa_child_created"},
	})
	views = app.RunningSubagents()
	if len(views) != 1 {
		t.Fatalf("activity events must never create rows: %+v", views)
	}
	bumped := views[0].LastActivityAt
	if bumped <= int64(1700000000000) {
		t.Fatalf("activity must refresh lastActivity: %d", bumped)
	}

	// 无主 ref 的进度事件：不造行、不碰既有行。
	tab.sink.Emit(event.Event{
		Kind: event.ToolProgress,
		Tool: event.Tool{ID: "other", Name: event.SubagentProgressStatusName, SubagentRef: "sa_unknown"},
	})
	views = app.RunningSubagents()
	if len(views) != 1 || views[0].LastActivityAt != bumped {
		t.Fatalf("unowned ref must be a no-op: rows=%d lastActivity=%d", len(views), views[0].LastActivityAt)
	}

	// 非进度事件即使带 ref 也不刷新（活性源=进度管道，口径单一）。
	tab.sink.Emit(event.Event{Kind: event.Text, Text: "noise", Tool: event.Tool{SubagentRef: "sa_child_created"}})
	if views := app.RunningSubagents(); views[0].LastActivityAt != bumped {
		t.Fatalf("non-progress events must not refresh activity: %d", views[0].LastActivityAt)
	}
}

// ②「继续」桥：固定载荷、persist-first、运行中 steer 注入。
func TestTL1447ContinueSubagentSteersRunning(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261106_120000_000000000_000000001111"
	writeRunningSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), agent.SubagentRunning)

	consumed := ""
	un := subagentmailbox.GlobalRegistry.Publish(ref, func(itemID string, load func() (string, error)) bool {
		text, err := load()
		if err != nil {
			t.Fatalf("consume load: %v", err)
		}
		consumed = text
		return true
	})
	defer un()

	receipt, err := app.ContinueSubagentForSession(sessionPath, ref)
	if err != nil {
		t.Fatalf("ContinueSubagentForSession: %v", err)
	}
	if receipt.Disposition != "steered" {
		t.Fatalf("disposition = %q, want steered", receipt.Disposition)
	}
	if receipt.MessageID == "" {
		t.Fatal("receipt missing messageId (persist-first broken)")
	}
	if !strings.Contains(consumed, "Continue the current task") || !strings.Contains(consumed, "blocker") {
		t.Fatalf("consumed payload = %q, want the fixed heartbeat-continue text", consumed)
	}
}

// ②「继续」桥：已停靠子代理 → parked（排队等 continue_from 续轮送达），文件
// 落盘（persist-first）。
func TestTL1447ContinueSubagentParksForEnded(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261106_120000_000000000_000000002222"
	writeRunningSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), agent.SubagentCompleted)

	receipt, err := app.ContinueSubagentForSession(sessionPath, ref)
	if err != nil {
		t.Fatalf("ContinueSubagentForSession: %v", err)
	}
	if receipt.Disposition != "parked" {
		t.Fatalf("disposition = %q, want parked", receipt.Disposition)
	}
	mailboxDir := filepath.Join(dir, "subagents", subagentmailbox.MailboxDirName(ref))
	entries, err := os.ReadDir(mailboxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("mailbox dir = %v, %v; want one pending file", entries, err)
	}
}

// ②「继续」桥：消息开关关闭只停用户消息面，不停处置面——SendSubagentMessage
// disabled 的同一形态下 Continue 仍投递。
func TestTL1447ContinueBypassesMessagingSwitch(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261106_120000_000000000_000000003333"
	writeRunningSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), agent.SubagentRunning)

	previous, readErr := os.ReadFile(config.UserConfigPath())
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(config.UserConfigPath())
			return
		}
		_ = os.WriteFile(config.UserConfigPath(), previous, 0o644)
	})
	toml := "[agent]\nexperimental_subagent_messaging = false\n"
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	// 同形态下用户消息面 disabled。
	sendReceipt, err := app.SendSubagentMessage(sessionPath, ref, "", "user note")
	if err != nil {
		t.Fatalf("SendSubagentMessage: %v", err)
	}
	if sendReceipt.Disposition != "disabled" {
		t.Fatalf("send disposition = %q, want disabled while the switch is off", sendReceipt.Disposition)
	}
	// 处置面照常投递。
	continueReceipt, err := app.ContinueSubagentForSession(sessionPath, ref)
	if err != nil {
		t.Fatalf("ContinueSubagentForSession: %v", err)
	}
	if continueReceipt.Disposition != "parked" && continueReceipt.Disposition != "steered" && continueReceipt.Disposition != "queued" {
		t.Fatalf("continue disposition = %q, want a delivered disposition with the switch off", continueReceipt.Disposition)
	}
}

// ②「继续」桥：归属证明同一道闸——不属于本会话的 ref 拒绝，不写盘。
func TestTL1447ContinueRejectsForeignRef(t *testing.T) {
	app, _, sessionPath := newCapsuleTestSession(t)
	if _, err := app.ContinueSubagentForSession(sessionPath, "sa_not_in_this_session"); err == nil {
		t.Fatal("foreign ref must be rejected by the membership proof")
	}
	if _, err := app.ContinueSubagentForSession(sessionPath, ""); err == nil {
		t.Fatal("empty ref must be rejected")
	}
}

// 时间格式守卫：静默计算的输入面（unix ms）不漂。
func TestTL1447LastActivityIsUnixMilli(t *testing.T) {
	app, tab := foregroundApp(t)
	tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_created", Ref: "sa_clock", Skill: "时钟",
		StartUnixMs: time.Now().UnixMilli(),
	})
	views := app.RunningSubagents()
	if len(views) != 1 {
		t.Fatalf("registry must hold the child: %+v", views)
	}
	now := time.Now().UnixMilli()
	if diff := now - views[0].LastActivityAt; diff < 0 || diff > 60_000 {
		t.Fatalf("lastActivityAt %d not within [now-60s, now] (%d)", views[0].LastActivityAt, now)
	}
}
