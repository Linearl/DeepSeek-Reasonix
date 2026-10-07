package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// 任务 509 未消费消息前置门（软开关，默认关）：delete_session 的 confirm 路径
// 在目标收件箱还有 seen 游标未覆盖的条目（queued≠已读）时必须拒绝；dry-run
// 如实上报条数（mailGate）；门关闭时行为与既有版本逐字节一致——有未读也放行，
// 且结果里不出现 mailGate 键。归档 ≠ 删除，但排队未读会随归档退出活跃协作面，
// 自动归档编排必须先消费。

// newGateEnv builds one registrable target session plus a shared mail dir.
func newGateEnv(t *testing.T) (dir, mailDir, target, targetID string) {
	t.Helper()
	dir = t.TempDir()
	mailDir = filepath.Join(dir, "mail")
	target = filepath.Join(dir, "target.jsonl")
	writeEmpty(t, target)
	var err error
	targetID, err = EnsureContactID(target)
	if err != nil {
		t.Fatal(err)
	}
	return dir, mailDir, target, targetID
}

func deliverGateMail(t *testing.T, mailDir, toID, body string) sessioncollab.MailMessage {
	t.Helper()
	msg, err := sessioncollab.NewMailStore(mailDir).Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_sender", To: toID, Body: body, Delivery: "followup",
	})
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func newGateTool(dir, mailDir string, gate bool, hostCalled *bool) tool.Tool {
	return NewDeleteSessionTool(SessionCollabConfig{
		Enabled:          true,
		SessionDir:       dir,
		WorkspaceRoot:    dir,
		MailDir:          mailDir,
		DeleteUnreadGate: gate,
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		if dryRun {
			return DeleteSessionImpact{Title: "target"}, DeleteSessionResult{}, nil
		}
		*hostCalled = true
		return DeleteSessionImpact{Title: "target"}, DeleteSessionResult{ContactID: contactID, SessionPath: sessionPath, Trashed: true, RestoreUntil: "manual"}, nil
	})
}

// 门关闭（默认）：收件箱有未读也放行，且结果不带 mailGate 键——默认行为逐字节不回退。
func TestDeleteSessionGateOffKeepsLegacyBehavior(t *testing.T) {
	dir, mailDir, _, targetID := newGateEnv(t)
	deliverGateMail(t, mailDir, targetID, "queued while gate off")
	hostCalled := false
	tool := newGateTool(dir, mailDir, false, &hostCalled)
	out, err := tool.Execute(nil, []byte(`{"target":"`+targetID+`","confirm":true}`))
	if err != nil {
		t.Fatalf("gate off must keep the legacy allow behavior, got %v", err)
	}
	if !hostCalled {
		t.Fatal("gate off: host delete must run")
	}
	if !strings.Contains(out, `"trashed"`) || strings.Contains(out, "mailGate") {
		t.Fatalf("gate off: result must keep the legacy shape without mailGate, got %s", out)
	}
}

// 门开 + 有未读：confirm 拒绝，宿主回调绝不触发。
func TestDeleteSessionGateRefusesUnconsumedConfirm(t *testing.T) {
	dir, mailDir, _, targetID := newGateEnv(t)
	deliverGateMail(t, mailDir, targetID, "queued unread one")
	hostCalled := false
	tool := newGateTool(dir, mailDir, true, &hostCalled)
	_, err := tool.Execute(nil, []byte(`{"target":"`+targetID+`","confirm":true}`))
	if err == nil || !strings.Contains(err.Error(), "unconsumed inbox message") || !strings.Contains(err.Error(), "task-509") {
		t.Fatalf("gate on: unconsumed confirm must be refused with the task-509 reason, got %v", err)
	}
	if hostCalled {
		t.Fatal("gate on: the host delete must not run for an unconsumed target")
	}
}

