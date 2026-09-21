package main

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
	"reasonix/internal/tool"
)

type workspaceContextProvider struct{}

func (workspaceContextProvider) Name() string { return "workspace-context" }

func (workspaceContextProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func installStubControllerWithCurrentPrompt(t *testing.T, app *App, tab *WorkspaceTab) *control.Controller {
	t.Helper()
	if tab == nil || tab.Ctrl == nil {
		t.Fatal("tab controller is required")
	}
	sys := systemPromptFrom(tab.Ctrl.History())
	if strings.TrimSpace(sys) == "" {
		t.Fatal("tab controller did not expose a system prompt")
	}
	sessionDir := tab.Ctrl.SessionDir()
	sessionPath := tab.Ctrl.SessionPath()
	workspaceRoot := tab.Ctrl.WorkspaceRoot()
	label := tab.Ctrl.Label()
	tab.Ctrl.Close()

	sess := agent.NewSession(sys)
	ag := agent.New(workspaceContextProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{
		Runner:               ag,
		Executor:             ag,
		SessionDir:           sessionDir,
		SessionPath:          sessionPath,
		WorkspaceRoot:        workspaceRoot,
		Label:                label,
		SystemPrompt:         sys,
		SessionContextStatic: sessioncontext.Sections{Workspace: "Current workspace: " + strconv.Quote(workspaceRoot)},
		Sink:                 event.Discard,
	})
	app.mu.Lock()
	tab.Ctrl = ctrl
	app.mu.Unlock()
	app.bindControllerDisplayRecorder(ctrl)
	return ctrl
}

func assertWorkspaceSessionContext(t *testing.T, messages []provider.Message, want, unwanted string) {
	t.Helper()
	for i := range slices.Backward(messages) {
		snapshot, ok := sessioncontext.Parse(messages[i].Content)
		if !ok || messages[i].Origin != provider.MessageOriginHost {
			continue
		}
		if !strings.Contains(snapshot.Sections.Workspace, "Current workspace: "+strconv.Quote(want)) {
			t.Fatalf("session-context missing workspace %q:\n%s", want, snapshot.Content)
		}
		if strings.Contains(snapshot.Sections.Workspace, "Current workspace: "+strconv.Quote(unwanted)) {
			t.Fatalf("session-context retained workspace %q:\n%s", unwanted, snapshot.Content)
		}
		return
	}
	t.Fatalf("history contains no valid host session-context: %+v", messages)
}

// Task 200: the boot snapshot and later context-state injections are machine
// context. The transcript builders (cold first-open and hot re-open) both
// funnel through hostGuidanceRows, which must render nothing once
// StripTransientUserBlocks removes the injected blocks — while real guidance
// (readiness catch-up, nudges) keeps its notice row.
func TestHostGuidanceRowsHideSessionContextOnBothPaths(t *testing.T) {
	boot := "<session-context version=\"1\">\n## Environment\n- OS: windows/amd64\n" +
		"## Skills catalog\n- some-skill — does things\n</session-context>\n\n" +
		"<reasoning-language>\n必须使用简体中文书写全部可见思考/推理文本\n</reasoning-language>\n\n"
	laterTurn := "<context-state>window occupancy 12k/1000k (12%)</context-state>\n\n"

	for _, tc := range []struct{ name, content string }{
		{"cold first turn", boot},
		{"hot later turn", laterTurn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, handled := hostGuidanceRows(agent.HostGeneratedUserMessage(tc.content))
			if handled {
				t.Fatalf("rows = %+v, want the injected block to render nothing", rows)
			}
		})
	}

	// Real guidance must not be swallowed by the same rule.
	rows, handled := hostGuidanceRows(agent.HostGeneratedUserMessage("readiness catch-up: resuming the pending plan step"))
	if !handled || len(rows) != 1 || !strings.HasPrefix(rows[0].Content, "↪ readiness catch-up") {
		t.Fatalf("rows = %+v handled = %v, want the guidance notice row", rows, handled)
	}
}
