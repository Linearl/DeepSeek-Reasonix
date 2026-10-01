package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/collabchannel"
	"reasonix/internal/sessioncollab"
)

// channelFixture wires the three tools against a temp mailbox with one
// channel: members = this session + sc_m1 (so the caller can both receive a
// fan-out — read-marking — and send without becoming a recipient).
func channelFixture(t *testing.T) (SessionCollabConfig, *collabchannel.Store) {
	t.Helper()
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)
	contact, err := EnsureContactID(self)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentContactID:   contact,
		ResolveSessionPath: func() string { return self },
	}
	store, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateChannel("dev", "engineering", 0, contact, "sc_m1"); err != nil {
		t.Fatal(err)
	}
	return cfg, store
}

// 验收 5：agent 三工具（查看频道/获取消息/发送消息）全绿。
func TestChannelToolsListSendRead(t *testing.T) {
	cfg, store := channelFixture(t)
	ctx := context.Background()

	// 捕获后台 drain job（channelSpawn 是包级缝，测试不与之赛跑）。
	var captured []func()
	origSpawn := channelSpawn
	channelSpawn = func(job func()) { captured = append(captured, job) }
	defer func() { channelSpawn = origSpawn }()

	// ① channel_list：频道 + 成员 + 计数可见。
	listOut, err := NewChannelListTool(cfg).Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var listPayload struct {
		Channels []collabchannel.Channel `json:"channels"`
	}
	if err := json.Unmarshal([]byte(listOut), &listPayload); err != nil || len(listPayload.Channels) != 1 {
		t.Fatalf("list: %v %s", err, listOut)
	}
	if listPayload.Channels[0].Name != "dev" || len(listPayload.Channels[0].Members) != 2 {
		t.Fatalf("channel row: %+v", listPayload.Channels[0])
	}

	// ② channel_send：发布入库 + 队列 fan-out（自己不入列）+ 捕获后台投递。
	sendOut, err := NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"dev","message":"ship it"}`))
	if err != nil {
		t.Fatal(err)
	}
	var sendPayload struct {
		MessageID string `json:"messageId"`
		QueuedFor int    `json:"queuedFor"`
	}
	if err := json.Unmarshal([]byte(sendOut), &sendPayload); err != nil {
		t.Fatal(err)
	}
	if sendPayload.MessageID == "" || sendPayload.QueuedFor != 1 {
		t.Fatalf("send: %s", sendOut)
	}
	if len(captured) != 1 {
		t.Fatalf("send must kick exactly one background drain, got %d", len(captured))
	}
	rows, err := store.FanoutStates("dev")
	if err != nil || len(rows) != 1 || rows[0].State != "queued" {
		t.Fatalf("fan-out not queued: %+v err=%v", rows, err)
	}
	// 跑一次捕获的 job：端到端工具→309 邮箱（单成员无间隔，即时）。
	captured[0]()
	rows, _ = store.FanoutStates("dev")
	if rows[0].State != "delivered" || rows[0].DeliveredAt == 0 {
		t.Fatalf("background drain must deliver: %+v", rows[0])
	}
	mailRows, err := sessioncollab.NewMailStore(cfg.MailDir).Inbox("sc_m1")
	if err != nil || len(mailRows) != 1 || mailRows[0].Delivery != "followup" {
		t.Fatalf("fan-out mail: %v %+v", err, mailRows)
	}

	// ③ channel_read：sc_m1 发来一条 → 本会话（成员）读取即标记自己的已读。
	if _, err := store.Publish("dev", "sc_m1", "status report"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DrainFanout(context.Background()); err != nil { // pace: 单行无间隔
		t.Fatal(err)
	}
	readOut, err := NewChannelReadTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"dev"}`))
	if err != nil {
		t.Fatal(err)
	}
	var readPayload struct {
		Returned   int `json:"returned"`
		MarkedRead int `json:"markedRead"`
		Messages   []struct {
			ID     string `json:"id"`
			Sender string `json:"sender"`
			Body   string `json:"body"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(readOut), &readPayload); err != nil {
		t.Fatal(err)
	}
	if readPayload.Returned != 2 || len(readPayload.Messages) != 2 {
		t.Fatalf("history: %s", readOut)
	}
	if readPayload.Messages[0].Body != "ship it" || readPayload.Messages[1].Body != "status report" {
		t.Fatalf("chronological order: %+v", readPayload.Messages)
	}
	if readPayload.MarkedRead != 1 {
		t.Fatalf("reading must mark exactly MY rows (1 fan-out addressed to me), got %d", readPayload.MarkedRead)
	}
	// 只动自己的行：sc_m1 对第一条的已读仍空。
	rows, _ = store.FanoutStates("dev")
	for _, r := range rows {
		if r.Member == "sc_m1" && r.MessageID == sendPayload.MessageID && r.ReadAt != 0 {
			t.Fatalf("my read must not leak onto sc_m1's row: %+v", r)
		}
	}
}

// 工具边界：缺参/未知频道/无身份发送全部拒绝，不静默。
func TestChannelToolsRejectInvalidCalls(t *testing.T) {
	cfg, _ := channelFixture(t)
	ctx := context.Background()

	if _, err := NewChannelReadTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"ghost"}`)); err == nil {
		t.Fatal("unknown channel must be rejected")
	}
	if _, err := NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"dev"}`)); err == nil {
		t.Fatal("message is required")
	}
	anon := cfg
	anon.CurrentContactID = ""
	anon.ResolveSessionPath = func() string { return "" }
	if _, err := NewChannelSendTool(anon).Execute(ctx, json.RawMessage(`{"channel":"dev","message":"x"}`)); err == nil {
		t.Fatal("sending without a session identity must be rejected")
	}
	// 小时限额透传：cap=1 的频道第二条按 ErrHourlyCap 拒绝（工具层不吞错）。
	store, err := collabchannel.Open(cfg.MailDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateChannel("capped", "", 1, "sc_x"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish("capped", "sc_s", "one"); err != nil {
		t.Fatal(err)
	}
	_, err = NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"capped","message":"two"}`))
	if err == nil || !strings.Contains(err.Error(), "hourly") {
		t.Fatalf("hourly cap must surface through the tool: %v", err)
	}
}
