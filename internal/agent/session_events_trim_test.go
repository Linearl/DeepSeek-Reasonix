package agent

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl 1172 L3 验收：确定性裁剪只动超大 tool 结果、头尾逐字保留、中间置显式
// 标记（红线：可识别、可解释），其余消息不动；重写走 SaveRewriteCompact
// （失败不损原日志），lease 取不到即失败。
func TestTrimSessionOversizeToolResults(t *testing.T) {
	path := dagTestSession(t)
	fx := bigTrimFixture(t, path)
	plain := store.SessionEventLog(path)
	before, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	if before.Size() < int64(sessionTrimMinLogBytes) {
		t.Fatalf("fixture too small for the trim floor: %d", before.Size())
	}

	b, a, trimmed, err := TrimSessionOversizeToolResults(path)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if trimmed != 1 {
		t.Fatalf("trimmed fields = %d, want 1", trimmed)
	}
	if a >= b {
		t.Fatalf("trim must shrink the log: before=%d after=%d", b, a)
	}

	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("load after trim: %v", err)
	}
	byID := make(map[string]string, len(s.Messages))
	for _, m := range s.Messages {
		byID[m.ID] = m.Content
	}
	// ① 被裁字段：头尾逐字保留 + 显式标记带出被删字节数。
	tool := byID["tool-big"]
	head := fx.bigTool[:sessionTrimKeepHead]
	tail := fx.bigTool[len(fx.bigTool)-sessionTrimKeepTail:]
	if !strings.HasPrefix(tool, head) {
		t.Fatal("trimmed tool result lost its head bytes")
	}
	if !strings.HasSuffix(tool, tail) {
		t.Fatal("trimmed tool result lost its tail bytes")
	}
	if !sessionTrimMarkerPresent(tool) {
		t.Fatal("trimmed tool result missing the explicit marker")
	}
	if len(tool) >= sessionTrimFieldThreshold {
		t.Fatalf("trimmed field still %d bytes, want < %d", len(tool), int(sessionTrimFieldThreshold))
	}
	// ② 门槛之下的小 tool 结果不动。
	if got := byID["tool-small"]; got != fx.smallTool {
		t.Fatal("tool result under the threshold must stay verbatim")
	}
	// ③ 用户/助手消息永远不动。
	if got := byID["user-1"]; got != fx.userMsg {
		t.Fatal("user message must never be trimmed")
	}
	if got := byID["asst-1"]; got != fx.asstMsg {
		t.Fatal("assistant message must never be trimmed")
	}
}

// TrimSessionOversizeToolResults 契约：lease 忙 = 显式失败，不碰日志。
func TestTrimSessionOversizeToolResultsRequiresIdle(t *testing.T) {
	path := dagTestSession(t)
	bigTrimFixture(t, path)
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	defer lease.Release()
	if _, _, _, err := TrimSessionOversizeToolResults(path); err == nil {
		t.Fatal("trim must refuse a lease-held session")
	}
}

type trimFixture struct {
	bigTool   string
	smallTool string
	userMsg   string
	asstMsg   string
}

// bigTrimFixture builds an >8MiB log whose bloat is one oversized tool result
// plus filler; content 首尾带独特锚文本，头尾逐字断言才有意义。
func bigTrimFixture(t *testing.T, sessionPath string) trimFixture {
	t.Helper()
	fx := trimFixture{
		// 200KiB 工具结果（>64KiB 门槛），首尾含独特锚文本。
		bigTool:   "BIGTOOL-HEAD-ANCHOR|" + strings.Repeat("payload-row-", 17000) + "|BIGTOOL-TAIL-ANCHOR",
		smallTool: "small tool output",
		userMsg:   "user asks something",
		asstMsg:   "assistant replies",
	}
	if len(fx.bigTool) <= sessionTrimFieldThreshold {
		t.Fatalf("fixture big tool too small: %d", len(fx.bigTool))
	}
	filler := strings.Repeat("filler-message-body-", 4000) // ~80KiB × 110 条 ≈ 8.8MiB
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1}}
	parent := ""
	appendMsg := func(role provider.Role, id, content string) {
		m := dagMsg(role, content, id)
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, time.Date(2026, 1, 8, 10, 0, 0, 0, time.UTC)))
		parent = m.ID
	}
	appendMsg(provider.RoleSystem, "sys-1", "sys")
	appendMsg(provider.RoleUser, "user-1", fx.userMsg)
	appendMsg(provider.RoleAssistant, "asst-1", fx.asstMsg)
	appendMsg(provider.RoleTool, "tool-big", fx.bigTool)
	appendMsg(provider.RoleTool, "tool-small", fx.smallTool)
	for i := 0; i < 110; i++ {
		appendMsg(provider.RoleUser, fmt.Sprintf("fill%03d", i), filler)
	}
	dagAppend(t, sessionPath, entries...)
	return fx
}
