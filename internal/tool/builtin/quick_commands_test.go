package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// fakeQuickCommandsStore replays canned list state and records the last
// Replace, so tool-layer tests exercise decoding, seam wiring, index
// addressing, and error pass-through only.
type fakeQuickCommandsStore struct {
	entries   []config.QuickCommandEntry
	replaced  []config.QuickCommandEntry
	replaceN  int
	listEr    error
	replaceEr error
}

func (f *fakeQuickCommandsStore) List() ([]config.QuickCommandEntry, error) {
	if f.listEr != nil {
		return nil, f.listEr
	}
	return append([]config.QuickCommandEntry(nil), f.entries...), nil
}

func (f *fakeQuickCommandsStore) Replace(entries []config.QuickCommandEntry) error {
	if f.replaceEr != nil {
		return f.replaceEr
	}
	f.replaceN++
	f.replaced = append([]config.QuickCommandEntry(nil), entries...)
	return nil
}

func withFakeQuickCommandsStore(t *testing.T) *fakeQuickCommandsStore {
	t.Helper()
	fake := &fakeQuickCommandsStore{}
	SetQuickCommandsStore(fake)
	t.Cleanup(func() { SetQuickCommandsStore(nil) })
	return fake
}

func execSetQuickCommands(t *testing.T, args string) (string, error) {
	t.Helper()
	return setQuickCommands{}.Execute(context.Background(), json.RawMessage(args))
}

func boolPtr(b bool) *bool { return &b }

func TestQuickCommandsToolFailsWithoutStore(t *testing.T) {
	SetQuickCommandsStore(nil)
	out, err := execSetQuickCommands(t, `{"action":"list"}`)
	if err == nil {
		t.Fatal("set_quick_commands must fail without an injected store")
	}
	if !strings.Contains(err.Error(), "desktop") {
		t.Fatalf("error should explain the missing desktop store, got: %v", err)
	}
	if out != "" {
		t.Fatalf("must not return output on failure, got %q", out)
	}
}

