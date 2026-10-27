package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl 1201 验收②：write authority stale 必须能在一次修复内纠正，而不是五轮
// 重试同一份死 authority。本文件用真 Controller（带 executor session，否则
// authority bind 对空 executor 是静默 no-op）走 restoreTabWriteAuthorityAfterStale
// 的两条路径（活 lease 重绑 / 接管重取），并钉死互斥不回退的反向半边。

func newAuthorityTestController(t *testing.T, dir, path, label string) *control.Controller {
	t.Helper()
	executor := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	return control.New(control.Options{
		Runner:      executor,
		Executor:    executor,
		Sink:        event.Discard,
		SessionDir:  dir,
		SessionPath: path,
		Label:       label,
	})
}

func newWriteAuthorityRestoreFixture(t *testing.T, name string) (*App, *WorkspaceTab, *control.Controller, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, name+".jsonl")
	ctrl := newAuthorityTestController(t, dir, path, name)
	t.Cleanup(ctrl.Close)
	tab := &WorkspaceTab{ID: "restore_" + name, Scope: "global", Ctrl: ctrl}
	app := &App{
		tabs:        map[string]*WorkspaceTab{tab.ID: tab},
		activeTabID: tab.ID,
		tabOrder:    []string{tab.ID},
	}
	return app, tab, ctrl, path
}

// 快路径：tab 的 lease 仍活着，只是 authority 世代被顶掉——同一 lease 对象
// 重新发一代即可修复，lease 对象不得被替换。
func TestRestoreTabWriteAuthorityAfterStaleRebindsLiveLease(t *testing.T) {
	app, tab, ctrl, path := newWriteAuthorityRestoreFixture(t, "restore_fast")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	tab.adoptSessionLease(lease)

	// 第一代 authority 绑给本 controller；再让同一 lease 给一个旁路
	// controller 发新一代——本 controller 的 authority 立即 stale（即
	// 17:01 风暴的进程内形态：lease 健康、authority 落伍）。
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	side := newAuthorityTestController(t, config.SessionDir(), path, "restore_fast_side")
	defer side.Close()
	if err := control.IssueAndBindWriteAuthority(side, lease); err != nil {
		t.Fatalf("rotate generation via side controller: %v", err)
	}
	staleGen := ctrl.WriteAuthorityGeneration()

	if !app.restoreTabWriteAuthorityAfterStale(tab) {
		t.Fatal("restore failed with a live lease in hand")
	}
	if got := ctrl.WriteAuthorityGeneration(); got <= staleGen {
		t.Fatalf("generation after restore = %d, want a fresh value above %d", got, staleGen)
	}
	tab.sessionLeaseMu.Lock()
	sameLease := tab.sessionLease == lease
	tab.sessionLeaseMu.Unlock()
	if !sameLease {
		t.Fatal("fast path replaced the lease object; want an in-place rebind")
	}
}

// 接管路径：tab 手上的 lease 已死（release 后 authority 失配，17:03 的
// 残留形态），锁已空闲——restore 必须重新拿到 lease、装回 tab、并让
// controller 恢复可写。
func TestRestoreTabWriteAuthorityAfterStaleTakesOverFreeLease(t *testing.T) {
	app, tab, ctrl, path := newWriteAuthorityRestoreFixture(t, "restore_takeover")
	dead, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire dead lease: %v", err)
	}
	if err := control.IssueAndBindWriteAuthority(ctrl, dead); err != nil {
		t.Fatalf("bind from live lease: %v", err)
	}
	dead.Release()
	// 死 lease 上的 bind 必须失败（stale 形态的判据本身）。
	if err := control.IssueAndBindWriteAuthority(ctrl, dead); !errors.Is(err, agent.ErrSessionWriteAuthorityStale) {
		t.Fatalf("bind from released lease = %v, want stale", err)
	}

	if !app.restoreTabWriteAuthorityAfterStale(tab) {
		t.Fatal("restore failed with a free lock to take over")
	}
	if got := tab.sessionLeaseRuntimeKey(); got != sessionRuntimeKey(path) {
		t.Fatalf("tab lease key after restore = %q, want %q", got, sessionRuntimeKey(path))
	}
	tab.sessionLeaseMu.Lock()
	fresh := tab.sessionLease
	tab.sessionLeaseMu.Unlock()
	if fresh == nil || fresh.Released() {
		t.Fatal("tab did not adopt a live lease after the takeover")
	}
	if err := control.IssueAndBindWriteAuthority(ctrl, fresh); err != nil {
		t.Fatalf("bind from adopted lease after restore: %v", err)
	}
}

