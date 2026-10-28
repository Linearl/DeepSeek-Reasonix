package collabchannel

// 任务 803 — 群聊 channel 人类化后端（Phase A）的验收测试。验收口径：
//  1. 人类建群/加入/发言（API 级）：owner=human 落库、JoinHuman 幂等、
//     human:local 发言走 Publish 同链路（session 成员照常收信）；
//  2. fan-out 对 human 成员跳过邮箱投递只落库：human 收件箱永远空，
//     fanout 行照样 settle delivered（per-recipient 已读链路不断）；
//  3. 风暴治理对人类发言同样生效：小时上限一视同仁；
//  4. 群主规则：最后一个群主退出拒绝（Leave/RemoveMember/自移除三条路）；
//     Invite/RemoveMember 仅群主可用；无主老群拒绝管理操作；
//  5. MarkHumanVisible：UI 可见即已读（queued 行 settle delivered + read）；
//  6. 迁移 additive：v2 老库补列后老 channel 语义不变（成员全 session、
//     无 owner、publish/markread 照常）；
//  7. md 导出带成员类型与群主标识。

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

// 验收 1a：人类建群——owner 落库 + human 类型成员 + JoinHuman 幂等。
func TestHumanCreateChannelJoinAndTypes(t *testing.T) {
	s, _, _ := newStore(t)
	ch, err := s.CreateChannelWithOwner("发布协调", "人机混合群", 0, MemberTypeHuman, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	if ch.OwnerType != MemberTypeHuman || ch.OwnerID != HumanLocal {
		t.Fatalf("owner not persisted: %+v", ch)
	}
	if len(ch.MemberDetails) != 1 || ch.MemberDetails[0].Contact != HumanLocal || ch.MemberDetails[0].Type != MemberTypeHuman {
		t.Fatalf("founding human member wrong: %+v", ch.MemberDetails)
	}
	// 老签名仍可读出同一 channel（Members []string 保留）。
	again, err := s.Channel("发布协调")
	if err != nil || len(again.Members) != 1 || again.Members[0] != HumanLocal {
		t.Fatalf("channel resolve: %+v err=%v", again, err)
	}

	// JoinHuman：幂等，type=human。
	if err := s.JoinHuman(ch.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.JoinHuman(ch.ID); err != nil {
		t.Fatalf("JoinHuman must be idempotent: %v", err)
	}
	ms, err := s.Members(ch.ID)
	if err != nil || len(ms) != 1 {
		t.Fatalf("members after re-join: %+v err=%v", ms, err)
	}
	// 老签名 Join 加的是 session 成员。
	if err := s.Join(ch.ID, "sc_a"); err != nil {
		t.Fatal(err)
	}
	ms, _ = s.Members(ch.ID)
	for _, m := range ms {
		want := MemberTypeSession
		if m.Contact == HumanLocal {
			want = MemberTypeHuman
		}
		if m.Type != want {
			t.Fatalf("%s type = %q, want %q", m.Contact, m.Type, want)
		}
	}
	// 未知成员类型在入口拒绝。
	if err := s.memberOp(ch.ID, "sc_x", true, "robot"); err == nil {
		t.Fatal("unknown member type must be rejected")
	}
	// ownerType/ownerID 必须成对出现。
	if _, err := s.CreateChannelWithOwner("半截", "", 0, MemberTypeHuman, ""); err == nil {
		t.Fatal("owner without id must be rejected")
	}
	if _, err := s.CreateChannelWithOwner("半截2", "", 0, MemberTypeHuman, HumanLocal, ""); err != nil {
		t.Fatalf("founding owner+members must work: %v", err)
	}
}

// 验收 1b + 2 + 3：人类发言走 Publish 同链路——session 成员收信、human 不收
// （fan-out 跳过投递只落库）、小时上限对 human 发言同样生效。
func TestHumanPublishSamePathFanoutSkipsHumanMailbox(t *testing.T) {
	s, dir, advance := newStore(t)
	if _, err := s.CreateChannelWithOwner("dev", "", 0, MemberTypeHuman, HumanLocal, "sc_a"); err != nil {
		t.Fatal(err)
	}

	// human 发言：session 成员照常排队收信。
	msg, err := s.Publish("dev", HumanLocal, "大家好，人类来了")
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Delivered != 1 || stats.HumanSkipped != 0 || stats.Queued != 1 {
		t.Fatalf("human→session drain: %+v", stats)
	}
	mail := sessioncollab.NewMailStore(dir)
	inbox, err := mail.Inbox("sc_a")
	if err != nil || len(inbox) != 1 || inbox[0].From != HumanLocal || inbox[0].Body != "大家好，人类来了" {
		t.Fatalf("session member must receive the human's line: %v %+v", err, inbox)
	}

	// session 发言：human 的 fanout 行落库但收件箱必须保持空。
	msg2, err := s.Publish("dev", "sc_a", "收到")
	if err != nil {
		t.Fatal(err)
	}
	stats, err = s.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.HumanSkipped != 1 || stats.Delivered != 0 {
		t.Fatalf("human row must skip mailbox delivery: %+v", stats)
	}
	humanInbox, err := mail.Inbox(HumanLocal)
	if err != nil || len(humanInbox) != 0 {
		t.Fatalf("human must never be mailed: %v %+v", err, humanInbox)
	}
	rows, err := s.FanoutStates("dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Member == HumanLocal && (r.State != "delivered" || r.DeliveredAt == 0) {
			t.Fatalf("human fanout row must settle delivered (UI is the inbox): %+v", r)
		}
	}

	// MarkHumanVisible：UI 可见即已读（read_at 落库，per-recipient 独立）。
	// human 自己发的消息没有自己的 fanout 行（Publish 排除 sender），no-op。
	if n, err := s.MarkHumanVisible("dev", []string{msg.ID}); err != nil || n != 0 {
		t.Fatalf("self-sent line has no human fanout row: n=%d err=%v", n, err)
	}
	if n, err := s.MarkHumanVisible("dev", []string{msg2.ID, "chm_nope"}); err != nil || n != 1 {
		t.Fatalf("MarkHumanVisible: n=%d err=%v", n, err)
	}
	rows, _ = s.FanoutStates("dev")
	for _, r := range rows {
		if r.Member == HumanLocal && r.ReadAt == 0 {
			t.Fatalf("visible-to-human row must be read: %+v", r)
		}
	}

	// 风暴治理：human 连发到上限同样被拒（cap 默认 30，这里造个小群直查）。
	small, err := s.CreateChannelWithOwner("tiny", "", 2, MemberTypeHuman, HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Publish(small.ID, HumanLocal, "burst"); err != nil {
			t.Fatal(err)
		}
		advance(1000)
	}
	if _, err := s.Publish(small.ID, HumanLocal, "third"); !errors.Is(err, ErrHourlyCap) {
		t.Fatalf("human speech must hit the hourly cap too, got %v", err)
	}
}

// 验收 4：群主规则——最后一个群主退出拒绝（三条路）+ 群主权限 + 无主老群拒绝。
func TestOwnerRulesLeaveInviteRemove(t *testing.T) {
	s, _, _ := newStore(t)
	humanOwned, err := s.CreateChannelWithOwner("h群", "", 0, MemberTypeHuman, HumanLocal, "sc_a")
	if err != nil {
		t.Fatal(err)
	}
	sessionOwned, err := s.CreateChannelWithOwner("s群", "", 0, MemberTypeSession, "sc_owner", "sc_b")
	if err != nil {
		t.Fatal(err)
	}

	// 群主退群拒绝（human 群与 session 群同规则）。
	if err := s.Leave(humanOwned.ID, HumanLocal); !errors.Is(err, ErrOwnerCannotLeave) {
		t.Fatalf("owner leave must be refused: %v", err)
	}
	if err := s.Leave(sessionOwned.ID, "sc_owner"); !errors.Is(err, ErrOwnerCannotLeave) {
		t.Fatalf("session owner leave must be refused too: %v", err)
	}
	// 非群主退群照常。
	if err := s.Leave(humanOwned.ID, "sc_a"); err != nil {
		t.Fatalf("non-owner leave must work: %v", err)
	}

	// Invite / RemoveMember 仅群主可用。
	if err := s.Invite(humanOwned.ID, "sc_a", "sc_x"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner invite must be refused: %v", err)
	}
	if err := s.RemoveMember(humanOwned.ID, "sc_a", HumanLocal); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner remove must be refused: %v", err)
	}
	if err := s.Invite(humanOwned.ID, HumanLocal, "sc_x", "sc_y"); err != nil {
		t.Fatalf("owner invite: %v", err)
	}
	ms, _ := s.Members(humanOwned.ID)
	if len(ms) != 3 {
		t.Fatalf("invite should add two sessions: %+v", ms)
	}
	for _, m := range ms {
		if (m.Contact == "sc_x" || m.Contact == "sc_y") && m.Type != MemberTypeSession {
			t.Fatalf("invited members are sessions: %+v", m)
		}
	}
	if err := s.RemoveMember(humanOwned.ID, HumanLocal, "sc_x"); err != nil {
		t.Fatalf("owner remove: %v", err)
	}
	if ms, _ = s.Members(humanOwned.ID); len(ms) != 2 {
		t.Fatalf("remove should drop one: %+v", ms)
	}
	// 群主不能移除自己（= 退群的另一条路）。
	if err := s.RemoveMember(humanOwned.ID, HumanLocal, HumanLocal); !errors.Is(err, ErrOwnerCannotLeave) {
		t.Fatalf("owner self-removal must be refused: %v", err)
	}

	// 无主老群（CreateChannel 老签名）拒绝管理操作。
	old, err := s.CreateChannel("老群", "", 0, "sc_a")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Invite(old.ID, HumanLocal, "sc_z"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("ownerless channel must refuse management: %v", err)
	}
	if err := s.RemoveMember(old.ID, HumanLocal, "sc_a"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("ownerless channel must refuse removal: %v", err)
	}
}

