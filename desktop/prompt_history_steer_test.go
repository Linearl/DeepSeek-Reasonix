package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl 1602 rework: the ↑-recall prompt-history panel must surface mid-turn
// steers. The scan previously reused agent.IsUserAuthoredTurnMessage, whose
// job is turn attribution ("a steer does not start a user turn") — the wrong
// criterion for "the user typed this into the composer", so steers silently
// vanished from the panel while the transcript showed them. These pins hold
// the panel's own criterion: user steers in (JSONL checkpoint and event log
// alike), legacy wrapped-only steer records display the user's exact text,
// host guidance of every shape stays out.

func steerTestJSONL(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt: base,
		UpdatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func steerTestRecord(t *testing.T, record map[string]any) string {
	t.Helper()
	buf, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func steerTestTapeEntries(t *testing.T, path string) []string {
	t.Helper()
	tape, err := newPromptHistoryTape(filepath.Dir(path), path)
	if err != nil {
		t.Fatal(err)
	}
	entries := tape.readAll()
	texts := make([]string, 0, len(entries))
	for _, entry := range entries {
		texts = append(texts, entry.Text)
	}
	return texts
}

// Current sessions persist an applied steer as role=user with the wrapped
// Content and the exact user text in RawContent. The panel must recall it
// next to ordinary turns, without the "[Mid-turn steer ...]" wrapper.
func TestPromptHistoryPanelIncludesSteerJSONL(t *testing.T) {
	steerText := "改用 Pillow 10 验证"
	steerContent := agent.MidTurnSteerPrefix + "\n" + steerText
	path := steerTestJSONL(t, []string{
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "user", "content": "first question",
		}),
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "user",
			"content": steerContent, "raw_content": steerText,
		}),
	})

	texts := steerTestTapeEntries(t, path)

	if len(texts) != 2 {
		t.Fatalf("panel entries = %q, want 2 (steer must not vanish from the panel)", texts)
	}
	found := false
	for _, text := range texts {
		if text == steerText {
			found = true
		}
		if strings.Contains(text, "Mid-turn steer") {
			t.Fatalf("panel leaks the steer wrapper: %q", text)
		}
	}
	if !found {
		t.Fatalf("panel lost the exact steer text %q: %q", steerText, texts)
	}
}

// The event log is the live transcript the panel follows between checkpoints
// (tl 1150). Steers appended there must surface the same way.
func TestPromptHistoryPanelIncludesSteerFromEventLog(t *testing.T) {
	steerText := "先补这个文件的回归钉"
	steerContent := agent.MidTurnSteerPrefix + "\n" + steerText
	base := time.Date(2026, 11, 16, 10, 5, 0, 0, time.UTC)
	path := steerTestJSONL(t, []string{
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "user", "content": "checkpoint question",
		}),
	})
	replaceRec := map[string]any{
		"schema_version": 1,
		"type":           "replace",
		"revision":       1,
		"messages":       []map[string]string{{"role": "user", "content": "checkpoint question"}},
		"created_at":     base.UTC().Format(time.RFC3339Nano),
	}
	appendRec := map[string]any{
		"schema_version": 1,
		"type":           "append",
		"revision":       2,
		"base_revision":  1,
		"message_index":  1,
		"messages": []map[string]string{{
			"role":        "user",
			"origin":      "user",
			"content":     steerContent,
			"raw_content": steerText,
		}},
		"created_at": base.Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
	writeSteerEventLog(t, path, []map[string]any{replaceRec, appendRec})

	texts := steerTestTapeEntries(t, path)

	if len(texts) != 2 {
		t.Fatalf("panel entries = %q, want 2 (event-log steer must not vanish)", texts)
	}
	if texts[0] != steerText || texts[1] != "checkpoint question" {
		t.Fatalf("panel entries = %q, want [%q checkpoint question]", texts, steerText)
	}
}

// Older sessions wrapped the steer text only in Content (no RawContent). The
// panel must strip the wrapper and the frozen @-reference framing (tl 1347)
// the same way the transcript replay does, or junk chrome becomes a
// "prompt" the user would resubmit.
func TestPromptHistoryLegacySteerWithoutRawContentShowsUserText(t *testing.T) {
	steerBody := "Referenced context:\n\n" +
		"<image path=\".reasonix/attachments/shot.png\">\n已附为视觉输入。\n</image>\n\n" +
		"晚点换 Pillow 10"
	steerContent := agent.MidTurnSteerPrefix + "\n" + steerBody
	path := steerTestJSONL(t, []string{
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "user", "content": steerContent,
		}),
	})

	texts := steerTestTapeEntries(t, path)

	if len(texts) != 1 {
		t.Fatalf("panel entries = %q, want 1", texts)
	}
	if texts[0] != "晚点换 Pillow 10" {
		t.Fatalf("panel text = %q, want the exact user text with wrapper and framing stripped", texts[0])
	}
}

// Host guidance stays out of the panel in every persisted shape: origin-host
// user-role rows (storm_breaker/goal_run_boundary checkpoints ride
// HostGeneratedUserMessage) and host Auto Guard policy riding steer-shaped
// records (recovery-gate "A tool failed..." / "The tool timed out..." bodies
// — the transcript's own classifier, agent.ReplaySteerText, hides them), plus
// the reference-only steer body (no user text after stripping — the
// transcript drops it too). None of it is composer input; recalling it would
// let the user resubmit system text as a prompt.
func TestPromptHistoryPanelKeepsHostGuidanceOut(t *testing.T) {
	hostSteerContent := agent.MidTurnSteerPrefix + "\n" +
		agent.HostRecoveryGuidanceToolFailedPrefix + ", continue unrelated work automatically, and do not ask the user."
	refOnlyContent := agent.MidTurnSteerPrefix + "\n" +
		"Referenced context:\n\n" +
		"<image path=\".reasonix/attachments/shot.png\">\n已附为视觉输入。\n</image>"
	path := steerTestJSONL(t, []string{
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "user", "content": "real question",
		}),
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "host", "content": "Host progress check: still working",
		}),
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "user", "content": hostSteerContent,
		}),
		steerTestRecord(t, map[string]any{
			"role": "user", "origin": "user", "content": refOnlyContent,
		}),
	})

	texts := steerTestTapeEntries(t, path)

	if len(texts) != 1 || texts[0] != "real question" {
		t.Fatalf("panel entries = %q, want only [real question] (host guidance and chrome must stay out)", texts)
	}
}

// Attribution semantics are owned by agent.IsUserAuthoredTurnMessage and must
// not move with this rework: a steer still does not start a user turn even
// though the panel now recalls it.
func TestPromptHistoryCriterionKeepsTurnAttributionUntouched(t *testing.T) {
	steer := provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content:    agent.MidTurnSteerPrefix + "\nuse the smaller patch",
		RawContent: "use the smaller patch",
	}
	if agent.IsUserAuthoredTurnMessage(steer) {
		t.Fatal("attribution regression: a steer must not start a user turn (agent.IsUserAuthoredTurnMessage changed)")
	}
	if !promptHistoryIncludesUserInput(steer) {
		t.Fatal("panel criterion regression: a user steer must be recallable")
	}
}

func writeSteerEventLog(t *testing.T, path string, records []map[string]any) {
	t.Helper()
	logPath := store.SessionEventLog(path)
	if logPath == "" {
		t.Fatalf("no event log path for %s", path)
	}
	body := ""
	for _, rec := range records {
		buf, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		body += string(buf) + "\n"
	}
	if err := os.WriteFile(logPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