// 互斥不回退（tl 1201 验收④）：同会话被本进程另一个活 runtime 持有时，
// restore 不得接管——自愈只救 zombie，不偷活锁。
func TestRestoreTabWriteAuthorityAfterStaleRefusesLiveHolder(t *testing.T) {
	app, tab1, ctrl1, path := newWriteAuthorityRestoreFixture(t, "restore_mutex_holder")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire holder lease: %v", err)
	}
	tab1.adoptSessionLease(lease)

	// 第二个 tab 指向同一会话（同 key），authority 落伍、手上没有 lease。
	tab2 := &WorkspaceTab{ID: "restore_mutex_contender", Scope: "global"}
	app.tabs[tab2.ID] = tab2
	app.tabOrder = append(app.tabOrder, tab2.ID)
	ctrl2 := newAuthorityTestController(t, config.SessionDir(), path, "restore_mutex_contender")
	defer ctrl2.Close()
	tab2.Ctrl = ctrl2
	if err := control.IssueAndBindWriteAuthority(ctrl2, lease); err != nil {
		t.Fatalf("bind contender from holder lease: %v", err)
	}
	if err := control.IssueAndBindWriteAuthority(ctrl1, lease); err != nil {
		t.Fatalf("rebind holder to rotate generation: %v", err)
	}

	if app.restoreTabWriteAuthorityAfterStale(tab2) {
		t.Fatal("restore stole the lease from the live in-process holder")
	}
	if !agent.SessionLeaseHeldByCurrentRuntime(sessionRuntimeKey(path)) {
		t.Fatal("holder lease lost during a refused restore")
	}
	tab1.sessionLeaseMu.Lock()
	sameLease := tab1.sessionLease == lease
	tab1.sessionLeaseMu.Unlock()
	if !sameLease {
		t.Fatal("holder tab's lease object was swapped by a refused restore")
	}
	if got := tab2.sessionLeaseRuntimeKey(); got != "" {
		t.Fatalf("contender adopted lease key %q after a refused restore", got)
	}
}

// 归档/删除 busy 分类的 zombie 判据：死 pid → errSessionBusyZombie；
// 活 pid / 本进程 / 无身份 → errSessionBusyElsewhere 原样。
func TestSessionLeaseBusyClassification(t *testing.T) {
	orig := agent.ProcessAlive
	defer func() { agent.ProcessAlive = orig }()

	cases := []struct {
		name  string
		info  *agent.SessionLeaseInfo
		alive func(int) bool
		want  error
	}{
		{"dead foreign pid", &agent.SessionLeaseInfo{PID: 424242}, func(int) bool { return false }, errSessionBusyZombie},
		{"live foreign pid", &agent.SessionLeaseInfo{PID: 424242}, func(int) bool { return true }, errSessionBusyElsewhere},
		{"self pid", &agent.SessionLeaseInfo{PID: os.Getpid()}, func(int) bool { return false }, errSessionBusyElsewhere},
		{"no identity", nil, func(int) bool { return false }, errSessionBusyElsewhere},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent.ProcessAlive = tc.alive
			got := sessionLeaseBusyClassification(&agent.SessionLeaseError{Path: "p", Info: tc.info})
			if !errors.Is(got, tc.want) {
				t.Fatalf("classification = %v, want %v", got, tc.want)
			}
		})
	}
}

// 验收①：zombie（死 pid）残留不再把归档钉成「another window」——分类层
// 给出可操作的 zombie 文案，且保持 forceable（1103 语义对 stale 放宽的
// 结论：保持 forceable 通道，文案精确化）。
func TestZombieBusyErrorStaysForceable(t *testing.T) {
	marked := markTopicArchiveForceable(errSessionBusyZombie)
	var forceable *topicArchiveForceableError
	if !errors.As(marked, &forceable) {
		t.Fatalf("zombie busy error not forceable: %v", marked)
	}
	if !errors.Is(forceable.Unwrap(), errSessionBusyZombie) {
		t.Fatalf("forceable cause mismatch: %v", forceable.Unwrap())
	}
}
