package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1434 追加验收 harness —— 运行中节点「工作过程」增量可见的写侧：
//  ① 周期快照：运行中 transcript 已写盘部分即时落盘（增量可见的数据源）；
//  ② stop 契约：stop 之后零写入（终态 SaveOutcome 永远没有并发写者）；
//  ③ opt-in 边界：无标记的普通委派保持终态才落盘（行为逐字节不变）；
//  ④ mint 时观察者：ref 生成处回调在 Execute 返回前触发（登记即时上报）。

// gatedStreamProvider 阻塞在 Stream 直到 release 关闭——为「运行中」提供
// 确定性窗口（provider 未答 = 子代理仍在跑）。
type gatedStreamProvider struct{ release chan struct{} }

func (p *gatedStreamProvider) Name() string { return "gated-stream" }

func (p *gatedStreamProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case <-p.release:
		}
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "final answer"}
		ch <- provider.Chunk{Type: provider.ChunkDone}
	}()
	return ch, nil
}

// waitForFiles polls cond until it holds or the deadline passes.
func waitForCond(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

// ① 单元面：dirty-check 快照真的落盘，stop 后零写入。
func TestTranscriptAutosaveSnapshotsAndStopsCleanly(t *testing.T) {
	old := subagentTranscriptAutosaveInterval
	subagentTranscriptAutosaveInterval = 25 * time.Millisecond
	t.Cleanup(func() { subagentTranscriptAutosaveInterval = old })

	dir := t.TempDir()
	store := NewSubagentStore(dir)
	run := &SubagentRun{Ref: "sa_20261106_100000_000000000_000000000001", Session: NewSession("sys"), store: store}
	path := store.sessionPath(run.Ref)

	stop := store.startTranscriptAutosave(run)
	t.Cleanup(stop)

	run.Session.Add(provider.Message{Role: provider.RoleUser, Content: "hello mid-run"})
	waitForCond(t, 5*time.Second, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("mid-run snapshot must load: %v", err)
	}
	snap := loaded.Snapshot()
	if len(snap) < 2 {
		t.Fatalf("mid-run snapshot = %d messages, want system+user", len(snap))
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	run.Session.Add(provider.Message{Role: provider.RoleAssistant, Content: "written after stop"})
	time.Sleep(3 * subagentTranscriptAutosaveInterval)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("autosave wrote after stop (terminal save would race a concurrent writer)")
	}
}

// ①+③ 集成面：工作流派发（带标记）在 provider 未答时 transcript 已可读，
// 且终态保存照常落全量（CAS 协议未被运行中快照破坏）。
func TestWorkflowDispatchAutosavePersistsMidRun(t *testing.T) {
	old := subagentTranscriptAutosaveInterval
	subagentTranscriptAutosaveInterval = 25 * time.Millisecond
	t.Cleanup(func() { subagentTranscriptAutosaveInterval = old })

	release := make(chan struct{})
	task := newTestTaskTool(t, &gatedStreamProvider{release: release}, tool.NewRegistry(), "sys", "", "", nil)
	rec := &recordSink{}
	ctx := withCallContext(context.Background(), "task-1434-auto", rec, nil, false)
	ctx = WithParentSession(ctx, "sess-1434-auto")
	ctx = WithSubagentRefSink(ctx)
	ctx = WithSubagentTranscriptAutosave(ctx)

	done := make(chan error, 1)
	go func() {
		_, err := task.RunProfileSpec(ctx, ProfileExecSpec{
			Task: TaskSpec{Objective: "probe objective"}, Grant: CapabilityGrant{AllowNoTools: true},
			Worker: WorkerSpec{Kind: "task", Name: "task", SystemPrompt: "sys"},
		})
		done <- err
	}()

	var ref string
	waitForCond(t, 5*time.Second, func() bool {
		ref = SubagentRef(ctx)
		return ref != ""
	})
	path := task.transcripts.sessionPath(ref)

	// 运行中（provider 仍阻塞）：transcript 已在盘上且含已发生的对话内容。
	waitForCond(t, 5*time.Second, func() bool {
		loaded, err := LoadSession(path)
		if err != nil {
			return false
		}
		for _, m := range loaded.Snapshot() {
			if strings.Contains(m.Content, "probe objective") {
				return true
			}
		}
		return false
	})

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("RunProfileSpec after autosave: %v", err)
	}
	// 终态权威保存落全量：final answer 在盘上，meta=completed。
	waitForCond(t, 5*time.Second, func() bool {
		loaded, err := LoadSession(path)
		if err != nil {
			return false
		}
		for _, m := range loaded.Snapshot() {
			if strings.Contains(m.Content, "final answer") {
				return true
			}
		}
		return false
	})
	meta, err := task.transcripts.LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Status != SubagentCompleted {
		t.Fatalf("meta status after run = %q, want completed (terminal save stays authoritative)", meta.Status)
	}
}

