package collabchannel

import (
	"context"
	"testing"
)

// tl1563 已读推进：MarkReadByDelivery 按投递编号（chf_ fanout_id）推进
// read_at。口径（20261112 msg_04cdadbd）：已读 = 消息进入成员上下文 且 该
// 成员回合运行中——推进动作本身在消费侧（control/agent 包），本包只钉存储
// 语义：行键 (message, member)、幂等、撤回诚实、未知编号零行。

// 主语义：成员用自己的投递编号推进自己的行；他人行不覆写；重复推进幂等；
// 未知编号/空参零行。
func TestMarkReadByDeliveryAdvancesOwnRowOnly(t *testing.T) {
	s, _, _ := newStore(t)
	if _, err := s.CreateChannel("dev", "", 0, "sc_a", "sc_b", "sc_c"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("dev", "sc_s", "hello channel"); err != nil {
		t.Fatal(err)
	}
	stats, err := s.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Delivered != 3 {
		t.Fatalf("fan-out stats: %+v", stats)
	}

	// 收集每个成员自己的投递编号（fanout_id 一人一枚，tl1566 后称投递编号）。
	byMember := map[string]string{}
	for _, r := range mustFanoutRows(t, s, "dev") {
		byMember[r.Member] = r.FanoutID
	}
	for _, m := range []string{"sc_a", "sc_b", "sc_c"} {
		if byMember[m] == "" {
			t.Fatalf("member %s has no fanout id: %+v", m, byMember)
		}
	}

	// sc_a 按投递编号推进——只动自己的行。
	n, err := s.MarkReadByDelivery("sc_a", []string{byMember["sc_a"]})
	if err != nil || n != 1 {
		t.Fatalf("MarkReadByDelivery: n=%d err=%v", n, err)
	}
	for _, r := range mustFanoutRows(t, s, "dev") {
		if r.Member == "sc_a" && r.ReadAt == 0 {
			t.Fatalf("sc_a must be read: %+v", r)
		}
		if r.Member != "sc_a" && r.ReadAt != 0 {
			t.Fatalf("%s read state leaked: %+v", r.Member, r)
		}
	}

	// 幂等：同一编号再推不动（read_at IS NULL 守卫）。
	if n, _ := s.MarkReadByDelivery("sc_a", []string{byMember["sc_a"]}); n != 0 {
		t.Fatalf("re-advance must be a no-op, moved %d", n)
	}

	// 他人编号对 sc_a 是空匹配——成员拿别人的投递编号推不动别人的行。
	if n, _ := s.MarkReadByDelivery("sc_a", []string{byMember["sc_b"]}); n != 0 {
		t.Fatalf("foreign delivery id must match nothing, moved %d", n)
	}
	// 编号对但成员错：sc_b 的行仍可由 sc_b 自己推进（行键含 member）。
	if n, _ := s.MarkReadByDelivery("sc_b", []string{byMember["sc_b"]}); n != 1 {
		t.Fatalf("sc_b marks its own row, got %d", n)
	}

	// 未知编号与空参零行不报错。
	if n, _ := s.MarkReadByDelivery("sc_c", []string{"chf_nonexistent"}); n != 0 {
		t.Fatalf("unknown id must move 0, moved %d", n)
	}
	if n, _ := s.MarkReadByDelivery("", []string{byMember["sc_c"]}); n != 0 {
		t.Fatalf("empty member must move 0, moved %d", n)
	}
	if n, _ := s.MarkReadByDelivery("sc_c", nil); n != 0 {
		t.Fatalf("nil ids must move 0, moved %d", n)
	}
}

// 边界（撤回诚实）：投递前被取消的行 state='cancelled'，推进守卫不把它标
// 已读——撤回消息成员根本没见过，不能在 own 消息下显示「XX 已读」。
func TestMarkReadByDeliverySkipsCancelledRows(t *testing.T) {
	s, _, _ := newStore(t)
	if _, err := s.CreateChannel("dev", "", 0, "sc_a", "sc_b"); err != nil {
		t.Fatal(err)
	}
	msg, err := s.Publish("dev", "sc_s", "retract me")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Cancel("dev", msg.ID, "sc_s")
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	stats, err := s.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Pre-drain cancel flips rows to 'cancelled' before the drain's queued
	// scan ever sees them, so the pass is empty (撤回=从未投递).
	if stats.Queued != 0 || stats.Cancelled != 0 {
		t.Fatalf("pre-drain cancel means nothing to drain: %+v", stats)
	}
	for _, r := range mustFanoutRows(t, s, "dev") {
		if r.State != "cancelled" {
			t.Fatalf("row must be cancelled: %+v", r)
		}
		if n, _ := s.MarkReadByDelivery(r.Member, []string{r.FanoutID}); n != 0 {
			t.Fatalf("cancelled row must not advance read_at, moved %d", n)
		}
	}
}

func mustFanoutRows(t *testing.T, s *Store, ref string) []FanoutRow {
	t.Helper()
	rows, err := s.FanoutStates(ref)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
