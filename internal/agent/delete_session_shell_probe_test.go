package agent

// 任务 1145 机制测试：delete_session 的注册表空壳触达（desktop 宿主注入的
// 双探针 RegistryShellProbe / RegistryShellTrash）。
//
// 覆盖任务书验收点：
//   AC2 树里可见（注册表有）而目录查不到的条目可被 delete_session 触达；
//   AC6 空壳识别只认 topic_id（desktop 侧判定，agent 侧透传不加工）；
//   铁律 2 探针双 nil（默认关）时行为与既有版本逐字节一致——原 not found
//       错误原样返回，宿主删除回调永不执行。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Iron rule 2, agent side: with both probes nil (the default, and every CLI
// host), delete_session on an unresolvable target stays byte-identical to the
// pre-1145 versions — the same not-found error, no shell branch in sight.
func TestDeleteSessionShellProbesNilKeepsBaselineError(t *testing.T) {
	dir := t.TempDir()
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		// 双 nil：铁律 2 默认关。探针缺席时 delete_session 必须与既有版本
		// 逐字节一致——同一 not found 错误，无 shell 分支痕迹。
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		t.Fatal("host delete callback must never run for an unresolvable target")
		return DeleteSessionImpact{}, DeleteSessionResult{}, nil
	})
	_, err := tool.Execute(context.Background(), []byte(`{"target":"topic-shell"}`))
	if err == nil {
		t.Fatal("an unresolvable target must keep failing with the baseline error")
	}
	if !strings.Contains(err.Error(), "is not in the contact directory (use list_addressable_sessions)") {
		t.Fatalf("baseline not-found error changed: %v", err)
	}
	if !strings.Contains(err.Error(), "topic-shell") {
		t.Fatalf("baseline error must echo the target: %v", err)
	}
}

// The probes present but the host declining the shell verdict (its switch is
// off, or the ref is not a shell) converges on the same baseline error — the
// agent side adds no shell face of its own.
func TestDeleteSessionShellProbeDeclineKeepsBaselineError(t *testing.T) {
	dir := t.TempDir()
	trashCalled := false
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		RegistryShellProbe: func(ref string) (topicID, title string, ok bool) {
			return "", "", false // the host (switch off / not a shell) declines
		},
		RegistryShellTrash: func(topicID string, dryRun bool) error {
			trashCalled = true
			return nil
		},
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		t.Fatal("host delete callback must never run for an unresolvable target")
		return DeleteSessionImpact{}, DeleteSessionResult{}, nil
	})
	_, err := tool.Execute(context.Background(), []byte(`{"target":"topic-shell"}`))
	if err == nil || !strings.Contains(err.Error(), "is not in the contact directory") {
		t.Fatalf("a declined shell verdict must keep the baseline error, got %v", err)
	}
	if trashCalled {
		t.Fatal("trash must never run when the probe declines the shell verdict")
	}
}