func TestQuickCommandsListReportsIndexesAndEffectiveEnabled(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	fake.entries = []config.QuickCommandEntry{
		{Title: "转线", Text: "切换到另一条线"},
		{Title: "停机检查", Text: "跑停机检查单", Enabled: boolPtr(false)},
	}
	out, err := execSetQuickCommands(t, `{"action":"list"}`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var view struct {
		Entries []struct {
			Index   int    `json:"index"`
			Title   string `json:"title"`
			Enabled bool   `json:"enabled"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("list output must be JSON: %v\n%s", err, out)
	}
	if len(view.Entries) != 2 {
		t.Fatalf("list must report 2 entries, got %d", len(view.Entries))
	}
	if view.Entries[0].Index != 0 || view.Entries[1].Index != 1 {
		t.Fatalf("indexes must be 0-based positions, got %+v", view.Entries)
	}
	// nil Enabled renders as enabled (absent = enabled, same as the UI).
	if !view.Entries[0].Enabled || view.Entries[1].Enabled {
		t.Fatalf("effective enabled must expand nil→true and keep explicit false, got %+v", view.Entries)
	}
}

func TestQuickCommandsSetReplacesWholeList(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	out, err := execSetQuickCommands(t, `{"action":"set","entries":[{"title":"A","text":"a"},{"title":"B","text":"b"}]}`)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if fake.replaceN != 1 || len(fake.replaced) != 2 || fake.replaced[1].Title != "B" {
		t.Fatalf("set must replace the whole list, got n=%d entries=%+v", fake.replaceN, fake.replaced)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("set receipt should count entries, got %q", out)
	}
}

func TestQuickCommandsSetRequiresEntries(t *testing.T) {
	withFakeQuickCommandsStore(t)
	if _, err := execSetQuickCommands(t, `{"action":"set"}`); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("set without entries must be rejected, got %v", err)
	}
}

func TestQuickCommandsAddAppendsAfterCurrentList(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	fake.entries = []config.QuickCommandEntry{{Title: "已有", Text: "x"}}
	out, err := execSetQuickCommands(t, `{"action":"add","title":"新增","text":"y","enabled":false}`)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(fake.replaced) != 2 {
		t.Fatalf("add must append to the current list, got %+v", fake.replaced)
	}
	got := fake.replaced[1]
	if got.Title != "新增" || got.Text != "y" || got.Enabled == nil || *got.Enabled {
		t.Fatalf("added entry must carry title/text/enabled, got %+v", got)
	}
	if !strings.Contains(out, "index 1") {
		t.Fatalf("add receipt should report the insertion index (after 1 existing entry), got %q", out)
	}
}

func TestQuickCommandsAddRequiresTitleAndText(t *testing.T) {
	withFakeQuickCommandsStore(t)
	if _, err := execSetQuickCommands(t, `{"action":"add","text":"y"}`); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("add without title must be rejected, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":"add","title":"x"}`); err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("add without text must be rejected, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":"add","title":"  ","text":"y"}`); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("add with blank title must be rejected, got %v", err)
	}
}

func TestQuickCommandsEditPatchesOnlyProvidedFields(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	fake.entries = []config.QuickCommandEntry{
		{Title: "A", Text: "a", Enabled: boolPtr(false)},
		{Title: "B", Text: "b"},
	}
	if _, err := execSetQuickCommands(t, `{"action":"edit","index":1,"title":"B2"}`); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if fake.replaced[0] != fake.entries[0] && (fake.replaced[0].Title != "A" || fake.replaced[0].Text != "a") {
		t.Fatalf("edit must not touch other entries, got %+v", fake.replaced[0])
	}
	if fake.replaced[1].Title != "B2" || fake.replaced[1].Text != "b" {
		t.Fatalf("edit must patch only provided fields, got %+v", fake.replaced[1])
	}
	// enabled omitted on edit keeps current state (nil stays nil).
	if fake.replaced[1].Enabled != nil {
		t.Fatalf("edit with enabled omitted must keep current value, got %+v", fake.replaced[1])
	}
	// explicit false on edit writes the pointer.
	if _, err := execSetQuickCommands(t, `{"action":"edit","index":0,"enabled":false}`); err != nil {
		t.Fatalf("edit enabled: %v", err)
	}
	if fake.replaced[0].Enabled == nil || *fake.replaced[0].Enabled {
		t.Fatalf("edit enabled=false must persist explicit false, got %+v", fake.replaced[0])
	}
}

func TestQuickCommandsEditGuards(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	fake.entries = []config.QuickCommandEntry{{Title: "A", Text: "a"}}
	if _, err := execSetQuickCommands(t, `{"action":"edit","title":"x"}`); err == nil || !strings.Contains(err.Error(), "index") {
		t.Fatalf("edit without index must be rejected, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":"edit","index":0}`); err == nil || !strings.Contains(err.Error(), "title, text, enabled") {
		t.Fatalf("edit with nothing to change must be rejected, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":"edit","index":5,"title":"x"}`); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("edit out of range must be rejected, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":"edit","index":-1,"title":"x"}`); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("edit negative index must be rejected, got %v", err)
	}
	if fake.replaceN != 0 {
		t.Fatalf("rejected edits must not write, got %d replaces", fake.replaceN)
	}
}

func TestQuickCommandsRemoveDropsIndexedEntry(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	fake.entries = []config.QuickCommandEntry{
		{Title: "A", Text: "a"}, {Title: "B", Text: "b"}, {Title: "C", Text: "c"},
	}
	out, err := execSetQuickCommands(t, `{"action":"remove","index":1}`)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fake.replaced) != 2 || fake.replaced[0].Title != "A" || fake.replaced[1].Title != "C" {
		t.Fatalf("remove must drop only the indexed entry, got %+v", fake.replaced)
	}
	if !strings.Contains(out, `"B"`) || !strings.Contains(out, "2 remaining") {
		t.Fatalf("remove receipt should name the dropped entry and remaining count, got %q", out)
	}
}

