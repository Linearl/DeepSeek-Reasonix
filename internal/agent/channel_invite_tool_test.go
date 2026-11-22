package agent

// tl1721 — channel_invite 工具面契约钉（副管理员拉人工具缺口，承接 856 ⑤）：
//
//   权限同源：desktop 桥 ChannelInvite 与本工具共用 Store.Invite →
//            requireManager（owner OR deputy）——正例=副管理员（human 群）/
//            群主（session 群）成功；反例=普通成员 ErrNotOwner 拒绝
//            （正反例钉齐，与 desktop 同权限源）。
//   human 不可拉：human:local 行类型是 human，按 session 插行=类型污染——
//            拒绝并指明既有入口（桌面 UI 入群）。
//   幂等：已在群的联系人重复邀请不报错、名册不变。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/collabchannel"
)

// inviteFixture：一只 human 群主群（副管理员 sc_deputy 位 + 普通成员位）与
// 一只 session 群主群（owner 位）；三个调用方身份各自持真实 contact id——
// 联系人 id 由 EnsureContactID 生成，群成员用同一 id 落座（身份=成员，工具
// 调用方与名册对得上）。
func inviteFixture(t *testing.T) (deputyCfg, memberCfg, ownerCfg SessionCollabConfig, humanGroupID, sessionGroupID string) {
	t.Helper()
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")

	cfgOf := func(selfPath, contact string) SessionCollabConfig {
		return SessionCollabConfig{
			Enabled:            true,
			SessionDir:         dir,
			WorkspaceRoot:      dir,
			MailDir:            mailDir,
			CurrentContactID:   contact,
			ResolveSessionPath: func() string { return selfPath },
		}
	}

	depPath := filepath.Join(dir, "deputy.jsonl")
	memPath := filepath.Join(dir, "member.jsonl")
	ownPath := filepath.Join(dir, "owner.jsonl")
	writeEmpty(t, depPath)
	writeEmpty(t, memPath)
	writeEmpty(t, ownPath)
	depID, err := EnsureContactID(depPath)
	if err != nil {
		t.Fatal(err)
	}
	memID, err := EnsureContactID(memPath)
	if err != nil {
		t.Fatal(err)
	}
	ownID, err := EnsureContactID(ownPath)
	if err != nil {
		t.Fatal(err)
	}
	deputyCfg = cfgOf(depPath, depID)
	memberCfg = cfgOf(memPath, memID)
	ownerCfg = cfgOf(ownPath, ownID)

	store, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// human 群：副管理员位 + 普通成员位（856 ⑤ 主场景）。
	g, err := store.CreateChannelWithOwner("拉人演练", "", 0, collabchannel.MemberTypeHuman, collabchannel.HumanLocal)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{depID, memID} {
		if err := store.Join(g.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SetDeputy(g.ID, depID); err != nil {
		t.Fatal(err)
	}

	// session 群：群主=ownID（fan-out-all 广播语义的群主拉人场景）。
	sg, err := store.CreateChannelWithOwner("会话群拉人", "", 0, collabchannel.MemberTypeSession, ownID)
	if err != nil {
		t.Fatal(err)
	}
	return deputyCfg, memberCfg, ownerCfg, g.ID, sg.ID
}

// 正例①：副管理员拉人成功（856 ⑤ 的工具面载体——desktop 同权限源）；
// 正例②：session 群的群主身份成功。
func TestChannelInviteDeputyAndOwnerSucceed(t *testing.T) {
	deputyCfg, _, ownerCfg, _, sessionGroupID := inviteFixture(t)
	ctx := context.Background()

	out, err := NewChannelInviteTool(deputyCfg).Execute(ctx, json.RawMessage(`{"channel":"拉人演练","contacts":["sc_new1","sc_new2"]}`))
	if err != nil {
		t.Fatalf("deputy invite must pass (856 ⑤ tool face): %v", err)
	}
	var payload struct {
		Members []string `json:"members"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload: %v %s", err, out)
	}
	for _, want := range []string{"sc_new1", "sc_new2"} {
		if !containsString(payload.Members, want) {
			t.Fatalf("roster after deputy invite missing %s: %s", want, out)
		}
	}

	// 正例②：session 群群主（按群 id 引用，名字/id 双解析都钉）。
	if _, err := NewChannelInviteTool(ownerCfg).Execute(ctx, json.RawMessage(`{"channel":"`+sessionGroupID+`","contacts":["sc_new3"]}`)); err != nil {
		t.Fatalf("session-group owner invite (by id) must pass: %v", err)
	}
	store, err := collabchannel.Open(deputyCfg.MailDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sg, err := store.Channel(sessionGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(sg.Members, "sc_new3") {
		t.Fatalf("session-group roster: %v", sg.Members)
	}
}

// 反例①：普通成员被权限拒绝（与 desktop 同一 ErrNotOwner 面）；反例②：拉
// human 被类型守卫拒绝；反例③：幂等——重复邀请已在群成员不报错、名册不变；
// 反例④：参数面（空 contacts/空 channel/未知频道）逐一拒绝。
func TestChannelInvitePermissionAndHumanGuard(t *testing.T) {
	deputyCfg, memberCfg, _, humanGroupID, _ := inviteFixture(t)
	ctx := context.Background()

	// 反例①：普通成员（human 群，无主以外的任何人）。
	_, err := NewChannelInviteTool(memberCfg).Execute(ctx, json.RawMessage(`{"channel":"拉人演练","contacts":["sc_anyone"]}`))
	if err == nil || !strings.Contains(err.Error(), "neither the owner nor the deputy") {
		t.Fatalf("ordinary member must be refused with the owner-only reason, got %v", err)
	}

	// 反例②：拉 human（类型守卫，错误指明既有入口=桌面 UI）。
	_, err = NewChannelInviteTool(deputyCfg).Execute(ctx, json.RawMessage(`{"channel":"拉人演练","contacts":["human:local"]}`))
	if err == nil || !strings.Contains(err.Error(), "human:local") {
		t.Fatalf("inviting the human must be refused, got %v", err)
	}

	// 幂等：同一成员连拉两次，第二遍走 INSERT OR IGNORE 不报错、名册不变。
	for range 2 {
		if _, err := NewChannelInviteTool(deputyCfg).Execute(ctx, json.RawMessage(`{"channel":"拉人演练","contacts":["sc_new1"]}`)); err != nil {
			t.Fatalf("inviting (twice, idempotent) must pass: %v", err)
		}
	}
	store, err := collabchannel.Open(deputyCfg.MailDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c, err := store.Channel(humanGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Members) != 4 { // human + deputy + member + sc_new1（sc_new2 未拉）
		t.Fatalf("roster after idempotent invite = %d, want 4", len(c.Members))
	}

	// 反例④：参数面。
	if _, err := NewChannelInviteTool(deputyCfg).Execute(ctx, json.RawMessage(`{"channel":"拉人演练"}`)); err == nil {
		t.Fatal("missing contacts must be refused")
	}
	if _, err := NewChannelInviteTool(deputyCfg).Execute(ctx, json.RawMessage(`{"contacts":["sc_x"]}`)); err == nil {
		t.Fatal("missing channel must be refused")
	}
	if _, err := NewChannelInviteTool(deputyCfg).Execute(ctx, json.RawMessage(`{"channel":"不存在","contacts":["sc_x"]}`)); err == nil {
		t.Fatal("unknown channel must be refused")
	}
}