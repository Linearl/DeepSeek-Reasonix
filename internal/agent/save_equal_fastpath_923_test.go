package agent

// 任务 923 切片 C 语义测试：messagesEqualForStorage 的 DeepEqual 快路径必须与
// 原双 marshal 语义完全等价。快路径只允许让「结构相同」的消息免于序列化，
// 不允许改变任何比较结果——本文件用对照实现（原版纯 marshal 比较）逐例证伪。

import (
	"encoding/json"
	"reflect"
	"testing"

	"reasonix/internal/provider"
)

func t923MarshalCompare(a, b provider.Message) bool {
	a = messageForSessionIdentity(a)
	b = messageForSessionIdentity(b)
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}

func t923Messages() []provider.Message {
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi", ReasoningContent: "think"},
		{Role: provider.RoleAssistant, Content: "call", ToolCalls: []provider.ToolCall{{ID: "c1", Name: "bash", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: "c1", Name: "bash", Content: "out"},
		// ID/CreatedAt are folded out of identity: these must compare equal to
		// the bare "hello" user message above.
		{Role: provider.RoleUser, Content: "hello", ID: "msg-x", CreatedAt: 12345},
		// nil vs empty slice: DeepEqual keeps them distinct and so do the json
		// encoders ("null" vs "[]"), so the fast path must not fire and the
		// marshal comparison decides.
		{Role: provider.RoleUser, Content: "imgs", Images: nil},
		{Role: provider.RoleUser, Content: "imgs", Images: []string{}},
		{Role: provider.RoleUser, Content: "imgs2", Images: []string{"a"}},
		{Role: provider.RoleUser, LocalOnly: true, Content: "local"},
	}
}

func TestT923StorageEqualFastPathMatchesMarshalSemantics(t *testing.T) {
	msgs := t923Messages()
	for i := range msgs {
		for j := range msgs {
			want := t923MarshalCompare(msgs[i], msgs[j])
			got := messagesEqualForStorage(msgs[i], msgs[j])
			if got != want {
				t.Fatalf("messagesEqualForStorage(%d,%d)=%v, marshal reference=%v", i, j, got, want)
			}
		}
	}
}

// The identity fold (ID/CreatedAt zeroing) must happen before the fast-path
// comparison, or the adopt path would misclassify re-minted messages.
func TestT923StorageEqualFastPathFoldsIdentityFirst(t *testing.T) {
	a := provider.Message{Role: provider.RoleUser, Content: "same", ID: "a", CreatedAt: 1}
	b := provider.Message{Role: provider.RoleUser, Content: "same", ID: "b", CreatedAt: 2}
	if !messagesEqualForStorage(a, b) {
		t.Fatal("ID/CreatedAt differences must be invisible to storage equality")
	}
	// Content difference must still be visible.
	b.Content = "other"
	if messagesEqualForStorage(a, b) {
		t.Fatal("content difference must remain visible")
	}
}

// The diff classifier's decisions must be unchanged by the fast path: a prefix
// of identical messages with an appended tail still classifies as a pure
// append, and a mid-prefix content change still classifies as a rewrite.
func TestT923DiffClassificationUnchangedByFastPath(t *testing.T) {
	persisted := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "u1", ID: "m1", CreatedAt: 100},
		{Role: provider.RoleAssistant, Content: "a1", ID: "m2", CreatedAt: 101},
	}
	// Same transcript shape, freshly minted IDs (mintMessageIDs runs before the
	// diff in production, so every live message carries a unique ID).
	next := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "u1", ID: "n1"},
		{Role: provider.RoleAssistant, Content: "a1", ID: "n2"},
		{Role: provider.RoleUser, Content: "u2", ID: "n3"},
	}
	diff := diffDAGTranscript(append([]provider.Message(nil), persisted...), append([]provider.Message(nil), next...), sessionSaveSnapshot)
	if diff.k != 3 || diff.rewriteAt != -1 || len(diff.patches) != 0 || diff.systemChange {
		t.Fatalf("append shape misclassified: %+v", diff)
	}
	if len(diff.adopted) != 2 {
		t.Fatalf("ID renames must be adopted, got %v", diff.adopted)
	}
	// Mid-prefix content change → rewrite at that index.
	edited := append([]provider.Message(nil), persisted...)
	edited[1].Content = "u1-edited"
	diff2 := diffDAGTranscript(append([]provider.Message(nil), persisted...), edited, sessionSaveSnapshot)
	if diff2.rewriteAt != 1 {
		t.Fatalf("edited prefix must classify as rewrite at 1, got %+v", diff2)
	}
}

// DeepEqual on provider.Message compares slice headers structurally; guard
// that the fast path never claims equality for messages whose tool calls
// differ only inside arguments.
func TestT923StorageEqualFastPathToolCallArguments(t *testing.T) {
	a := provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c", Name: "n", Arguments: "{\"x\":1}"}}}
	b := provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c", Name: "n", Arguments: "{\"x\":2}"}}}
	if messagesEqualForStorage(a, b) {
		t.Fatal("differing tool-call arguments must not compare equal")
	}
	if reflect.DeepEqual(a, b) {
		t.Fatal("test premise broken: DeepEqual should already distinguish these")
	}
}
