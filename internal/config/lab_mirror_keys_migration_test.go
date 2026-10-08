package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 473 (X7-②/M1): the [desktop] boolean mirrors of the [agent] lab
// switches are retired. Every mirror folds into its [agent] key at load
// (OR semantics — the settings view always displayed the OR of both), the
// mirror field is cleared so an explicit off can never be resurrected, and
// the render face carries each key exactly once (in the [agent] section).

// labMirrorCases pins the whole fold table: [desktop] TOML key in, [agent]
// field (and its rendered [agent] key) out. experimental_trace_as_state is
// the odd one — the [agent] key has no experimental_ prefix.
var labMirrorCases = []struct {
	desktopKey string // [desktop] legacy TOML key
	agentKey   string // rendered [agent] TOML key
	agentField func(*Config) *bool
	desktopFld func(*Config) *bool
}{
	{"experimental_trace_as_state", "trace_as_state",
		func(c *Config) *bool { return &c.Agent.TraceAsState },
		func(c *Config) *bool { return &c.Desktop.ExperimentalTraceAsState }},
	{"experimental_dream", "experimental_dream",
		func(c *Config) *bool { return &c.Agent.ExperimentalDream },
		func(c *Config) *bool { return &c.Desktop.ExperimentalDream }},
	{"experimental_session_collab", "experimental_session_collab",
		func(c *Config) *bool { return &c.Agent.ExperimentalSessionCollab },
		func(c *Config) *bool { return &c.Desktop.ExperimentalSessionCollab }},
	{"experimental_auto_load_older", "experimental_auto_load_older",
		func(c *Config) *bool { return &c.Agent.ExperimentalAutoLoadOlder },
		func(c *Config) *bool { return &c.Desktop.ExperimentalAutoLoadOlder }},
	{"experimental_perf_monitor", "experimental_perf_monitor",
		func(c *Config) *bool { return &c.Agent.ExperimentalPerfMonitor },
		func(c *Config) *bool { return &c.Desktop.ExperimentalPerfMonitor }},
	{"experimental_heap_high_profile", "experimental_heap_high_profile",
		func(c *Config) *bool { return &c.Agent.ExperimentalHeapHighProfile },
		func(c *Config) *bool { return &c.Desktop.ExperimentalHeapHighProfile }},
	// 任务 517：B1/B2/B3 三个镜像键已随合并迁入 experimental_safety_cost_control
	// （fold → migrateSafetyCostControlMerge），不再作为独立镜像用例——
	// 迁移语义由 safety_cost_control_merge_test.go 覆盖。
	{"experimental_orphan_handling", "experimental_orphan_handling",
		func(c *Config) *bool { return &c.Agent.ExperimentalOrphanHandling },
		func(c *Config) *bool { return &c.Desktop.ExperimentalOrphanHandling }},
	{"experimental_model_capability_filter", "experimental_model_capability_filter",
		func(c *Config) *bool { return &c.Agent.ExperimentalModelCapabilityFilter },
		func(c *Config) *bool { return &c.Desktop.ExperimentalModelCapabilityFilter }},
	{"experimental_runtime_reuse", "experimental_runtime_reuse",
		func(c *Config) *bool { return &c.Agent.ExperimentalRuntimeReuse },
		func(c *Config) *bool { return &c.Desktop.ExperimentalRuntimeReuse }},
	{"collab_guidance_merge", "collab_guidance_merge",
		func(c *Config) *bool { return &c.Agent.CollabGuidanceMerge },
		func(c *Config) *bool { return &c.Desktop.CollabGuidanceMerge }},
}

func writeLabMirrorConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 验收①: a [desktop]-only true (older desktop build, no [agent] key) folds
// into the [agent] key and the mirror field is cleared — no value drift.
func TestLabMirrorKeysFoldDesktopTrueIntoAgent(t *testing.T) {
	for _, tc := range labMirrorCases {
		t.Run(tc.desktopKey, func(t *testing.T) {
			path := writeLabMirrorConfig(t, "[desktop]\n"+tc.desktopKey+" = true\n")
			cfg := LoadForEdit(path)
			if !*tc.agentField(cfg) {
				t.Fatalf("[desktop]-only true must fold into the [agent] key %s", tc.agentKey)
			}
			if *tc.desktopFld(cfg) {
				t.Fatalf("the [desktop] mirror %s must be cleared after the fold", tc.desktopKey)
			}
		})
	}
}

