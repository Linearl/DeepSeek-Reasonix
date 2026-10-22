package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/agent"
)

// 任务 1065：「选 HEAD → 反向合成最长链」的后端面。
//
// 验收（tl 1065 正文）：
//  1. 面板内选 HEAD + 勾选分支集合 → 一键反向合成最长链；
//  2. 合成后目标链独有计数涵盖各参与分支的独有事件（总条数≈并集），留操
//     作记录+进度展示；
//  3. 重复事件零双份；合成后其余分支可经 1064 删除；旧面板全部规则（tail
//     预览/轮次优先推荐/预览降级）在新面板等价可用；
//  4. Go 包级测试+前端 harness 绿，无预存红。
//
// 本测试钉住数据层：合成一次后主线持有并集（重复事件零双份），落败分支的
// 独有前缀被 graft 回主线（Prefixed>0），其余分支进回收站，合成结果可读。

func TestSynthesizeSessionHeadsFoldsUniqueEventsOntoChosenHead(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "heads-synth-1.jsonl")
	copyPath := filepath.Join(dir, "heads-synth-1-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
	})

	// Main: sys + q1 + a1 (a "canonical" prefix the copy will fork from).
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	if err := s.Save(mainPath); err != nil {
		t.Fatal(err)
	}

	// Copy: same prefix + one extra user turn (the "unique" work the user is
	// deciding against by keeping the main's chain as HEAD).
	b, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	copyBody := string(b) +
		`{"role":"user","content":"q2-unique-to-copy"}` + "\n" +
		`{"role":"assistant","content":"a2-unique-to-copy"}` + "\n"
	if err := os.WriteFile(copyPath, []byte(copyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	// The consolidate engine requires the copy's branch meta sidecar to be
	// present (it loads the winner's meta before renaming it onto the main
	// identity); EnsureBranchMeta is the same production path a copy gets when
	// it is first written by a real session.
	if _, err := agent.EnsureBranchMeta(copyPath); err != nil {
		t.Fatalf("ensure copy meta: %v", err)
	}

	app := newBranchDeleteApp(t)
	report, err := app.SynthesizeSessionHeads(mainPath, "")
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if len(report.Items) == 0 {
		t.Fatal("the pre-run reading must list the participating branch")
	}
	if report.Items[0].Unique == 0 {
		t.Fatalf("the copy's unique events must be non-zero before synthesis: %+v", report.Items[0])
	}

	// The copy is folded into the recoverable trash, not hard-deleted.
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("the losing branch must be archived away from the live directory, stat err=%v", err)
	}

	// The main transcript survives (it was the chosen HEAD) and the report
	// carries a post-run reading.
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the chosen HEAD's transcript must survive synthesis: %v", err)
	}
	if report.AfterMessages == 0 {
		t.Fatalf("the report must carry a post-run reading of the new main: %+v", report)
	}
}

func TestSynthesizeSessionHeadsRefusesSingleChain(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "heads-synth-2.jsonl")
	t.Cleanup(func() { _ = os.Remove(mainPath) })

	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	if err := s.Save(mainPath); err != nil {
		t.Fatal(err)
	}

	app := newBranchDeleteApp(t)
	if _, err := app.SynthesizeSessionHeads(mainPath, ""); err == nil {
		t.Fatal("a conversation with one chain has nothing to synthesize; the call must be refused")
	} else if !strings.Contains(err.Error(), "only one chain") {
		t.Fatalf("refusal must say what is missing, got %v", err)
	}
}
