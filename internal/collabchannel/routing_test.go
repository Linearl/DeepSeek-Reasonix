package collabchannel

// tl1406（用户 20261105 四条裁决终版）——群聊投递路由三分支验收：
//
//   ① 有副管理员 + 未 @  → 仅落副管理员一行（副管理员=任务分配者/默认收件人，
//      Q1=A 拉人权限并存不变）；其余成员零 fanout 行、mailbox 零推送。
//   ② 无副管理员 + 未 @  → 零投递零唤醒（Q3），Deputyless=true 供前端弹窗
//      提示用户；历史仍落库，成员可主动查询（Q2 只读查询 ≠ 推送投递）。
//   ③ 显式 @            → 仅落被 @ 者（多人 @ 落多行）；@ 语法 = @contact_id
//      或 @别名（前端 composer 插入格式）；未知 @ 不路由、落回 ①② 分支。
//
// 路由范围 = human-owned 群（副管理员概念仅存于此）；ownerless / session 群
// 保留 349 原语义（全成员 fanout），v2 迁移契约不破。

import (
	"context"
	"testing"

	"reasonix/internal/sessioncollab"
)

// inboxLen reads one contact's mailbox size through the shared MailStore.
func inboxLen(t *testing.T, dir, contact string) int {
	t.Helper()
	rows, err := sessioncollab.NewMailStore(dir).Inbox(contact)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// fanoutMembers flattens the channel's fanout rows into member→count.
func fanoutMembers(t *testing.T, s *Store, ref string) map[string]int {
	t.Helper()
	rows, err := s.FanoutStates(ref)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Member]++
	}
	return out
}

