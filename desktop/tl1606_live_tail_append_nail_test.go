package main

// tl1606 断言③ hydrate 尾部追加钉（报告断点候选 5-A 前置化，tl1601/1518 同款
// 「复用路径不追加新落盘数据」判据的回归面）：
//
// 复用路径（display index 前缀 + live tail）下，回合结束产生的新转录行
// （随后落盘）必须出现在 HistorySliceForTab 的读面里——tab 复用/切回时
// 前端就是向这个面重新请求的；前缀认得、尾部丢弃 = 5-A 缺陷形态（画面
// 空白直到下一次全量 hydrate）。
//
// 正例三轮推进：①仅持久前缀（index 侧车在册）→ 复用面 Source=live-index；
// ②真回合增长（内存尾 + 落盘）→ 切片必须含新用户行与新回复行；③再次
// 增长 → 追加语义可重复。反例为 checker 牙齿证明：缺尾部的切片（5-A
// 缺陷形态的投影）必须被逐一报警。
//
// 面边界：只读生产读面（HistorySliceForTab / liveHistorySliceSource），
// 不改任何读路径语义；与 wt-tl1600-1601 的投影/渲染修复面零触碰。

import (
	"os"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// tl1606SliceProblems reports every expected needle missing from the slice's
// rendered entries — the reusable tooth behind both assertions below.
func tl1606SliceProblems(slice HistorySlice, needles ...string) []string {
	var problems []string
	for _, needle := range needles {
		hit := false
		for _, entry := range slice.Entries {
			if strings.Contains(entry.Message.Content, needle) {
				hit = true
				break
			}
		}
		if !hit {
			problems = append(problems, "切片缺行（5-A 复用不追加形态）: "+needle)
		}
	}
	return problems
}

// 正例：复用路径下新落盘行必须出现在切片里，且追加可重复。
func TestTl1606ReusePathLiveTailAppendsAfterNewPersistedRows(t *testing.T) {
	dir := t.TempDir()
	// ① 仅持久前缀：两行（一轮用户+回复），Save 同步发布 display index 侧车。
	path := newTl1606Session(t, dir, "tl1606 前缀用户行", "tl1606 前缀回复行")
	if _, err := agent.LoadSessionDisplayIndex(store.SessionDisplayIndex(path)); err != nil {
		t.Fatalf("premise: display index sidecar must exist after save: %v", err)
	}

	const (
		growthOne  = "tl1606 尾部追加回合一"
		growthTwo  = "tl1606 尾部追加回合二"
		answerText = "tl1606 尾部答案"
	)
	h := attachTl1606Harness(t, dir, path, []tl1606Round{{text: answerText}})

	// 复用路径前提自证：index 读面在册（live-index）。
	first := h.sliceEntries()
	if first.Source != "live-index" {
		t.Fatalf("premise: reuse path must serve from the display index, source = %q", first.Source)
	}
	if problems := tl1606SliceProblems(first, "tl1606 前缀用户行", "tl1606 前缀回复行"); len(problems) > 0 {
		t.Fatalf("prefix rows must be intact: %v", problems)
	}

	// ② 真回合增长 + 落盘：切片必须含新用户行与回复行（5-A 钉点）。
	h.ctrl.Send(growthOne)
	h.waitForTurns(1)
	h.waitIdle()
	// 先读切片（此刻 index 大概率仍只盖前缀——live tail 必须补上尾部），
	// 再走生产 Snapshot 落盘并等原文在册。
	after := h.sliceEntries()
	if problems := tl1606SliceProblems(after, growthOne, answerText); len(problems) > 0 {
		t.Fatalf("live tail must append the newly landed rows (source=%s, entries=%d): %v", after.Source, len(after.Entries), problems)
	}
	h.snapshotToDisk(growthOne)
	if body := h.readSessionJSONL(); !strings.Contains(body, growthOne) {
		t.Fatal("premise: the grown rows must be durable in the session jsonl")
	}
	persisted := h.sliceEntries()
	if problems := tl1606SliceProblems(persisted, growthOne, answerText); len(problems) > 0 {
		t.Fatalf("rows must stay visible after persistence (source=%s): %v", persisted.Source, problems)
	}

	// ③ 追加语义可重复：第二次增长同样必须追加。
	h.ctrl.Send(growthTwo)
	h.waitForTurns(2)
	h.waitIdle()
	h.snapshotToDisk(growthTwo)
	final := h.sliceEntries()
	if problems := tl1606SliceProblems(final, growthOne, growthTwo, answerText); len(problems) > 0 {
		t.Fatalf("repeated growth must keep appending (source=%s, entries=%d): %v", final.Source, len(final.Entries), problems)
	}
}

// 反例（断言牙齿）：把 checker 分别喂「缺第一轮尾部」「缺第二轮尾部」的
// 残缺切片（5-A 缺陷形态的投影），必须逐一报警；完整切片必须零报警。
func TestTl1606LiveTailCheckerFlagsMissingTailRows(t *testing.T) {
	entry := func(content string) HistoryEntry {
		return HistoryEntry{Message: HistoryMessage{Role: "user", Content: content}}
	}
	full := HistorySlice{Source: "live-index", Entries: []HistoryEntry{
		entry("tl1606 前缀用户行"),
		entry("tl1606 尾部追加回合一"),
		entry("tl1606 尾部追加回合二"),
	}}
	if problems := tl1606SliceProblems(full, "tl1606 前缀用户行", "tl1606 尾部追加回合一", "tl1606 尾部追加回合二"); len(problems) != 0 {
		t.Fatalf("complete slice must be clean, got %v", problems)
	}

	// 只盖前缀的投影（5-A：复用路径只认前缀不重算尾部）。
	prefixOnly := HistorySlice{Source: "live-index", Entries: []HistoryEntry{entry("tl1606 前缀用户行")}}
	problems := tl1606SliceProblems(prefixOnly, "tl1606 尾部追加回合一", "tl1606 尾部追加回合二")
	if len(problems) != 2 {
		t.Fatalf("missing tail rows must each be flagged, got %v", problems)
	}
}

// 前提自证：index 侧车真的盖住持久前缀（避免断言在无效前提上恒绿）。
func TestTl1606DisplayIndexPremiseCoversPersistedPrefix(t *testing.T) {
	dir := t.TempDir()
	path := newTl1606Session(t, dir, "tl1606 前提自证用户行", "tl1606 前提自证回复行")
	idx, err := agent.LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	if err != nil {
		t.Fatalf("load display index: %v", err)
	}
	if idx.MessageCount != 3 || idx.AuthoredTurns != 1 {
		t.Fatalf("index must cover the persisted prefix (system+user+reply = 3 messages, 1 authored turn), got count=%d turns=%d", idx.MessageCount, idx.AuthoredTurns)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file must exist: %v", err)
	}
}
