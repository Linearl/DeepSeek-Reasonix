package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// tl1431（tl1403 拒发反馈面补齐）：发信身份互证拒收不再静默——boot 快照
// path≠活路径（污染签名）整体拒收快照身份时，talk_to_session 回执必须带
// 拒因（快照 path/id + 生效身份）与「按对话名等辅助信息自查重定位」指引。
// 拒发语义本身零改动：信仍按活路径权威身份发出，禁 fallback 到他人 identity
// 的铁律原样不动（recovery_contact_identity_tl1403_test.go 的 6 断言钉语义，
// 本文件钉回执面）。

type tl1431Receipt struct {
	From                     string `json:"from"`
	IdentitySnapshotRejected bool   `json:"identitySnapshotRejected"`
	IdentitySnapshotPath     string `json:"identitySnapshotPath"`
	IdentitySnapshotContact  string `json:"identitySnapshotContactId"`
	IdentitySnapshotNote     string `json:"identitySnapshotRejectedNote"`
}

func tl1431SendTool(dir, mailDir, livePath, snapshotPath, snapshotID, statusPath string) talkToSessionTool {
	cfg := SessionCollabConfig{
		Enabled:          true,
		SessionDir:       dir,
		WorkspaceRoot:    dir,
		MailDir:          mailDir,
		CollabStatusPath: statusPath,
	}
	if snapshotPath != "" {
		cfg.CurrentSessionPath = snapshotPath
		cfg.CurrentContactID = snapshotID
	}
	cfg.ResolveSessionPath = func() string { return livePath }
	return talkToSessionTool{cfg: cfg}
}

func tl1431Send(t *testing.T, tool talkToSessionTool, to, message string) (string, error) {
	t.Helper()
	return tool.Execute(context.Background(), json.RawMessage(`{"to":"`+to+`","message":"`+message+`"}`))
}

// 身份错配发信：拒发（快照身份拒用）+ 发送方收到含拒因的回执；信仍按活路径
// 权威身份落库（拒发语义不变）；协作状态流留 identity_rejected 审计行。
func Test1431IdentityMismatchSendCarriesRejectionReceipt(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	livePath := filepath.Join(dir, "live.jsonl")
	otherPath := filepath.Join(dir, "other.jsonl")
	targetPath := filepath.Join(dir, "target.jsonl")
	if err := SaveBranchMeta(livePath, BranchMeta{ID: BranchID(livePath), ContactID: "sc_live_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta live: %v", err)
	}
	if err := SaveBranchMeta(otherPath, BranchMeta{ID: BranchID(otherPath), ContactID: "sc_other_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta other: %v", err)
	}
	writeEmpty(t, targetPath)
	targetID, err := EnsureContactID(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(mailDir, "collab-status.jsonl")

	send := tl1431SendTool(dir, mailDir, livePath, otherPath, "sc_other_identity", statusPath)
	out, err := tl1431Send(t, send, targetID, "hello")
	if err != nil {
		t.Fatalf("send must proceed under the live identity (拒发语义不变): %v", err)
	}
	var receipt tl1431Receipt
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("send receipt: %v (%s)", err, out)
	}
	if !receipt.IdentitySnapshotRejected {
		t.Fatalf("a polluted-snapshot send must carry identitySnapshotRejected=true, got: %s", out)
	}
	if receipt.IdentitySnapshotContact != "sc_other_identity" || receipt.IdentitySnapshotPath != otherPath {
		t.Fatalf("receipt must name the rejected snapshot evidence (id/path), got: %+v", receipt)
	}
	if receipt.From != "sc_live_identity" {
		t.Fatalf("from = %q, want the live-path authority %q (never the snapshot's)", receipt.From, "sc_live_identity")
	}
	// 回执含拒因（快照 id 与活路径证据都要出现）+ 自查重定位指引。
	for _, want := range []string{"sc_other_identity", "sc_live_identity", otherPath, livePath, "list_addressable_sessions", "set_session_purpose"} {
		if !strings.Contains(receipt.IdentitySnapshotNote, want) {
			t.Fatalf("rejection note must mention %q, got: %s", want, receipt.IdentitySnapshotNote)
		}
	}
	// 落库的身份同样是活路径权威（不是回执说说而已）。
	box, err := sessioncollab.NewMailStore(mailDir).Inbox(targetID)
	if err != nil || len(box) != 1 {
		t.Fatalf("inbox: %v %v", box, err)
	}
	if box[0].From != "sc_live_identity" {
		t.Fatalf("delivered mail from=%q, want the live identity (snapshot id must never reach the wire)", box[0].From)
	}
	// 协作状态流审计行：identity_rejected。
	stream, rerr := os.ReadFile(statusPath)
	if rerr != nil {
		t.Fatalf("collab status stream: %v", rerr)
	}
	if !strings.Contains(string(stream), `"identity_rejected"`) {
		t.Fatalf("status stream must carry an identity_rejected audit line, got: %s", string(stream))
	}
}

// 正常发信零影响：无污染时回执不带任何拒收字段；同路径快照（显式裁定语义）
// 同样不触发拒收回执——tl1403 越权拒绝测试钉住的产品行为原样保留。
func Test1431NormalSendCarriesNoIdentityReceipt(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	livePath := filepath.Join(dir, "live.jsonl")
	targetPath := filepath.Join(dir, "target.jsonl")
	if err := SaveBranchMeta(livePath, BranchMeta{ID: BranchID(livePath), ContactID: "sc_live_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta live: %v", err)
	}
	writeEmpty(t, targetPath)
	targetID, err := EnsureContactID(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(mailDir, "collab-status.jsonl")

	// ① 无快照身份（desktop 现场形态：boot 快照为空）。
	send := tl1431SendTool(dir, mailDir, livePath, "", "", statusPath)
	out, err := tl1431Send(t, send, targetID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "identitySnapshotRejected") {
		t.Fatalf("a clean send must carry no identity rejection receipt, got: %s", out)
	}
	var receipt tl1431Receipt
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("send receipt: %v (%s)", err, out)
	}
	if receipt.From != "sc_live_identity" {
		t.Fatalf("from = %q, want the live identity", receipt.From)
	}

	// ② 同路径快照 = 显式裁定（保留 tl1403 产品语义，非污染、无回执）。
	send2 := tl1431SendTool(dir, mailDir, livePath, livePath, "sc_explicit_override", statusPath)
	out2, err := tl1431Send(t, send2, targetID, "again")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "identitySnapshotRejected") {
		t.Fatalf("a same-path snapshot is an explicit override, not pollution — no rejection receipt, got: %s", out2)
	}
	var receipt2 tl1431Receipt
	if err := json.Unmarshal([]byte(out2), &receipt2); err != nil {
		t.Fatalf("send receipt: %v (%s)", err, out2)
	}
	if receipt2.From != "sc_explicit_override" {
		t.Fatalf("same-path override from = %q, want the explicit %q", receipt2.From, "sc_explicit_override")
	}
	if stream, rerr := os.ReadFile(statusPath); rerr == nil && strings.Contains(string(stream), `"identity_rejected"`) {
		t.Fatalf("clean sends must not write identity_rejected audit lines, got: %s", string(stream))
	}
}