// 验收①：有副管理员 + 未 @ → 仅副管理员收到并唤醒；其余成员零 fanout 行、
// mailbox 零推送（用户 20261105 17:57 现场实证场景的回归钉）。
func TestPublishDeputyOnlyRouting(t *testing.T) {
	s, dir, _ := newStore(t)
	g := humanGroup(t, s, "常用对话组")
	for _, m := range []string{"sc_dep", "sc_b", "sc_c"} {
		if err := s.Join(g.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetDeputy(g.ID, "sc_dep"); err != nil {
		t.Fatal(err)
	}

	msg, err := s.Publish(g.ID, HumanLocal, "请大家看一下这个需求")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Deputyless {
		t.Fatal("a deputy is set — deputyless must be false")
	}
	if len(msg.RoutedTo) != 1 || msg.RoutedTo[0] != "sc_dep" {
		t.Fatalf("routed = %v, want [sc_dep]", msg.RoutedTo)
	}
	rows := fanoutMembers(t, s, g.ID)
	if len(rows) != 1 || rows["sc_dep"] != 1 {
		t.Fatalf("fanout rows = %v, want exactly one for sc_dep", rows)
	}

	stats, err := s.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Delivered != 1 || stats.Queued != 1 {
		t.Fatalf("drain: %+v", stats)
	}
	if n := inboxLen(t, dir, "sc_dep"); n != 1 {
		t.Fatalf("deputy inbox = %d, want 1", n)
	}
	// mailbox 零推送：非副管理员成员一个字都收不到（不投递 = 不唤醒）。
	for _, m := range []string{"sc_b", "sc_c", HumanLocal} {
		if n := inboxLen(t, dir, m); n != 0 {
			t.Fatalf("%s inbox = %d, want 0 (mailbox zero-push)", m, n)
		}
	}

	// 副管理员自己发言未 @：路由仍指向副管理员，但发送者不入列 → 零行。
	msg2, err := s.Publish(g.ID, "sc_dep", "收到，我来分配")
	if err != nil {
		t.Fatal(err)
	}
	if len(msg2.RoutedTo) != 0 || msg2.Deputyless {
		t.Fatalf("deputy's own line must fan nobody without prompting: %+v", msg2)
	}
}

// 验收②：human 群无副管理员 + 未 @ → 零投递零唤醒 + Deputyless=true（前端
// 弹窗提示用户）；消息仍入历史，成员可主动查询（只读，不触发唤醒）。
func TestPublishDeputylessZeroDelivery(t *testing.T) {
	s, dir, _ := newStore(t)
	g := humanGroup(t, s, "未设副群")
	if err := s.Join(g.ID, "sc_a"); err != nil {
		t.Fatal(err)
	}

	msg, err := s.Publish(g.ID, HumanLocal, "今天先做 A 项")
	if err != nil {
		t.Fatal(err)
	}
	if !msg.Deputyless {
		t.Fatal("human-owned + un-@ + no deputy must flag deputyless")
	}
	if len(msg.RoutedTo) != 0 {
		t.Fatalf("routed = %v, want empty", msg.RoutedTo)
	}
	if rows := fanoutMembers(t, s, g.ID); len(rows) != 0 {
		t.Fatalf("fanout rows = %v, want none", rows)
	}
	stats, err := s.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Queued != 0 || stats.Delivered != 0 {
		t.Fatalf("drain must be a no-op: %+v", stats)
	}
	if n := inboxLen(t, dir, "sc_a"); n != 0 {
		t.Fatalf("sc_a inbox = %d, want 0", n)
	}

	// Q2：成员主动查询历史可用（只读查询 ≠ 推送投递）。
	history, err := s.Messages(g.ID, 0, 50)
	if err != nil || len(history) != 1 || history[0].Body != "今天先做 A 项" {
		t.Fatalf("member query must still see history: %v %+v", err, history)
	}
	// 历史读带不出发布期注记（注记只在 Publish 返回值上）。
	if history[0].Deputyless || history[0].RoutedTo != nil {
		t.Fatalf("history rows must not carry publish-time annotations: %+v", history[0])
	}
	// 未读标记：没有被 fan 的成员无行可标（channel_read 侧等价 no-op）。
	if n, err := s.MarkRead(g.ID, "sc_a", []string{msg.ID}); err != nil || n != 0 {
		t.Fatalf("non-fanned member must have no row to mark: n=%d err=%v", n, err)
	}
}

// 验收③：显式 @ → 仅落被 @ 者。覆盖：contact_id、别名、多人 @、@ 发送者
// 自身、未知 @ 落回默认分支、前缀名碰撞取最长 needle。
func TestPublishMentionRouting(t *testing.T) {
	s, dir, _ := newStore(t)
	g := humanGroup(t, s, "mention 群")
	for _, m := range []string{"sc_dep", "sc_b"} {
		if err := s.Join(g.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetDeputy(g.ID, "sc_dep"); err != nil {
		t.Fatal(err)
	}
	names := map[string][]string{
		"sc_dep": {"新12"},
		"sc_b":   {"需求与计划2"},
	}

	// ③a @contact_id → 仅被 @ 者（agent 工具面主用形态）。
	msg, err := s.PublishWithMentionNames(g.ID, HumanLocal, "@sc_b 请看一下", names)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.RoutedTo) != 1 || msg.RoutedTo[0] != "sc_b" {
		t.Fatalf("routed = %v, want [sc_b]", msg.RoutedTo)
	}
	// ③b @别名（前端 composer 插入「@显示名 」的形态）。
	msg, err = s.PublishWithMentionNames(g.ID, HumanLocal, "@新12 请分配任务", names)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.RoutedTo) != 1 || msg.RoutedTo[0] != "sc_dep" {
		t.Fatalf("alias mention routed = %v, want [sc_dep]", msg.RoutedTo)
	}
	// ③c 多人 @ → 落多行（多人 @ 语义在此定死）。
	msg, err = s.PublishWithMentionNames(g.ID, HumanLocal, "@sc_b @新12 两位一起看", names)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.RoutedTo) != 2 {
		t.Fatalf("multi-mention routed = %v, want both members", msg.RoutedTo)
	}
	// ③d 前缀碰撞：@新12 不得连带路由名为「新1」的成员（最长 needle 胜出）。
	pfx := map[string][]string{"sc_dep": {"新12"}, "sc_b": {"新1"}}
	msg, err = s.PublishWithMentionNames(g.ID, HumanLocal, "@新12 只找新12", pfx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.RoutedTo) != 1 || msg.RoutedTo[0] != "sc_dep" {
		t.Fatalf("prefix collision routed = %v, want [sc_dep] only", msg.RoutedTo)
	}
	// ③e @ 发送者自身 → 不入列（自回声铁律不变），其余无人。
	msg, err = s.PublishWithMentionNames(g.ID, "sc_dep", "@sc_dep 自言自语", names)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.RoutedTo) != 0 {
		t.Fatalf("self-mention routed = %v, want empty", msg.RoutedTo)
	}
	// ③f 未知 @ → 不是路由指令，落回副管理员分支。
	msg, err = s.PublishWithMentionNames(g.ID, HumanLocal, "@路人甲 大家看下", names)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.RoutedTo) != 1 || msg.RoutedTo[0] != "sc_dep" {
		t.Fatalf("unknown mention must fall back to deputy, routed = %v", msg.RoutedTo)
	}

	// mailbox 端到端：drain 后只有被 @ 过的成员收到信。
	if _, err := s.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := inboxLen(t, dir, "sc_dep"); n < 2 { // ③b + ③f 两封（③c 一封 + …）
		t.Fatalf("sc_dep inbox = %d, want >= 2", n)
	}
	if n := inboxLen(t, dir, "sc_b"); n < 1 {
		t.Fatalf("sc_b inbox = %d, want >= 1", n)
	}
	if n := inboxLen(t, dir, HumanLocal); n != 0 {
		t.Fatalf("human inbox = %d, want 0", n)
	}
}

