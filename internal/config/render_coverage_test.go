package config

import (
	"reflect"
	"strings"
	"testing"
)

// desktopRenderOmissions lists [desktop] keys that are deliberately not written to
// the canonical user config. Everything else MUST appear in the rendered output:
// the renderer writes a fixed key set, so anything missing here can be set in
// Settings and still vanish on save (that is exactly how the restart-and-update and
// session-monitor switches shipped broken until 2026-09-15).
var desktopRenderOmissions = map[string]string{
	"update_channel": "legacy compatibility field: accepted on read, never written back",
	// Conditionally rendered: written only once the user leaves the default, so an
	// untouched config stays short. They are listed here deliberately - the point of
	// this guard is that no preference can reach the config surface without someone
	// deciding, in one of these two lists, how it gets there.
	"autopilot":                "rendered together with its bound, only when configured",
	"autopilot_max_runtime":    "rendered with the autopilot flag",
	"autopilot_approval_grace": "rendered with the autopilot flag",
	"session_experience":       "rendered by renderDesktopSessionExperience",
	"reasoning_display_mode":   "rendered by renderDesktopReasoningDisplayMode",
	"conversation_width":       "rendered with the session-experience block",
}

// TestDesktopRenderTableCoversEveryKey is the single guard against that class of bug:
// it fails as soon as a new [desktop] preference is added without teaching the
// renderer about it, instead of shipping a switch that cannot be turned on.
func TestDesktopRenderTableCoversEveryKey(t *testing.T) {
	c := Default()
	out := RenderTOMLForScope(c, RenderScopeUser)

	typ := reflect.TypeOf(c.Desktop)
	var missing []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("toml")
		if tag == "" || tag == "-" {
			continue
		}
		key := strings.Split(tag, ",")[0]
		if _, ok := desktopRenderOmissions[key]; ok {
			continue
		}
		// Only leaf preferences have a rendered line of their own.
		switch field.Type.Kind() {
		case reflect.Struct, reflect.Map, reflect.Slice, reflect.Ptr:
			continue
		}
		if !strings.Contains(out, key+" =") {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("these [desktop] keys never reach the config file (add them to render.go, or list them in desktopRenderOmissions with a reason): %v", missing)
	}
}
