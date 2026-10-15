package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// 任务 907（上游 8859 / PR11085 语义对照）：final-readiness 失败结局必须把
// 侧栏带回 idle——topic 行渲染 awaiting_delivery 独立徽标，而不是上游旧码
// `running := status != ""` 那样永久 running。上游 PR 正文说明 running 半在
// 1.x 已由 controller active work 推导（本测试即 fork 侧该语义的回归锁）；
// PR11085 的代码改动只修提示音（wt-907 前端切片承担）。
func TestFinalReadinessTurnEndsSidebarIdleAwaitingDelivery(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	app.ctx = ctx
	snapshots := make(chan ProjectTreeRuntimeSnapshot, 64)
	app.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if name == "project-tree:runtime-changed" {
			snapshots <- payload[0].(ProjectTreeRuntimeSnapshot)
		}
	}
	sink := &tabEventSink{tabID: "readiness-907", app: app}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	releaseRunner := sync.OnceFunc(func() { close(runner.release) })
	sessionDir := t.TempDir()
	sessionPath := writeTopicSessionWithPrompt(t, sessionDir, "readiness-907.jsonl", "readiness-907-topic", "", "", "finish the delivery-checked turn", time.Now())
	ctrl := control.New(control.Options{
		Runner: runner, SessionDir: sessionDir, SessionPath: sessionPath, Label: "readiness 907", Sink: sink,
	})
	defer ctrl.Close()
	defer releaseRunner()
	tab := &WorkspaceTab{
		ID: sink.tabID, Scope: "global", TopicID: "readiness-907-topic",
		SessionPath: sessionPath,
		Ctrl:        ctrl, Ready: true, sink: sink, disabledMCP: map[string]ServerView{},
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	// Run a real turn to completion so the controller's runtime-state lane is
	// live; its own clean TurnDone must not mask the readiness outcome below.
	ctrl.Submit("finish the delivery-checked turn")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	releaseRunner()
	waitNotRunning(t, ctrl)

	// The controller reports final-readiness failure exactly once at turn end
	// (internal/agent FinalReadinessError → done.Err, Outcome=final_readiness).
	// Drive the same event through the tab sink: the sink records the
	// awaiting_delivery activity status, the same way the live turn does.
	sink.Emit(event.Event{
		Kind:    event.TurnDone,
		Err:     &agent.FinalReadinessError{Attempts: 3, Reason: "missing verification"},
		Outcome: event.TurnOutcomeFinalReadiness,
	})
	if tab.ActivityStatus != topicStatusAwaitingDelivery {
		t.Fatalf("ActivityStatus = %q, want %q", tab.ActivityStatus, topicStatusAwaitingDelivery)
	}

	// 侧栏投影：topic 行 running=false 且渲染 awaiting_delivery 徽标。
	// 上游缺陷（旧 session_catalog_runtime.go:230-231 running := status != ""）
	// 会让这里 running 永久为 true。
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		snapshot := app.GetProjectTreeRuntimeSnapshot()
		if len(snapshot.Topics) == 1 {
			node := snapshot.Topics[0].Node
			if node.Status == topicStatusAwaitingDelivery && !node.Running {
				status, running := catalogControllerStatus(ctrl, tab.ActivityStatus)
				if running {
					t.Fatalf("idle controller with awaiting_delivery projects running=true (status=%q)", status)
				}
				if status != topicStatusAwaitingDelivery {
					t.Fatalf("catalog status = %q, want %q", status, topicStatusAwaitingDelivery)
				}
				return
			}
			if node.Running {
				t.Fatalf("readiness-failed topic still running in the sidebar projection: %+v", node)
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("sidebar never settled on idle+awaiting_delivery: %+v", snapshot.Topics)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// schema1（controller snapshot）与 schema0（legacy/无 reader）两条推导路径
// 在「控制器 idle + awaiting_delivery」输入下都不得把行标成 running。
func TestCatalogStateStatusIdleReadinessNotRunning(t *testing.T) {
	status, running := catalogStateStatus(event.RuntimeStateSnapshot{SchemaVersion: 1, Phase: "idle"}, topicStatusAwaitingDelivery)
	if running {
		t.Fatalf("schema1 idle+awaiting_delivery running=true (status=%q)", status)
	}
	if status != topicStatusAwaitingDelivery {
		t.Fatalf("schema1 status = %q, want %q", status, topicStatusAwaitingDelivery)
	}
}
