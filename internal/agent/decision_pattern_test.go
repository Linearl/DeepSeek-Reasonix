package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/memory"
)

// 任务 801（决策模式提取 MVP）测试。验收标准（方案文档 Phase 1）：
// 1. 开关关闭 = 零行为（不读盘不写盘，行为与引入前一致）；
// 2. 开启后压缩触发时对裁决轮次产出决策记忆条目；
// 3. 非裁决轮次零产出（信噪比验证）；
// 4. 增量游标推进后不重复产出；
// 5. transcript 缺失等失败不 panic、不阻断。

func decisionTestAgent(t *testing.T, transcript string, store memory.Store, enabled bool) *Agent {
	t.Helper()
	a := &Agent{}
	if transcript != "" {
		a.SetSessionPath(transcript)
	}
	a.decisionExtract = DecisionExtractOptions{Enabled: enabled, Memory: store}
	return a
}

func userLine(text string) string {
	return fmt.Sprintf(`{"role":"user","content":%q,"ts":%q}`, text, time.Now().UTC().Format(time.RFC3339))
}

func TestDecisionRulingCandidateWordlist(t *testing.T) {
	hits := []string{
		"不对，应该用 worktree 才对",
		"你理解错了，我说的是压缩事件触发",
		"别这样，直接改 config 就行",
		"No, that's the wrong branch to merge into",
		"搞错了，重新做一遍",
	}
	for _, h := range hits {
		if !decisionRulingCandidate(h) {
			t.Fatalf("expected ruling candidate: %q", h)
		}
	}
	misses := []string{
		"请继续按计划执行",
		"帮我看一下这个函数的实现",
		"好的，收到",
		"短",                       // 过短
		strings.Repeat("很长的一段日志内容", 100), // 超长
	}
	for _, m := range misses {
		if decisionRulingCandidate(m) {
			t.Fatalf("expected non-ruling input rejected: %q", m)
		}
	}
}

func TestDecisionExtractDisabledIsZeroBehavior(t *testing.T) {
	sessions := t.TempDir()
	transcript := writeSession(t, sessions, "a.jsonl", userLine("不对，应该用 worktree"))
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	a := decisionTestAgent(t, transcript, store, false)
	// 关闭态：同步返回，无 goroutine，无任何写入。
	a.maybeExtractDecisionPatterns(true)
	a.maybeExtractDecisionPatterns(false)
	if got := store.List(); len(got) != 0 {
		t.Fatalf("disabled switch must not write memories, got %d", len(got))
	}
	entries, err := os.ReadDir(store.Dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil && len(entries) != 0 {
		t.Fatalf("disabled switch must not touch the memory dir, found %d entries", len(entries))
	}
}

func TestDecisionExtractSavesRulingRoundsOnly(t *testing.T) {
	sessions := t.TempDir()
	transcript := writeSession(t, sessions, "a.jsonl",
		userLine("不对，应该用 worktree 才对"),
		userLine("请继续按计划执行"),               // 非裁决轮次：零产出
		userLine("你理解错了，我说的是压缩事件触发"), // 裁决轮次
		userLine("帮我看一下这个函数的实现"),         // 非裁决轮次
	)
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	a := decisionTestAgent(t, transcript, store, true)
	if err := a.extractDecisionPatterns(); err != nil {
		t.Fatalf("extractDecisionPatterns: %v", err)
	}
	saved := store.List()
	if len(saved) != 2 {
		t.Fatalf("expected exactly 2 ruling memories, got %d: %+v", len(saved), saved)
	}
	for _, m := range saved {
		if m.Type != memory.TypeFeedback {
			t.Fatalf("entry %s: type = %q, want feedback", m.Name, m.Type)
		}
		if m.Activation != memory.ActivationRelevant {
			t.Fatalf("entry %s: activation = %q, want relevant", m.Name, m.Activation)
		}
		if m.Scope != memory.FactScopeProject {
			t.Fatalf("entry %s: scope = %q, want project", m.Name, m.Scope)
		}
		if !strings.Contains(m.Body, "a.jsonl") {
			t.Fatalf("entry %s: body must carry the source session for traceability:\n%s", m.Name, m.Body)
		}
		if !strings.HasPrefix(m.Name, "decision-") {
			t.Fatalf("entry %s: name must be namespaced decision-*", m.Name)
		}
	}
}

func TestDecisionExtractIncrementalCursorPreventsRescan(t *testing.T) {
	sessions := t.TempDir()
	transcript := writeSession(t, sessions, "b.jsonl", userLine("不对，这个分支不该合并"))
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	a := decisionTestAgent(t, transcript, store, true)
	if err := a.extractDecisionPatterns(); err != nil {
		t.Fatalf("first extract: %v", err)
	}
	if got := len(store.List()); got != 1 {
		t.Fatalf("first extract should save 1, got %d", got)
	}
	// Transcript unchanged: the cursor must prevent a second save.
	if err := a.extractDecisionPatterns(); err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if got := len(store.List()); got != 1 {
		t.Fatalf("unchanged transcript must not duplicate, got %d", got)
	}
	// New ruling round appended after the cursor: exactly one more save.
	if err := os.WriteFile(transcript, []byte(strings.Join([]string{
		userLine("不对，这个分支不该合并"),
		userLine("别这样，测试先跑再提交"),
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.extractDecisionPatterns(); err != nil {
		t.Fatalf("third extract: %v", err)
	}
	if got := len(store.List()); got != 2 {
		t.Fatalf("appended ruling round should add exactly one entry, got %d", got)
	}
}

func TestDecisionExtractMissingTranscriptDoesNotBlock(t *testing.T) {
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	a := decisionTestAgent(t, filepath.Join(t.TempDir(), "missing.jsonl"), store, true)
	err := a.extractDecisionPatterns()
	if err == nil {
		t.Fatal("expected a descriptive error for the log, not silence")
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("failed extract must not write memories, got %d", len(got))
	}
	// 绑定了 transcript 的提取在空 store 路径下也不得 panic（防御性）。
	empty := decisionTestAgent(t, "", store, true)
	_ = empty.extractDecisionPatterns()
}

func TestDecisionFactNameStableAndDistinct(t *testing.T) {
	a := decisionFactName("不对，应该用 worktree 才对")
	b := decisionFactName("不对，应该用 worktree 才对")
	c := decisionFactName("不对，应该直接改主仓")
	if a != b {
		t.Fatalf("same text must hash to the same name: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different texts must not collide: %q", a)
	}
	if !strings.HasPrefix(a, "decision-") {
		t.Fatalf("name must carry the decision- namespace: %q", a)
	}
}

// 状态文件损坏时从零重扫（最坏多一轮，同名去重兜底），绝不因状态文件失败。
func TestDecisionExtractCorruptStateRecovers(t *testing.T) {
	sessions := t.TempDir()
	transcript := writeSession(t, sessions, "c.jsonl", userLine("错了，配置要重启才生效"))
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	statePath := filepath.Join(store.Dir, decisionExtractStateFile)
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := decisionTestAgent(t, transcript, store, true)
	if err := a.extractDecisionPatterns(); err != nil {
		t.Fatalf("extract with corrupt state: %v", err)
	}
	if got := len(store.List()); got != 1 {
		t.Fatalf("corrupt state must not block extraction, got %d", got)
	}
	// 状态文件已被重写为合法格式。
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state decisionExtractState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("state file must be rewritten as valid JSON: %v", err)
	}
}
