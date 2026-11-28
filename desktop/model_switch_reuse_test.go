package main

// tl1389 P0: the model-switch rebuild must feed the 363A runtime-reuse pool —
// acquire the OUTGOING generation's assembly and publish the fresh one under
// the post-switch identity, so switching back skips discovery too.
// tl1859（方案A）：键放宽为 root-only 后出向/入向天然同键（last-writer-wins
// 覆盖），切模型复用出向装配的链路不变。The sentinel assembly's Skills slice
// doubles as the hit probe: on reuse boot's returned assembly shares the exact
// objects (skills = opts.ReuseAssembly.Skills), so the marker skill riding the
// post-switch pool entry proves the full chain acquire → boot reuse → store
// actually executed.

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/skill"
)

func TestSetModelForTabRebuildReusesRuntimeAssembly(t *testing.T) {
	personaBoundaryTestConfig(t)
	// tl1741：复用池常开——363A gate fixture 退役（acquire/store 无条件生效）。
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
	// fixture — the key derives from the empty root, matching what
	// buildModelSwitchController derives from the same snapshot.
	// tl1859（方案A）：键放宽为 root-only——模型切换的出向/入向天然同键
	// （这正是放宽的目的：切模型复用出向装配，不再冷建）。
	switchKey := runtimeAssemblyKeyForTab(tab.WorkspaceRoot)
	// The sentinel mirrors a pooled assembly's real shape: boot refuses reuse
	// when the ImplicitSkillInvocation flag moved between generations (the
	// default config has it enabled, so the sentinel must carry true).
	sentinel := &boot.ReusedAssembly{
		Skills:                  []skill.Skill{{Name: "tl1389-reuse-sentinel"}},
		ImplicitSkillInvocation: true,
	}
	runtimeAssemblies.mu.Lock()
	runtimeAssemblies.entries[switchKey] = sentinel
	runtimeAssemblies.mu.Unlock()

	// tl1859 验收②：命中路径必须在日志里留痕——desktop 池层 hit 行 +
	// 复用后的 store 行（grep 面与人工验收口径一致）。
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	if err := app.SetModelForTab(tab.ID, "prov-b/model-b1"); err != nil {
		t.Fatalf("rebuild switch: %v", err)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "desktop: runtime assembly hit") {
		t.Fatalf("reuse chain must log the pool hit marker, got:\n%s", logs)
	}
	if !strings.Contains(logs, "desktop: runtime assembly stored") {
		t.Fatalf("reuse chain must log the post-switch store marker, got:\n%s", logs)
	}

	runtimeAssemblies.mu.Lock()
	stored := runtimeAssemblies.entries[switchKey]
	runtimeAssemblies.mu.Unlock()
	if stored == nil {
		t.Fatal("post-switch assembly was never published under the new identity; switching back would pay full discovery")
	}
	if len(stored.Skills) != 1 || stored.Skills[0].Name != "tl1389-reuse-sentinel" {
		t.Fatalf("stored assembly skills = %+v, want the sentinel skill — the build did not consume the pooled outgoing assembly (acquire key or reuse plan broken)", stored.Skills)
	}
}