// ③ tl1537 翻转钉：普通派发（ctx 无标记）现在也装载 autosave——运行中
// transcript 已写盘可读（透明全派发），终态保存仍是权威。
func TestPlainDispatchAutosavesMidRun(t *testing.T) {
	old := subagentTranscriptAutosaveInterval
	subagentTranscriptAutosaveInterval = 25 * time.Millisecond
	t.Cleanup(func() { subagentTranscriptAutosaveInterval = old })

	release := make(chan struct{})
	task := newTestTaskTool(t, &gatedStreamProvider{release: release}, tool.NewRegistry(), "sys", "", "", nil)
	rec := &recordSink{}
	ctx := withCallContext(context.Background(), "task-1537-plain", rec, nil, false)
	ctx = WithParentSession(ctx, "sess-1537-plain")
	ctx = WithSubagentRefSink(ctx)
	// 无 WithSubagentTranscriptAutosave——RunProfileSpec 自装（tl1537）。

	done := make(chan error, 1)
	go func() {
		_, err := task.RunProfileSpec(ctx, ProfileExecSpec{
			Task: TaskSpec{Objective: "probe plain"}, Grant: CapabilityGrant{AllowNoTools: true},
			Worker: WorkerSpec{Kind: "task", Name: "task", SystemPrompt: "sys"},
		})
		done <- err
	}()

	var ref string
	waitForCond(t, 5*time.Second, func() bool {
		ref = SubagentRef(ctx)
		return ref != ""
	})
	path := task.transcripts.sessionPath(ref)

	// 运行中（provider 仍阻塞）：普通派发的 transcript 已落盘且含已发生的
	// 对话内容（验收 a/b 的写侧；此前此窗口 .jsonl 不存在）。
	waitForCond(t, 5*time.Second, func() bool {
		loaded, err := LoadSession(path)
		if err != nil {
			return false
		}
		for _, m := range loaded.Snapshot() {
			if strings.Contains(m.Content, "probe plain") {
				return true
			}
		}
		return false
	})

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("RunProfileSpec after autosave: %v", err)
	}
	// 终态权威不变（验收 e）：final answer 在盘上，meta=completed。
	waitForCond(t, 5*time.Second, func() bool {
		loaded, err := LoadSession(path)
		if err != nil {
			return false
		}
		for _, m := range loaded.Snapshot() {
			if strings.Contains(m.Content, "final answer") {
				return true
			}
		}
		return false
	})
	meta, err := task.transcripts.LoadMeta(ref)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Status != SubagentCompleted {
		t.Fatalf("meta status after plain run = %q, want completed", meta.Status)
	}
}

