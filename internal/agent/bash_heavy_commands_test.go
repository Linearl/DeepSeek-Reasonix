package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"reasonix/internal/evidence"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/workspacelease"
)

// ────────────────────────── 白名单命中 / 未命中 ──────────────────────────

// 任务 575 验收 4（命中侧）：装依赖/构建/git 变更类命令必须全部命中白名单，
// 包括 env 前缀、命令链、Windows shim 后缀、双词子命令（go mod download）。
func TestBashHeavyCommandWhitelistHits(t *testing.T) {
	heavy := []string{
		"pnpm install",
		"pnpm add zod",
		"pnpm.cmd install",
		"npm ci",
		"npm run build",
		"npm run-script package",
		"yarn install",
		"bun add zod",
		"go build ./...",
		"go mod download",
		"go mod tidy",
		"go work sync",
		"cargo build --release",
		"cargo check",
		"pip install requests",
		"pip3 uninstall requests",
		"uv sync",
		"uv pip install ruff",
		"poetry install",
		"dotnet restore",
		"composer install",
		"bundle install",
		"git checkout main",
		"git rebase -i HEAD~3",
		"git stash",
		"git pull",
		"git reset --hard HEAD~1",
		// 构建编排器：任意子命令都按重命令处理（配置阶段写项目缓存）。
		"make",
		"make -j4 all",
		"./gradlew build",
		"gradlew.bat tasks",
		"mvn -q clean package",
		"bazel query //...",
		// 复合形状。
		"cd app && pnpm install",
		"PNPM_DEBUG=1 pnpm install",
		"npm install | tee install.log",
		"echo starting && npm ci && echo done",
	}
	for _, command := range heavy {
		args := mustJSON(t, map[string]string{"command": command})
		if !bashHeavyWriteCommand("bash", args) {
			t.Errorf("command %q must hit the heavy whitelist", command)
		}
	}
}

// 任务 575 验收 4（未命中侧）+ 边界 3：只读命令绝不能命中——否则乐观并行
// 收益被抹掉。ls/cat/git status 是任务书点名样例。
func TestBashHeavyCommandWhitelistMisses(t *testing.T) {
	light := []string{
		"",
		"ls",
		"ls -la",
		"cat README.md",
		"grep -rn TODO internal/",
		"git status",
		"git log --oneline -5",
		"git branch -a",
		"git diff",
		"git show HEAD",
		"npm --version",
		"npm ls --json",
		"pnpm -v",
		"pnpm list",
		"yarn --version",
		"go vet ./...",
		"go test ./...",
		"go list ./...",
		"cargo metadata --format-version 1",
		"python scripts/x.py",
		"node build.js",
		"npx tsc --noEmit",
		"echo pnpm install",
		"ssh deploy@example.com",
	}
	for _, command := range light {
		args := mustJSON(t, map[string]string{"command": command})
		if bashHeavyWriteCommand("bash", args) {
			t.Errorf("command %q must NOT hit the heavy whitelist (read-only must stay lock-free)", command)
		}
	}
}

// 非 bash 工具（MCP 等）永不命中：本声明只作用于 bash 粒度。
func TestBashHeavyWriteCommandOnlyBashTool(t *testing.T) {
	args := json.RawMessage(`{"command":"pnpm install"}`)
	for _, name := range []string{"mcp__srv__exec", "write_file", "read_file", ""} {
		if bashHeavyWriteCommand(name, args) {
			t.Errorf("tool %q must never hit the bash heavy whitelist", name)
		}
	}
}

// 静态解析失败（env 展开等动态形状）时的保守退化路径：分词邻接扫描要接住
// `FOO=$(x) pnpm install` 这类重命令；接不住的形状（如 `$(pnpm install)`
// 头词粘连）属已知残留缺口，bash_heavy_commands.go 头注释留档，宁缺勿滥。
func TestBashHeavyCommandFallbackTokenScan(t *testing.T) {
	if !bashCommandIsHeavy("FOO=$(compute) pnpm install") {
		t.Fatal("env-expansion prefixed heavy command must be caught by the token-scan fallback")
	}
	if bashCommandIsHeavy("FOO=$(compute) ls -la") {
		t.Fatal("env-expansion prefixed light command must stay lock-free")
	}
}

// ────────────────── 开关 on/off 两态 × workspace 租约交互 ──────────────────

// heavyLeaseTestAgent 仿 deliveryLeaseTestAgent，但暴露乐观/守卫两个开关。
func heavyLeaseTestAgent(t *testing.T, owner *workspacelease.Owner, optimistic, guard bool, tools ...tool.Tool) *Agent {
	t.Helper()
	reg := tool.NewRegistry()
	for _, candidate := range tools {
		reg.Add(candidate)
	}
	a := New(nil, reg, NewSession(""), Options{
		WorkspaceLease:  owner,
		OptimisticWrite: optimistic,
		BashHeavyGuard:  guard,
	}, event.Discard)
	a.turn.deliveryCriteriaEstablished = true
	a.setTodoState([]evidence.TodoItem{{Content: "mutate", Status: "in_progress"}})
	return a
}

