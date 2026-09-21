package agent

import (
	"strings"
	"testing"
)

// Every tag the host prepends must strip cleanly, whatever else precedes it.
// A tag missing from TransientUserBlockTags used to reach the UI verbatim —
// <autoresearch-runtime> showed up in session titles and the rewind picker.
func TestStripTransientUserBlocksCoversEveryDeclaredTag(t *testing.T) {
	const prompt = "refactor the parser"
	for _, tag := range TransientUserBlockTags {
		t.Run(tag, func(t *testing.T) {
			block := "<" + tag + ">\nruntime detail\n</" + tag + ">\n\n"
			if got := StripTransientUserBlocks(block + prompt); got != prompt {
				t.Fatalf("StripTransientUserBlocks(%q) = %q, want %q", block+prompt, got, prompt)
			}
			if got := UserPreviewText(block + prompt); !strings.HasPrefix(got, prompt) {
				t.Fatalf("UserPreviewText leaked markup: %q", got)
			}
		})
	}
}

// The blocks arrive stacked (active-goal then autoresearch-runtime then the
// language blocks), so stripping has to consume the whole run, not just the
// first one.
func TestStripTransientUserBlocksConsumesStackedBlocks(t *testing.T) {
	const prompt = "继续执行计划"
	stacked := "<active-goal>\ngoal: ship it\n</active-goal>\n\n" +
		"<autoresearch-runtime>\nstatus: running\n</autoresearch-runtime>\n\n" +
		"<response-language>\nprefer zh\n</response-language>\n\n" +
		prompt
	if got := StripTransientUserBlocks(stacked); got != prompt {
		t.Fatalf("StripTransientUserBlocks = %q, want %q", got, prompt)
	}
}

// Attribute-carrying open tags (hook-context, capability-route) must strip too.
func TestStripTransientUserBlocksHandlesAttributedTags(t *testing.T) {
	const prompt = "run the tests"
	in := `<capability-route version="1">` + "\nroute: test\n</capability-route>\n\n" + prompt
	if got := StripTransientUserBlocks(in); got != prompt {
		t.Fatalf("StripTransientUserBlocks = %q, want %q", got, prompt)
	}
}

// Task 200: the boot snapshot (<session-context version="1">, hundreds of
// lines of environment/workspace/memory/skill catalog) opens a session's
// first user turn, and <context-state> prepends later turns. Both are machine
// context: stripping must remove them while keeping any user text, so the
// transcript and titles never surface them (the display layer renders an
// empty remainder as nothing at all).
func TestStripTransientUserBlocksRemovesSessionContextBootSnapshot(t *testing.T) {
	const prompt = "心跳巡检：检查任务队列"
	in := "<session-context version=\"1\">\n" +
		"## Environment\n- OS: windows/amd64\n## Skills catalog\n- some-skill — does things\n" +
		"</session-context>\n\n" +
		"<reasoning-language>\n必须使用简体中文书写全部可见思考/推理文本\n</reasoning-language>\n\n" +
		"<context-state>window occupancy 12k/1000k (12%)</context-state>\n\n" +
		prompt
	if got := StripTransientUserBlocks(in); got != prompt {
		t.Fatalf("StripTransientUserBlocks = %q, want %q", got, prompt)
	}
	// A turn that carries only the injected blocks strips to nothing — the
	// host-guidance renderer relies on that to drop the row entirely.
	onlyBlocks := "<session-context version=\"1\">\nlots of machine context\n</session-context>\n\n" +
		"<context-state>window occupancy 12k/1000k (12%)</context-state>\n\n"
	if got := StripTransientUserBlocks(onlyBlocks); got != "" {
		t.Fatalf("StripTransientUserBlocks = %q, want empty", got)
	}
	if got := UserPreviewText(onlyBlocks); got != "" {
		t.Fatalf("UserPreviewText = %q, want empty", got)
	}
}

// hasLeadingInjectedBlock walks the same list, so a block already present
// behind other injected blocks is detected instead of being added twice.
func TestHasLeadingInjectedBlockSkipsEveryDeclaredTag(t *testing.T) {
	const target = "reasoning-language"
	for _, tag := range TransientUserBlockTags {
		if tag == target {
			continue
		}
		t.Run(tag, func(t *testing.T) {
			content := "<" + tag + ">\nx\n</" + tag + ">\n\n" +
				"<" + target + ">\nprefer zh\n</" + target + ">\n\nhello"
			if !hasLeadingInjectedBlock(content, target) {
				t.Fatalf("hasLeadingInjectedBlock(%q) = false, want the existing %s block detected", content, target)
			}
		})
	}
}

func TestHasLeadingInjectedBlockIgnoresUserProse(t *testing.T) {
	if hasLeadingInjectedBlock("what does <response-language> mean?", "response-language") {
		t.Fatal("prose mentioning a tag must not count as an injected block")
	}
	if hasLeadingInjectedBlock("<active-goal>\ng\n</active-goal>\n\nplain text", "reasoning-language") {
		t.Fatal("walking past other blocks must not invent a target block")
	}
}