func TestQuickCommandsRemoveGuards(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	fake.entries = []config.QuickCommandEntry{{Title: "A", Text: "a"}}
	if _, err := execSetQuickCommands(t, `{"action":"remove"}`); err == nil || !strings.Contains(err.Error(), "index") {
		t.Fatalf("remove without index must be rejected, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":"remove","index":3}`); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("remove out of range must be rejected, got %v", err)
	}
	if fake.replaceN != 0 {
		t.Fatalf("rejected removes must not write, got %d replaces", fake.replaceN)
	}
}

func TestQuickCommandsActionGuard(t *testing.T) {
	withFakeQuickCommandsStore(t)
	if _, err := execSetQuickCommands(t, `{}`); err == nil || !strings.Contains(err.Error(), "action is required") {
		t.Fatalf("missing action must be rejected, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":"delete_everything"}`); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("unknown action must be named, got %v", err)
	}
	if _, err := execSetQuickCommands(t, `{"action":42}`); err == nil {
		t.Fatalf("non-string action must be rejected")
	}
}

func TestQuickCommandsPassesStoreErrorsThrough(t *testing.T) {
	fake := withFakeQuickCommandsStore(t)
	fake.replaceEr = errors.New("too many quick commands: 51 (max 50)")
	if _, err := execSetQuickCommands(t, `{"action":"set","entries":[{"title":"A","text":"a"}]}`); err == nil || !strings.Contains(err.Error(), "max 50") {
		t.Fatalf("store Replace error must pass through, got %v", err)
	}
	fake.replaceEr = nil
	fake.listEr = errors.New("config load failed")
	if _, err := execSetQuickCommands(t, `{"action":"add","title":"x","text":"y"}`); err == nil || !strings.Contains(err.Error(), "config load failed") {
		t.Fatalf("store List error must pass through, got %v", err)
	}
}

// configBackedQuickCommandsStore runs every Replace through the real config
// layer's SetQuickCommands validation — the same function App.SetQuickCommands
// (settings UI) calls. It proves the tool's write path shares the UI channel's
// validation chain end to end instead of re-implementing limits.
type configBackedQuickCommandsStore struct {
	cfg *config.Config
}

func (s *configBackedQuickCommandsStore) List() ([]config.QuickCommandEntry, error) {
	return s.cfg.DesktopQuickCommands(), nil
}

func (s *configBackedQuickCommandsStore) Replace(entries []config.QuickCommandEntry) error {
	return s.cfg.SetQuickCommands(entries)
}

func withConfigBackedQuickCommandsStore(t *testing.T) *configBackedQuickCommandsStore {
	t.Helper()
	s := &configBackedQuickCommandsStore{cfg: &config.Config{}}
	SetQuickCommandsStore(s)
	t.Cleanup(func() { SetQuickCommandsStore(nil) })
	return s
}

// TestQuickCommandsValidationSameChainAsUI is the task-960 acceptance anchor:
// every over-limit shape the settings UI rejects is rejected identically when
// it arrives through the tool, because both call Config.SetQuickCommands.
func TestQuickCommandsValidationSameChainAsUI(t *testing.T) {
	withConfigBackedQuickCommandsStore(t)

	longTitle := strings.Repeat("标", config.QuickCommandMaxTitle+1)
	bigText := strings.Repeat("x", config.QuickCommandMaxTextSize+1)

	tooMany := make([]config.QuickCommandEntry, config.QuickCommandMaxEntries+1)
	for i := range tooMany {
		tooMany[i] = config.QuickCommandEntry{Title: "e" + string(rune('a'+i%26)), Text: "t"}
	}
	overEntries, err := json.Marshal(map[string]any{"action": "set", "entries": tooMany})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		args    string
		wantSub string
	}{
		{"too many entries", string(overEntries), "max 50"},
		{"title too long", `{"action":"add","title":"` + longTitle + `","text":"t"}`, "too long"},
		{"text too large", `{"action":"add","title":"ok","text":"` + bigText + `"}`, "too large"},
		{"text without title", `{"action":"set","entries":[{"text":"no title"}]}`, "needs a title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := execSetQuickCommands(t, tc.args); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}

	// A valid write goes through and is readable back — the chain rejects the
	// bad shapes without blocking the good one.
	if _, err := execSetQuickCommands(t, `{"action":"add","title":"转线","text":"切换到另一条线"}`); err != nil {
		t.Fatalf("valid add must pass the shared validation chain: %v", err)
	}
	out, err := execSetQuickCommands(t, `{"action":"list"}`)
	if err != nil || !strings.Contains(out, "转线") {
		t.Fatalf("stored entry must be readable back, got %q err=%v", out, err)
	}
}