// 验收①: a legacy double-write config (both sections true, as pre-473 setters
// wrote them) folds without drift — the [agent] key stays on, the mirror clears.
func TestLabMirrorKeysFoldDoubleWriteWithoutDrift(t *testing.T) {
	for _, tc := range labMirrorCases {
		t.Run(tc.desktopKey, func(t *testing.T) {
			path := writeLabMirrorConfig(t, "[agent]\n"+tc.agentKey+" = true\n\n[desktop]\n"+tc.desktopKey+" = true\n")
			cfg := LoadForEdit(path)
			if !*tc.agentField(cfg) {
				t.Fatalf("double-write true must keep %s on", tc.agentKey)
			}
			if *tc.desktopFld(cfg) {
				t.Fatalf("the [desktop] mirror %s must be cleared after the fold", tc.desktopKey)
			}
		})
	}
}

// A mirror false (absent or explicit) carries no intent: nothing folds,
// nothing changes.
func TestLabMirrorKeysMirrorFalseKeepsAgentOff(t *testing.T) {
	path := writeLabMirrorConfig(t, `[desktop]
experimental_dream = false
experimental_perf_monitor = false
`)
	cfg := LoadForEdit(path)
	if cfg.Agent.ExperimentalDream || cfg.Desktop.ExperimentalDream {
		t.Fatal("mirror false must not turn the [agent] key on")
	}
	if cfg.Agent.ExperimentalPerfMonitor || cfg.Desktop.ExperimentalPerfMonitor {
		t.Fatal("mirror false must not turn the [agent] key on")
	}
}

// 验收③ (settings round-trip, off leg): a migrated-on switch turned off in
// Settings must stay off across save + reload — no resurrection from a stale
// mirror true. Runs through the real user-config path.
func TestLabMirrorKeysOffSurvivesSaveRoundTrip(t *testing.T) {
	home := isolateUserConfigHome(t)
	path := UserConfigPath()
	requireTestPathWithin(t, home, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[desktop]
experimental_dream = true
experimental_session_collab = true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadForEdit(path)
	if !cfg.Agent.ExperimentalDream || !cfg.Agent.ExperimentalSessionCollab {
		t.Fatal("precondition: the fold must turn both switches on")
	}
	if err := cfg.SetExperimentalDream(false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetExperimentalSessionCollab(false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "experimental_dream = true") ||
		strings.Contains(text, "experimental_session_collab = true") {
		t.Fatalf("save must not carry a stale on state:\n%s", text)
	}

	reloaded := LoadForEdit(path)
	if reloaded.Agent.ExperimentalDream || reloaded.Desktop.ExperimentalDream {
		t.Fatal("off must survive the reload (no resurrection from a stale mirror true)")
	}
	if reloaded.Agent.ExperimentalSessionCollab || reloaded.Desktop.ExperimentalSessionCollab {
		t.Fatal("off must survive the reload (no resurrection from a stale mirror true)")
	}
}

// 验收③ (settings round-trip, on leg): a folded-on switch must survive
// save + reload with the [agent] key carrying the value and the [desktop]
// mirror gone from the file.
func TestLabMirrorKeysOnSurvivesSaveRoundTrip(t *testing.T) {
	home := isolateUserConfigHome(t)
	path := UserConfigPath()
	requireTestPathWithin(t, home, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[desktop]
experimental_auto_load_older = true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadForEdit(path)
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "experimental_auto_load_older = true") {
		t.Fatalf("save must carry the folded on state:\n%s", text)
	}
	if strings.Count(text, "experimental_auto_load_older =") != 1 {
		t.Fatalf("the key must be assigned exactly once (the [agent] row) after save:\n%s", text)
	}

	reloaded := LoadForEdit(path)
	if !reloaded.Agent.ExperimentalAutoLoadOlder {
		t.Fatal("folded on value must survive the round trip (读值不丢)")
	}
}

// 验收②: the render face carries each mirror key at most once — the
// [desktop] mirror rows are gone, the [agent] row (unconditional, fixed key
// set) remains. experimental_trace_as_state disappears entirely: the [agent]
// key spells trace_as_state.
func TestLabMirrorKeysRenderExactlyOnce(t *testing.T) {
	out := RenderTOMLForScope(&Config{}, RenderScopeUser)
	for _, tc := range labMirrorCases {
		// Count the "key =" assignment form, not the bare key: surviving
		// numeric-mirror comment texts may still mention a key name.
		assignment := tc.desktopKey + " ="
		if tc.desktopKey == tc.agentKey {
			// Task 551: the B9 gate is removed — the legacy key is accepted on
			// read and deliberately NEVER rendered, so its expected count is 0.
			if tc.desktopKey == "experimental_model_capability_filter" {
				if got := strings.Count(out, assignment); got != 0 {
					t.Fatalf("the retired B9 key %s must not render (task 551), got %d:\n%s", tc.desktopKey, got, out)
				}
				continue
			}
			if got := strings.Count(out, assignment); got != 1 {
				t.Fatalf("%s must be assigned exactly once in the render (the [agent] row), got %d:\n%s", tc.desktopKey, got, out)
			}
			continue
		}
		// Spelling mismatch (trace_as_state): the retired [desktop] spelling
		// must vanish; the [agent] spelling remains exactly once.
		if strings.Contains(out, tc.desktopKey) {
			t.Fatalf("the retired [desktop] spelling %s must not render:\n%s", tc.desktopKey, out)
		}
		if got := strings.Count(out, tc.agentKey+" ="); got != 1 {
			t.Fatalf("%s must be assigned exactly once in the render, got %d:\n%s", tc.agentKey, got, out)
		}
	}
	for _, want := range []string{
		"trace_as_state = false",
		"experimental_dream = false",
		"experimental_session_collab = false",
		"experimental_auto_load_older = false",
		"experimental_perf_monitor = false",
		"experimental_heap_high_profile = false",
		"experimental_autonomous_idle_terminate = false",
		"experimental_loop_streak_note = false",
		"experimental_event_wait_recheck = false",
		"experimental_orphan_handling = false",
		// (experimental_model_capability_filter absent: task 551 never renders it.)
		"experimental_runtime_reuse = false",
		"collab_guidance_merge = false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the [agent] row must still render false when off, missing %q\n---\n%s", want, out)
		}
	}
}

