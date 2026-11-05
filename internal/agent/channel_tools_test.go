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

// 取消消息工具（20261002 增量）：发送→取消→后台 drain 落空（邮箱无信）→
// channel_read 显示墓碑；已投递后取消只报计数；越权/无身份/缺参全拒。
func TestChannelCancelToolStopsPendingFanout(t *testing.T) {
	cfg, _ := channelFixture(t)
	ctx := context.Background()

	var captured []func()
	origSpawn := channelSpawn
	channelSpawn = func(job func()) { captured = append(captured, job) }
	defer func() { channelSpawn = origSpawn }()

	// ① 发送 → 立即取消 → 跑捕获的 drain job：sc_m1 邮箱必须无信。
	sendOut, err := NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"dev","message":"typo"}`))
	if err != nil {
		t.Fatal(err)
	}
	var sendPayload struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal([]byte(sendOut), &sendPayload); err != nil || sendPayload.MessageID == "" {
		t.Fatalf("send: %s", sendOut)
	}
	cancelOut, err := NewChannelCancelTool(cfg).Execute(ctx,
		json.RawMessage(`{"channel":"dev","messageId":"`+sendPayload.MessageID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var cancelPayload struct {
		CancelledQueued  int  `json:"cancelledQueued"`
		AlreadyDelivered int  `json:"alreadyDelivered"`
		AlreadyCancelled bool `json:"alreadyCancelled"`
	}
	if err := json.Unmarshal([]byte(cancelOut), &cancelPayload); err != nil {
		t.Fatal(err)
	}
	if cancelPayload.CancelledQueued != 1 || cancelPayload.AlreadyDelivered != 0 || cancelPayload.AlreadyCancelled {
		t.Fatalf("cancel: %s", cancelOut)
	}
	captured[0]() // drain 空转：无 queued 行
	mailRows, err := sessioncollab.NewMailStore(cfg.MailDir).Inbox("sc_m1")
	if err != nil || len(mailRows) != 0 {
		t.Fatalf("cancelled fan-out must not land in the mailbox: %v %+v", err, mailRows)
	}

	// ② channel_read 返回墓碑（cancelledAt > 0）。
	readOut, err := NewChannelReadTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"dev"}`))
	if err != nil {
		t.Fatal(err)
	}
	var readPayload struct {
		Messages []struct {
			ID          string `json:"id"`
			CancelledAt int64  `json:"cancelledAt"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(readOut), &readPayload); err != nil {
		t.Fatal(err)
	}
	if len(readPayload.Messages) != 1 || readPayload.Messages[0].CancelledAt == 0 {
		t.Fatalf("history must carry the tombstone: %s", readOut)
	}

	// ③ 已投递后取消：cancelledQueued=0，alreadyDelivered=1（副本不追回，如实报数）。
	sendOut2, err := NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"dev","message":"too late"}`))
	if err != nil {
		t.Fatal(err)
	}
	var send2 struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal([]byte(sendOut2), &send2); err != nil {
		t.Fatal(err)
	}
	captured[1]() // 真投递
	out2, err := NewChannelCancelTool(cfg).Execute(ctx,
		json.RawMessage(`{"channel":"dev","messageId":"`+send2.MessageID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var cancel2 struct {
		CancelledQueued  int `json:"cancelledQueued"`
		AlreadyDelivered int `json:"alreadyDelivered"`
	}
	if err := json.Unmarshal([]byte(out2), &cancel2); err != nil {
		t.Fatal(err)
	}
	if cancel2.CancelledQueued != 0 || cancel2.AlreadyDelivered != 1 {
		t.Fatalf("post-delivery cancel must only report: %s", out2)
	}

	// ④ 越权拒：sc_m1 不能取消本会话发的消息。
	other := cfg
	other.CurrentContactID = "sc_m1"
	_, err = NewChannelCancelTool(other).Execute(ctx,
		json.RawMessage(`{"channel":"dev","messageId":"`+send2.MessageID+`"}`))
	if err == nil || !strings.Contains(err.Error(), "only the sender") {
		t.Fatalf("non-sender cancel must be rejected: %v", err)
	}

	// ⑤ 无身份/缺参/未知消息拒。
	anon := cfg
	anon.CurrentContactID = ""
	anon.ResolveSessionPath = func() string { return "" }
	if _, err := NewChannelCancelTool(anon).Execute(ctx,
		json.RawMessage(`{"channel":"dev","messageId":"chm_x"}`)); err == nil {
		t.Fatal("cancel without identity must be rejected")
	}
	if _, err := NewChannelCancelTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"dev"}`)); err == nil {
		t.Fatal("messageId is required")
	}
	if _, err := NewChannelCancelTool(cfg).Execute(ctx,
		json.RawMessage(`{"channel":"dev","messageId":"chm_ghost"}`)); err == nil {
		t.Fatal("unknown message must be rejected")
	}
}

// tl1406 工具面验收：channel_send 的 queuedFor 如实上报路由结果（不再「全员
// -自己」的群发口径），deputyless 标志透出零投递分支；@<contact_id> 按描述
// 契约解析。用户 20261105 实证场景（四人群未 @ 报 queuedFor=3）在此钉死。
func TestChannelSendReportsRoutingVerdict(t *testing.T) {
	cfg, store := channelFixture(t)
	ctx := context.Background()

	// 捕获后台 drain job，测试内同步跑（与 TestChannelToolsListSendRead 同款）。
	var captured []func()
	origSpawn := channelSpawn
	channelSpawn = func(job func()) { captured = append(captured, job) }
	defer func() { channelSpawn = origSpawn }()

	// human-owned 群：调用会话 + sc_dep（副管理员）+ sc_b（普通成员）。
	if _, err := store.CreateChannelWithOwner("ops", "tl1406", 0, collabchannel.MemberTypeHuman, collabchannel.HumanLocal); err != nil {
		t.Fatal(err)
	}
	contact := cfg.CurrentContactID
	for _, m := range []string{contact, "sc_dep", "sc_b"} {
		if err := store.Join("ops", m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SetDeputy("ops", "sc_dep"); err != nil {
		t.Fatal(err)
	}

	type sendView struct {
		MessageID  string `json:"messageId"`
		QueuedFor  int    `json:"queuedFor"`
		Deputyless bool   `json:"deputyless"`
	}

	// ① 有副管理员 + 未 @ → queuedFor=1，只有副管理员被投递唤醒。
	out, err := NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"ops","message":"请大家看一下这个需求"}`))
	if err != nil {
		t.Fatal(err)
	}
	var v sendView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.QueuedFor != 1 || v.Deputyless {
		t.Fatalf("un-@ with deputy: want queuedFor=1 deputyless=false, got %s", out)
	}
	if _, err := store.DrainFanout(ctx); err != nil {
		t.Fatal(err)
	}
	mail := sessioncollab.NewMailStore(cfg.MailDir)
	if rows, err := mail.Inbox("sc_dep"); err != nil || len(rows) != 1 {
		t.Fatalf("deputy inbox: %v %+v", err, rows)
	}
	if rows, err := mail.Inbox("sc_b"); err != nil || len(rows) != 0 {
		t.Fatalf("plain member must get zero pushes: %v %+v", err, rows)
	}

	// ② 显式 @<contact_id> → 仅落被 @ 者（queuedFor=1 且不是副管理员）。
	out, err = NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"ops","message":"@sc_b 这条给你"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.QueuedFor != 1 || v.Deputyless {
		t.Fatalf("explicit @: want queuedFor=1, got %s", out)
	}
	if _, err := store.DrainFanout(ctx); err != nil {
		t.Fatal(err)
	}
	if rows, err := mail.Inbox("sc_b"); err != nil || len(rows) != 1 {
		t.Fatalf("mentioned member inbox: %v %+v", err, rows)
	}
	if rows, err := mail.Inbox("sc_dep"); err != nil || len(rows) != 1 {
		t.Fatalf("deputy must stay at one (only the first message): %v %+v", err, rows)
	}

	// ③ human 群无副管理员 + 未 @ → queuedFor=0 + deputyless=true；历史仍可
	// 只读查询（channel_read 看得到这行），零推送零唤醒。
	if _, err := store.CreateChannelWithOwner("lonely", "无副管理员群", 0, collabchannel.MemberTypeHuman, collabchannel.HumanLocal); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{contact, "sc_c"} { // sc_c 未收过件：零推送断言不被前面的用例污染
		if err := store.Join("lonely", m); err != nil {
			t.Fatal(err)
		}
	}
	out, err = NewChannelSendTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"lonely","message":"今天先做 A 项"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.QueuedFor != 0 || !v.Deputyless {
		t.Fatalf("deputyless group: want queuedFor=0 deputyless=true, got %s", out)
	}
	readOut, err := NewChannelReadTool(cfg).Execute(ctx, json.RawMessage(`{"channel":"lonely"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readOut, "今天先做 A 项") {
		t.Fatalf("member read-only query must still see history: %s", readOut)
	}
	if _, err := store.DrainFanout(ctx); err != nil {
		t.Fatal(err)
	}
	if rows, err := mail.Inbox("sc_c"); err != nil || len(rows) != 0 {
		t.Fatalf("deputyless zero-push broken: %v %+v", err, rows)
	}
	_ = captured // drain job 已被上方同步 DrainFanout 等价覆盖（job 即同一 store 链路）
}
