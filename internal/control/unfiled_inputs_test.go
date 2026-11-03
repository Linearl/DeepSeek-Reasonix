package control

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/unfiled"
)

func newUnfiledTestController(t *testing.T) *Controller {
	t.Helper()
	sess := agent.NewSession("sys")
	ag := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	return New(Options{
		Runner:        ag,
		Executor:      ag,
		SessionDir:    t.TempDir(),
		SessionPath:   t.TempDir() + "/unfiled.jsonl",
		WorkspaceRoot: t.TempDir(),
		Sink:          event.Discard,
	})
}

// TestUnfiledInputsReconcilesLiveTranscript pins the controller-side glue:
// checkpoint prompts matching a live user message count as filed, missing ones
// come back with the turn-start anchor, and the anchor names the message the
// recovered card must be inserted before. Read-only by construction — the path
// touches Checkpoints() and a session snapshot only.
func TestUnfiledInputsReconcilesLiveTranscript(t *testing.T) {
	c := newUnfiledTestController(t)
	sess := c.executor.Session()

	// Turn 0: a properly filed input — checkpoint prompt and user message agree.
	c.beginCheckpoint(context.Background(), "已经入档的输入")
	sess.Add(provider.Message{Role: provider.RoleUser, ID: "msg-filed", Content: "<context-state>x</context-state>\n已经入档的输入"})

	// Turn 1: the gap — checkpoint opens, the user message never lands.
	c.beginCheckpoint(context.Background(), "丢失的输入")
	sess.Add(provider.Message{Role: provider.RoleAssistant, ID: "msg-assistant", Content: "回答"})

	got := c.UnfiledInputs()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 unfiled input, got %+v", got)
	}
	entry := got[0]
	if entry.Prompt != "丢失的输入" {
		t.Fatalf("wrong prompt: %q", entry.Prompt)
	}
	// AnchorIndex is the turn-start boundary (len before the missing user
	// message): turn 0 contributed user+assistant, so turn 1 opens at index 2
	// — exactly where the assistant reply that "answered" the lost input sits.
	if entry.AnchorIndex != 2 {
		t.Fatalf("anchor index = %d, want 2", entry.AnchorIndex)
	}
	if entry.AnchorID != "msg-assistant" {
		t.Fatalf("anchor id = %q, want the message sitting at the anchor", entry.AnchorID)
	}
}

func TestUnfiledInputsSkipsSystemAndHostPrompts(t *testing.T) {
	c := newUnfiledTestController(t)
	sess := c.executor.Session()
	c.beginCheckpoint(context.Background(), unfiled.SystemRecoveryPrompt)
	sess.Add(provider.Message{Role: provider.RoleUser, ID: "m1", Content: "真实输入"})
	c.beginCheckpoint(context.Background(), "[跨会话消息] 来自 contact_id=sc_a → 发至 contact_id=sc_b")
	sess.Add(provider.Message{Role: provider.RoleAssistant, ID: "m2", Content: "回复"})

	if got := c.UnfiledInputs(); len(got) != 0 {
		t.Fatalf("system/host prompts must not surface as unfiled, got %+v", got)
	}
}

func TestUnfiledInputsNilSafe(t *testing.T) {
	var c *Controller
	if got := c.UnfiledInputs(); got != nil {
		t.Fatalf("nil controller must return nil, got %+v", got)
	}
	hollow := &Controller{}
	if got := hollow.UnfiledInputs(); got != nil {
		t.Fatalf("executor-less controller must return nil, got %+v", got)
	}
}

// tl1372 A1 接线：repairlog 里最近一次完成的投影折叠晚于 checkpoint 的 turn
// 归「已折叠存档」桶，不再计入「未进入」横幅（免报）。三态：fold 前免报 /
// fold 后正常报 / 无 repairlog 不影响。
func TestUnfiledInputsFoldExemption(t *testing.T) {
	// 无 repairlog：口径与原状一致，缺口照报。
	c := newUnfiledTestController(t)
	c.beginCheckpoint(context.Background(), "无折叠史的缺口")
	if got := c.UnfiledInputs(); len(got) != 1 {
		t.Fatalf("no repairlog must keep the gap reported, got %+v", got)
	}

	// repairlog 折叠晚于 checkpoint：免报。
	writeFoldRepairRecord(t, c.SessionPath(), 2*time.Hour)
	if got := c.UnfiledInputs(); len(got) != 0 {
		t.Fatalf("a completed fold newer than the checkpoint must exempt the turn, got %+v", got)
	}

	// repairlog 折叠早于 checkpoint：不豁免，缺口照报（换新 controller 避免同
	// 一 checkpoint 元数据跨态混淆）。
	c2 := newUnfiledTestController(t)
	c2.beginCheckpoint(context.Background(), "折叠前的缺口")
	writeFoldRepairRecord(t, c2.SessionPath(), -2*time.Hour)
	if got := c2.UnfiledInputs(); len(got) != 1 {
		t.Fatalf("a fold older than the checkpoint must not exempt the turn, got %+v", got)
	}
}

// writeFoldRepairRecord 在会话旁落一条 repairlog 折叠（done）记录，at 相对
// 当前时间偏移 offset——checkpoint 的 turn 时间即 beginCheckpoint 的 now，
// 正偏移=折叠晚于 turn（免报态），负偏移=折叠早于 turn（照报态）。
func writeFoldRepairRecord(t *testing.T, sessionPath string, offset time.Duration) {
	t.Helper()
	if sessionPath == "" {
		t.Fatal("test controller must carry a session path")
	}
	record := map[string]any{
		"at":   time.Now().Add(offset).UTC().Format(time.RFC3339),
		"path": sessionPath,
		"actions": []map[string]string{
			{"action": "fold", "status": "done"},
		},
		"before": "projection_overflow",
		"after":  "healthy",
	}
	blob, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	logPath := strings.TrimSuffix(sessionPath, ".jsonl") + ".repairlog.jsonl"
	if err := os.WriteFile(logPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(logPath) })
}
