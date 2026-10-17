package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// Task 960: the desktop adapter must persist through App.SetQuickCommands —
// the exact function the settings UI calls — so agent edits share the UI
// channel's validation chain and lock discipline. These tests cover the
// read-back round trip, the validation rejection (nothing written), and the
// change event that lets the composer mirror refresh immediately.

func TestQuickCommandsStoreReplacePersistsThroughUIChannel(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	store := newQuickCommandsStoreAdapter(app)

	entries := []config.QuickCommandEntry{
		{Title: "转线", Text: "切换到另一条线"},
		{Title: "停机检查", Text: "跑停机检查单", Enabled: new(bool)},
	}
	if err := store.Replace(entries); err != nil {
		t.Fatalf("replace: %v", err)
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Title != "转线" || got[1].Text != "跑停机检查单" {
		t.Fatalf("round trip must persist entries, got %+v", got)
	}
	// The persisted file is the UI channel's own config field — the same view
	// DesktopStartupSettings serves to the composer.
	if startup := app.DesktopStartupSettings(); !reflect.DeepEqual(startup.QuickCommands, got) {
		t.Fatalf("startup snapshot %+v must match store list %+v", startup.QuickCommands, got)
	}
}

func TestQuickCommandsStoreReplaceRejectsOverLimitWithoutWriting(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	store := newQuickCommandsStoreAdapter(app)

	if err := store.Replace([]config.QuickCommandEntry{{Title: "keep", Text: "k"}}); err != nil {
		t.Fatalf("seed replace: %v", err)
	}
	longTitle := strings.Repeat("标", config.QuickCommandMaxTitle+1)
	err := store.Replace([]config.QuickCommandEntry{{Title: longTitle, Text: "t"}})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("over-limit title must be rejected by the shared validation chain, got %v", err)
	}
	// Nothing written: the previous list survives.
	got, lerr := store.List()
	if lerr != nil {
		t.Fatalf("list: %v", lerr)
	}
	if len(got) != 1 || got[0].Title != "keep" {
		t.Fatalf("failed validation must not write, got %+v", got)
	}
}

func TestQuickCommandsStoreReplaceEmitsChangeEvent(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	store := newQuickCommandsStoreAdapter(app)

	var eventName string
	var payload []config.QuickCommandEntry
	orig := runtimeEventsEmitFallback
	runtimeEventsEmitFallback = func(_ context.Context, name string, data ...any) {
		if eventName == "" {
			eventName = name
			if len(data) > 0 {
				if entries, ok := data[0].([]config.QuickCommandEntry); ok {
					payload = entries
				}
			}
		}
	}
	t.Cleanup(func() { runtimeEventsEmitFallback = orig })

	if err := store.Replace([]config.QuickCommandEntry{{Title: "转线", Text: "切换"}}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if eventName != quickCommandsChangedEvent {
		t.Fatalf("replace must emit %q, got %q", quickCommandsChangedEvent, eventName)
	}
	if len(payload) != 1 || payload[0].Title != "转线" {
		t.Fatalf("event payload must carry the persisted list, got %+v", payload)
	}
	// The payload must be the JSON shape the frontend consumes (title/text/enabled).
	raw, err := json.Marshal(payload)
	if err != nil || !strings.Contains(string(raw), `"title":"转线"`) {
		t.Fatalf("event payload must marshal to the frontend entry shape, got %s err=%v", raw, err)
	}
}

func TestQuickCommandsStoreReplaceFailureEmitsNoEvent(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	store := newQuickCommandsStoreAdapter(app)

	emitted := false
	orig := runtimeEventsEmitFallback
	runtimeEventsEmitFallback = func(context.Context, string, ...any) { emitted = true }
	t.Cleanup(func() { runtimeEventsEmitFallback = orig })

	if err := store.Replace([]config.QuickCommandEntry{{Text: "no title"}}); err == nil {
		t.Fatal("entry without title must be rejected")
	}
	if emitted {
		t.Fatal("a failed write must not emit the change event")
	}
}
