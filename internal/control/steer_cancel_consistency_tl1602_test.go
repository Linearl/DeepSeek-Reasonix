package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1602 回归钉：回合中途注入的 steer（含 host 引导）已被模型消费并在当轮生效，
// 取消清洗（stripCancelledVisibleTurnMessagesAfterWithFallbackAt）必须原位保留
// 这些 user 行。「三处一致」= 内存会话有 + jsonl 落盘有 + 历史提示词（下一轮
// provider 输入）含；UI 重放可见性的前提（ReplaySteerText 可识别）一并钉住。
// 修复前尾段循环只接 LocalOnly/Assistant/Tool，user 行静默坠落即被删除——
// 2026-11-15 用户致命问题（580 家族新形态）的断环。

type tl1602PermissiveProvider struct{}

func (tl1602PermissiveProvider) Name() string { return "tl1602-permissive" }
func (tl1602PermissiveProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	panic("unexpected Stream call")
}
func (tl1602PermissiveProvider) RequiresAssistantReasoningReplay(provider.Message) bool { return false }

func tl1602SteerContent(text string) string {
	return agent.MidTurnSteerPrefix + "\n" + text
}

func TestTl1602CancelledTurnKeepsSteerInMemoryTranscriptAndPrompt(t *testing.T) {
	sess := agent.NewSession("system")
	user := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "三合一深查：历史盘点+机制+竞品"}
	sess.Add(user)
	// 完整工具轮（steer 只会落在这样的轮间隙之后）
	sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
		ID: "c1", Name: "read_file", Arguments: `{"path":"report.md"}`,
	}}})
	sess.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "c1", Name: "read_file", Content: "report body"})
	// 回合中途注入并消费的 steer（run_loop.go consumeSteer 的落库形态）
	steer := provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content:    tl1602SteerContent("转向 B 面：先查 history_steer.go"),
		RawContent: "转向 B 面：先查 history_steer.go",
	}
	sess.Add(steer)
	// 取消时的未配对残段（sub-agent 被取消的现场）
	sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
		ID: "c2", Name: "task", Arguments: `{"prompt":"review"}`,
	}}})

	exec := agent.New(tl1602PermissiveProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec})

	c.stripCancelledVisibleTurnMessagesAfterWithFallback(1, user)

	// ① 内存会话：steer 行原位保留
	msgs := sess.Snapshot()
	steerIdx := -1
	for i, m := range msgs {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "转向 B 面") {
			steerIdx = i
			if m.Content != steer.Content {
				t.Fatalf("steer content mutated: %q", m.Content)
			}
			if m.LocalOnly {
				t.Fatalf("steer demoted to local-only: %+v", m)
			}
		}
	}
	if steerIdx < 0 {
		t.Fatalf("consumed steer dropped by cancel cleanup: %+v", msgs)
	}
	// 原位约束：完整轮之后、被降级的残段之前
	if steerIdx < 3 {
		t.Fatalf("steer position shifted (want after complete round): idx=%d msgs=%+v", steerIdx, msgs)
	}
	tail := msgs[len(msgs)-1]
	if !tail.LocalOnly || tail.InterruptedTurn == nil || !tail.InterruptedTurn.Pending {
		t.Fatalf("interrupted-fragment recovery contract broken: %+v", tail)
	}

	// ② 历史提示词：下一轮 provider 输入含 steer 原文
	found := false
	for _, m := range provider.ModelMessages(sess.Snapshot()) {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "转向 B 面：先查 history_steer.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history prompt lost the steer guidance")
	}

	// ③ jsonl 落盘：SaveRewrite → LoadSession 往返仍在
	path := filepath.Join(t.TempDir(), "tl1602-steer.jsonl")
	if err := sess.SaveRewrite(path); err != nil {
		t.Fatalf("save rewrite: %v", err)
	}
	reloaded, err := agent.LoadSession(path)
	if err != nil || reloaded == nil {
		t.Fatalf("load session: %v (%v)", reloaded, err)
	}
	onDisk := false
	for _, m := range reloaded.Messages {
		if m.Role == provider.RoleUser && m.Content == steer.Content {
			onDisk = true
		}
	}
	if !onDisk {
		t.Fatalf("persisted transcript lost the steer row")
	}

	// ④ UI 重放前提：历史重放可识别该行为 steer（desktop historySteerRows 的判据）
	if _, handled := agent.ReplaySteerText(steer.Content); !handled {
		t.Fatalf("replay no longer recognizes the steer shape")
	}
	if text, ok := agent.VisibleSteerText(steer.Content); !ok || !strings.Contains(text, "转向 B 面") {
		t.Fatalf("visible steer text broken: %q %v", text, ok)
	}
}

// tl1602 同机制面：host 生成的引导行（Origin=host 的用户角色行）此前同样从
// 尾段循环坠落。协议消息（IsHostProtocolMessage）保持排除，与
// desktop hostGuidanceRows 的转录契约一致。
func TestTl1602CancelledTurnKeepsHostGuidanceRow(t *testing.T) {
	sess := agent.NewSession("system")
	user := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "继续任务"}
	sess.Add(user)
	guidance := provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginHost,
		Content: "Your previous reply was interrupted because it repeated the same text. Continue with a different approach.",
	}
	sess.Add(guidance)
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "interrupted partial text"})

	exec := agent.New(tl1602PermissiveProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec})
	c.stripCancelledVisibleTurnMessagesAfterWithFallback(1, user)

	kept := false
	for _, m := range sess.Snapshot() {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "Continue with a different approach") {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("host guidance row dropped by cancel cleanup: %+v", sess.Snapshot())
	}
}
