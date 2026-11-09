package agent

// tl1357b 必做2 · 契约钉：unapplied steer dock hook 的触发面。
//
// hook 只为「无持久行」的 unapplied 引导触发（empty itemID）；带 itemID 的面
// 由 control 的 inbox sink 停靠既有行（onInboxUnappliedSteer），hook 必须沉默，
// 否则同一引导出现双持久行。flushSteerQueue 的 legacy 条目（Steer(string) 旧
// 面）逐条触发；批量汇总通知不触发。

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

func dockHookAgent(t *testing.T) (*Agent, *[]string) {
	t.Helper()
	var docked []string
	ag := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	ag.SetUnappliedSteerDockHook(func(text string) {
		docked = append(docked, text)
	})
	return ag, &docked
}

// hook只为无 itemID 的 RecordUnappliedSteer 触发，且拿到的是原文（非本地化
// 警告文本）。
func TestUnappliedSteerDockHookFiresOnlyWithoutItemID(t *testing.T) {
	ag, docked := dockHookAgent(t)

	ag.RecordUnappliedSteer("tl1357b 无行引导")
	if len(*docked) != 1 || !strings.Contains((*docked)[0], "tl1357b 无行引导") {
		t.Fatalf("empty-itemID face must dock once with the raw text, docked = %+v", *docked)
	}

	ag.RecordUnappliedSteer("tl1357b 有行引导", "item-1")
	if len(*docked) != 1 {
		t.Fatalf("itemID face must NOT dock (existing row parks via the sink), docked = %+v", *docked)
	}
}

// flushSteerQueue：legacy 条目（无 itemID）逐条触发 hook；持久条目不触发。
func TestFlushSteerQueueDocksLegacyEntries(t *testing.T) {
	ag, docked := dockHookAgent(t)
	ag.sess.conversation = NewSession("")

	ag.steerMu.Lock()
	ag.steerRunActive = true
	ag.steerQueue = []steerEntry{
		{text: "tl1357b legacy flush"},
		{itemID: "item-2", load: func() (SteerPayload, error) { return SteerPayload{Text: "durable"}, nil }},
	}
	ag.steerMu.Unlock()

	ag.flushSteerQueue()

	if len(*docked) != 1 || !strings.Contains((*docked)[0], "tl1357b legacy flush") {
		t.Fatalf("flush must dock legacy entries only, docked = %+v", *docked)
	}
	if !ag.steerUnapplied {
		t.Fatal("flush with dropped entries must mark steerUnapplied")
	}
}