// 门开 + 有未读：dry-run 不拒（只读），如实上报条数并预告 confirm 会被拒。
func TestDeleteSessionGateDryRunReportsUnconsumed(t *testing.T) {
	dir, mailDir, _, targetID := newGateEnv(t)
	deliverGateMail(t, mailDir, targetID, "unread one")
	deliverGateMail(t, mailDir, targetID, "unread two")
	hostCalled := false
	tool := newGateTool(dir, mailDir, true, &hostCalled)
	out, err := tool.Execute(nil, []byte(`{"target":"`+targetID+`"}`))
	if err != nil {
		t.Fatalf("dry run must not fail under the gate, got %v", err)
	}
	var parsed struct {
		Status   string `json:"status"`
		MailGate struct {
			Unconsumed     int  `json:"unconsumed"`
			RefusesConfirm bool `json:"refusesConfirm"`
		} `json:"mailGate"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("dry-run output must stay valid json: %v", err)
	}
	if parsed.Status != "dry_run" || parsed.MailGate.Unconsumed != 2 || !parsed.MailGate.RefusesConfirm {
		t.Fatalf("dry run must report unconsumed=2 + refusesConfirm, got %s", out)
	}
	if hostCalled {
		t.Fatal("dry run must never call the host delete")
	}
}

// 门开 + 已消费（Ack 推进 seen 游标覆盖全部条目）：confirm 放行并留痕 unconsumed=0。
func TestDeleteSessionGatePassesWhenConsumed(t *testing.T) {
	dir, mailDir, _, targetID := newGateEnv(t)
	msg := deliverGateMail(t, mailDir, targetID, "will be consumed")
	if err := sessioncollab.NewMailStore(mailDir).Ack(context.Background(), targetID, msg.ID); err != nil {
		t.Fatal(err)
	}
	hostCalled := false
	tool := newGateTool(dir, mailDir, true, &hostCalled)
	out, err := tool.Execute(nil, []byte(`{"target":"`+targetID+`","confirm":true}`))
	if err != nil {
		t.Fatalf("gate on: consumed inbox must pass, got %v", err)
	}
	if !hostCalled {
		t.Fatal("gate on: host delete must run for a consumed target")
	}
	if !strings.Contains(out, `"unconsumed":0`) {
		t.Fatalf("gate on: trashed result must carry mailGate evidence, got %s", out)
	}
}

// 门开 + 从未收过信：直接放行（无 inbox 文件，无需核验）。
func TestDeleteSessionGatePassesWithoutInbox(t *testing.T) {
	dir, mailDir, _, targetID := newGateEnv(t)
	hostCalled := false
	tool := newGateTool(dir, mailDir, true, &hostCalled)
	if _, err := tool.Execute(nil, []byte(`{"target":"`+targetID+`","confirm":true}`)); err != nil {
		t.Fatalf("gate on: a session that never received mail must pass, got %v", err)
	}
	if !hostCalled {
		t.Fatal("gate on: host delete must run for a mail-less target")
	}
}

// 门开 + 收件箱不可读（宁紧勿松）：核验失败视同未核验，拒绝归档。
func TestDeleteSessionGateFailsClosedOnUnreadableInbox(t *testing.T) {
	dir, mailDir, _, targetID := newGateEnv(t)
	// 把 inbox 路径占成一个目录：readAll 走 os.ReadFile 必报非 NotExist 错误。
	if err := os.MkdirAll(filepath.Join(mailDir, targetID+".inbox.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	hostCalled := false
	tool := newGateTool(dir, mailDir, true, &hostCalled)
	_, err := tool.Execute(nil, []byte(`{"target":"`+targetID+`","confirm":true}`))
	if err == nil || !strings.Contains(err.Error(), "fails closed") {
		t.Fatalf("gate on: an unverifiable inbox must refuse the archive, got %v", err)
	}
	if hostCalled {
		t.Fatal("gate on: the host delete must not run when the inbox cannot be verified")
	}
}