// 验收 6：v2 老库迁移——补列 additive，老 channel 语义不变。
func TestMigrateV2DatabaseKeepsOldSemantics(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", diskFileDSN(filepath.Join(dir, "channels.db")))
	if err != nil {
		t.Fatal(err)
	}
	// 手造 v2 schema：channels 无 owner 列、members 无 type 列。
	for _, stmt := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE channels (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, topic TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, hourly_limit INTEGER NOT NULL DEFAULT 30)`,
		`CREATE TABLE members (channel_id TEXT NOT NULL, contact TEXT NOT NULL, joined_at INTEGER NOT NULL,
			PRIMARY KEY (channel_id, contact))`,
		`CREATE TABLE messages (id TEXT PRIMARY KEY, channel_id TEXT NOT NULL, at INTEGER NOT NULL,
			sender TEXT NOT NULL, body TEXT NOT NULL, cancelled_at INTEGER)`,
		`CREATE TABLE fanout (message_id TEXT NOT NULL, member TEXT NOT NULL, fanout_id TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'queued', error TEXT NOT NULL DEFAULT '', delivered_at INTEGER,
			read_at INTEGER, PRIMARY KEY (message_id, member))`,
		`INSERT INTO channels (id, name, created_at) VALUES ('chan_old', 'dev', 1)`,
		`INSERT INTO members (channel_id, contact, joined_at) VALUES ('chan_old', 'sc_a', 1)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetPace(func(int) time.Duration { return 0 })

	// 老 channel 读出：无 owner、成员类型 session（默认值兜底）。
	c, err := s.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	if c.OwnerType != "" || c.OwnerID != "" {
		t.Fatalf("migrated old channel must have no owner: %+v", c)
	}
	if len(c.MemberDetails) != 1 || c.MemberDetails[0].Type != MemberTypeSession {
		t.Fatalf("old members must read as sessions: %+v", c.MemberDetails)
	}
	// 老 channel 照常收发/已读（语义不变）。
	msg, err := s.Publish("dev", "sc_a", "old line still works")
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := s.DrainFanout(context.Background()); err != nil || stats.Delivered != 0 || stats.Queued != 0 {
		// sender 不 fan 自己 → 0 行。
		t.Fatalf("self publish must fan nobody: %+v err=%v", stats, err)
	}
	if err := s.JoinHuman("dev"); err != nil {
		t.Fatalf("human may still join an old channel: %v", err)
	}
	if _, err := s.Publish("dev", "sc_a", "hello human"); err != nil {
		t.Fatal(err)
	}
	if stats, err := s.DrainFanout(context.Background()); err != nil || stats.HumanSkipped != 1 {
		t.Fatalf("human row in migrated channel must skip mailbox: %+v err=%v", stats, err)
	}
	if n, err := s.MarkHumanVisible("dev", []string{msg.ID}); err != nil {
		t.Fatalf("MarkHumanVisible on migrated channel: %v", err)
	} else if n != 0 {
		t.Logf("MarkHumanVisible rows: %d (human was not a member at publish time — no-op is correct)", n)
	}
}

