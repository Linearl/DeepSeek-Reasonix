package control

import (
	"context"
	"errors"
	"os"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1723 压缩持久化三链统一钉：手动家族四个入口标签（ui/slash/serve/manual）
// 经同一收口 CompactForTrigger 成功后，主 session 文件与投影 sidecar 都立即
// 落盘，且读回形态逐标签一致。收口前 ui（compact now 按钮）与冷缓存 live 链
// 没有任何即时落盘——压缩结果只活在内存，进程退出靠 shutdown 快照兜底；
// slash 显式 SnapshotRewrite、serve 显式 Snapshot 三链三层不齐。本文件钉
// 收口后的不变量：入口不再各自带持久化尾巴，CompactForTrigger 是唯一落盘
// 保障点。
//
// 前置事实钉（unified pin 的 pre-compact 断言）：fresh controller 从未保存
// 过 ⇒ 收口动作确实来自压缩本身，而非某个后台 tick。session path 由调用方
// 生成一次传入（NewSessionPath 每次调用带纳秒戳，两侧必须同一路径）。

// tl1723Fixture wires the same foldable stack compactFixture uses, plus an
// explicit session path on both sides (controller = transcript file, executor
// = projection sidecar) so persistence can be asserted on disk.
func tl1723Fixture(t *testing.T, dir, sessionPath string) *Controller {
	t.Helper()
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("tl1723 digest")}}
	exec := agent.New(prov, tool.NewRegistry(), bulkHistorySession("sys"), agent.Options{
		SessionPath:   sessionPath,
		ContextWindow: 10_000, CompactRatio: 0.8, RecentKeep: 2,
	}, event.Discard)
	return New(Options{
		Executor:    exec,
		SessionPath: sessionPath,
		SessionDir:  dir,
		Label:       "tl1723",
	})
}

// tl1723TranscriptShape reads the persisted transcript back and returns the
// (role, content) pairs — the storage shape the parity pin compares.
func tl1723TranscriptShape(t *testing.T, path string) [][2]string {
	t.Helper()
	sess, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession(%q): %v", path, err)
	}
	msgs := sess.Snapshot()
	shape := make([][2]string, 0, len(msgs))
	for _, m := range msgs {
		shape = append(shape, [2]string{string(m.Role), m.Content})
	}
	return shape
}

// TestTl1723CompactPersistUnifiedPin pins the unified closure: every
// manual-family entry label compacts and immediately finds both the
// transcript file and the projection sidecar on disk, with identical
// transcript shapes across labels (same core + same closure ⇒ no
// entry-private persistence).
func TestTl1723CompactPersistUnifiedPin(t *testing.T) {
	labels := []string{agent.CompactionTriggerUI, agent.CompactionTriggerSlash, agent.CompactionTriggerServe, agent.CompactionTriggerManual}
	shapes := map[string][][2]string{}
	for _, label := range labels {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			sessionPath := agent.NewSessionPath(dir, "tl1723")
			if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
				t.Fatalf("pre-compact transcript must not exist (fresh session, got stat err=%v)", err)
			}
			c := tl1723Fixture(t, dir, sessionPath)
			if err := c.CompactForTrigger(context.Background(), "", label); err != nil {
				t.Fatalf("CompactForTrigger(%q): %v", label, err)
			}
			// 即时落盘钉：压缩返回即有主文件——收口前 ui/manual 链在此处为空。
			if _, err := os.Stat(sessionPath); err != nil {
				t.Fatalf("post-compact transcript missing after %q compact: %v", label, err)
			}
			// 投影 sidecar 钉：折叠结果必须在盘上（commit 内即落，但仅在
			// executor 绑定 path 时——fixture 两侧同 path）。
			if _, err := os.Stat(agent.ContextStatePath(sessionPath)); err != nil {
				t.Fatalf("post-compact projection sidecar missing after %q compact: %v", label, err)
			}
			shapes[label] = tl1723TranscriptShape(t, sessionPath)
			if len(shapes[label]) == 0 {
				t.Fatalf("persisted transcript after %q compact is empty", label)
			}
		})
	}
	base := shapes[agent.CompactionTriggerManual]
	for _, label := range []string{agent.CompactionTriggerUI, agent.CompactionTriggerSlash, agent.CompactionTriggerServe} {
		if len(shapes[label]) != len(base) {
			t.Fatalf("entry %q persisted %d messages, manual persisted %d — persistence shape divergence", label, len(shapes[label]), len(base))
		}
		for i := range base {
			if shapes[label][i] != base[i] {
				t.Fatalf("entry %q message %d = %+v, manual = %+v — persistence shape divergence", label, i, shapes[label][i], base[i])
			}
		}
	}
}

// TestTl1723CompactFailsWithoutPersistenceTail pins the failure contract: a
// failed compaction must not leave the closure writing anything (the fold did
// not happen, so there is nothing to persist). The provider errors every
// summary request.
func TestTl1723CompactFailsWithoutPersistenceTail(t *testing.T) {
	dir := t.TempDir()
	sessionPath := agent.NewSessionPath(dir, "tl1723")
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		{{Type: provider.ChunkError, Err: errCompactFails}},
	}}
	exec := agent.New(prov, tool.NewRegistry(), bulkHistorySession("sys"), agent.Options{
		SessionPath:   sessionPath,
		ContextWindow: 10_000, CompactRatio: 0.8, RecentKeep: 2,
	}, event.Discard)
	c := New(Options{Executor: exec, SessionPath: sessionPath, SessionDir: dir, Label: "tl1723"})
	if err := c.CompactForTrigger(context.Background(), "", agent.CompactionTriggerUI); err == nil {
		t.Fatal("CompactForTrigger with a failing provider must return an error")
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("failed compaction must not persist (stat err=%v)", err)
	}
}

// errCompactFails is the summary-request failure used above; declared at file
// scope to keep the chunk literal a constant shape.
var errCompactFails = errors.New("tl1723 scripted summary failure")
