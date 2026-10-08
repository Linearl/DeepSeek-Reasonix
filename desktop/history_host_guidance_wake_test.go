package main

import (
	"strings"
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/provider"
)

// 任务553: the background-job wake turn is labeled in the desktop transcript —
// a 「〔自动续轮〕后台任务已完成」 notice row replaces the model-facing body
// (English instruction + transient <background-jobs> block), keeping the auto
// turn visible and attributable without leaking the raw prompt.
func TestHistoryHostGuidanceLabelsBackgroundWakeTurn(t *testing.T) {
	const wakePrompt = "Automatic background-job wake turn: background job(s) you started finished while the session was idle. Their completion summaries are in the <background-jobs> block above. Continue the work that depended on those results, report anything the user must decide, and avoid starting new background jobs unless the original request still requires them."
	msgs := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost,
			Content: "<background-jobs>\njob compile done: build succeeded\n</background-jobs>\n\n" + wakePrompt},
		{Role: provider.RoleAssistant, Content: "handled the background result"},
	}
	history := historyMessages(msgs, identityPromptDisplay)
	if len(history) != 2 {
		t.Fatalf("history has %d rows, want the wake label row plus the assistant answer", len(history))
	}
	row := history[0]
	if row.Role != "notice" {
		t.Fatalf("wake row role = %q, want notice", row.Role)
	}
	if want := "↪ " + control.BackgroundWakeTurnMarker; row.Content != want {
		t.Fatalf("wake row = %q, want %q", row.Content, want)
	}
	if strings.Contains(row.Content, wakePrompt) || strings.Contains(row.Content, "<background-jobs>") {
		t.Fatalf("wake row leaks the model-facing body: %q", row.Content)
	}
	// 非 wake 的 host 指引仍走既有 hostGuidanceRows 路径（防回归）。
	other := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "innocent host guidance"},
	}
	fallback := historyMessages(other, identityPromptDisplay)
	if len(fallback) != 1 || fallback[0].Role != "notice" || fallback[0].Content != "↪ innocent host guidance" {
		t.Fatalf("non-wake host guidance = %+v, want the generic quoted row", fallback)
	}
}
