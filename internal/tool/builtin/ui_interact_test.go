package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

func TestParseUIKey(t *testing.T) {
	tests := []struct {
		raw       string
		vk        uint16
		modifiers int
		wantErr   bool
	}{
		{raw: "enter", vk: 0x0D},
		{raw: "f5", vk: 0x74},
		{raw: "ctrl+s", vk: 0x53, modifiers: 1},
		{raw: "ctrl+shift+tab", vk: 0x09, modifiers: 2},
		{raw: "a", vk: 0x41},
		{raw: "5", vk: 0x35},
		{raw: "up", vk: 0x26},
		{raw: "", wantErr: true},
		{raw: "ctrl+", wantErr: true},
		{raw: "hyper+q", wantErr: true},
	}
	for _, tc := range tests {
		vk, modifiers, err := parseUIKey(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseUIKey(%q) succeeded with vk=%#x", tc.raw, vk)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseUIKey(%q): %v", tc.raw, err)
		}
		if vk != tc.vk || len(modifiers) != tc.modifiers {
			t.Fatalf("parseUIKey(%q) = %#x+%d mods, want %#x+%d mods", tc.raw, vk, len(modifiers), tc.vk, tc.modifiers)
		}
	}
}

func TestUIInteractValidatesActionsAndArgs(t *testing.T) {
	tool := NewUIInteractTool(UIInteractConfig{})

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"hover"}`)); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("err = %v, want unknown-action error", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"click","x":-1,"y":0}`)); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("err = %v, want a coordinates error", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"type"}`)); err == nil || !strings.Contains(err.Error(), "non-empty text") {
		t.Fatalf("err = %v, want a text-required error", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"key","key":"hyper+q"}`)); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("err = %v, want an unknown-key error", err)
	}
	// Validation must fail before any window lookup: a bogus target with a
	// valid-shaped action reaches the platform layer, which errors anyway —
	// but an invalid action must never get there.
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"ACTIVATE","window_title":"zz-no-such-window-zz"}`)); err == nil {
		t.Fatal("platform error expected for a non-existent window")
	}
}

func TestUIInteractNotRegisteredByDefault(t *testing.T) {
	// Iron rule 2: with experimental_ui_driver off, the tool is not part of
	// the compile-time builtin set at all — boot registers it explicitly.
	if _, ok := tool.LookupBuiltin("ui_interact"); ok {
		t.Fatal("ui_interact must not be in the compile-time builtin set (gated tool)")
	}
}