// 副管理员失效（退群/被移除后 deputy_id 未清）→ 降级为 deputyless 分支：
// 不给非成员投信，Deputyless=true 照常提示用户重设。
func TestPublishStaleDeputyDegradesToDeputyless(t *testing.T) {
	s, dir, _ := newStore(t)
	g := humanGroup(t, s, "副管离职群")
	if err := s.Join(g.ID, "sc_dep"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDeputy(g.ID, "sc_dep"); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(g.ID, "sc_c"); err != nil {
		t.Fatal(err)
	}
	// 副管理员退群（RemoveMember 群主专用，Leave 是成员自己的路）。
	if err := s.Leave(g.ID, "sc_dep"); err != nil {
		t.Fatal(err)
	}

	msg, err := s.Publish(g.ID, HumanLocal, "副管不在了，先停发")
	if err != nil {
		t.Fatal(err)
	}
	if !msg.Deputyless || len(msg.RoutedTo) != 0 {
		t.Fatalf("stale deputy must degrade to deputyless: %+v", msg)
	}
	if rows := fanoutMembers(t, s, g.ID); len(rows) != 0 {
		t.Fatalf("fanout rows = %v, want none", rows)
	}
	if n := inboxLen(t, dir, "sc_dep"); n != 0 {
		t.Fatalf("ex-deputy inbox = %d, want 0 (non-member must not be mailed)", n)
	}
	if n := inboxLen(t, dir, "sc_c"); n != 0 {
		t.Fatalf("sc_c inbox = %d, want 0", n)
	}
}

// 路由范围边界：ownerless / session-owned 群保留 349 全成员 fanout 原语义
// （迁移契约 + 副管理员概念不存在），Deputyless 永不为真。
func TestPublishLegacyChannelsKeepFanoutAll(t *testing.T) {
	s, dir, _ := newStore(t)
	legacy, err := s.CreateChannel("老群", "", 0, "sc_a", "sc_b") // 无主
	if err != nil {
		t.Fatal(err)
	}
	owned, err := s.CreateChannelWithOwner("会话群", "", 0, MemberTypeSession, "sc_owner", "sc_x", "sc_y")
	if err != nil {
		t.Fatal(err)
	}

	msg, err := s.Publish(legacy.ID, "sc_a", "老群照常广播")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Deputyless || len(msg.RoutedTo) != 1 || msg.RoutedTo[0] != "sc_b" {
		t.Fatalf("ownerless channel must keep fan-out-all: %+v", msg)
	}
	msg, err = s.Publish(owned.ID, "sc_owner", "会话群照常广播")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Deputyless || len(msg.RoutedTo) != 2 {
		t.Fatalf("session-owned channel must keep fan-out-all: %+v", msg)
	}
	if _, err := s.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := inboxLen(t, dir, "sc_b"); n != 1 {
		t.Fatalf("legacy delivery broken: sc_b inbox = %d", n)
	}

	// routeRecipients 纯函数级补钉：显式 @ 优先于副管理员分支；nil names 走
	// contact-id 解析（工具面默认形态）。
	route, deputyless := routeRecipients("@sc_x 看一下", []string{"sc_x", "sc_y"}, nil, "sc_y", MemberTypeHuman)
	if deputyless || len(route) != 1 || route[0] != "sc_x" {
		t.Fatalf("mention must beat deputy branch: route=%v deputyless=%v", route, deputyless)
	}
}

// mentionedMembers 单测：语法边界（@在哪都算、换行分隔、@独立不成词不误判）。
func TestMentionedMembersEdges(t *testing.T) {
	members := []string{"sc_a", "sc_b"}
	names := map[string][]string{"sc_a": {"甲"}, "sc_b": {"乙"}}
	cases := []struct {
		body string
		want []string
	}{
		{"@sc_a 收", []string{"sc_a"}},
		{"先看这个\n@乙 再看", []string{"sc_b"}},   // 换行后的别名
		{"邮箱 a@sc_a.com 里的 @ 不是提及", nil},      // 语法只认 @开头 needle —— a@sc_a 不以 @ 开头，不成路由
		{"@@sc_a 双写也算", []string{"sc_a"}},       // 第二个 @ 起始同样命中
		{"@甲@乙 连写两个", []string{"sc_a", "sc_b"}}, // 连写各自命中
		{"没有提及", nil},
	}
	for _, c := range cases {
		got := mentionedMembers(c.body, members, names)
		if len(got) != len(c.want) {
			t.Fatalf("mentionedMembers(%q) = %v, want %v", c.body, got, c.want)
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("mentionedMembers(%q) = %v, want %v", c.body, got, c.want)
			}
		}
	}
}
