package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// setupIsolatedCollabMail 把用户目录隔离到临时路径并落盘实验配置，
// 使泵（verifyHop / notifySenderOnce 经 live config 解析邮箱根）与测试夹具
// 落在同一个临时邮箱目录上。返回邮箱根路径。
func setupIsolatedCollabMail(t *testing.T) string {
	t.Helper()
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Agent.ExperimentalSessionCollab = true
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return config.SessionCollabMailDir()
}

// TestRefusalNoticeReachesTheSender: 任务548 P0-2（死信可达）端到端。
// 2026-10-06 事故：hop=2 且 threadId 自指的派单被泵溯源门拒收后，拒收通知
// 拷回了 Hop+ThreadID 且 From 为空——通知自身在发送方的泵再次被拒收
// （"reply has no sender"），派活方对「消息被拒」永久失聪，事后核查 seen
// 还命中（拒收也结算游标）。修复后通知走新链形态（Hop=0、threadId 留空、
// From=目标 contact），必过溯源门：派活方邮箱能收到含原因与 messageId 的
// 死信通知。seen 游标维持「投递结算」语义不变（报告 §建议 3）。
func TestRefusalNoticeReachesTheSender(t *testing.T) {
	mailDir := setupIsolatedCollabMail(t)
	target, sender := "sc_target", "sc_from"
	mail := sessioncollab.NewMailStore(mailDir)
	pump := &sessionCollabPump{}

	// 事故形态消息直接落盘（任务548 P0-1 之后 Deliver 已拒发这种记录；
	// raw 写入正代表「校验生效前已入库的存量」或绕过工具面的外部写入方）。
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	incident := `{"id":"msg_incident1","fromContactId":"sc_from","toContactId":"sc_target","body":"【3 支待合并】","delivery":"followup","hop":2,"threadId":"msg_incident1","at":1696500000000}`
	if err := appendRawLine(filepath.Join(mailDir, target+".inbox.jsonl"), incident); err != nil {
		t.Fatal(err)
	}

	d := collabDelivery{
		enqueue: func(sessioncollab.MailMessage, string) (bool, error) {
			t.Fatal("a provenance-refused message must not reach the target")
			return false, nil
		},
		notify:    pump.notifySenderOnce, // 真实通知路径，不 mock
		deriveHop: pump.verifyHop,        // 真实溯源门，不 mock
		render:    sessionCollabDeliveryText,
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 0 || refused != 1 {
		t.Fatalf("the incident shape must be refused: %d delivered / %d refused", delivered, refused)
	}

	// 对照钉：历史通知形态（拷 Hop+ThreadID、From 为空）必须仍被溯源门拒收
	// ——否则本测试证不出「新形态可达」靠的是什么。
	legacy := sessioncollab.MailMessage{ID: "msg_notice_old", To: sender, Body: "legacy shape", Hop: 2, ThreadID: "msg_incident1", Kind: "system"}
	if _, err := pump.verifyHop(legacy); err == nil {
		t.Fatal("the legacy notice shape must stay refused by verifyHop — the gate must not have been relaxed")
	}

	// 死信通知必须真实落进派活方邮箱，且形态可被派活方泵再次消费。
	box, err := mail.Inbox(sender)
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 1 {
		t.Fatalf("the sender must receive exactly one dead-letter notice, got %d", len(box))
	}
	notice := box[0]
	if notice.Kind != "system" {
		t.Fatalf("a status notice must stay Kind=system (auto-read), got %q", notice.Kind)
	}
	if notice.Hop != 0 {
		t.Fatalf("a refusal notice must be new-chain shaped (hop=0), got hop=%d", notice.Hop)
	}
	if notice.From != target {
		t.Fatalf("the notice must name its real origin (the target), got from=%q", notice.From)
	}
	if !strings.Contains(notice.Body, "msg_incident1") || !strings.Contains(notice.Body, "无法核实") {
		t.Fatalf("the dead-letter notice must carry the messageId and the reason: %s", notice.Body)
	}
	if _, err := pump.verifyHop(notice); err != nil {
		t.Fatalf("the notice itself must pass the sender-side provenance gate (reachable): %v", err)
	}

	// seen 语义按报告设计保持「投递结算」不动：事故消息已结算，不再重放；
	// 「已读 vs 已拒收」的区分由这封可达的死信通知承担。
	if !mail.Settled(target, "msg_incident1") {
		t.Fatal("a refused message must stay settled (report §建议 3: seen semantics unchanged)")
	}
}

// TestDegradedSteerNoticeIsNewChainShaped: 任务548 P0-2 同修钉——降级通知
// 不再拷回原消息 hop（hop>0 的通知会被发送方泵的溯源门灭掉）。该函数当前
// 无调用点（真实路径经 notifySenderOnce），此测试防未来复用时带回病灶。
func TestDegradedSteerNoticeIsNewChainShaped(t *testing.T) {
	mailDir := setupIsolatedCollabMail(t)
	mail := sessioncollab.NewMailStore(mailDir)
	pump := &sessionCollabPump{}

	pump.notifyDegradedSteer(sessioncollab.MailMessage{ID: "msg_steer1", From: "sc_from", To: "sc_target", Hop: 2}, "no_injectable_turn")

	box, err := mail.Inbox("sc_from")
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 1 {
		t.Fatalf("the sender must receive exactly one degraded-steer notice, got %d", len(box))
	}
	notice := box[0]
	if notice.Hop != 0 {
		t.Fatalf("a degraded-steer notice must be new-chain shaped (hop=0), got hop=%d", notice.Hop)
	}
	if notice.From != "sc_target" {
		t.Fatalf("the notice must name its real origin, got from=%q", notice.From)
	}
	if !strings.Contains(notice.Body, "no_injectable_turn") {
		t.Fatalf("the notice must carry the disposition: %s", notice.Body)
	}
	if _, err := pump.verifyHop(notice); err != nil {
		t.Fatalf("the notice must pass the sender-side provenance gate: %v", err)
	}
}
