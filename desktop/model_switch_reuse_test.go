package main

// tl1389 P0: the model-switch rebuild must feed the 363A runtime-reuse pool —
// acquire the OUTGOING generation's assembly (pre-switch root/model/effort
// identity, the one guaranteed warm) and publish the fresh one under the
// post-switch identity, so switching back skips discovery too. The sentinel
// assembly's Skills slice doubles as the hit probe: on reuse boot's returned
// assembly shares the exact objects (skills = opts.ReuseAssembly.Skills), so
// the marker skill riding the post-switch pool entry proves the full chain
// acquire → boot reuse → store actually executed.

import (
	"context"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/skill"
)

func TestSetModelForTabRebuildReusesRuntimeAssembly(t *testing.T) {
	personaBoundaryTestConfig(t)
	// Flip the 363A gate on for this test (acquire/store are no-ops when off).
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Agent.ExperimentalRuntimeReuse = true
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config with reuse gate: %v", err)
	}
	t.Cleanup(DropRuntimeAssemblyPools)
	DropRuntimeAssemblyPools()

	app := NewApp()
	app.ctx = context.Background()

	tab := &WorkspaceTab{
		ID:          "tab_model_reuse",
		Scope:       "global",
		Ready:       true,
		model:       "ds/deepseek-v4-pro",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	oldCtrl := control.New(control.Options{
		Executor: agent.New(nil, nil, agent.NewSession("old system"), agent.Options{}, event.Discard),
		Sink:     tab.sink,
	})
	tab.Ctrl = oldCtrl
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
	})

	// The tab runs no effort override and holds no workspace root in this
	// fixture — both key segments are the empty string, matching what
	// buildModelSwitchController derives from the same snapshot.
	oldKey := runtimeAssemblyKeyForTab(tab.WorkspaceRoot, "ds/deepseek-v4-pro", "")
	newKey := runtimeAssemblyKeyForTab(tab.WorkspaceRoot, "prov-b/model-b1", "")
	if oldKey == newKey {
		t.Fatal("old and new assembly keys must differ for a model switch")
	}
	// The sentinel mirrors a pooled assembly's real shape: boot refuses reuse
	// when the ImplicitSkillInvocation flag moved between generations (the
	// default config has it enabled, so the sentinel must carry true).
	sentinel := &boot.ReusedAssembly{
		Skills:                  []skill.Skill{{Name: "tl1389-reuse-sentinel"}},
		ImplicitSkillInvocation: true,
	}
	runtimeAssemblies.mu.Lock()
	runtimeAssemblies.entries[oldKey] = sentinel
	runtimeAssemblies.mu.Unlock()

	if err := app.SetModelForTab(tab.ID, "prov-b/model-b1"); err != nil {
		t.Fatalf("rebuild switch: %v", err)
	}

	runtimeAssemblies.mu.Lock()
	stored := runtimeAssemblies.entries[newKey]
	runtimeAssemblies.mu.Unlock()
	if stored == nil {
		t.Fatal("post-switch assembly was never published under the new identity; switching back would pay full discovery")
	}
	if len(stored.Skills) != 1 || stored.Skills[0].Name != "tl1389-reuse-sentinel" {
		t.Fatalf("stored assembly skills = %+v, want the sentinel skill — the build did not consume the pooled outgoing assembly (acquire key or reuse plan broken)", stored.Skills)
	}
}
