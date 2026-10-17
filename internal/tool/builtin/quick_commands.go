package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// Quick-command management tool (task 960): expose the composer's quick-command
// snippets to the agent so they can be listed and edited through the engine
// itself instead of only through the settings UI.
//
// Architecture follows the task-201 heartbeat seam: the config layer owns
// validation (config.Config.SetQuickCommands — entry count, title length, text
// size), the desktop persists and broadcasts, and this package only decodes
// arguments and sequences read-modify-write calls through a QuickCommandsStore
// the desktop injects at startup. Until a desktop process injects one (CLI
// runs, tests without a desktop), Execute fails with a dedicated message
// instead of guessing at the config file — the same field contract the UI
// channel uses is therefore the single source of truth for validation.
//
// Mutating actions are whole-list replace under the hood (store.Replace), so
// every write passes through the config layer's SetQuickCommands validation
// chain — identical to what App.SetQuickCommands runs for the settings UI.
// add/edit/remove first List the current entries and rebuild the list in the
// tool layer; index addressing matches the list order.

// QuickCommandsStore is the seam the desktop injects. Implementations must be
// safe for concurrent use; the desktop adapter owns locking and persistence.
type QuickCommandsStore interface {
	// List returns a copy of the stored quick-command entries.
	List() ([]config.QuickCommandEntry, error)
	// Replace validates and persists the full list. Validation is the config
	// layer's SetQuickCommands chain (entry count / title length / text size);
	// implementations must not re-implement it.
	Replace(entries []config.QuickCommandEntry) error
}

var quickCommandsStore atomic.Pointer[QuickCommandsStore]

// SetQuickCommandsStore wires the desktop implementation in at startup.
// Passing nil clears a previous registration (used by tests).
func SetQuickCommandsStore(s QuickCommandsStore) {
	if s == nil {
		quickCommandsStore.Store(nil)
		return
	}
	quickCommandsStore.Store(&s)
}

func quickCommandsStoreFromCtx(ctx context.Context) (QuickCommandsStore, error) {
	_ = ctx
	if p := quickCommandsStore.Load(); p != nil {
		return *p, nil
	}
	return nil, fmt.Errorf("quick-command management requires the Reasonix desktop app; this session has no quick-command store attached (CLI/serve sessions cannot edit the composer's quick commands)")
}

func init() {
	tool.RegisterBuiltin(setQuickCommands{})
}

// quickCommandsContractDoc is the authoritative contract shared by the tool
// description and every action's error messages.
const quickCommandsContractDoc = `Contract (authoritative):
- Limits (same as the settings UI, enforced by the config layer): max 50 entries, title max 60 characters, text max 8192 bytes. Oversized entries are rejected, never silently truncated; fully blank entries are dropped.
- An entry with text but no title is rejected (the menu needs a label).
- "enabled": omit to keep current state on edit (absent means enabled for new entries); explicit false hides the snippet from the composer menu without deleting it.
- Mutations replace the whole stored list after validation; a failed validation writes nothing.`

// quickCommandsArgs is the decoded argument shape shared by every action.
type quickCommandsArgs struct {
	Action  string                     `json:"action"`
	Entries []config.QuickCommandEntry `json:"entries"`
	Index   *int                       `json:"index"`
	Title   *string                    `json:"title"`
	Text    *string                    `json:"text"`
	Enabled *bool                      `json:"enabled"`
}

type setQuickCommands struct{}

func (setQuickCommands) Name() string { return "set_quick_commands" }

func (setQuickCommands) Description() string {
	return "Manage the desktop composer's quick-command snippets (task 960). Actions: list (read the stored entries with their indexes), set (replace the whole list), add (append one entry), edit (patch one entry by index), remove (delete one entry by index). " + quickCommandsContractDoc
}

