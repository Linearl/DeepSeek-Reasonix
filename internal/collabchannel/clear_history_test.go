package collabchannel

// tl1721 — 消息清空（入口：聊天-详情）契约钉：
//
//   语义：清空 = 共享历史的软删除墓碑（channels.cleared_at）——所有读路径
//        （Messages / 花名册计数 / ExportMarkdown）对全体成员隐藏墓碑及更早
//        的消息；已投递进成员邮箱的副本不回收（task-309 收件箱属收件人，
//        与单条 Cancel 同一边界）；仍排队的 fanout 行翻 cancelled。
//   权限：owner-only（requireOwner）——副管理员只有 ChannelInvite（856 ⑤），
//        删除面与 RemoveMember/Dissolve 同线；无主老群拒绝。
//   「怎么退」：RestoreHistory 归零墓碑，历史全量回归（行未删，软删除）。
//   schema：v5 additive——v4 库（无 cleared_at 列）打开即迁移，旧行值不动。
//
//   投递路由注意（tl1406）：human 群未 @ 且无副管理员 = 零投递（裁决 Q3），
//   所以 fanout 影响面测试用 session 群（fan-out-all 原语义）。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearClock 给出递增毫秒时钟：clear 墓碑必须晚于既有消息、早于清空后的
// 新发布——固定时钟会把「清空后发布」也藏进墓碑（at <= cleared_at），那是
// 真实世界不会出现的同毫秒退化，测试里用递增时钟绕开。
func clearClock(s *Store) {
	ms := int64(1_700_000_000_000)
	s.SetClock(func() int64 { ms += 1_000; return ms })
}