// ③ 范围钉：ephemeral（空 ref）派发运行中照旧零落盘——preview-only 形态
// 逐字节保持（tl1537 范围边界）。
func TestEphemeralDispatchNeverPersistsMidRun(t *testing.T) {
	old := subagentTranscriptAutosaveInterval
	subagentTranscriptAutosaveInterval = 25 * time.Millisecond
	t.Cleanup(func() { subagentTranscriptAutosaveInterval = old })

	release := make(chan struct{})
	storeDir := t.TempDir()
	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       &gatedStreamProvider{release: release},
		ParentRegistry: tool.NewRegistry(),
		MaxSteps:       20,
		SysPrompt:      "sys",
	}).WithTranscripts(NewSubagentStore(storeDir), t.TempDir(), "base-model", "base-effort")
	rec := &recordSink{}
	// 无 WithParentSession——ephemeral 形态（空 ref）。
	ctx := withCallContext(context.Background(), "task-1537-ephemeral", rec, nil, false)
	ctx = WithSubagentRefSink(ctx)

	done := make(chan error, 1)
	go func() {
		_, err := task.RunProfileSpec(ctx, ProfileExecSpec{
			Task: TaskSpec{Objective: "probe ephemeral"}, Grant: CapabilityGrant{AllowNoTools: true},
			Worker: WorkerSpec{Kind: "task", Name: "task", SystemPrompt: "sys"},
		})
		done <- err
	}()

	// provider 阻塞即子代理运行中；此时 store 目录必须仍是空的。
	time.Sleep(10 * subagentTranscriptAutosaveInterval)
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("ephemeral dispatch must not persist mid-run, store has %d entries", len(entries))
	}
	if ref := SubagentRef(ctx); ref != "" {
		t.Fatalf("ephemeral dispatch must not mint a ref, got %q", ref)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("RunProfileSpec: %v", err)
	}
}

// ③ 验收 b 写侧：后台派发（job ctx 派生自管理器 root，不经派发 ctx）运行中
// 同样落盘——闭包内补装的标记必须生效。
func TestBackgroundDispatchAutosavesMidRun(t *testing.T) {
	old := subagentTranscriptAutosaveInterval
	subagentTranscriptAutosaveInterval = 25 * time.Millisecond
	t.Cleanup(func() { subagentTranscriptAutosaveInterval = old })

	release := make(chan struct{})
	storeDir := t.TempDir()
	task := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       &gatedStreamProvider{release: release},
		ParentRegistry: tool.NewRegistry(),
		MaxSteps:       20,
		SysPrompt:      "sys",
	}).WithTranscripts(NewSubagentStore(storeDir), t.TempDir(), "base-model", "base-effort")
	rec := &recordSink{}
	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	ctx := jobs.WithManager(withCallContext(context.Background(), "task-1537-bg", rec, nil, false), jm)
	ctx = jobs.WithSession(ctx, "sess-1537-bg")
	ctx = WithParentSession(ctx, "sess-1537-bg")
	ctx = WithSubagentRefSink(ctx)

	done := make(chan error, 1)
	go func() {
		_, err := task.Execute(ctx, json.RawMessage(`{"prompt":"probe background","run_in_background":true,"description":"autosave bg"}`))
		done <- err
	}()

	var ref string
	waitForCond(t, 5*time.Second, func() bool {
		ref = SubagentRef(ctx)
		return ref != ""
	})
	path := filepath.Join(storeDir, ref+".jsonl")

	// job 已在跑（provider 仍阻塞）：transcript 已落盘——root 派生的 job ctx
	// 也装上了标记（tl1537 闭包补装点）。
	waitForCond(t, 5*time.Second, func() bool {
		loaded, err := LoadSession(path)
		if err != nil {
			return false
		}
		for _, m := range loaded.Snapshot() {
			if strings.Contains(m.Content, "probe background") {
				return true
			}
		}
		return false
	})

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Execute: %v", err)
	}
	waitForCond(t, 5*time.Second, func() bool {
		meta, err := task.transcripts.LoadMeta(ref)
		return err == nil && meta.Status == SubagentCompleted
	})
}

