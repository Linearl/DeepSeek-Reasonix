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
	"autopilot_proxy_scope":    "task 388: always rendered via the normalized dial (default related)",
	"autopilot_proxy_manifest": "task 388: rendered only when the manifest path is set; empty = model self-judgment",
	"update_channel":           "legacy compatibility field: accepted on read, never written back",
	// Conditionally rendered: written only once the user leaves the default, so an
	// untouched config stays short. They are listed here deliberately - the point of
	// this guard is that no preference can reach the config surface without someone
	// deciding, in one of these two lists, how it gets there.
	"staging_dir":              "task 381: rendered only when the override is set; empty = the byte-identical default staging directory",
	"autopilot":                "rendered together with its bound, only when configured",
	"autopilot_max_runtime":    "rendered with the autopilot flag",
	"autopilot_approval_grace": "rendered with the autopilot flag",
	// Pre-existing red on main-v2-stable (task 326 landed these keys rendered
	// inside the autopilot block without an omission entry): both render only
	// once the guard interval/policy leaves the default.
	"autopilot_guard_interval":  "task 326: rendered inside the autopilot guard block, only when the interval leaves the default",
	"autopilot_guard_quiescent": "task 326: rendered inside the autopilot guard block, only when the self-close policy is set",
	// Task 477: rendered inside the autopilot block only once the sub-option
	// leaves its default (switch off, wait 0 = built-in 15s).
	"experimental_autopilot_ask_timeout": "task 477: rendered inside the autopilot block, only when the sub-option is configured",
	"autopilot_ask_wait_seconds":         "task 477: rendered inside the autopilot block, only when the wait dial is set; 0/absent = 15",
	// Task 544: rendered inside the autopilot block only while the ask
	// auto-continue sub-option is on (default off).
	"experimental_autopilot_ask_auto_continue": "task 544: rendered inside the autopilot block, only when the sub-option is on",
	// Task 547: rendered inside the autopilot block only while the opt-in is
	// on; off/absent = autopilot never creates guard tasks (byte-identical
	// config for a user who never touched the switch).
	"experimental_autopilot_guard_autocreate": "task 547: rendered inside the autopilot block, only when the guard auto-creation opt-in is on",
	// Task 551: the B9 gate is removed; the legacy key is READ-ONLY for load
	// compatibility and deliberately never written back (a stale true is inert
	// and vanishes on the next save).
	"experimental_model_capability_filter": "task 551: legacy task-244 B9 key, accepted on read, never rendered (gate removed)",
	"session_experience":                   "rendered by renderDesktopSessionExperience",
	"reasoning_display_mode":               "rendered by renderDesktopReasoningDisplayMode",
	"conversation_width":                   "rendered with the session-experience block",
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

// serveRenderOmissions lists [serve]/[serve.bus_mcp]/[serve.bus_worker] keys
// that are deliberately not written to the config file. Empty on purpose: the
// task 432 bug was exactly a key with no entry in either list — enroll set
// [serve.bus_mcp] and the renderer dropped it, so every future serve key must
// either reach render.go or be named here with a reason.
var serveRenderOmissions = map[string]string{}

// TestServeRenderTableCoversEveryKey is the [serve] twin of the desktop guard
// above: it fails as soon as a Serve/BusMCP/BusWorker field is added without
// teaching renderServeConfig about it.
func TestServeRenderTableCoversEveryKey(t *testing.T) {
	c := Default()
	out := RenderTOMLForScope(c, RenderScopeUser)

	tables := []struct {
		name string
		typ  reflect.Type
	}{
		{"serve", reflect.TypeOf(c.Serve)},
		{"serve.bus_mcp", reflect.TypeOf(c.Serve.BusMCP)},
		{"serve.bus_worker", reflect.TypeOf(c.Serve.BusWorker)},
	}
	for _, table := range tables {
		var missing []string
		for i := 0; i < table.typ.NumField(); i++ {
			field := table.typ.Field(i)
			tag := field.Tag.Get("toml")
			if tag == "" || tag == "-" {
				continue
			}
			key := strings.Split(tag, ",")[0]
			if _, ok := serveRenderOmissions[key]; ok {
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
			t.Fatalf("these [%s] keys never reach the config file (add them to renderServeConfig, or list them in serveRenderOmissions with a reason): %v", table.name, missing)
		}
	}
}