// clearedGroup 造一只 human 群主群（聊天-详情面板的主场景），成员 sc_a/sc_b。
func clearedGroup(t *testing.T, s *Store) Channel {
	t.Helper()
	c, err := s.CreateChannelWithOwner("清空演练", "", 0, MemberTypeHuman, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"sc_a", "sc_b"} {
		if err := s.Join(c.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func mustPublish(t *testing.T, s *Store, ref, sender, body string) Message {
	t.Helper()
	m, err := s.Publish(ref, sender, body)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func visibleCount(t *testing.T, s *Store, ref string) int {
	t.Helper()
	msgs, err := s.Messages(ref, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	return len(msgs)
}

// 主链路：发布→清空（全体读面隐藏+计数归零+导出为空）→清空后发布可见→
// 恢复→全量回归（软删除的行都在）。
func TestClearHistoryHidesSharedHistoryThenRestoreBringsItBack(t *testing.T) {
	s := testStore(t)
	clearClock(s)
	g := clearedGroup(t, s)

	m1 := mustPublish(t, s, g.ID, HumanLocal, "第一条")
	m2 := mustPublish(t, s, g.ID, "sc_a", "第二条")
	if n := visibleCount(t, s, g.ID); n != 2 {
		t.Fatalf("pre-clear visible = %d, want 2", n)
	}

	res, err := s.ClearHistory(g.ID, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	if res.WasCleared {
		t.Fatal("first clear must not report WasCleared")
	}
	if res.ClearedMessages != 2 {
		t.Fatalf("cleared = %d, want 2", res.ClearedMessages)
	}
	if res.ClearedAt == 0 {
		t.Fatal("tombstone time missing")
	}

	// 共享可见变化：频道读面=全体成员的读面，清空后为空（不是本地已读态）。
	if n := visibleCount(t, s, g.ID); n != 0 {
		t.Fatalf("post-clear visible = %d, want 0", n)
	}
	c, err := s.Channel(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClearedAt != res.ClearedAt || c.Messages != 0 {
		t.Fatalf("channel tombstone/count = %d/%d, want %d/0", c.ClearedAt, c.Messages, res.ClearedAt)
	}

	// 清空后发布照常可见（新对话不被墓碑吞掉）。
	mustPublish(t, s, g.ID, HumanLocal, "清空后的新话")
	if n := visibleCount(t, s, g.ID); n != 1 {
		t.Fatalf("after fresh publish visible = %d, want 1", n)
	}

	// 导出面同语义（md 导出只见可见区间）。
	path := filepath.Join(t.TempDir(), "export.md")
	if _, err := s.ExportMarkdown(g.ID, path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{m1.Body, m2.Body} {
		if strings.Contains(string(b), body) {
			t.Fatalf("cleared body %q must not appear in export", body)
		}
	}
	if !strings.Contains(string(b), "清空后的新话") {
		t.Fatal("fresh post-clear line must appear in export")
	}

	// 「怎么退」：恢复 = 墓碑归零，历史全量回归。
	back, err := s.RestoreHistory(g.ID, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	if back.ClearedAt != 0 || back.Messages != 3 {
		t.Fatalf("restored channel = clearedAt %d count %d, want 0/3", back.ClearedAt, back.Messages)
	}
	if n := visibleCount(t, s, g.ID); n != 3 {
		t.Fatalf("restored visible = %d, want 3", n)
	}
}

// 墓碑前移与幂等：未恢复就再清空=墓碑前移（WasCleared=true，只藏增量）；
// 恢复归零后再清空=全新清空（WasCleared=false，覆盖当前全部可见历史——
// 恢复把旧行也带回来了，它们重新属于「现有历史」）；重复恢复幂等。
func TestClearHistoryReClearMovesTombstoneAndRestoreIdempotent(t *testing.T) {
	s := testStore(t)
	clearClock(s)
	g := clearedGroup(t, s)

	mustPublish(t, s, g.ID, "sc_a", "旧一")
	first, err := s.ClearHistory(g.ID, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	if first.WasCleared {
		t.Fatal("first clear must not report WasCleared")
	}
	mustPublish(t, s, g.ID, "sc_b", "新一")

	// 未恢复就再清空 = 墓碑前移，只藏增量（旧一已在旧墓碑之下）。
	res, err := s.ClearHistory(g.ID, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	if !res.WasCleared {
		t.Fatal("re-clear on a cleared channel must report WasCleared")
	}
	if res.ClearedMessages != 1 {
		t.Fatalf("re-clear newly hidden = %d, want 1 (only the new line)", res.ClearedMessages)
	}
	if n := visibleCount(t, s, g.ID); n != 0 {
		t.Fatalf("post re-clear visible = %d, want 0", n)
	}

	// 恢复 = 全量回归；重复恢复幂等。
	if _, err := s.RestoreHistory(g.ID, HumanLocal); err != nil {
		t.Fatalf("restore on cleared channel must work: %v", err)
	}
	again, err := s.RestoreHistory(g.ID, HumanLocal)
	if err != nil {
		t.Fatalf("second restore must stay idempotent: %v", err)
	}
	if again.ClearedAt != 0 || again.Messages != 2 {
		t.Fatalf("idempotent restore = %d/%d, want 0/2", again.ClearedAt, again.Messages)
	}

	// 恢复后再清空 = 全新清空：可见历史=两行，全部隐藏。
	fresh, err := s.ClearHistory(g.ID, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.WasCleared {
		t.Fatal("clear after restore is a fresh tombstone, not a move")
	}
	if fresh.ClearedMessages != 2 {
		t.Fatalf("fresh clear after restore newly hidden = %d, want 2", fresh.ClearedMessages)
	}
}

// 权限：普通成员拒绝；副管理员拒绝（拉人权≠删除权，856 ⑤ 边界）；无主老群拒绝。
func TestClearHistoryPermissionOwnerOnly(t *testing.T) {
	s := testStore(t)
	clearClock(s)
	g := clearedGroup(t, s)
	if err := s.Join(g.ID, "sc_c"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ClearHistory(g.ID, "sc_c"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("ordinary member clear must be ErrNotOwner, got %v", err)
	}
	if _, err := s.RestoreHistory(g.ID, "sc_c"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("ordinary member restore must be ErrNotOwner, got %v", err)
	}

	// 副管理员：有 ChannelInvite，无清空权。
	if err := s.Invite(g.ID, HumanLocal, "sc_d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDeputy(g.ID, "sc_d"); err != nil {
		t.Fatal(err)
	}
	if err := s.Invite(g.ID, "sc_d", "sc_e"); err != nil {
		t.Fatalf("deputy invite sanity: %v", err)
	}
	if _, err := s.ClearHistory(g.ID, "sc_d"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("deputy clear must be ErrNotOwner (invite-only), got %v", err)
	}

	// 无主老群（pre-803）：清空拒绝。
	old, err := s.CreateChannel("无主老群", "", 0, "sc_x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearHistory(old.ID, "sc_x"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("ownerless channel clear must be ErrNotOwner, got %v", err)
	}
}

// 投递影响面（session 群=fan-out-all 原语义）：清空取消仍排队的 fanout
// （不再把将藏的消息投出去）；已投递计数如实上报（副本属收件人，不回收）。
func TestClearHistoryStopsQueuedFanoutAndCountsDelivered(t *testing.T) {
	s := testStore(t)
	clearClock(s)
	g, err := s.CreateChannelWithOwner("投递清空", "", 0, MemberTypeSession, "sc_owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Join(g.ID, "sc_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.JoinHuman(g.ID); err != nil {
		t.Fatal(err)
	}

	// 一条排队中（测试不跑 drain，行保持 queued）；sc_owner 是发送者不入列，
	// fan-out = sc_a + human:local 共 2 行。
	mustPublish(t, s, g.ID, "sc_owner", "排队一")

	res, err := s.ClearHistory(g.ID, "sc_owner")
	if err != nil {
		t.Fatal(err)
	}
	if res.CancelledQueued != 2 {
		t.Fatalf("cancelledQueued = %d, want 2", res.CancelledQueued)
	}
	if res.AlreadyDelivered != 0 {
		t.Fatalf("alreadyDelivered = %d, want 0", res.AlreadyDelivered)
	}

	// fanout 行真的翻了 cancelled。
	rows, err := s.FanoutStates(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.State != "cancelled" {
			t.Fatalf("fanout row %s→%s state = %q, want cancelled", r.MessageID, r.Member, r.State)
		}
	}

	// 恢复后发新消息：human 行经 MarkHumanVisible 落 delivered（UI 可见即已读），
	// sc_a 行保持排队——再清空时 CancelledQueued=1、AlreadyDelivered=1。
	if _, err := s.RestoreHistory(g.ID, "sc_owner"); err != nil {
		t.Fatal(err)
	}
	m2 := mustPublish(t, s, g.ID, "sc_owner", "恢复后新话")
	if _, err := s.MarkHumanVisible(g.ID, []string{m2.ID}); err != nil {
		t.Fatal(err)
	}
	res2, err := s.ClearHistory(g.ID, "sc_owner")
	if err != nil {
		t.Fatal(err)
	}
	if res2.CancelledQueued != 1 {
		t.Fatalf("second clear cancelledQueued = %d, want 1 (sc_a row)", res2.CancelledQueued)
	}
	if res2.AlreadyDelivered != 1 {
		t.Fatalf("second clear alreadyDelivered = %d, want 1 (human row)", res2.AlreadyDelivered)
	}
	if res2.ClearedMessages != 2 {
		t.Fatalf("second clear newly hidden = %d, want 2 (restore made the whole span visible again)", res2.ClearedMessages)
	}

	// 清空对已投递副本的边界：副本仍 delivered（不回收），drain 语义靠既有
	// cancelled 守卫跳过——这里只钉行状态，不再重复 DrainFanout 职责。
	rows, err = s.FanoutStates(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivered := 0
	for _, r := range rows {
		switch r.State {
		case "delivered":
			delivered++
		case "cancelled":
		default:
			t.Fatalf("unexpected fanout state %q after clear", r.State)
		}
	}
	if delivered != 1 {
		t.Fatalf("delivered copies = %d, want 1 (recipient-owned, untouched)", delivered)
	}
}
