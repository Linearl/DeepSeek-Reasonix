package boot

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// writeHubConfig writes a reasonix.toml with session collab on and the hub
// switch set as requested, plus the two panel gates that withhold read_tail and
// create (so all nine collab tools register and the hub exposes every action).
func writeHubConfig(t *testing.T, dir string, hubOn bool) {
	t.Helper()
	collab := `
[agent]
experimental_session_collab = true
session_collab_allow_read_tail = true
session_collab_allow_create = true
`
	if hubOn {
		collab += "experimental_capability_groups = true\n"
	}
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"
`+collab+`
[[providers]]
name = "test-model"
kind = "boot-collab-probe"
model = "x"
`)
}

// hubProbeOnce registers the probe provider kind exactly once for the whole
// test binary — provider.Register panics on a duplicate kind, and this helper
// builds twice (off + on) plus once per extra test.
var hubProbeOnce sync.Once

// buildHubBoot runs a real BuildRuntime with a registered probe provider and
// returns the registry, so the switch双态 can be asserted against the genuine
// boot path rather than a hand-wired registry.
func buildHubBoot(t *testing.T, hubOn bool) *tool.Registry {
	t.Helper()
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	hubProbeOnce.Do(func() {
		provider.Register("boot-collab-probe", func(provider.Config) (provider.Provider, error) {
			return &coalesceWiringProvider{}, nil
		})
	})
	writeHubConfig(t, dir, hubOn)
	// create_collab_session needs a host callback; supply a no-op so the tool
	// registers (the gate is opts.OnCreateCollabSession != nil).
	res, err := BuildRuntime(context.Background(), Options{
		OnCreateCollabSession: func(agent.CreateCollabSessionRequest) (agent.CreateCollabSessionResult, error) {
			return agent.CreateCollabSessionResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	t.Cleanup(func() { res.Controller.Close() })
	return res.Assembly.Registry
}

// TestSessionHubSwitchGatesRegistrationAndVisibility pins the two-state contract
// (任务书验收「开关双态」):
//
//	off (default) — session_hub is NOT registered; the nine collab tools stay
//	                 direct-mounted exactly as before (行为等价 by absence of change).
//	on            — session_hub IS registered AND joins the provider-visible
//	                 surface (the extras channel); the nine old tools stay
//	                 registered (存量转录兼容) but are NOT re-exposed to the provider
//	                 (they were never provider-visible; the hub is the L0 face).
func TestSessionHubSwitchGatesRegistrationAndVisibility(t *testing.T) {
	// --- OFF: hub absent, nine tools direct-mounted, none provider-visible. ---
	off := buildHubBoot(t, false)
	if _, ok := off.Get("session_hub"); ok {
		t.Error("OFF: session_hub must not be registered")
	}
	for _, name := range nineCollabToolNames {
		if _, ok := off.Get(name); !ok {
			t.Errorf("OFF: collab tool %s should stay registered (direct-mounted)", name)
		}
		if off.ProviderVisible(name) {
			t.Errorf("OFF: collab tool %s should not be provider-visible", name)
		}
	}

	// --- ON: hub registered + provider-visible; nine old tools still registered. ---
	on := buildHubBoot(t, true)
	hub, ok := on.Get("session_hub")
	if !ok {
		t.Fatal("ON: session_hub must be registered")
	}
	if hub.Name() != "session_hub" {
		t.Errorf("ON: hub Name() = %q, want session_hub", hub.Name())
	}
	if !on.ProviderVisible("session_hub") {
		t.Error("ON: session_hub must be provider-visible (L0 常驻)")
	}
	// The old nine stay registered for存量转录兼容 but remain non-provider-visible.
	for _, name := range nineCollabToolNames {
		if _, ok := on.Get(name); !ok {
			t.Errorf("ON: collab tool %s must stay registered (存量转录兼容)", name)
		}
		if on.ProviderVisible(name) {
			t.Errorf("ON: collab tool %s should stay off the provider surface (hub is the L0 face)", name)
		}
	}
	// And the hub is in the provider schema list itself.
	found := false
	for _, s := range on.Schemas() {
		if s.Name == "session_hub" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ON: session_hub must appear in reg.Schemas()")
	}
}

// TestSessionHubProviderExtrasFollowTheFlag pins the extras channel that carries
// session_hub onto the provider-visible surface, mirroring the restart_update /
// multi_edit gate tests (铁律 2: default off).
func TestSessionHubProviderExtrasFollowTheFlag(t *testing.T) {
	on := &config.Config{}
	on.Agent.ExperimentalCapabilityGroups = true
	if got := providerSurfaceExtras(on); !contains(got, "session_hub") {
		t.Errorf("extras with the switch on = %v, want session_hub", got)
	}
	off := &config.Config{}
	if got := providerSurfaceExtras(off); contains(got, "session_hub") {
		t.Errorf("extras with the switch off = %v, want no session_hub", got)
	}
	if got := providerSurfaceExtras(nil); contains(got, "session_hub") {
		t.Errorf("extras with nil config = %v, want no session_hub", got)
	}
}

// TestSessionHubForwardsToRegisteredTools pins行为等价 at the boot level: for the
// read-only actions the hub's result must equal calling the registered tool
// directly (495 口径 = 行为等价, not byte-level). It exercises the real registry
// instances the hub dispatches to, on a live collab directory.
func TestSessionHubForwardsToRegisteredTools(t *testing.T) {
	on := buildHubBoot(t, true)
	hub, ok := on.Get("session_hub")
	if !ok {
		t.Fatal("ON: session_hub must be registered")
	}
	// Read-only actions with stable, side-effect-free outputs over an empty
	// directory: hub result must match the direct tool result exactly.
	for _, tc := range []struct {
		action string
		args   string
		direct string // registered tool name
	}{
		{"contacts", `{"action":"contacts"}`, "list_addressable_sessions"},
		{"status", `{"action":"status"}`, "get_session_status"},
	} {
		directTool, ok := on.Get(tc.direct)
		if !ok {
			t.Fatalf("direct tool %s not registered", tc.direct)
		}
		// Direct call strips the hub's action key.
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.args), &m); err != nil {
			t.Fatalf("bad args: %v", err)
		}
		delete(m, "action")
		directArgs, _ := json.Marshal(m)
		want, wantErr := directTool.Execute(context.Background(), directArgs)
		got, gotErr := hub.Execute(context.Background(), json.RawMessage(tc.args))
		if (wantErr == nil) != (gotErr == nil) {
			t.Errorf("action %s: direct err=%v hub err=%v", tc.action, wantErr, gotErr)
			continue
		}
		if got != want {
			t.Errorf("action %s: hub result %q != direct result %q (行为等价 violated)", tc.action, got, want)
		}
	}
}

// TestSessionHubDescriptionSizeBudget guards the ≈2.5KB L0 description (the hub
// replaces the nine scattered tools' ≈7KB) and the presence of the tail index.
func TestSessionHubDescriptionSizeBudget(t *testing.T) {
	on := buildHubBoot(t, true)
	hub, _ := on.Get("session_hub")
	if n := len(hub.Description()); n > 3500 {
		t.Errorf("hub description is %dB, want ≈2.5KB (under 3500B)", n)
	}
	if !strings.Contains(hub.Description(), "inspect tool:group/session") {
		t.Error("hub description missing deep-tool tail index")
	}
}

// nineCollabToolNames are the nine task-19/174/570 tools the hub aggregates.
var nineCollabToolNames = []string{
	"talk_to_session", "list_addressable_sessions", "read_session_tail",
	"get_session_status", "create_collab_session", "set_session_purpose",
	"get_message_status", "task_card", "extend_research_budget",
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