// tl1537 追加：autosave 首写前种子化 branch-meta——CreatedAt 保持派发时刻，
// 不被首个运行中快照回填成保存时刻（ensureBranchCreatedAt 的终态契约对
// 运行中首写同样成立）。
func TestAutosaveSeedsBranchCreatedAtFromDispatch(t *testing.T) {
	old := subagentTranscriptAutosaveInterval
	subagentTranscriptAutosaveInterval = 25 * time.Millisecond
	t.Cleanup(func() { subagentTranscriptAutosaveInterval = old })

	dir := t.TempDir()
	store := NewSubagentStore(dir)
	ref := "sa_20261110_100000_000000000_000000000001"
	created := time.Date(2026, 11, 10, 9, 59, 0, 0, time.UTC)
	run := &SubagentRun{
		Ref:     ref,
		Session: NewSession("sys"),
		Meta:    SubagentMeta{Ref: ref, CreatedAt: created},
		store:   store,
	}
	path := store.sessionPath(run.Ref)

	stop := store.startTranscriptAutosave(run)
	t.Cleanup(stop)

	waitForCond(t, 5*time.Second, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	meta, ok, err := LoadBranchMeta(path)
	if err != nil {
		t.Fatalf("LoadBranchMeta: %v", err)
	}
	if !ok {
		t.Fatal("autosave must seed the branch-meta sidecar before the first snapshot")
	}
	if !meta.CreatedAt.Equal(created) {
		t.Fatalf("branch meta CreatedAt = %v, want dispatch time %v (must not be backfilled by the snapshot moment)", meta.CreatedAt, created)
	}
}

// ④ mint 时观察者：回调在 provider 未答（Execute 未返回）时就已触发——
// 工作流 runner 靠它做运行中即时登记。
func TestOnSubagentRefRecordedFiresAtMint(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease) // 失败路径也放行阻塞中的 provider goroutine
	task := newTestTaskTool(t, &gatedStreamProvider{release: release}, tool.NewRegistry(), "sys", "", "", nil)
	rec := &recordSink{}
	ctx := withCallContext(context.Background(), "task-1434-obs", rec, nil, false)
	ctx = WithParentSession(ctx, "sess-1434-obs")
	ctx = WithSubagentRefSink(ctx)
	observed := make(chan string, 1)
	ctx = OnSubagentRefRecorded(ctx, func(ref string) { observed <- ref })

	done := make(chan error, 1)
	go func() {
		_, err := task.RunProfileSpec(ctx, ProfileExecSpec{
			Task: TaskSpec{Objective: "do the thing"}, Grant: CapabilityGrant{AllowNoTools: true},
			Worker: WorkerSpec{Kind: "task", Name: "task", SystemPrompt: "sys"},
		})
		done <- err
	}()

	// 观察者在 Execute 返回前触发，且与 sink 读回一致。
	var seen string
	select {
	case seen = <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("mint-time observer never fired before the run finished")
	}
	select {
	case err := <-done:
		t.Fatalf("RunProfileSpec returned while the provider gate was still closed: %v", err)
	default:
	}
	closeRelease()
	if err := <-done; err != nil {
		t.Fatalf("RunProfileSpec: %v", err)
	}
	if seen == "" || seen != SubagentRef(ctx) {
		t.Fatalf("observer ref %q must match the sink %q", seen, SubagentRef(ctx))
	}
}

// 观察者降级：nil 回调不炸（ctx 原样返回）；无 sink 的 Record 也不触发观察者
// （普通委派路径零成本）。
func TestOnSubagentRefRecordedNilAndSinkless(t *testing.T) {
	ctx := OnSubagentRefRecorded(context.Background(), nil)
	fired := false
	ctx = OnSubagentRefRecorded(ctx, func(string) { fired = true })
	// 有观察者、无 sink：Record 是 no-op，观察者不得被绕过 sink 触发。
	RecordSubagentRef(ctx, "sa_x")
	if fired {
		t.Fatal("observer must not fire without a sink (no sink = nobody listening)")
	}
}

// 增量快照的盘上身份：写路径 = 读桥解析路径。桌面查看面经
// ListSubagentsByParent(sessionDir, …) 定位 <sessionDir>/subagents/<ref>.jsonl
// ——store 的目录本身就是 subagents 目录，autosave 落盘必须命中同一路径，
// 运行中打开才能读到已写盘部分。
func TestAutosaveLandsOnTheReadBridgesLookupPath(t *testing.T) {
	sessionDir := t.TempDir()
	subagentsDir := filepath.Join(sessionDir, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewSubagentStore(subagentsDir)
	run := &SubagentRun{Ref: "sa_20261106_100001_000000000_000000000002", Session: NewSession("sys"), store: store}
	stop := store.startTranscriptAutosave(run)
	t.Cleanup(stop)
	lookupPath := filepath.Join(sessionDir, "subagents", run.Ref+".jsonl")
	waitForCond(t, 5*time.Second, func() bool {
		_, err := os.Stat(lookupPath)
		return err == nil
	})
	if _, err := LoadSession(lookupPath); err != nil {
		t.Fatalf("read bridge loader must parse the mid-run snapshot: %v", err)
	}
}