// The settings setters are the only write path: they write the [agent] key
// and clear the retired [desktop] mirror, so a save drops the legacy key.
func TestLabMirrorSettersSingleWrite(t *testing.T) {
	c := &Config{}
	c.Desktop.ExperimentalDream = true // pre-473 leftover, not yet normalized
	setters := []struct {
		name    string
		apply   func(bool) error
		agent   func() bool
		desktop func() bool
	}{
		{"SetExperimentalTraceAsState", c.SetExperimentalTraceAsState,
			func() bool { return c.Agent.TraceAsState },
			func() bool { return c.Desktop.ExperimentalTraceAsState }},
		{"SetExperimentalDream", c.SetExperimentalDream,
			func() bool { return c.Agent.ExperimentalDream },
			func() bool { return c.Desktop.ExperimentalDream }},
		{"SetExperimentalSessionCollab", c.SetExperimentalSessionCollab,
			func() bool { return c.Agent.ExperimentalSessionCollab },
			func() bool { return c.Desktop.ExperimentalSessionCollab }},
		{"SetExperimentalAutoLoadOlder", c.SetExperimentalAutoLoadOlder,
			func() bool { return c.Agent.ExperimentalAutoLoadOlder },
			func() bool { return c.Desktop.ExperimentalAutoLoadOlder }},
		{"SetExperimentalPerfMonitor", c.SetExperimentalPerfMonitor,
			func() bool { return c.Agent.ExperimentalPerfMonitor },
			func() bool { return c.Desktop.ExperimentalPerfMonitor }},
		{"SetExperimentalHeapHighProfile", c.SetExperimentalHeapHighProfile,
			func() bool { return c.Agent.ExperimentalHeapHighProfile },
			func() bool { return c.Desktop.ExperimentalHeapHighProfile }},
		// 任务 517：B1/B2/B3 三个 setter 撤销，写路径并入 SetExperimentalSafetyCostControl
		// （safety_cost_control_merge_test.go 覆盖）。
		{"SetExperimentalOrphanHandling", c.SetExperimentalOrphanHandling,
			func() bool { return c.Agent.ExperimentalOrphanHandling },
			func() bool { return c.Desktop.ExperimentalOrphanHandling }},
		// Task 551 retired the B9 writable path (gate removed, desktop mirror
		// read-only); the migrate fold still covers legacy [desktop] values at
		// load, but the key no longer participates in the setter table below.
		{"SetExperimentalRuntimeReuse", c.SetExperimentalRuntimeReuse,
			func() bool { return c.Agent.ExperimentalRuntimeReuse },
			func() bool { return c.Desktop.ExperimentalRuntimeReuse }},
		{"SetCollabGuidanceMerge", c.SetCollabGuidanceMerge,
			func() bool { return c.Agent.CollabGuidanceMerge },
			func() bool { return c.Desktop.CollabGuidanceMerge }},
	}
	for _, s := range setters {
		t.Run(s.name, func(t *testing.T) {
			if err := s.apply(true); err != nil {
				t.Fatal(err)
			}
			if !s.agent() {
				t.Fatalf("%s(true) must write the [agent] key", s.name)
			}
			if s.desktop() {
				t.Fatalf("%s(true) must leave the retired [desktop] mirror off", s.name)
			}
			if err := s.apply(false); err != nil {
				t.Fatal(err)
			}
			if s.agent() || s.desktop() {
				t.Fatalf("%s(false) must clear both fields", s.name)
			}
		})
	}
}
