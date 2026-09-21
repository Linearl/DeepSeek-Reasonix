package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// gateFixture builds a sender and a titled target with one shared mailbox.
func gateFixture(t *testing.T) (SessionCollabConfig, string) {
	t.Helper()
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	if err := UpdateBranchMeta(to, true, func(m *BranchMeta) error {
		m.CustomTitle = "gate target"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fromID, err := EnsureContactID(from)
	if err != nil {
		t.Fatal(err)
	}
	return SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	}, fromID
}

// Task 173 ③: require_reply is refused with the panel switch and the real
// settings entry named — never a misleading "invalid argument".
func TestRequireReplyGateRefusalNamesThePanelSwitch(t *testing.T) {
	cfg, _ := gateFixture(t)
	tool := NewTalkToSessionTool(cfg)
	_, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"do the thing","require_reply":true}`))
	if err == nil {
		t.Fatal("require_reply must be refused while the panel switch is off")
	}
	msg := err.Error()
	if !strings.Contains(msg, "实验面板") || !strings.Contains(msg, "允许配置回信要求") {
		t.Fatalf("refusal must name the panel switch: %v", err)
	}
	if !strings.Contains(msg, "设置 → 实验特性 → 跨会话通信") {
		t.Fatalf("refusal must point at the real settings entry: %v", err)
	}
	if strings.Contains(strings.ToLower(msg), "invalid argument") {
		t.Fatalf("refusal must not read as a bad-argument error: %v", err)
	}
}

// Task 173: with the gate open the flag rides the record, and without it the
// record stays exactly as before (zero regression on the default path).
func TestRequireReplyRidesTheRecordOnlyWhenAllowed(t *testing.T) {
	cfg, _ := gateFixture(t)
	tool := NewTalkToSessionTool(cfg)
	if _, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"no flag"}`)); err != nil {
		t.Fatal(err)
	}
	box, err := sessioncollab.NewMailStore(cfg.MailDir).Inbox(mustContact(t, cfg))
	if err != nil || len(box) != 1 || box[0].RequireReply {
		t.Fatalf("default path must stay flag-free: %+v %v", box, err)
	}

	cfg.AllowRequireReply = true
	tool = NewTalkToSessionTool(cfg)
	if _, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"need an answer","require_reply":true}`)); err != nil {
		t.Fatal(err)
	}
	box, err = sessioncollab.NewMailStore(cfg.MailDir).Inbox(mustContact(t, cfg))
	if err != nil || len(box) != 2 || !box[1].RequireReply {
		t.Fatalf("the demanded reply must ride the record: %+v %v", box, err)
	}
}

func mustContact(t *testing.T, cfg SessionCollabConfig) string {
	t.Helper()
	ids := scanAddressable(cfg.SessionDir, cfg.WorkspaceRoot)
	for _, id := range ids {
		if id.Title == "gate target" {
			return id.ContactID
		}
	}
	t.Fatal("fixture target vanished")
	return ""
}

// Task 173 ④: with the steer switch off, delivery=steer degrades to followup —
// the message still lands, so existing callers are never broken by a panel
// they have never seen.
func TestSteerGateDegradesToFollowup(t *testing.T) {
	cfg, _ := gateFixture(t)
	tool := NewTalkToSessionTool(cfg)
	out, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"urgent","delivery":"steer"}`))
	if err != nil {
		t.Fatalf("steer must degrade, not refuse: %v", err)
	}
	var payload struct {
		Delivery string `json:"delivery"`
	}
	_ = json.Unmarshal([]byte(out), &payload)
	if payload.Delivery != "followup" {
		t.Fatalf("a gated steer must land as followup: %s", out)
	}
}

// Task 173 ⑥: the daily cap refuses the message that would exceed it and the
// refusal says so — the first sends within the cap still land.
func TestDailySendLimitStopsTheStorm(t *testing.T) {
	cfg, _ := gateFixture(t)
	cfg.DailySendLimit = 1
	tool := NewTalkToSessionTool(cfg)
	if _, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"one"}`)); err != nil {
		t.Fatalf("the first message must land: %v", err)
	}
	_, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"two"}`))
	if err == nil {
		t.Fatal("the second message must hit the daily cap")
	}
	if !strings.Contains(err.Error(), "单日发信上限") {
		t.Fatalf("refusal must name the daily cap: %v", err)
	}
}
