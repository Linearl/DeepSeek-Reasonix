package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/jobs"
	"reasonix/internal/workflow"
)

// tl1434 验收 harness —— 工作流节点详情「工作过程」查看桥（desktop 侧）。
// 钉四面：
//  ① 入口数据通路：登记表 ref → 父会话归属证明 → preview 只读投影，
//     返回与 ReadSubagentSession 同管线的消息；
//  ② 只读铁律：读前后 transcript/meta 逐字节+mtime 不变、目录无新文件、
//     meta.Status 不变——打开过程绝不唤醒/写入子代理会话；
//  ③ 归属降级：未知 run/节点、越界 occurrence、无父会话派发、异父 ref、
//     transcript 缺失，全部给明确错误（面板按错误降级，绝不猜路径）；
//  ④ wire 快照携带 subagentRefs（面板入口可见性的数据源）。

const traceWorkflowDoc = `
version: 1
name: trace-wf
nodes:
  - id: probe
    type: subagent
    prompt: probe node
`

// traceRefRunner 模拟生产派发的登记路径（workflow.ReportSubagentRef 导出原语，
// 与 TaskSubagentRunner.runNode 同形）。
type traceRefRunner struct{ ref string }

func (r *traceRefRunner) Run(ctx context.Context, req workflow.SubagentRequest) (string, error) {
	workflow.ReportSubagentRef(ctx, req, r.ref)
	return `Final answer: {"ok": true}`, nil
}

