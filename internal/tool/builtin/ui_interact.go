package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// uiNamedKeyVK maps the named keys the schema documents to virtual key codes.
var uiNamedKeyVK = map[string]uint16{
	"enter": 0x0D, "return": 0x0D, "tab": 0x09, "esc": 0x1B, "escape": 0x1B,
	"backspace": 0x08, "delete": 0x2E, "del": 0x2E, "space": 0x20,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"home": 0x24, "end": 0x23, "pgup": 0x21, "pgdn": 0x22,
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74, "f6": 0x75,
	"f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79, "f11": 0x7A, "f12": 0x7B,
}

// uiCharVK covers single characters (letters/digits) not in the named table.
var uiCharVK = func() map[byte]uint16 {
	out := map[byte]uint16{}
	for c := byte('a'); c <= 'z'; c++ {
		out[c] = uint16(c) - 'a' + 0x41
	}
	for c := byte('0'); c <= '9'; c++ {
		out[c] = uint16(c)
	}
	return out
}()

// UIInteractConfig carries the workspace binding for the UI driver.
type UIInteractConfig struct {
	WorkDir string
}

// uiInteractTool drives a desktop window in a controlled way (task 233
// batch 2): activate / click / type / key. The click coordinate space is the
// same as the screenshot output (window top-left origin), so an agent can
// capture, look, and click without any offset arithmetic. Gated behind
// experimental_ui_driver (iron rule 2); with the switch off the tool is never
// registered.
type uiInteractTool struct{ cfg UIInteractConfig }

// NewUIInteractTool constructs the gated UI driver tool.
func NewUIInteractTool(cfg UIInteractConfig) tool.Tool {
	return uiInteractTool{cfg: cfg}
}

func (uiInteractTool) Name() string { return "ui_interact" }

func (uiInteractTool) Description() string {
	return "Drive a desktop window in a controlled way (task 233 batch 2; experimental_ui_driver must be on). Actions: activate (bring a window to the foreground), click (mouse click at window coordinates — the same origin as screenshot output, so capture first, look, then click), type (send unicode text to the foreground window), key (send a named key such as enter/tab/esc/up/down/f5, optionally ctrl+ prefixed). Every action implicitly activates the target window first; coordinates are window-relative (top-left origin, title bar included). Read-only on files; it changes the driven application, so verify what you are about to click."
}

func (uiInteractTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["activate","click","type","key"],"description":"activate: foreground the target window. click: mouse click at window coordinates. type: send unicode text. key: send a named key."},"window_title":{"type":"string","description":"Case-insensitive substring of the target window's title; defaults to the Reasonix window. Activated before the action."},"x":{"type":"integer","description":"click: window-relative X (same origin as screenshot output)."},"y":{"type":"integer","description":"click: window-relative Y."},"double":{"type":"boolean","description":"click: double-click."},"right":{"type":"boolean","description":"click: right button."},"text":{"type":"string","description":"type: text to send."},"key":{"type":"string","description":"key: enter|tab|esc|backspace|delete|space|up|down|left|right|home|end|pgup|pgdn|f1..f12|a..z|0..9, optionally ctrl+ prefixed (e.g. ctrl+s)."}},"required":["action"]}`)
}

func (uiInteractTool) ReadOnly() bool { return false }

func (t uiInteractTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action      string `json:"action"`
		WindowTitle string `json:"window_title"`
		X           int    `json:"x"`
		Y           int    `json:"y"`
		Double      bool   `json:"double"`
		Right       bool   `json:"right"`
		Text        string `json:"text"`
		Key         string `json:"key"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	target := strings.TrimSpace(p.WindowTitle)
	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "activate":
		title, err := uiDriverActivate(target)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("activated %q", title), nil
	case "click":
		if p.X < 0 || p.Y < 0 {
			return "", fmt.Errorf("click needs non-negative window coordinates x/y")
		}
		title, err := uiDriverClick(target, p.X, p.Y, p.Double, p.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("clicked (%d,%d) on %q", p.X, p.Y, title), nil
	case "type":
		if p.Text == "" {
			return "", fmt.Errorf("type needs non-empty text")
		}
		title, err := uiDriverType(target, p.Text)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("typed %d characters on %q", len([]rune(p.Text)), title), nil
	case "key":
		vk, modifiers, kerr := parseUIKey(p.Key)
		if kerr != nil {
			return "", kerr
		}
		title, err := uiDriverKey(target, vk, modifiers)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("sent key %q on %q", p.Key, title), nil
	default:
		return "", fmt.Errorf("unknown action %q; use activate, click, type, or key", p.Action)
	}
}

// parseUIKey resolves a named key (optionally ctrl+ prefixed) to a virtual
// key code plus the modifier VKs to hold.
func parseUIKey(raw string) (vk uint16, modifiers []uint16, err error) {
	spec := strings.ToLower(strings.TrimSpace(raw))
	if spec == "" {
		return 0, nil, fmt.Errorf("key is required (e.g. enter, f5, ctrl+s)")
	}
	for strings.Contains(spec, "ctrl+") {
		spec = strings.Replace(spec, "ctrl+", "", 1)
		modifiers = append(modifiers, uiVKControl)
	}
	if strings.Contains(spec, "shift+") {
		spec = strings.Replace(spec, "shift+", "", 1)
		modifiers = append(modifiers, uiVKShift)
	}
	if strings.Contains(spec, "alt+") {
		spec = strings.Replace(spec, "alt+", "", 1)
		modifiers = append(modifiers, uiVKMenu)
	}
	vk, ok := uiNamedKeyVK[spec]
	if !ok && len(spec) == 1 {
		if code, isChar := uiCharVK[spec[0]]; isChar {
			vk = code
			ok = true
		}
	}
	if !ok {
		return 0, nil, fmt.Errorf("unknown key %q; use enter, tab, esc, backspace, delete, space, up/down/left/right, home, end, pgup, pgdn, f1..f12, a..z, 0..9, optionally ctrl+/shift+/alt+ prefixed", raw)
	}
	return vk, modifiers, nil
}