func (setQuickCommands) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["list", "set", "add", "edit", "remove"],
      "description": "list = read; set = replace whole list; add = append one; edit = patch one by index; remove = delete one by index."
    },
    "entries": {
      "type": "array",
      "description": "set: the full replacement list, in display order.",
      "items": {"$ref": "#/definitions/entry"}
    },
    "index": {
      "type": "integer",
      "description": "edit/remove: 0-based index from a prior list action."
    },
    "title": {"type": "string", "description": "add/edit: menu label (max 60 characters)."},
    "text": {"type": "string", "description": "add/edit: snippet text inserted into the composer verbatim (max 8192 bytes)."},
    "enabled": {"type": "boolean", "description": "add/edit: false hides the snippet without deleting it. Omit to keep current state on edit."}
  },
  "required": ["action"],
  "definitions": {
    "entry": {
      "type": "object",
      "properties": {
        "title": {"type": "string"},
        "text": {"type": "string"},
        "enabled": {"type": "boolean"}
      },
      "required": ["title", "text"]
    }
  }
}`)
}

func (setQuickCommands) ReadOnly() bool { return false }

func (setQuickCommands) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var in quickCommandsArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	store, err := quickCommandsStoreFromCtx(ctx)
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(in.Action) {
	case "list":
		return quickCommandsList(store)
	case "set":
		return quickCommandsSet(store, in)
	case "add":
		return quickCommandsAdd(store, in)
	case "edit":
		return quickCommandsEdit(store, in)
	case "remove":
		return quickCommandsRemove(store, in)
	case "":
		return "", fmt.Errorf("action is required: one of list, set, add, edit, remove")
	default:
		return "", fmt.Errorf("unknown action %q: use list, set, add, edit, or remove", in.Action)
	}
}

// quickCommandsListView is the list result: entries with their 0-based indexes
// so subsequent edit/remove calls can address them.
type quickCommandsListView struct {
	Entries []quickCommandsListEntry `json:"entries"`
}

type quickCommandsListEntry struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Enabled bool   `json:"enabled"`
}

func quickCommandsList(store QuickCommandsStore) (string, error) {
	entries, err := store.List()
	if err != nil {
		return "", err
	}
	view := quickCommandsListView{Entries: make([]quickCommandsListEntry, 0, len(entries))}
	for i, e := range entries {
		enabled := e.Enabled == nil || *e.Enabled
		view.Entries = append(view.Entries, quickCommandsListEntry{
			Index: i, Title: e.Title, Text: e.Text, Enabled: enabled,
		})
	}
	out, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func quickCommandsSet(store QuickCommandsStore, in quickCommandsArgs) (string, error) {
	if in.Entries == nil {
		return "", fmt.Errorf("entries is required for action=set: the full replacement list")
	}
	if err := store.Replace(in.Entries); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set %d quick command(s).", len(in.Entries)), nil
}

func quickCommandsAdd(store QuickCommandsStore, in quickCommandsArgs) (string, error) {
	if in.Title == nil || strings.TrimSpace(*in.Title) == "" {
		return "", fmt.Errorf("title is required for action=add")
	}
	if in.Text == nil {
		return "", fmt.Errorf("text is required for action=add")
	}
	current, err := store.List()
	if err != nil {
		return "", err
	}
	next := append(append([]config.QuickCommandEntry(nil), current...), config.QuickCommandEntry{
		Title: *in.Title, Text: *in.Text, Enabled: in.Enabled,
	})
	if err := store.Replace(next); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added quick command %q at index %d (%d total).", strings.TrimSpace(*in.Title), len(current), len(next)), nil
}

func quickCommandsEdit(store QuickCommandsStore, in quickCommandsArgs) (string, error) {
	if in.Index == nil {
		return "", fmt.Errorf("index is required for action=edit (0-based index from a prior list action)")
	}
	if in.Title == nil && in.Text == nil && in.Enabled == nil {
		return "", fmt.Errorf("action=edit needs at least one of title, text, enabled to change")
	}
	current, err := store.List()
	if err != nil {
		return "", err
	}
	if *in.Index < 0 || *in.Index >= len(current) {
		return "", fmt.Errorf("index %d is out of range (%d entries stored; call list for current indexes)", *in.Index, len(current))
	}
	edited := current[*in.Index]
	if in.Title != nil {
		edited.Title = *in.Title
	}
	if in.Text != nil {
		edited.Text = *in.Text
	}
	if in.Enabled != nil {
		edited.Enabled = in.Enabled
	}
	next := append([]config.QuickCommandEntry(nil), current...)
	next[*in.Index] = edited
	if err := store.Replace(next); err != nil {
		return "", err
	}
	return fmt.Sprintf("Edited quick command at index %d (%q).", *in.Index, strings.TrimSpace(edited.Title)), nil
}

func quickCommandsRemove(store QuickCommandsStore, in quickCommandsArgs) (string, error) {
	if in.Index == nil {
		return "", fmt.Errorf("index is required for action=remove (0-based index from a prior list action)")
	}
	current, err := store.List()
	if err != nil {
		return "", err
	}
	if *in.Index < 0 || *in.Index >= len(current) {
		return "", fmt.Errorf("index %d is out of range (%d entries stored; call list for current indexes)", *in.Index, len(current))
	}
	removed := current[*in.Index]
	next := append([]config.QuickCommandEntry(nil), current[:*in.Index]...)
	next = append(next, current[*in.Index+1:]...)
	if err := store.Replace(next); err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed quick command %q (%d remaining).", strings.TrimSpace(removed.Title), len(next)), nil
}