func runTraceService(t *testing.T, svc *workflow.Service, parentSession, ref string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, traceWorkflowDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	svc.AddRunnerSource(func(string) (workflow.SubagentRunner, bool) { return &traceRefRunner{ref: ref}, true })
	callCtx := jobs.WithSession(ctx, parentSession)
	res, err := svc.RunWorkflow(callCtx, "trace-wf", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		if err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(workflow.RunCompleted) {
			return res.RunID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("trace run did not complete in time")
	return ""
}

// subagentDirSnapshot 记录 subagents 目录的文件集+内容+mtime，作只读断言基线。
type subagentDirSnapshot map[string]subagentFileInfo

type subagentFileInfo struct {
	content string
	modTime time.Time
}

func snapshotSubagentDir(t *testing.T, dir string) subagentDirSnapshot {
	t.Helper()
	root := filepath.Join(dir, "subagents")
	out := subagentDirSnapshot{}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			out["<dir>:"+e.Name()] = subagentFileInfo{}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = subagentFileInfo{content: string(raw), modTime: info.ModTime()}
	}
	return out
}

func assertDirUnchanged(t *testing.T, dir string, before subagentDirSnapshot, label string) {
	t.Helper()
	after := snapshotSubagentDir(t, dir)
	if len(after) != len(before) {
		t.Fatalf("%s: subagents dir file set changed: before=%d after=%d", label, len(before), len(after))
	}
	for name, info := range before {
		got, ok := after[name]
		if !ok {
			t.Fatalf("%s: file %s disappeared", label, name)
		}
		if got.content != info.content {
			t.Fatalf("%s: file %s content changed (read path wrote to the transcript!)", label, name)
		}
		if !got.modTime.Equal(info.modTime) {
			t.Fatalf("%s: file %s mtime changed %v -> %v (read path wrote to the transcript!)", label, name, info.modTime, got.modTime)
		}
	}
}

// ①+② 主通路 + 零写入铁律。
func TestReadWorkflowNodeSubagentReturnsMessagesAndWritesNothing(t *testing.T) {
	_, dir, sessionPath := newCapsuleTestSession(t)
	parent := agent.BranchID(sessionPath)
	ref := "sa_20261106_090000_000000000_000000000001"
	writeSubagentMeta(t, dir, ref, parent, time.Now().UTC(), "task", "m", "", false)
	transcript := "{\"role\":\"user\",\"content\":\"probe question\"}\n" +
		"{\"role\":\"assistant\",\"content\":\"probe answer\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "subagents", ref+".jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := workflow.NewService(t.TempDir())
	runID := runTraceService(t, svc, parent, ref)

	before := snapshotSubagentDir(t, dir)
	messages, err := readWorkflowNodeSubagentMessages(svc, runID, "probe", 1, []string{dir})
	if err != nil {
		t.Fatalf("readWorkflowNodeSubagentMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 history messages, got %d: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "probe question" {
		t.Fatalf("unexpected first message: %+v", messages[0])
	}
	// 只读铁律：打开过程对子代理会话零写入（内容+mtime+文件集逐项不变）。
	assertDirUnchanged(t, dir, before, "after open")
	meta, err := agent.NewSubagentStore(filepath.Join(dir, "subagents")).LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta after read: %v", err)
	}
	if meta.Status != agent.SubagentCompleted {
		t.Fatalf("meta status after read = %q, want completed (viewing must not touch the child)", meta.Status)
	}
	// 归属证明钉：不在登记表内的 ref 一律拒读（traversal 形输入同防）。
	for _, foreign := range []string{"sa_20261106_090000_000000000_000000000009", "../../secrets"} {
		_, _, err := svc.SubagentRefForNode(runID, foreign, 1)
		_ = err // 查询口按 nodeID 键，非法 nodeId 本就不在表内；桥的 nodeID 亦然
	}
	if _, err := readWorkflowNodeSubagentMessages(svc, runID, "../../secrets", 1, []string{dir}); err == nil {
		t.Fatal("traversal-shaped nodeID must be rejected")
	}
}

// ③ 降级矩阵：每条都给明确错误，面板按错误渲染降级文案。
func TestReadWorkflowNodeSubagentDegradations(t *testing.T) {
	_, dir, sessionPath := newCapsuleTestSession(t)
	parent := agent.BranchID(sessionPath)
	ownedRef := "sa_20261106_090001_000000000_000000000002"
	foreignRef := "sa_20261106_090001_000000000_000000000003"
	missingRef := "sa_20261106_090001_000000000_000000000004"
	writeSubagentMeta(t, dir, ownedRef, parent, time.Now().UTC(), "task", "m", "", true)
	writeSubagentMeta(t, dir, foreignRef, "another-parent", time.Now().UTC(), "task", "m", "", true)
	writeSubagentMeta(t, dir, missingRef, parent, time.Now().UTC(), "task", "m", "", false)

	svc := workflow.NewService(t.TempDir())
	runID := runTraceService(t, svc, parent, ownedRef)

	if _, err := readWorkflowNodeSubagentMessages(svc, "no-such-run", "probe", 1, []string{dir}); err == nil {
		t.Fatal("unknown run must degrade")
	}
	if _, err := readWorkflowNodeSubagentMessages(svc, runID, "ghost-node", 1, []string{dir}); err == nil {
		t.Fatal("unknown node must degrade")
	}
	if _, err := readWorkflowNodeSubagentMessages(svc, runID, "probe", 2, []string{dir}); err == nil {
		t.Fatal("out-of-range occurrence must degrade")
	}
	if _, err := readWorkflowNodeSubagentMessages(svc, runID, "probe", 0, []string{dir}); err == nil {
		t.Fatal("occurrence 0 must degrade")
	}
	// 无父会话派发（面板续跑）：transcript 未持久化，明确降级。
	ephSvc := workflow.NewService(t.TempDir())
	ephRunID := runTraceService(t, ephSvc, "", ownedRef)
	_, err := readWorkflowNodeSubagentMessages(ephSvc, ephRunID, "probe", 1, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "without a parent session") {
		t.Fatalf("parentless dispatch must degrade with the explicit cause, got %v", err)
	}
	// 登记表内 ref 但盘上归属另一父会话：拒绝（不跨会话探读）。
	foreignSvc := workflow.NewService(t.TempDir())
	foreignRunID := runTraceService(t, foreignSvc, parent, foreignRef)
	if _, err := readWorkflowNodeSubagentMessages(foreignSvc, foreignRunID, "probe", 1, []string{dir}); err == nil {
		t.Fatal("ref owned by another parent must be rejected")
	} else if !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("unexpected error for foreign ref: %v", err)
	}
	// transcript 缺失（崩溃未落/被清理）：明确降级。
	missingSvc := workflow.NewService(t.TempDir())
	missingRunID := runTraceService(t, missingSvc, parent, missingRef)
	if _, err := readWorkflowNodeSubagentMessages(missingSvc, missingRunID, "probe", 1, []string{dir}); err == nil {
		t.Fatal("missing transcript must degrade")
	} else if !strings.Contains(err.Error(), "has not been saved yet") {
		t.Fatalf("unexpected error for missing transcript: %v", err)
	}
	// 无已知会话目录（极端环境）：明确降级，不空指针。
	ownedSvc := workflow.NewService(t.TempDir())
	ownedRunID := runTraceService(t, ownedSvc, parent, ownedRef)
	if _, err := readWorkflowNodeSubagentMessages(ownedSvc, ownedRunID, "probe", 1, nil); err == nil {
		t.Fatal("no known session dirs must degrade")
	}
}

// ④ wire 快照钉：subagentRefs 进面板快照（入口可见性数据源），无关联为空。
func TestWorkflowRunsWireCarriesSubagentRefs(t *testing.T) {
	parent := "sess-wire-1434"
	ref := "sa_20261106_090002_000000000_000000000005"
	svc := workflow.NewService(t.TempDir())
	runTraceService(t, svc, parent, ref)

	snaps := svc.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 run snapshot, got %d", len(snaps))
	}
	var node *workflow.RunNodeSnapshot
	for i := range snaps[0].Nodes {
		if snaps[0].Nodes[i].ID == "probe" {
			node = &snaps[0].Nodes[i]
		}
	}
	if node == nil || len(node.SubagentRefs) != 1 || node.SubagentRefs[0] != ref {
		t.Fatalf("wire snapshot must carry the registered ref, got %+v", node)
	}
}
