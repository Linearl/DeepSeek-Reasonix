package main

// tl1643 复现钉+修复钉一体：重启注入（auto-resume）消息的 wire 渲染通道，
// 同时是 tl1606②「新消息必渲染」断言库的 auto-resume 注入扩形态。
//
// 现象：装机重启后 host 注入消息（"The app updated and restarted itself..."）
// 首次不渲染、切 tab 才出。断点（tl1643 排查结论）：resume 提交链
// （restartResumeSubmit → SubmitToTab → ctrl.SubmitDisplay）只落盘、从不发射
// event.UserInput —— 前端 live reducer 的唯一非 composer 用户行通道
// （useController case "user_input"，任务580）因此收不到任何通报；首次
// hydrate 读盘又发生在注入落盘之前（tl1366 维护等待可把注入拖后到分钟级），
// 行只在下一次历史重读（切 tab）才出现。
//
// 五点合同（tl1606② harness 复用，注入形态扩入断言库）+ wire 面第⑥点：
//   1. 台账关联：TurnIDForSubmission 在册（受理令未丢）；
//   2. events 在册：turns.jsonl envelope 在册；
//   3. 转录落盘：会话 jsonl 含注入原文；
//   4. UI 转换可见：生产历史转换渲染出该行（无切 tab）；
//   5. 复用读面：HistorySliceForTab 含该行；
//   6. wire 面可见：tab sink 发射 user_input 载荷携带注入原文 —— live
//      reducer 的唯一渲染通道，修复前恒缺失（本钉的复现形态）。
//
// 撞面申报：roster/goal 两路 resume 链（autonomous_update_resume.go /
// tabs.go maybeResumeAutopilotTab）是 wt-tl1642 的时间戳写入侧之外的另一段
// ——本文件只断言渲染通报面，不触时间戳/面板覆盖；tl1606 断言文件本体
// 零触碰（harness 复用，同包引用）。

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/eventwire"
)

// tl1643WireLog records the wire payloads the tab sink emitted — the exact
// surface the frontend live reducer consumes.
type tl1643WireLog struct {
	mu   sync.Mutex
	hits []eventwire.Event
}

func (l *tl1643WireLog) emit(_ context.Context, _ string, payload ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range payload {
		if w, ok := p.(wireEventTab); ok {
			l.hits = append(l.hits, w.Event)
		}
	}
}

// sawUserInput reports whether a user_input wire event carrying textNeedle
// was emitted.
func (l *tl1643WireLog) sawUserInput(textNeedle string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.hits {
		if e.Kind == "user_input" && strings.Contains(e.Text, textNeedle) {
			return true
		}
	}
	return false
}

// waitWireUserInput polls until the wire face carries the needle (the async
// runtime emitter dispatches on its own goroutine).
func (l *tl1643WireLog) waitWireUserInput(t *testing.T, textNeedle string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if l.sawUserInput(textNeedle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("wire face never carried user_input with %q (live reducer render channel dead — tl1643 断点形态)", textNeedle)
}

// attachTl1643WireRecorder swaps the harness tab sink's no-op frontend emit
// for the recorder.
func attachTl1643WireRecorder(h *tl1606Harness) *tl1643WireLog {
	log := &tl1643WireLog{}
	h.sink.runtimeEvents.emit = log.emit
	return log
}

// shrinkResumeLivenessWindows tightens the tl1366 delivery knobs so the
// delivery-confirmed exit runs in milliseconds; production defaults restored
// via t.Cleanup.
func shrinkResumeLivenessWindows(t *testing.T) {
	t.Helper()
	oldWin, oldPoll := resumeLivenessWindow, resumeLivenessPoll
	resumeLivenessWindow = 10 * time.Second
	resumeLivenessPoll = 20 * time.Millisecond
	t.Cleanup(func() {
		resumeLivenessWindow, resumeLivenessPoll = oldWin, oldPoll
	})
}

// tl1643五点合同+wire 面（roster 路径）：auto-resume 注入形态。
func TestTl1643RosterResumeInjectionRendersWithoutTabSwitch(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkResumeLivenessWindows(t)
	dir := t.TempDir()
	path := newTl1606Session(t, dir, "tl1643 roster 种子用户输入", "tl1643 种子回复")
	h := attachTl1606TabSinkHarness(t, dir, path, []tl1606Round{{text: "tl1643 roster 注入回合答案"}})
	wire := attachTl1643WireRecorder(h)

	// 装机重启形态：marker 在册，恢复点消费它并注入 continue 提示。
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{
		{Path: path, StagedAt: time.Now().Unix()},
	}}); err != nil {
		t.Fatal(err)
	}
	h.app.maybeResumeAutonomousUpdateTab(h.tab)

	h.waitForTurns(1)
	h.waitIdle()

	// 合同6（本钉本体，修复前红）：wire 面必带 user_input 注入原文。
	wire.waitWireUserInput(t, autonomousUpdateResumePrompt)

	// 合同1+2：受理令换到回合身份 + 台账在册。
	envs, err := h.ctrl.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("turn events: %v", err)
	}
	if len(envs) == 0 {
		t.Fatal("resume turn must leave a ledger envelope")
	}

	// 合同3：转录落盘（落盘链在案——修复前就通，钉住不再断）。
	h.snapshotToDisk(autonomousUpdateResumePrompt)

	// 合同4：UI 转换可见（无切 tab）。
	rows := h.uiRows()
	hitUI := false
	for _, row := range rows {
		if strings.Contains(row.Content, autonomousUpdateResumePrompt) {
			hitUI = true
			break
		}
	}
	if !hitUI {
		t.Fatalf("injected resume prompt must render via the history conversion without a tab switch, rows = %d", len(rows))
	}

	// 合同5：复用读面可见。
	slice := h.sliceEntries()
	hitSlice := false
	for _, entry := range slice.Entries {
		if strings.Contains(entry.Message.Content, autonomousUpdateResumePrompt) {
			hitSlice = true
			break
		}
	}
	if !hitSlice {
		t.Fatalf("reuse-path slice must surface the injected resume prompt (source=%s, entries=%d)", slice.Source, len(slice.Entries))
	}

	// marker 必被消费（既有合同随钉）。
	if state := readAutonomousUpdateResumeFile(); len(state.Sessions) != 0 {
		t.Fatalf("roster marker must be consumed on first sight: %+v", state.Sessions)
	}
}

// tl1643五点合同+wire 面（goal 路径）：autopilot 恢复注入同机制同修。
func TestTl1643AutopilotGoalResumeInjectionRendersWithoutTabSwitch(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	path := newTl1606Session(t, dir, "tl1643 goal 种子用户输入", "tl1643 种子回复")
	h := attachTl1606TabSinkHarness(t, dir, path, []tl1606Round{{text: "tl1643 goal 注入回合答案"}})
	wire := attachTl1643WireRecorder(h)

	h.tab.autopilot = true
	h.tab.goal = "tl1643 unattended goal"

	h.app.maybeResumeAutopilotTab(h.tab)

	h.waitForTurns(1)
	h.waitIdle()

	// 合同6（本钉本体）：wire 面必带 user_input 注入原文。
	wire.waitWireUserInput(t, autopilotResumePrompt)

	// 合同3+4：落盘 + UI 转换（无切 tab）。
	h.snapshotToDisk(autopilotResumePrompt)
	rows := h.uiRows()
	hitUI := false
	for _, row := range rows {
		if strings.Contains(row.Content, autopilotResumePrompt) {
			hitUI = true
			break
		}
	}
	if !hitUI {
		t.Fatalf("goal resume prompt must render via the history conversion without a tab switch, rows = %d", len(rows))
	}
}
