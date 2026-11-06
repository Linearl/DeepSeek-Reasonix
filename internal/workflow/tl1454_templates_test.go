package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tl1454 acceptance through the real service + store + engine: create → located
// rejection → fix → save → run → located failure → timeout/cancel/budget →
// edit → new version → history; plus off-state, deny boundary, invalid files.

func templateHome(t *testing.T, enabled bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg := "[desktop]\nexperimental_workflow_templates = " + map[bool]string{true: "true", false: "false"}[enabled] +
		"\n\n[permissions]\ndeny = [\"Bash(rm *)\"]\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func templateService(t *testing.T, runner SubagentRunner) (*Service, string) {
	t.Helper()
	home := templateHome(t, true)
	svc := NewService(filepath.Join(home, "workflows"))
	svc.AddRunnerSource(func(sid string) (SubagentRunner, bool) { return runner, sid == "sess-tpl" })
	return svc, t.TempDir()
}

func waitTerminal(t *testing.T, svc *Service, runID string) RunSnapshot {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, snap := range svc.Snapshot() {
			if snap.ID == runID && snap.State != RunRunning {
				return snap
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not settle", runID)
	return RunSnapshot{}
}

func nodeUsage(snap RunSnapshot, id string) NodeUsageView {
	for _, n := range snap.Nodes {
		if n.ID == id {
			return n.Usage
		}
	}
	return NodeUsageView{}
}

const tplBroken = `version: 1
name: audit
nodes:
  - id: scan
    type: subagent
  - id: check
    type: gate
    command: "cat marker.txt"
edges:
  - {from: scan, to: check}
`

const tplFixed = `version: 1
name: audit
nodes:
  - id: scan
    type: subagent
    prompt: scan the workspace
  - id: check
    type: gate
    command: "cat marker.txt"
edges:
  - {from: scan, to: check}
`

func TestTL1454TemplateAcceptanceChain(t *testing.T) {
	runner := &scriptRunner{}
	svc, gateDir := templateService(t, runner)
	ctx := context.Background()
	opts := TemplateRunOptions{SessionID: "sess-tpl", GateDir: gateDir}

	// ① 新建 → 校验失败给定位（行 + 节点 + 字段路径）。
	bad := svc.ValidateTemplate(tplBroken)
	if bad.OK || bad.Diagnostic == nil || bad.Diagnostic.Line != 4 || bad.Diagnostic.NodeID != "scan" || bad.Diagnostic.Path != "nodes[0].prompt" {
		t.Fatalf("broken template must be rejected with a location: %+v", bad.Diagnostic)
	}
	if _, err := svc.SaveTemplate(tplBroken, ""); err == nil {
		t.Fatal("an invalid template must never be saved")
	}
	// ② 修好 → 校验通过（版本预览 + 契约摘要）→ 保存。
	good := svc.ValidateTemplate(tplFixed)
	if !good.OK || good.Summary == nil || len(good.Contracts) != 2 || good.Contracts[1].Inputs != "scan" {
		t.Fatalf("fixed template must validate with contracts: %+v", good)
	}
	if good.Contracts[0].Failure != ContractFailureAbortRun || good.Contracts[1].Failure != ContractFailureBranchOrAbort {
		t.Fatalf("contracts must carry failure-semantics codes (UI localizes them): %+v", good.Contracts)
	}
	saved, err := svc.SaveTemplate(tplFixed, "")
	if err != nil || !saved.Created || saved.Version != good.Summary.Version || !strings.HasPrefix(saved.Version, "v·") {
		t.Fatalf("save: %+v err=%v", saved, err)
	}
	// ③ 运行 → gate 在会话工作区执行：marker 缺失 ⇒ 节点 check 失败可定位。
	res, err := svc.RunTemplate(ctx, "audit", opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	snap := waitTerminal(t, svc, res.RunID)
	if snap.State != RunFailed || snap.Template.Failure == nil || snap.Template.Failure.NodeID != "check" || snap.Template.Failure.Reason != ReasonGateFailed {
		t.Fatalf("node failure must be located: state=%s failure=%+v", snap.State, snap.Template.Failure)
	}
	if snap.Template.Origin != RunOriginPanel || snap.Template.Version != saved.Version {
		t.Fatalf("run must carry origin and template version: %+v", snap.Template)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = svc.RunTemplate(ctx, "audit", opts)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if snap = waitTerminal(t, svc, res.RunID); snap.State != RunCompleted {
		t.Fatalf("gate must run in the session workspace (marker present): %s %+v", snap.State, snap.Template.Failure)
	}
	// ④ 编辑 → 版本变化，旧版本入历史并可载回；旧版本号保存被并发保护拒绝。
	edited := strings.Replace(tplFixed, "scan the workspace", "scan the workspace twice", 1)
	saved2, err := svc.SaveTemplate(edited, saved.Version)
	if err != nil || saved2.Created || saved2.Version == saved.Version {
		t.Fatalf("edit must change the version: %+v err=%v", saved2, err)
	}
	hist := svc.TemplateHistory("audit")
	if len(hist) != 1 || hist[0].Version != saved.Version {
		t.Fatalf("previous head must be archived: %+v", hist)
	}
	old, err := svc.TemplateVersionSource("audit", hist[0].Version)
	if err != nil || !strings.Contains(old, "scan the workspace\"") {
		t.Fatalf("history must load the previous text: %v\n%s", err, old)
	}
	if _, err := svc.SaveTemplate(tplFixed, saved.Version); err == nil || !strings.Contains(err.Error(), "changed since you opened it") {
		t.Fatalf("stale save must be refused: %v", err)
	}
	if _, err := svc.SaveTemplate(tplFixed, ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create over an existing name must be refused: %v", err)
	}
}

func TestTL1454TemplateTimeoutCancelAndBudget(t *testing.T) {
	runner := &scriptRunner{
		block: map[string]bool{"stuck": true, "wait": true},
		usage: map[string][2]int{"spend-a": {700, 2}, "spend-b": {700, 2}},
	}
	svc, gateDir := templateService(t, runner)
	ctx := context.Background()
	opts := TemplateRunOptions{SessionID: "sess-tpl", GateDir: gateDir}
	for _, doc := range []string{
		"version: 1\nname: slow\nnodes:\n  - {id: stuck, type: subagent, prompt: s, timeout: 80ms}\n",
		"version: 1\nname: wait\nnodes:\n  - {id: wait, type: subagent, prompt: w}\n",
		"version: 1\nname: spend\nbudget: {tokens: 1000}\nnodes:\n  - {id: spend-a, type: subagent, prompt: a}\n  - {id: spend-b, type: subagent, prompt: b}\n  - {id: spend-c, type: subagent, prompt: c}\nedges:\n  - {from: spend-a, to: spend-b}\n  - {from: spend-b, to: spend-c}\n",
	} {
		if _, err := svc.SaveTemplate(doc, ""); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	// 超时：节点 stuck 自动停止，原因 timeout，可定位。
	res, _ := svc.RunTemplate(ctx, "slow", opts)
	snap := waitTerminal(t, svc, res.RunID)
	if snap.State != RunFailed || snap.Template.Failure == nil || snap.Template.Failure.Reason != ReasonTimeout || snap.Template.Failure.NodeID != "stuck" {
		t.Fatalf("timeout must fail the node: %s %+v", snap.State, snap.Template.Failure)
	}
	if nodeUsage(snap, "stuck").Reason != ReasonTimeout {
		t.Fatalf("node record must carry the reason: %+v", nodeUsage(snap, "stuck"))
	}

	// 取消：用户停止 ⇒ interrupted（可续语义不变）+ 原因 cancelled。
	res, _ = svc.RunTemplate(ctx, "wait", opts)
	time.Sleep(60 * time.Millisecond)
	if !svc.CancelRun(res.RunID) {
		t.Fatal("cancel must hit the running run")
	}
	snap = waitTerminal(t, svc, res.RunID)
	if snap.State != RunInterrupted || snap.Template.Failure == nil || snap.Template.Failure.Reason != ReasonCancelled {
		t.Fatalf("user stop must land interrupted/cancelled: %s %+v", snap.State, snap.Template.Failure)
	}

	// 预算：token 越线 ⇒ 运行失败，明确报轴/已用/上限/越线节点，后续节点不派发。
	res, _ = svc.RunTemplate(ctx, "spend", opts)
	snap = waitTerminal(t, svc, res.RunID)
	f := snap.Template.Failure
	if snap.State != RunFailed || f == nil || f.Reason != ReasonBudget || f.NodeID != "spend-b" ||
		!strings.Contains(f.Detail, "workflow budget exceeded: tokens 1400 >= 1000 budget") {
		t.Fatalf("budget overrun must be a plain located error: %s %+v", snap.State, f)
	}
	if snap.Template.UsedTokens != 1400 || snap.Template.Budget == nil || snap.Template.Budget.Tokens != 1000 {
		t.Fatalf("budget usage must be visible: %+v", snap.Template)
	}
	if runner.callCount("spend-c") != 0 {
		t.Fatal("no node may run after the budget is spent")
	}
}

// C1: a gate command the user's deny rules forbid is flagged in the run
// preview and refused at run time — the command never executes.
func TestTL1454PanelGateDenyRulesBite(t *testing.T) {
	svc, gateDir := templateService(t, &scriptRunner{})
	victim := filepath.Join(gateDir, "build")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "version: 1\nname: wipe\nnodes:\n  - {id: clean, type: gate, command: \"rm -rf build\"}\n"
	if _, err := svc.SaveTemplate(doc, ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	preview, err := svc.PreviewTemplateRun("wipe", gateDir)
	if err != nil || !preview.Blocked || len(preview.Gates) != 1 || !preview.Gates[0].Denied {
		t.Fatalf("preview must flag the denied gate: %+v err=%v", preview, err)
	}
	res, err := svc.RunTemplate(context.Background(), "wipe", TemplateRunOptions{SessionID: "sess-tpl", GateDir: gateDir})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	snap := waitTerminal(t, svc, res.RunID)
	if snap.Template.Failure == nil || snap.Template.Failure.Reason != ReasonGateDenied || snap.Template.Failure.NodeID != "clean" {
		t.Fatalf("denied gate must fail located with gate_denied: %+v", snap.Template.Failure)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("a denied gate command must never execute")
	}
}

// A3: switch off ⇒ template writes refused, definition overwrites archive
// nothing (store byte-identical to pre-tl1454); switch on ⇒ the agent
// overwrite path archives too.
func TestTL1454SwitchOffKeepsStoreUnchanged(t *testing.T) {
	home := templateHome(t, false)
	dir := filepath.Join(home, "workflows")
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.SaveTemplate(tplFixed, ""); !errors.Is(err, ErrTemplatesDisabled) {
		t.Fatalf("save must be refused while off: %v", err)
	}
	if _, err := svc.RunTemplate(ctx, "audit", TemplateRunOptions{}); !errors.Is(err, ErrTemplatesDisabled) {
		t.Fatalf("run must be refused while off: %v", err)
	}
	if _, err := svc.DefineWorkflow(ctx, tplFixed, false); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(tplFixed, "scan the workspace", "rescan", 1)
	if _, err := svc.DefineWorkflow(ctx, edited, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, versionsDirName)); !os.IsNotExist(err) {
		t.Fatalf("switch off: overwrite must not create %s/ (err=%v)", versionsDirName, err)
	}

	templateHome(t, true)
	svc2 := NewService(dir)
	if _, err := svc2.DefineWorkflow(ctx, tplFixed, true); err != nil {
		t.Fatal(err)
	}
	if hist := svc2.TemplateHistory("audit"); len(hist) != 1 {
		t.Fatalf("switch on: the agent overwrite path archives the previous head too: %+v", hist)
	}
}

// B6: invalid catalog files are listed with a located error, never rewritten,
// and can be repaired explicitly (the original bytes go to history first).
func TestTL1454InvalidFilesListedNeverRewritten(t *testing.T) {
	svc, _ := templateService(t, &scriptRunner{})
	dir := svc.store.dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	future := "{\n  \"version\": 2,\n  \"name\": \"future\",\n  \"nodes\": [{\"id\": \"a\", \"type\": \"subagent\", \"prompt\": \"p\"}]\n}"
	broken := "{\n  \"version\": 1,\n  \"name\": \"broken\",\n  \"nodes\": [\n}"
	for file, body := range map[string]string{"future.json": future, "broken.json": broken} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list := svc.Templates()
	kinds := map[string]TemplateInvalidFile{}
	for _, inv := range list.Invalid {
		kinds[inv.File] = inv
	}
	if kinds["future.json"].Kind != DiagVersion || kinds["future.json"].Line != 2 {
		t.Fatalf("future version must be listed as a version problem at line 2: %+v", kinds["future.json"])
	}
	if kinds["broken.json"].Kind != DiagSyntax || kinds["broken.json"].Line == 0 {
		t.Fatalf("broken JSON must be listed with a syntax location: %+v", kinds["broken.json"])
	}
	for file, body := range map[string]string{"future.json": future, "broken.json": broken} {
		raw, _ := os.ReadFile(filepath.Join(dir, file))
		if string(raw) != body {
			t.Fatalf("%s was rewritten by listing", file)
		}
	}
	src, err := svc.InvalidTemplateSource("future.json")
	if err != nil || src != future {
		t.Fatalf("invalid source must be the raw bytes: %v", err)
	}
	if _, err := svc.InvalidTemplateSource("../config.toml"); err == nil {
		t.Fatal("only catalog files are readable")
	}
	repaired := strings.Replace(future, "\"version\": 2", "\"version\": 1", 1)
	if _, err := svc.SaveTemplate(repaired, ""); err == nil {
		t.Fatal("repair needs the explicit repair intent")
	}
	if _, err := svc.SaveTemplate(repaired, RepairVersion); err != nil {
		t.Fatalf("repair save: %v", err)
	}
	hist := svc.TemplateHistory("future")
	if len(hist) != 1 {
		t.Fatalf("the unreadable original must be archived before repair: %+v", hist)
	}
	orig, err := svc.TemplateVersionSource("future", hist[0].Version)
	if err != nil || orig != future {
		t.Fatalf("archived original must be byte-identical: %v", err)
	}
}