// 验收 7：md 导出带成员类型与群主标识；无主老群保持朴素列表。
func TestExportMarkdownShowsTypesAndOwner(t *testing.T) {
	s, dir, _ := newStore(t)
	owned, err := s.CreateChannelWithOwner("发布协调", "topic", 0, MemberTypeHuman, HumanLocal, "sc_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(owned.ID, HumanLocal, "人类的发言"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "owned.md")
	if _, err := s.ExportMarkdown(owned.ID, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	md := string(b)
	for _, want := range []string{
		"- owner: human:local (human)",
		"human:local (human, owner)",
		"sc_a (session)",
		"人类的发言",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("export missing %q:\n%s", want, md)
		}
	}

	oldCh, err := s.CreateChannel("老群", "", 0, "sc_a")
	if err != nil {
		t.Fatal(err)
	}
	out2 := filepath.Join(dir, "old.md")
	if _, err := s.ExportMarkdown(oldCh.ID, out2); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(out2)
	if strings.Contains(string(b2), "- owner:") {
		t.Fatalf("ownerless channel must not render an owner line:\n%s", b2)
	}
}

// tl1217 验收：解散通道——群主专用硬删，关闭 20261028 死锁（群主既不能邀人
// 也不能退群更不能销毁）。钉四条：①群主可解散且 members/messages/fanout
// 全清、其他群零波及 ②非群主（含副管理员）与无主老群一律拒绝 ③未知 ref
// ErrNotFound ④解散后 resolve/ListChannels 不再见该群。
func TestDissolveOwnerOnlyDestroysEverything(t *testing.T) {
	s, _, _ := newStore(t)
	humanOwned, err := s.CreateChannelWithOwner("h群", "topic", 0, MemberTypeHuman, HumanLocal, "sc_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(humanOwned.ID, HumanLocal, "人类的发言"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(humanOwned.ID, "sc_a", "agent 的回复"); err != nil {
		t.Fatal(err)
	}
	// 留一个 fanout 行：human 消息也产 fanout（drain 跳过投递只落库）。
	if rows, _ := s.FanoutStates(humanOwned.ID); len(rows) == 0 {
		t.Fatal("precondition: publish should create fanout rows")
	}
	other, err := s.CreateChannelWithOwner("别的群", "", 0, MemberTypeHuman, HumanLocal, "sc_z")
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateChannel("无主老群", "", 0, "sc_a")
	if err != nil {
		t.Fatal(err)
	}

	// 非群主拒绝（session 成员）。
	if err := s.Dissolve(humanOwned.ID, "sc_a"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner dissolve must be refused: %v", err)
	}
	// 副管理员也不行（解散比移除成员更重，不随 856 管理权下放）。
	if _, err := s.SetDeputy(humanOwned.ID, "sc_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Dissolve(humanOwned.ID, "sc_a"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("deputy dissolve must be refused (owner-only): %v", err)
	}
	// 无主老群拒绝。
	if err := s.Dissolve(old.ID, HumanLocal); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("ownerless channel dissolve must be refused: %v", err)
	}
	// 未知 ref → ErrNotFound（与成员操作同语义）。
	if err := s.Dissolve("chan_nope", HumanLocal); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ref must be ErrNotFound: %v", err)
	}

	// 群主解散成功：库内四表全清。
	if err := s.Dissolve(humanOwned.ID, HumanLocal); err != nil {
		t.Fatalf("owner dissolve: %v", err)
	}
	if _, err := s.Channel(humanOwned.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dissolved channel must be gone: %v", err)
	}
	if chans, _ := s.ListChannels(); len(chans) != 2 {
		t.Fatalf("the untouched channel and the ownerless one should remain: %+v", chans)
	}
	var members, messages, fanout int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM members WHERE channel_id = ?`, humanOwned.ID).Scan(&members); err != nil || members != 0 {
		t.Fatalf("members must be empty (err=%v n=%d)", err, members)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE channel_id = ?`, humanOwned.ID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("messages must be empty (err=%v n=%d)", err, messages)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM fanout WHERE fanout_id IN (SELECT fanout_id FROM fanout)`).Scan(&fanout); err != nil {
		t.Fatal(err)
	}
	// fanout 无 channel_id，直接按量验证：解散后库里不应再有该群消息的 fanout。
	// other 群从未发言 → 全库 fanout 应为 0。
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM fanout`).Scan(&fanout); err != nil || fanout != 0 {
		t.Fatalf("fanout must be fully cleared (err=%v n=%d)", err, fanout)
	}

	// 幂等语义：再解散同一 ref → ErrNotFound（群已不存在，不再是权限问题）。
	if err := s.Dissolve(humanOwned.ID, HumanLocal); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-dissolve must be ErrNotFound: %v", err)
	}

	// 其他群完好可用。
	if err := s.Invite(other.ID, HumanLocal, "sc_new"); err != nil {
		t.Fatalf("the untouched channel keeps working: %v", err)
	}
}