// A probe hit turns an unresolvable target into the shell face: dry run
// reports shell impact and calls nothing; confirm converges through the
// host trash with the exact topic id.
func TestDeleteSessionShellDryRunThenConfirm(t *testing.T) {
	dir := t.TempDir()
	const shellID = "topic-shell"
	const shellTitle = "Ghost Session"
	trashCalls := 0
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		RegistryShellProbe: func(ref string) (topicID, title string, ok bool) {
			if ref == shellID {
				return shellID, shellTitle, true
			}
			return "", "", false
		},
		RegistryShellTrash: func(topicID string, dryRun bool) error {
			trashCalls++
			if topicID != shellID {
				t.Fatalf("trash got topic %q, want %q", topicID, shellID)
			}
			if dryRun {
				t.Fatal("dry run must not reach the trash")
			}
			return nil
		},
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		t.Fatal("the regular file-trash callback must never run on the shell face")
		return DeleteSessionImpact{}, DeleteSessionResult{}, nil
	})

	// Dry run: shell impact, no trash call, next step named.
	out, err := tool.Execute(context.Background(), []byte(`{"target":"`+shellID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if trashCalls != 0 {
		t.Fatalf("dry run must not trash (got %d calls)", trashCalls)
	}
	var dry struct {
		Status string `json:"status"`
		Impact struct {
			Title       string `json:"title"`
			SessionPath string `json:"sessionPath"`
			Shell       bool   `json:"shell"`
		} `json:"impact"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &dry); err != nil {
		t.Fatalf("dry run must return JSON: %v\n%s", err, out)
	}
	if dry.Status != "dry_run" {
		t.Fatalf("dry run status: %s", dry.Status)
	}
	if !dry.Impact.Shell {
		t.Fatalf("dry run impact must mark the shell: %+v", dry.Impact)
	}
	if dry.Impact.Title != shellTitle {
		t.Fatalf("dry run impact title: %q, want %q", dry.Impact.Title, shellTitle)
	}
	if dry.Impact.SessionPath != "" {
		t.Fatalf("a shell has no file: sessionPath %q", dry.Impact.SessionPath)
	}
	if !strings.Contains(dry.Note, "confirm=true") {
		t.Fatalf("dry run note must name the next step: %s", dry.Note)
	}

	// Confirm: the trash converges the registry, the result reports a shell
	// delete (empty session path — nothing was on disk).
	out, err = tool.Execute(context.Background(), []byte(`{"target":"`+shellID+`","confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if trashCalls != 1 {
		t.Fatalf("confirm must trash exactly once (got %d)", trashCalls)
	}
	var done struct {
		Status string `json:"status"`
		Result struct {
			ContactID   string `json:"contactId"`
			SessionPath string `json:"sessionPath"`
			Trashed     bool   `json:"trashed"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &done); err != nil {
		t.Fatalf("confirm must return JSON: %v\n%s", err, out)
	}
	if done.Status != "trashed" {
		t.Fatalf("confirm status: %s", done.Status)
	}
	if done.Result.ContactID != shellID || !done.Result.Trashed || done.Result.SessionPath != "" {
		t.Fatalf("confirm result: %+v", done.Result)
	}
}

// A failing trash surfaces as an error naming the shell — the caller must be
// able to tell a converged shell from a refused one.
func TestDeleteSessionShellTrashFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		RegistryShellProbe: func(ref string) (topicID, title string, ok bool) {
			return ref, "Ghost", true
		},
		RegistryShellTrash: func(topicID string, dryRun bool) error {
			return errShellTrashInjected
		},
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		t.Fatal("the regular file-trash callback must never run on the shell face")
		return DeleteSessionImpact{}, DeleteSessionResult{}, nil
	})
	_, err := tool.Execute(context.Background(), []byte(`{"target":"topic-shell","confirm":true}`))
	if err == nil || !strings.Contains(err.Error(), "removing registry shell") || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("trash failure must surface naming the shell, got %v", err)
	}
}

type shellTrashError struct{}

func (shellTrashError) Error() string { return "injected trash failure" }

var errShellTrashInjected = shellTrashError{}

// ── 任务 tl-1144 ─────────────────────────────────────────────────────

// AC3：title 匹配命中多条时必须拒绝并报明确错误——不得猜一条删，也绝不能
// 借空壳回落通道绕过这个拒绝。宿主删除回调与空壳清理都不得运行。
func TestDeleteSessionTitleMultiMatchRefused(t *testing.T) {
	dir := t.TempDir()
	seedSessionWithTopic(t, dir, "a.jsonl", "topic-a", "Same Title")
	seedSessionWithTopic(t, dir, "b.jsonl", "topic-b", "Same Title")
	trashCalls := 0
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		RegistryShellProbe: func(ref string) (topicID, title string, ok bool) {
			// 标题不是 topic_id：空壳探针必须拒绝（只认精确 topic id），
			// 多命中拒绝因此不能被回落通道吞掉。
			return "", "", false
		},
		RegistryShellTrash: func(topicID string, dryRun bool) error {
			trashCalls++
			return nil
		},
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		t.Fatal("host delete callback must never run for an ambiguous title")
		return DeleteSessionImpact{}, DeleteSessionResult{}, nil
	})
	_, err := tool.Execute(context.Background(), []byte(`{"target":"Same Title","confirm":true}`))
	if err == nil {
		t.Fatal("an ambiguous title must be refused")
	}
	if !strings.Contains(err.Error(), `matches 2 sessions`) || !strings.Contains(err.Error(), "use topic_id instead") {
		t.Fatalf("ambiguous title must name the count and the topic_id escape hatch, got %v", err)
	}
	if trashCalls != 0 {
		t.Fatalf("nothing may be trashed for an ambiguous title (got %d shell-trash calls)", trashCalls)
	}
}

// AC5（工具面）：空壳确认删除后，同一 target 再调一次必须返回干净的
// 「已不存在」（基线 not found），不是报错崩溃，也绝不再触发清理。
func TestDeleteSessionSecondCallAfterShellGoneIsCleanNotFound(t *testing.T) {
	dir := t.TempDir()
	const shellID = "topic-shell"
	gone := false
	trashCalls := 0
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		RegistryShellProbe: func(ref string) (topicID, title string, ok bool) {
			if !gone && ref == shellID {
				return shellID, "Ghost", true
			}
			return "", "", false // 删除后：tombstone 生效，探针再也指不到它
		},
		RegistryShellTrash: func(topicID string, dryRun bool) error {
			trashCalls++
			gone = true // 桌面侧 deleteTopic 收敛：注册表条目 + tombstone
			return nil
		},
	}, func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		t.Fatal("the regular file-trash callback must never run on the shell face")
		return DeleteSessionImpact{}, DeleteSessionResult{}, nil
	})
	if _, err := tool.Execute(context.Background(), []byte(`{"target":"`+shellID+`","confirm":true}`)); err != nil {
		t.Fatalf("first confirm must succeed: %v", err)
	}
	_, err := tool.Execute(context.Background(), []byte(`{"target":"`+shellID+`","confirm":true}`))
	if err == nil {
		t.Fatal("the second call on a deleted shell must say it is gone, not succeed")
	}
	if !strings.Contains(err.Error(), "is not in the contact directory") {
		t.Fatalf("the second call must converge on the baseline not-found (已不存在), got %v", err)
	}
	if trashCalls != 1 {
		t.Fatalf("the second call must not re-run the cleanup (got %d calls)", trashCalls)
	}
}
