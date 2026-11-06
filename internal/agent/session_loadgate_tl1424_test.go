package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1424：LoadSession 分类闸门的「放行面」钉子。1071 闸门直接用
// IsSessionTranscriptName 后，guardian 伴随转录（<stem>.guardian.jsonl，会话
// 形态、经同一会话层存取）也被拒——guardian.Session.Load 自 2026-10-22 起在
// 每次重启静默失败：跨重启丢对话、增量游标不恢复，还照常写盘白写。闸门改走
// IsLoadableSessionTranscriptName（主转录 + guardian 伴随）后，本钉保证放行
// 面真实可达；拒绝面由 Test1071RepairLogLoadSessionRefuses 与
// TestLoadSessionGateSidecarsRefused 继续把守。

// TestLoadSessionGateAcceptsGuardianTranscript 断言 guardian 伴随转录能过闸门
// 并完整读回（消息数含 system 行）。
func TestLoadSessionGateAcceptsGuardianTranscript(t *testing.T) {
	dir := t.TempDir()
	guardianPath := filepath.Join(dir, "session.guardian.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "guardian review history"})
	if err := s.Save(guardianPath); err != nil {
		t.Fatalf("Save guardian transcript: %v", err)
	}
	loaded, err := LoadSession(guardianPath)
	if err != nil {
		t.Fatalf("LoadSession on a guardian transcript must pass the gate: %v", err)
	}
	if n := loaded.Len(); n != 2 {
		t.Fatalf("loaded guardian transcript messages = %d, want 2 (system + user)", n)
	}
}

// TestLoadSessionGateSidecarsRefused 断言审计/派生侧车仍被 1071 闸门拒绝，且
// 拒绝发生在任何写路径之前——文件字节不被触碰。
func TestLoadSessionGateSidecarsRefused(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"session.repairlog.jsonl", // 审计侧车（1071 本尊）
		"session.events.jsonl",    // 事件日志
		"session.turns.jsonl",     // 回合账本
		"session.conflicts.jsonl", // 冲突日志
	}
	for _, name := range cases {
		path := filepath.Join(dir, name)
		before := "audit payload\n"
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadSession(path)
		if err == nil {
			t.Fatalf("LoadSession on %s must refuse", name)
		}
		if !strings.Contains(err.Error(), "is not a session transcript") {
			t.Fatalf("LoadSession on %s err = %v, want the 1071 gate error", name, err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != before {
			t.Fatalf("%s bytes changed after a refused LoadSession", name)
		}
	}
}
