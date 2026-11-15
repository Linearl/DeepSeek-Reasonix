package main

// tl1606 断言①「三处一致」回归钉（tl1602 验收标准前置化）：
//
// 任意用户可见消息 ⇒ UI 可见 + jsonl/events 在册 + 历史提示词含。
// 形态库覆盖四类：steer / followup（排队跟进）/ 中断恢复 / restart 后。
//
// 「历史提示词」的权威证据面 = provider.Request.Messages：真实 agent 把
// 系统块 + 转录重放 + 本轮输入组态成模型请求，tl1606PromptProvider 逐次
// 捕获它。「UI 可见」= 生产历史转换（historyMessages，前端转录行的唯一
// 来源）。「jsonl 在册」= 会话转录文件经生产 Snapshot 落盘后原文在册；
// 「events 在册」= 回合台账（turns.jsonl）envelope 可读 + live sink 见过
// 对应事件种类。
//
// 语义边界（non-goal）：本断言钉的是当前注入语义的正确形态——steer 以
// [Mid-turn steer…] 前缀进转录与提示词、UI 呈现 notice（#4044 决策）；
// 不改注入语义（B 档机制改造走拍板），也不做 tl1601/1602 的修复本身
// （断言先立，修复带钉交付同批）。

import (
	"strings"
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// tl1606ThreeWayEvidence is one consistency sample: what the UI conversion
// renders, what the durable jsonl holds, and what the model prompt carried.
type tl1606ThreeWayEvidence struct {
	needle     string
	uiRows     []HistoryMessage
	jsonlBody  string
	promptBody string
}

// tl1606ThreeWayProblems reports every one of the three places missing the
// needle. Empty = 三处一致成立.
func tl1606ThreeWayProblems(ev tl1606ThreeWayEvidence) []string {
	var problems []string
	uiHit := false
	for _, row := range ev.uiRows {
		if strings.Contains(row.Content, ev.needle) {
			uiHit = true
			break
		}
	}
	if !uiHit {
		problems = append(problems, "UI 未含：生产历史转换没有可渲染的该消息行（tl1518/tl1601 复用不追加同款形态）")
	}
	if !strings.Contains(ev.jsonlBody, ev.needle) {
		problems = append(problems, "jsonl 未含：转录落盘缺该消息行（580 受理即落盘破坏形态）")
	}
	if !strings.Contains(ev.promptBody, ev.needle) {
		problems = append(problems, "历史提示词未含：模型可见上下文缺该消息（tl1602 引导丢失现场形态）")
	}
	return problems
}

func assertTl1606ThreeWayConsistent(t *testing.T, ev tl1606ThreeWayEvidence) {
	t.Helper()
	if problems := tl1606ThreeWayProblems(ev); len(problems) > 0 {
		t.Fatalf("三处一致被破坏（needle=%q）:\n  - %s", ev.needle, strings.Join(problems, "\n  - "))
	}
}

// 形态① steer：回合在飞时 TrySteer 的引导消息，被当轮消费后必须
// 三处在册——转录带 [Mid-turn steer…] 前缀原文、UI 呈 notice（#4044，
// "↪ "+原文）、下一轮模型提示词携带前缀原文。注入语义原样钉住。
func TestTl1606ThreeWaySteerGuidanceLandsInAllThreePlaces(t *testing.T) {
	h := newTl1606Harness(t, "steer", []tl1606Round{
		{text: "tl1606 steer 回合一答案", hold: true},
		{text: "tl1606 steer 回合二答案"},
	}, "tl1606 steer 种子用户输入", "tl1606 种子回复")
	const guidance = "tl1606 steer 引导原文"

	h.ctrl.Send("tl1606 steer 主任务输入")
	h.prov.waitHeld(t)
	if !h.ctrl.TrySteer(guidance) {
		t.Fatal("an in-flight turn must accept the steer")
	}
	h.prov.releaseHold()
	h.waitForTurns(2)
	h.waitIdle()

	// live sink 见过 Steer 事件（UI 的当轮呈现面）。
	if !h.events.sawKind(event.Steer) {
		t.Fatal("live event stream never carried the Steer delivery event")
	}
	h.snapshotToDisk(guidance)
	assertTl1606ThreeWayConsistent(t, tl1606ThreeWayEvidence{
		needle:     guidance,
		uiRows:     h.uiRows(),
		jsonlBody:  h.readSessionJSONL(),
		promptBody: h.prov.promptText(),
	})
	// 台账（turns.jsonl）在册：该会话的回合 envelope 可读。
	if envs, err := h.ctrl.TurnEventsAfter(0); err != nil || len(envs) == 0 {
		t.Fatalf("turn ledger must hold envelopes for the steering session, got %d, %v", len(envs), err)
	}
}

// 形态② followup：回合在飞时排队的持久跟进，收尾派发消费后必须三处在册
// ——转录落盘、UI 用户气泡、下一轮提示词以用户消息形态携带（竞品同款
// 「用户输入变体」；本断言只钉现状语义里的「必在」，不钉角色改写）。
func TestTl1606ThreeWayQueuedFollowupLandsInAllThreePlaces(t *testing.T) {
	h := newTl1606Harness(t, "followup", []tl1606Round{
		{text: "tl1606 followup 回合一答案", hold: true},
		{text: "tl1606 followup 回合二答案"},
	}, "tl1606 followup 种子用户输入", "tl1606 种子回复")
	const followup = "tl1606 排队跟进原文"

	h.ctrl.Send("tl1606 followup 主任务输入")
	h.prov.waitHeld(t)
	rec, err := h.ctrl.TryEnqueueFollowup(control.InboxRequest{
		Intent:      sessioninbox.IntentFollowup,
		Display:     followup,
		Raw:         followup,
		Submit:      followup,
		Source:      "tl1606-test",
		Idempotency: "tl1606:followup-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Disposition != sessioninbox.DispositionQueuedFollowup {
		t.Fatalf("in-flight followup must queue, disposition = %q", rec.Disposition)
	}
	h.prov.releaseHold()
	h.waitForTurns(2)
	h.waitIdle()

	// 收件箱清空 + live sink 见过 UserInput（排队项被消费的 UI 事件面）。
	if snap := h.ctrl.InboxSnapshot(); len(snap.Items) != 0 {
		t.Fatalf("consumed followup must leave the inbox, snapshot = %+v", snap)
	}
	if !h.events.sawKind(event.UserInput) {
		t.Fatal("live event stream never carried the UserInput consumption event")
	}
	h.snapshotToDisk(followup)
	assertTl1606ThreeWayConsistent(t, tl1606ThreeWayEvidence{
		needle:     followup,
		uiRows:     h.uiRows(),
		jsonlBody:  h.readSessionJSONL(),
		promptBody: h.prov.promptText(),
	})
}

// 形态③ 中断恢复：回合被 Cancel 打断后，中断前提交的用户消息必须仍三处
// 在册，且恢复轮的模型提示词继续携带（InterruptedTurnRecovery 不吞用户轮）。
func TestTl1606ThreeWayInterruptedTurnMessageSurvivesRecovery(t *testing.T) {
	h := newTl1606Harness(t, "interrupt", []tl1606Round{
		{text: "tl1606 中断前回合的部分答案", hold: true},
		{text: "tl1606 恢复轮答案"},
	}, "tl1606 interrupt 种子用户输入", "tl1606 种子回复")
	const interrupted = "tl1606 中断前提交原文"

	h.ctrl.Send(interrupted)
	h.prov.waitHeld(t)
	h.ctrl.Cancel()
	h.waitIdle()

	h.snapshotToDisk(interrupted)
	assertTl1606ThreeWayConsistent(t, tl1606ThreeWayEvidence{
		needle:     interrupted,
		uiRows:     h.uiRows(),
		jsonlBody:  h.readSessionJSONL(),
		promptBody: h.prov.promptText(),
	})

	// 恢复轮：中断前的用户消息必须仍进模型提示词（历史重放不丢）。
	h.ctrl.Send("tl1606 恢复后的输入")
	h.waitForTurns(2)
	h.waitIdle()
	if prompt := h.prov.promptText(); !strings.Contains(prompt, interrupted) {
		t.Fatal("the recovery turn's model prompt must still carry the interrupted user message")
	}
}

// 形态④ restart 后：同一路径关开（第二生命周期），重启前的消息必须经
// 转录重放三处在册——重启后 UI 历史、jsonl、下一轮模型提示词全含。
func TestTl1606ThreeWayMessageSurvivesRestartReplay(t *testing.T) {
	dir := t.TempDir()
	path := newTl1606Session(t, dir, "tl1606 restart 种子用户输入", "tl1606 种子回复")
	const before = "tl1606 重启前消息原文"

	h1 := attachTl1606Harness(t, dir, path, []tl1606Round{{text: "tl1606 重启前答案"}})
	h1.ctrl.Send(before)
	h1.waitForTurns(1)
	h1.waitIdle()
	h1.snapshotToDisk(before)
	h1.ctrl.Close()

	// 第二生命周期：同一路径重开（任务709 骨架同款）。
	h2 := attachTl1606Harness(t, dir, path, []tl1606Round{{text: "tl1606 重启后答案"}})
	assertTl1606ThreeWayConsistent(t, tl1606ThreeWayEvidence{
		needle: before,
		uiRows: h2.uiRows(),
		// 重开后 jsonl 本就是唯一持久真相，直接读。
		jsonlBody: h2.readSessionJSONL(),
		promptBody: func() string {
			h2.ctrl.Send("tl1606 重启后的消息")
			h2.waitForTurns(1)
			h2.waitIdle()
			return h2.prov.promptText()
		}(),
	})
	if envs, err := h2.ctrl.TurnEventsAfter(0); err != nil || len(envs) == 0 {
		t.Fatalf("reopened session must keep a readable turn ledger, got %d, %v", len(envs), err)
	}
}

// 反例（断言牙齿）：把三处一致 checker 分别喂「投影缺行」「转录缺行」
// 「提示词缺行」三种缺陷形态，必须逐一报警——证明断言能证伪，不是恒绿。
func TestTl1606ThreeWayCheckerFlagsEachMissingPlace(t *testing.T) {
	ev := tl1606ThreeWayEvidence{
		needle:     "tl1606 命中原文",
		uiRows:     []HistoryMessage{{Role: "user", Content: "前缀 tl1606 命中原文 后缀"}},
		jsonlBody:  `{"role":"user","content":"前缀 tl1606 命中原文 后缀"}`,
		promptBody: "system...\ntl1606 命中原文\n",
	}
	if problems := tl1606ThreeWayProblems(ev); len(problems) != 0 {
		t.Fatalf("consistent sample must be clean, got %v", problems)
	}

	// 缺 UI 行（tl1601/1518「复用路径不追加」形态）。
	noUI := ev
	noUI.uiRows = nil
	if problems := tl1606ThreeWayProblems(noUI); len(problems) != 1 || !strings.Contains(problems[0], "UI") {
		t.Fatalf("missing UI row must be flagged, got %v", problems)
	}
	// 缺 jsonl 行（580 家族「受理未落盘」形态）。
	noJSONL := ev
	noJSONL.jsonlBody = `{"role":"assistant","content":"别的行"}`
	if problems := tl1606ThreeWayProblems(noJSONL); len(problems) != 1 || !strings.Contains(problems[0], "jsonl") {
		t.Fatalf("missing jsonl row must be flagged, got %v", problems)
	}
	// 缺提示词行（tl1602 引导丢失现场形态）。
	noPrompt := ev
	noPrompt.promptBody = "system...\n别的历史\n"
	if problems := tl1606ThreeWayProblems(noPrompt); len(problems) != 1 || !strings.Contains(problems[0], "历史提示词") {
		t.Fatalf("missing prompt row must be flagged, got %v", problems)
	}
}
