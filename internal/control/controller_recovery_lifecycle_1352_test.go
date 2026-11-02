package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1352 A/B 验收：fork 副本转正与 fork 总量闸。
//
// 现场（20261031）：主会话文件 mtime 停更两天，活跃写入落在 recovery-*.jsonl
// 副本上，一晚 fork 出 6 个副本。两条修复：
//   A——fork 落地后副本整体 rename 回主文件（主文件字节先白名单归档），
//      活跃写入回到主文件（mtime 跟进）；
//   B——每个 logical topic 现存副本数达 2 即拒绝再 fork，强制 adopt 盘上
//      转录并 fail-loud notice（副本风暴的硬上限）。

func replyChunks1352(text string) []provider.Chunk {
	return []provider.Chunk{{Type: provider.ChunkText, Text: text}, {Type: provider.ChunkDone}}
}

// divergedControllers1352 reproduces the 20261031 field shape: two controllers
// on one session file, both append their own turn, so the first controller's
// snapshot save hits a genuine divergence (neither side is a prefix of the
// other) and enters recoverSnapshotConflict.
func divergedControllers1352(t *testing.T, dir, name string, sink event.Sink) (*Controller, *Controller, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	const systemPrompt = "SYS"

	provA := &recordingProvider{streams: [][]provider.Chunk{replyChunks1352("a1"), replyChunks1352("a2")}}
	execA := agent.New(provA, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlA := New(Options{Runner: execA, Executor: execA, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "a", Sink: sink})
	if err := ctrlA.RunTurn(context.Background(), "first from A"); err != nil {
		t.Fatalf("A first turn: %v", err)
	}
	if err := ctrlA.Snapshot(); err != nil {
		t.Fatalf("A snapshot 1: %v", err)
	}

	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	provB := &recordingProvider{streams: [][]provider.Chunk{replyChunks1352("b2")}}
	execB := agent.New(provB, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlB := New(Options{Runner: execB, Executor: execB, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "b", Sink: sink})
	ctrlB.Resume(loaded, path)
	if err := ctrlB.RunTurn(context.Background(), "second from B"); err != nil {
		t.Fatalf("B second turn: %v", err)
	}

	// A 的内存转录已与盘上（B 写入的）真分歧：互相都不是对方前缀。
	if err := ctrlA.RunTurn(context.Background(), "second from A"); err != nil {
		t.Fatalf("A second turn: %v", err)
	}
	if err := ctrlA.Snapshot(); err != nil {
		t.Fatalf("A snapshot 2 (conflict expected and handled inside): %v", err)
	}
	return ctrlA, ctrlB, path
}

func recoveryCopyCount1352(t *testing.T, path string) int {
	t.Helper()
	return agent.RecoveryForkCountForTopic(path)
}

func Test1352ForkPromotesBackToMainFile(t *testing.T) {
	dir := t.TempDir()
	ctrlA, _, path := divergedControllers1352(t, dir, "shared.jsonl", event.Discard)

	// A0 fork 先按既有契约隔离：controller 在副本上、副本存在（延迟转正未结算）。
	if got := ctrlA.SessionPath(); got == path {
		t.Fatalf("fork must isolate first: controller still on the main file before any quiet-main save cycle")
	}
	if n := recoveryCopyCount1352(t, path); n != 1 {
		t.Fatalf("recovery copies right after the fork = %d, want 1", n)
	}

	// A① 延迟转正结算：主文件自 fork 起安静，保存周期内收敛回主文件
	// （副本先落盘的中间周期允许存在——转正门槛要求内存与副本字节逐字稳定）。
	// SnapshotDurable 绕过 recently-durable 合并闸（生产上回合收尾的
	// SnapshotActivity 同样不过闸，转正在回合边界结算）。
	for range 4 {
		if err := ctrlA.SnapshotDurable(); err != nil {
			t.Fatalf("post-fork snapshot: %v", err)
		}
		if ctrlA.SessionPath() == path {
			break
		}
	}
	if got := ctrlA.SessionPath(); got != path {
		t.Fatalf("controller path = %s, want the main file after deferred promotion settled on the quiet main file", got)
	}
	// A② 副本消失（整体 rename 回主文件名）。
	if n := recoveryCopyCount1352(t, path); n != 0 {
		t.Fatalf("recovery copies left after promotion = %d, want 0 (the copy renames onto the main file)", n)
	}
	// A③ 被逐下的盘上字节零丢失：白名单归档目录在，且里面的主转录是 B 的
	// 内容（分歧的另一方）。
	backupDirs, err := filepath.Glob(filepath.Join(dir, "shared.pre-repair-*"))
	if err != nil || len(backupDirs) == 0 {
		t.Fatalf("archived backup of the displaced main bytes expected: %v err=%v", backupDirs, err)
	}
	archivedMain, err := os.ReadFile(filepath.Join(backupDirs[0], "shared.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archivedMain), "b2") {
		t.Fatal("archived main transcript must hold the displaced foreign content (B's turn)")
	}
	// A④ 主文件内容 = A 的转录（副本转正而来）。
	disk, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("reload main after promotion: %v", err)
	}
	diskText := requestMessagesText(disk.Snapshot())
	if !strings.Contains(diskText, "second from A") || !strings.Contains(diskText, "a2") {
		t.Fatalf("main file must hold the promoted transcript: %q", diskText)
	}

	// A⑤ 验收主腿：收敛之后主文件 mtime 跟进——活跃写入持续落在主文件上。
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // Windows mtime 分辨率兜底
	if err := ctrlA.RunTurn(context.Background(), "fourth from A"); err != nil {
		t.Fatalf("A fourth turn: %v", err)
	}
	if err := ctrlA.SnapshotDurable(); err != nil {
		t.Fatalf("A snapshot after convergence: %v", err)
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !infoAfter.ModTime().After(infoBefore.ModTime()) {
		t.Fatalf("main file mtime must advance after post-convergence saves (before=%v after=%v) — active writes must live on the main file",
			infoBefore.ModTime(), infoAfter.ModTime())
	}
	if n := recoveryCopyCount1352(t, path); n != 0 {
		t.Fatalf("post-convergence saves must not create new recovery copies: %d found", n)
	}
	disk, err = agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requestMessagesText(disk.Snapshot()), "fourth from A") {
		t.Fatalf("post-convergence turn must persist on the main file: %q", requestMessagesText(disk.Snapshot()))
	}
}

func Test1352ForkQuotaAdoptsDiskAtLimit(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var notices []event.Event
	sink := event.FuncSink(func(ev event.Event) {
		mu.Lock()
		defer mu.Unlock()
		notices = append(notices, ev)
	})

	path := filepath.Join(dir, "shared.jsonl")
	const systemPrompt = "SYS"
	provA := &recordingProvider{streams: [][]provider.Chunk{replyChunks1352("a1"), replyChunks1352("a2")}}
	execA := agent.New(provA, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlA := New(Options{Runner: execA, Executor: execA, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "a", Sink: sink})
	if err := ctrlA.RunTurn(context.Background(), "first from A"); err != nil {
		t.Fatalf("A first turn: %v", err)
	}
	if err := ctrlA.Snapshot(); err != nil {
		t.Fatalf("A snapshot 1: %v", err)
	}

	// 两个已存副本：该 topic 的 fork 配额已满（≤2）。
	for _, lane := range []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
		cp := agent.NewSession(systemPrompt)
		cp.Add(provider.Message{Role: provider.RoleUser, Content: "old copy " + lane})
		copyPath := filepath.Join(dir, "shared-recovery-"+lane+".jsonl")
		if err := cp.Save(copyPath); err != nil {
			t.Fatalf("craft recovery copy %s: %v", lane, err)
		}
	}
	if n := agent.RecoveryForkCountForTopic(path); n != 2 {
		t.Fatalf("fixture: recovery copy count = %d, want 2", n)
	}

	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	provB := &recordingProvider{streams: [][]provider.Chunk{replyChunks1352("b2")}}
	execB := agent.New(provB, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlB := New(Options{Runner: execB, Executor: execB, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "b", Sink: event.Discard})
	ctrlB.Resume(loaded, path)
	if err := ctrlB.RunTurn(context.Background(), "second from B"); err != nil {
		t.Fatalf("B second turn: %v", err)
	}
	if err := ctrlA.RunTurn(context.Background(), "second from A"); err != nil {
		t.Fatalf("A second turn: %v", err)
	}
	// 配额已满：不得 fork，强制 adopt 盘上转录（fail-loud notice）。
	if err := ctrlA.Snapshot(); err != nil {
		t.Fatalf("A snapshot 2: %v", err)
	}

	if got := ctrlA.SessionPath(); got != path {
		t.Fatalf("controller path after quota adopt = %s, want the main file", got)
	}
	if n := recoveryCopyCount1352(t, path); n != 2 {
		t.Fatalf("quota must prevent a third copy: %d recovery copies found", n)
	}
	// adopt 语义：executor 与盘上转录一致（B 的内容被采纳，A 的本地改动按
	// 任务裁决丢弃，fail-loud notice 承担知情义务）。
	disk, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	got := requestMessagesText(ctrlA.executor.Session().Snapshot())
	want := requestMessagesText(disk.Snapshot())
	if got != want {
		t.Fatalf("executor must adopt the disk transcript after quota exhaustion:\n got=%q\nwant=%q", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	failLoud := false
	for _, ev := range notices {
		if ev.Code == event.NoticeCodeSessionRecoveryAdopted && strings.Contains(ev.Text, "recovery-copy limit") {
			failLoud = true
		}
	}
	if !failLoud {
		t.Fatalf("quota adoption must emit the fail-loud notice: %+v", notices)
	}
}

func Test1352PromotionDeclinesWhenMainLockBusy(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "busy.jsonl")
	base := agent.NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "main content"})
	if err := base.Save(mainPath); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "busy-recovery-0123456789abcdef.jsonl")
	cp := agent.NewSession("sys")
	cp.Add(provider.Message{Role: provider.RoleUser, Content: "copy content"})
	if err := cp.Save(copyPath); err != nil {
		t.Fatal(err)
	}

	release := agent.HoldSessionSavePathForTest(mainPath)
	defer release()
	if _, err := agent.PromoteRecoveryBranchToMain(copyPath, mainPath); err == nil {
		t.Fatal("promotion must decline while the main save-path lock is busy")
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("declined promotion must leave the copy in place: %v", err)
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("declined promotion must leave the main file in place: %v", err)
	}
}
