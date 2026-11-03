package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestConcurrentWriterLocalRaceForksWithoutBlamingAnotherWindow（原
// TestConcurrentWriterLocalRaceStaysSilent）改述：task 203 的「不喊狼来了」
// 半边。原契约（进程内竞态零通知）钉在 schema-2 head 事件机制上，tl1280 退役
// 后失效——保存冲突的 conflict-copy 机制（tl1352 家族）对输家一律 fork 副本
// 并如实告知操作者，进程内与跨进程不再区别对待（probe 已不参与分类）。task
// 203 要防的误报在 newText 里结构性消除：通知只陈述「盘上会话已变化、未保存
// 转录已存为冲突副本」，绝不指认「另一个窗口/进程」。
func TestConcurrentWriterLocalRaceForksWithoutBlamingAnotherWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.jsonl")
	const systemPrompt = "SYS"
	reply := [][]provider.Chunk{{{Type: provider.ChunkText, Text: "ok"}, {Type: provider.ChunkDone}}}
	sinkA, sinkB := &noticeSink{}, &noticeSink{}
	execA := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlA := New(Options{Runner: execA, Executor: execA, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "a", Sink: sinkA})
	if err := ctrlA.RunTurn(context.Background(), "first from A"); err != nil {
		t.Fatal(err)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	execB := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlB := New(Options{Runner: execB, Executor: execB, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "b", Sink: sinkB})
	ctrlB.Resume(loaded, path)
	if err := ctrlA.RunTurn(context.Background(), "second from A"); err != nil {
		t.Fatal(err)
	}
	if err := ctrlB.RunTurn(context.Background(), "second from B"); err != nil {
		t.Fatal(err)
	}
	// B's fork notice is factual, not a phantom-window accusation.
	notice, ok := sinkB.lastNotice()
	if !ok || notice.Code != event.NoticeCodeSessionRecoveryForked {
		t.Fatalf("B notice = %+v ok=%v, want the conflict-copy recovery notice", notice, ok)
	}
	blamed := strings.ToLower(notice.Text)
	if strings.Contains(blamed, "another window") || strings.Contains(blamed, "another process") || strings.Contains(blamed, "other window") {
		t.Fatalf("in-process race must not be blamed on another window/process, got %q", notice.Text)
	}
	if notice, ok := sinkA.lastNotice(); ok {
		t.Fatalf("the other in-process writer must stay silent too, got notice %+v", notice)
	}
}