// 任务 575 验收 1：另一会话持写租约时，开关开 ⇒ 重 bash 命令（pnpm install）
// 被拒且文案即既有保守态同款（workspace write lease unavailable），工具未
// 执行；租约释放后同一命令放行（有界等待→通行，非永久拒绝）。
func TestHeavyBashTakesWorkspaceLeaseUnderOptimisticGuard(t *testing.T) {
	root, locks := t.TempDir(), t.TempDir()
	holder, err := workspacelease.New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workspacelease.New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	holder.BeginRun()
	if err := holder.AcquireWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer holder.EndRun()

	bash := &workspaceLeaseTestTool{name: "bash"}
	a := heavyLeaseTestAgent(t, second, true, true, bash)
	second.BeginRun()
	defer second.EndRun()

	// 有界等待面：ctx 120ms 预算内拿不到租约 ⇒ 既有保守态同款拒绝。
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	out := a.executeOne(ctx, &a.turn, provider.ToolCall{
		ID:        "install-1",
		Name:      "bash",
		Arguments: `{"command":"pnpm install"}`,
	})
	if !out.blocked || out.errMsg != "blocked: workspace write lease unavailable" {
		t.Fatalf("heavy bash under a held workspace must be refused, got %+v", out)
	}
	if bash.calls.Load() != 0 {
		t.Fatalf("heavy bash executed while another writer held the workspace: %d calls", bash.calls.Load())
	}

	// 租约释放后同一命令立即放行。
	holder.ReleaseWrite()
	out = a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID:        "install-2",
		Name:      "bash",
		Arguments: `{"command":"pnpm install"}`,
	})
	if out.blocked || out.errMsg != "" {
		t.Fatalf("heavy bash must proceed once the holder released, got %+v", out)
	}
	if bash.calls.Load() != 1 {
		t.Fatalf("heavy bash calls after release = %d, want 1", bash.calls.Load())
	}
}

// 任务 575 验收 3（开关关 = 当前行为）：同一争用面，开关关 ⇒ 重 bash 命令
// 照旧乐观零开销（不取锁、直接执行）——行为等价非字节级。
func TestHeavyBashLockFreeWhenGuardOff(t *testing.T) {
	root, locks := t.TempDir(), t.TempDir()
	holder, err := workspacelease.New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workspacelease.New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	holder.BeginRun()
	if err := holder.AcquireWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer holder.EndRun()

	bash := &workspaceLeaseTestTool{name: "bash"}
	a := heavyLeaseTestAgent(t, second, true, false, bash)
	second.BeginRun()
	defer second.EndRun()

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID:        "install",
		Name:      "bash",
		Arguments: `{"command":"pnpm install"}`,
	})
	if out.blocked || out.errMsg != "" {
		t.Fatalf("guard off must keep today's lock-free behavior, got %+v", out)
	}
	if bash.calls.Load() != 1 {
		t.Fatalf("heavy bash calls with guard off = %d, want 1", bash.calls.Load())
	}
}

// 任务 575 验收 2：开关开时只读 bash 命令（git status）照旧零开销——
// 只读分类根本不进租约面，白名单也未命中（双保险）。
func TestReadOnlyBashStaysLockFreeUnderGuard(t *testing.T) {
	root, locks := t.TempDir(), t.TempDir()
	holder, err := workspacelease.New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workspacelease.New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	holder.BeginRun()
	if err := holder.AcquireWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer holder.EndRun()

	bash := &workspaceLeaseTestTool{name: "bash"}
	a := heavyLeaseTestAgent(t, second, true, true, bash)
	second.BeginRun()
	defer second.EndRun()

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID:        "st",
		Name:      "bash",
		Arguments: `{"command":"git status"}`,
	})
	if out.blocked || out.errMsg != "" {
		t.Fatalf("read-only bash must stay lock-free under the guard: %+v", out)
	}
	if bash.calls.Load() != 1 {
		t.Fatalf("read-only bash calls = %d, want 1", bash.calls.Load())
	}
}

// 任务 575 验收 4（MarkOpaque 交互）：子代理声明（SubagentClaimID）下，重
// bash 命令既照旧走 MarkOpaque（乐观态记录 opaque 活声明、拒绝对抗面不变），
// 又取 workspace 租约（跨会话保护）；释放后租约归还。
func TestHeavyBashSubagentMarksOpaqueAndTakesLease(t *testing.T) {
	root, locks := t.TempDir(), t.TempDir()
	owner, err := workspacelease.New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	sched := NewSubagentScheduler(4, 2)
	sched.SetOptimistic(true)
	_, id, err := sched.AcquireWithID(context.Background(), AcquireRequest{Writer: true})
	if err != nil {
		t.Fatal(err)
	}
	bash := &workspaceLeaseTestTool{name: "bash"}
	reg := tool.NewRegistry()
	reg.Add(bash)
	a := New(nil, reg, NewSession(""), Options{
		WorkspaceLease:     owner,
		WriteScheduler:     sched,
		WriteWorkspaceRoot: root,
		OptimisticWrite:    true,
		BashHeavyGuard:     true,
	}, event.Discard)
	owner.BeginRun()
	defer owner.EndRun()

	plan := &toolCallPlan{
		execTool:     bash,
		execArgs:     json.RawMessage(`{"command":"pnpm install"}`),
		evidenceName: "bash",
		evidenceArgs: json.RawMessage(`{"command":"pnpm install"}`),
	}
	plan.classifyEffects()
	if !plan.effects.WorkspaceMutation {
		t.Fatal("pnpm install must classify as a workspace mutation for the lease path to engage")
	}

	ctx := WithSubagentClaimID(context.Background(), id)
	if blocked, early := a.prepareWriteCoordination(ctx, plan); early {
		t.Fatalf("uncontended heavy bash must not be blocked: %+v", blocked)
	}
	if n := len(sched.ActiveWriterClaims()); n != 1 {
		t.Fatalf("MarkOpaque path must still record the live claim, got %d", n)
	}
	if !owner.State().Acquired {
		t.Fatal("heavy bash must hold the workspace lease in the subagent path too")
	}
	plan.releaseLease()
	if owner.State().Acquired {
		t.Fatal("lease must be released via plan.releaseLease")
	}
}
