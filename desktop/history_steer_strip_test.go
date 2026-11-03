package main

import (
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1347: mid-turn steer bodies injected with frozen @-references carry the
// controller preamble ("Referenced context:" + <image path=...> XML blocks).
// historySteerRows must strip that model-side framing the same way the
// user-turn replay path does (historyReplayUserContent), or the raw XML leaks
// into replayed steer bubbles after a tab reopen.
func TestHistorySteerRowsStripsReferencedContextXML(t *testing.T) {
	frozen := "Referenced context:\n\n" +
		"<image path=\".reasonix/attachments/clipboard-230839.png\">\n已附为视觉输入。\n</image>\n\n" +
		"<image path=\".reasonix/attachments/clipboard-230902.png\">\n已附为视觉输入。\n</image>\n\n" +
		"1.这个问题是真实的么？2.渲染丢信息太严重了 @.reasonix/attachments/clipboard-230839.png"
	steer := agent.MidTurnSteerPrefix + "\n" + frozen

	rows, handled := historySteerRows(provider.Message{Role: provider.RoleUser, Content: steer}, false)
	if !handled {
		t.Fatal("historySteerRows not handled for a mid-turn steer")
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	want := "↪ 1.这个问题是真实的么？2.渲染丢信息太严重了 @.reasonix/attachments/clipboard-230839.png"
	if rows[0].Content != want {
		t.Fatalf("Content = %q, want %q", rows[0].Content, want)
	}
	if strings.Contains(rows[0].Content, "<image") || strings.Contains(rows[0].Content, "Referenced context:") {
		t.Fatalf("Content still carries injected framing: %q", rows[0].Content)
	}
}

// A steer whose visible body is only the injected reference preamble has no
// user-authored content; after the strip it must not render a junk row.
func TestHistorySteerRowsDropsReferenceOnlySteer(t *testing.T) {
	frozen := "Referenced context:\n\n" +
		"<image path=\".reasonix/attachments/shot.png\">\n已附为视觉输入。\n</image>"
	steer := agent.MidTurnSteerPrefix + "\n" + frozen

	rows, handled := historySteerRows(provider.Message{Role: provider.RoleUser, Content: steer}, false)
	if !handled {
		t.Fatal("handled = false, want true (steer recognized, row dropped)")
	}
	if rows != nil {
		t.Fatalf("rows = %+v, want nil", rows)
	}
}

// Plain steers without frozen references keep the exact pre-tl1347 rendering.
func TestHistorySteerRowsPlainTextUnchanged(t *testing.T) {
	steer := agent.MidTurnSteerPrefix + "\n改用 Pillow 10 验证"

	rows, handled := historySteerRows(provider.Message{Role: provider.RoleUser, Content: steer}, false)
	if !handled || len(rows) != 1 {
		t.Fatalf("handled=%v rows=%d, want handled with 1 row", handled, len(rows))
	}
	if rows[0].Content != "↪ 改用 Pillow 10 验证" {
		t.Fatalf("Content = %q, want plain steer text unchanged", rows[0].Content)
	}
}

// Unapplied steer archives (flush path) carry the same frozen framing; the
// notice body must be stripped too.
func TestHistorySteerRowsUnappliedStripsReferencedContextXML(t *testing.T) {
	frozen := "Referenced context:\n\n" +
		"<image path=\".reasonix/attachments/shot.png\">\n已附为视觉输入。\n</image>\n\n" +
		"晚点再看这张图"
	archive := agent.MidTurnSteerPrefix + "\n" + frozen

	rows, handled := historySteerRows(provider.Message{Role: provider.RoleTool, LocalOnly: true, Content: archive}, true)
	if !handled || len(rows) != 1 {
		t.Fatalf("handled=%v rows=%d, want handled with 1 notice row", handled, len(rows))
	}
	want := agent.UnappliedSteerNotice("晚点再看这张图")
	if rows[0].Content != want {
		t.Fatalf("Content = %q, want %q", rows[0].Content, want)
	}
	if rows[0].Code != event.NoticeCodeUnappliedSteer {
		t.Fatalf("Code = %q, want %q", rows[0].Code, event.NoticeCodeUnappliedSteer)
	}
}
