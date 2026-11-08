package control

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1500 刀③（装机全局挂死修复件）行为验收钉：启动恢复 in_flight_turn 的
// age 上限。
//
// 诊断报告（worktrees/tl1493/docs/reports-diag/装机挂死诊断-20261108.md）§2.2：
// 失控回合的 in_flight_turn 标记持久化在 meta 里，每次启动加载会话都重新触发
// 「裁剪+快照」恢复机器并撞保存锁面（「重启无效」）。钉两态：
//   - 超龄（3h 旧标记，>2h 上限，远超单回合正常时长）：终止态收尾——标记
//     CAS 清掉、转录原样保留（不裁剪、不转 LocalOnly、不做启动期快照）、
//     落带 cause=stale_in_flight_turn_recovered 的可见 notice。宁可不续不挂死。
//     修前红：恢复机器照跑，转录被裁剪/转换（len 变化或出现 LocalOnly）。
//   - 新鲜（对照钉）：既有恢复机器照常运行（裁剪未决尾部、消息转 LocalOnly、
//     清标记），字节级不回归。
//
// 本文件只引用既有 API（cause 字面量内联在断言里），修前可编译——红是行为红。
func TestStaleInFlightTurnTerminatedInPlaceAtOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "runaway turn prompt"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "partial tool-call tail"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	sink, _, seen := collectSink()
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "test", Sink: sink})

	marker := agent.InFlightTurnMeta{
		ID:                "stale-marker",
		StartMessageIndex: 1,
		PreserveUser:      true,
		StartedAt:         time.Now().Add(-3 * time.Hour),
	}
	if err := agent.SetSessionInFlightTurn(path, marker); err != nil {
		t.Fatalf("SetSessionInFlightTurn: %v", err)
	}
	before := sess.Len()

	c.recoverInterruptedTurn(path)

	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	if meta.InFlightTurn != nil {
		t.Fatalf("stale marker survived the age-capped recovery: %+v", meta.InFlightTurn)
	}
	if got := sess.Len(); got != before {
		t.Fatalf("stale marker must be terminated in place WITHOUT the recovery strip: transcript len %d → %d", before, got)
	}
	for _, m := range sess.Snapshot() {
		if m.LocalOnly {
			t.Fatal("stale marker must not convert any message to LocalOnly (recovery machinery must not run)")
		}
	}
	foundNotice := false
	for _, ev := range *seen {
		if ev.Kind == event.Notice && strings.Contains(ev.Text, "stale_in_flight_turn_recovered") {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatalf("no visible notice carrying cause=stale_in_flight_turn_recovered; events=%+v", *seen)
	}
}

// TestFreshInFlightTurnStillRecovered 是刀③对照钉：新鲜标记走既有恢复机器。
func TestFreshInFlightTurnStillRecovered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "fresh interrupted turn"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "partial tail"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "test", Sink: event.Discard})

	// Begin 戳 StartedAt=now，即「新鲜」；超龄态由上一钉用 -3h 覆盖。
	if _, err := agent.BeginSessionInFlightTurn(path, 1, true); err != nil {
		t.Fatalf("BeginSessionInFlightTurn: %v", err)
	}

	c.recoverInterruptedTurn(path)

	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	if meta.InFlightTurn != nil {
		t.Fatalf("fresh marker must still be recovered (cleared), got %+v", meta.InFlightTurn)
	}
	converted := false
	for _, m := range sess.Snapshot() {
		if m.LocalOnly {
			converted = true
		}
	}
	if !converted {
		t.Fatal("fresh marker recovery must run the existing strip machinery (partial tail converted to LocalOnly)")
	}
}
