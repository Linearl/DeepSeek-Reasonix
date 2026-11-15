package main

// tl1606 断言② 渲染「新消息必渲染」钉（tl1601 形态前置化）：
//
// 回合收尾窗口（TurnDone 扇出未闭、finishing=true）内提交的消息，在自己的
// turn_done 之后必须完整落地五点合同——
//   1. 台账关联：TurnIDForSubmission(submissionID) 非空（受理令未丢）；
//   2. events 在册：turns.jsonl envelope 携带该 submissionId；
//   3. 转录在册：会话 jsonl 落盘含该消息原文；
//   4. UI 可见（无切 tab）：生产历史转换渲染出该消息行；
//   5. 复用面可见：HistorySliceForTab（tab 复用路径的读面）含该消息行。
//
// 桌面受理链的生产等待语义（turn_admission.go：finishing 窗口内
// TurnFinishingDone 挡闸、窗口闭合后重试受理）是本断言的前提被测物；
// 投影/渲染层修复本体在 wt-tl1600-1601（撞面申报：本文件零触碰其面）。
//
// 反例：真被拒的提交（genuinely running，无 finishing 边界 → ErrTurnRunning）
// 不得留下任何幽灵——无台账关联、无 envelope、转录/UI/复用面皆无该行。

import (
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
)

// tl1606AlwaysRunningController forces RuntimeStatus().Running with no
// finishing boundary — the genuinely-running rejection premise (own copy, no
// dependence on other files' wrappers).
type tl1606AlwaysRunningController struct {
	control.SessionAPI
}

func (c *tl1606AlwaysRunningController) RuntimeStatus() control.RuntimeStatus {
	return control.RuntimeStatus{Running: true, Cancellable: true}
}

func (c *tl1606AlwaysRunningController) TurnFinishingDone() (<-chan struct{}, bool) {
	return nil, false
}

// 正例：收尾窗口提交 → 窗口闭合后受理 → 自己的 turn_done 后五点合同全在。
func TestTl1606FinishingWindowSubmissionRendersAfterTurnDone(t *testing.T) {
	dir := t.TempDir()
	path := newTl1606Session(t, dir, "tl1606 ②种子用户输入", "tl1606 种子回复")
	h := attachTl1606TabSinkHarness(t, dir, path, []tl1606Round{
		{text: "tl1606 预热回合答案"},
		{text: "tl1606 收尾窗口轮答案"},
	})
	// 把 TurnDone 扇出扣在 tab sink 的 bot 转发点（生产同款窗口语义：
	// tabEventSink 清受理令发生在 bot 转发返回之后）。
	gate := &tl1606TurnDoneGate{entered: make(chan struct{}), release: make(chan struct{})}
	h.sink.SetBotSink(gate)

	const (
		finText = "tl1606 收尾窗口提交原文"
		finSub  = "u-tl1606-fin"
	)
	// 第一回合：预热并进入 TurnDone 扇出（finishing 窗口打开）。
	if err := h.app.SubmitToTabWithID(h.tab.ID, "tl1606 预热消息", "u-tl1606-warmup"); err != nil {
		t.Fatalf("warmup submit: %v", err)
	}
	gate.waitEntered(t)
	if st := h.ctrl.RuntimeStatus(); !st.Finishing {
		t.Fatalf("premise: finishing window must be observable, status = %+v", st)
	}

	// 窗口内提交：受理链必须挂起等待（不得提前受理、不得被弹回）。
	submitted := make(chan error, 1)
	go func() { submitted <- h.app.SubmitToTabWithID(h.tab.ID, finText, finSub) }()
	select {
	case err := <-submitted:
		t.Fatalf("submission must wait inside the finishing window, returned early: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	// 窗口闭合：扇出放行 → 受理重试 → 第二回合跑完自己的 turn_done。
	gate.open()
	select {
	case err := <-submitted:
		if err != nil {
			t.Fatalf("finishing-window submission must be admitted after the window closes: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("finishing-window submission never returned")
	}
	h.waitForTurns(2)
	h.waitIdle()

	// 合同1：台账关联——受理令必须换到回合身份。
	turnID := h.ctrl.TurnIDForSubmission(finSub)
	if turnID == "" {
		t.Fatal("admitted submission must correlate to a turn id in the ledger")
	}
	// 合同2：events 在册——turns.jsonl envelope 携带该 submissionId。
	found := false
	envs, err := h.ctrl.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("turn events: %v", err)
	}
	for _, e := range envs {
		if e.SubmissionID == finSub {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("turn ledger envelopes must carry the submission %q", finSub)
	}
	// 合同3/4/5：转录落盘 + UI 转换（无切 tab）+ 复用读面。
	h.snapshotToDisk(finText)
	rows := h.uiRows()
	hitUI := false
	for _, row := range rows {
		if strings.Contains(row.Content, finText) {
			hitUI = true
			break
		}
	}
	if !hitUI {
		t.Fatalf("tl1601 nail: the finishing-window message must render via the history conversion without a tab switch, rows = %d", len(rows))
	}
	slice := h.sliceEntries()
	hitSlice := false
	for _, entry := range slice.Entries {
		if strings.Contains(entry.Message.Content, finText) {
			hitSlice = true
			break
		}
	}
	if !hitSlice {
		t.Fatalf("tl1601 nail: the reuse-path slice must surface the finishing-window message (source=%s, entries=%d)", slice.Source, len(slice.Entries))
	}
}

// 反例：genuinely running（无 finishing 边界）下被拒的提交不得留幽灵——
// 无台账关联、无 envelope、转录/UI/复用面皆无该行。
func TestTl1606RejectedSubmissionLeavesNoGhost(t *testing.T) {
	dir := t.TempDir()
	path := newTl1606Session(t, dir, "tl1606 反例种子用户输入", "tl1606 种子回复")
	events := &tl1606EventLog{}
	h := attachTl1606HarnessWithSink(t, dir, path, []tl1606Round{
		{text: "tl1606 反例回合答案"},
	}, events)
	h.events = events
	// tab 换上恒 Running 包装（受理链看到的是真在跑、无收尾边界）。
	h.tab.Ctrl = &tl1606AlwaysRunningController{SessionAPI: h.ctrl}

	const (
		ghostText = "tl1606 应被拒绝的幽灵原文"
		ghostSub  = "u-tl1606-ghost"
	)
	err := h.app.SubmitToTabWithID(h.tab.ID, ghostText, ghostSub)
	if !errors.Is(err, control.ErrTurnRunning) {
		t.Fatalf("genuinely running turn must reject the submit with ErrTurnRunning, got %v", err)
	}

	// 幽灵合同：四处皆无。
	if h.ctrl.TurnIDForSubmission(ghostSub) != "" {
		t.Fatal("a rejected submission must not correlate to any turn")
	}
	envs, err := h.ctrl.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("turn events: %v", err)
	}
	for _, e := range envs {
		if e.SubmissionID == ghostSub {
			t.Fatal("a rejected submission must leave no ledger envelope")
		}
	}
	if rows := h.uiRows(); len(rows) > 0 {
		for _, row := range rows {
			if strings.Contains(row.Content, ghostText) {
				t.Fatal("a rejected submission must not appear in the history conversion")
			}
		}
	}
	slice := h.sliceEntries()
	for _, entry := range slice.Entries {
		if strings.Contains(entry.Message.Content, ghostText) {
			t.Fatal("a rejected submission must not appear in the reuse-path slice")
		}
	}
	if err := h.ctrl.Snapshot(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if body := h.readSessionJSONL(); strings.Contains(body, ghostText) {
		t.Fatal("a rejected submission must leave nothing in the durable transcript")
	}
}
