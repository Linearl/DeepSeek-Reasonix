package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// newBranchFlowChatTUI builds a fresh flat session (schema-1 since tl-1173 P1;
// new sessions no longer carry an in-log DAG head) for the /branch and /switch
// command flows. The returned path is the source log; after /branch the
// controller binds a NEW session object (the child file), so tests must reach
// the live session through ctrl.Executor() rather than the original one.
func newBranchFlowChatTUI(t *testing.T) (chatTUI, *control.Controller, string) {
	t.Helper()
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "root prompt"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "root answer"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test", Sink: event.Discard})
	path := filepath.Join(dir, "root.jsonl")
	ctrl.SetSessionPath(path)
	if err := ctrl.Snapshot(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctrl.Close)
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	return m, ctrl, path
}

// TestBranchAndSwitchCommandsMoveBetweenSessionFiles pins the flat-session
// /branch semantics: branching at the tip copies the conversation into a child
// session FILE (the schema-1 path branchTip has always taken; in-log DAG heads
// are a schema-2-only legacy retired with tl1227), marks it current in the
// tree, and /switch moves back to the parent log and forward again by name.
func TestBranchAndSwitchCommandsMoveBetweenSessionFiles(t *testing.T) {
	m, ctrl, path := newBranchFlowChatTUI(t)
	m.runBranchCommand("/branch experiment")
	childPath := ctrl.SessionPath()
	if childPath == path {
		t.Fatal("/branch on a flat session must move to a child session file, got the same log")
	}
	if meta, ok, err := agent.LoadBranchMeta(childPath); err != nil || !ok || meta.Name != "experiment" || meta.ParentID != agent.BranchID(path) {
		t.Fatalf("child meta = %+v ok=%v err=%v, want name experiment parented on the source log", meta, ok, err)
	}
	tree := strings.Join(*m.pendingCommit, "\n")
	if !strings.Contains(tree, "experiment") || !strings.Contains(tree, "current") {
		t.Fatalf("/branch tree must list the new branch as current:\n%s", tree)
	}
	// The controller bound a fresh session object for the child file; grow the
	// live one (not the source session the fixture captured).
	ctrl.Executor().Session().Add(provider.Message{Role: provider.RoleUser, Content: "on the branch"})
	if err := ctrl.Snapshot(); err != nil {
		t.Fatal(err)
	}
	m.runSwitchCommand("/switch " + agent.BranchID(path))
	if got := len(ctrl.History()); got != 3 || !m.sessionSwitch || ctrl.SessionPath() != path {
		t.Fatalf("/switch to main: history %d switch %v path %q", got, m.sessionSwitch, ctrl.SessionPath())
	}
	m.sessionSwitch = false
	m.runSwitchCommand("/switch experiment")
	if got := ctrl.History(); len(got) != 4 || got[3].Content != "on the branch" || !m.sessionSwitch || ctrl.SessionPath() != childPath {
		t.Fatalf("/switch by name: history %d switch %v path %q", len(got), m.sessionSwitch, ctrl.SessionPath())
	}
}

func TestSessionsDiagnoseCountsSessionLogHeadsAndCleanupLeavesThem(t *testing.T) {
	t.Skip("tl1236 DAG 读退役善后(1227 后续): 「单日志多头不构成恢复组」场景不可达——tl-1173 P1 起新会话扁平、ForkHead 对扁平会话拒绝（session log is not schema 2），存量 DAG 日志已由 MigrateSessionDAGLog 一次性迁移且 tl1227 ListSessionHeads 垫片恒空，显式退役非静默")
	t.Setenv("REASONIX_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	s := agent.NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "shared question"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "shared answer"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ForkHead(path, s.Snapshot()[2].ID, agent.HeadKindFork, "alt"); err != nil {
		t.Fatal(err)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "alt question"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) sessionRecoveryReport {
		t.Helper()
		var report sessionRecoveryReport
		out := captureStdout(t, func() {
			if rc := sessionsRecoveryCommand(append([]string{"--dir", dir, "--json"}, args...), true); rc != 0 {
				t.Fatalf("sessions cleanup rc = %d", rc)
			}
		})
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("report %q: %v", out, err)
		}
		return report
	}
	dry := run()
	if dry.SourceSessions != 1 || dry.SessionLogs != 1 || dry.Heads != 2 || dry.CoveredHeads != 1 || dry.RetiredHeads != 0 {
		t.Fatalf("dry-run report = %+v", dry)
	}
	if dry.Groups != 0 || dry.CleanupEligible != 0 || !dry.DryRun {
		t.Fatalf("a session log must not form a recovery group: %+v", dry)
	}
	applied := run("--apply")
	if applied.MovedToTrash != 0 || applied.Busy != 0 || applied.DryRun {
		t.Fatalf("cleanup --apply on a session log = %+v, want nothing moved", applied)
	}
	heads, err := agent.ListSessionHeads(path)
	if err != nil || len(heads) != 2 || heads[0].Retired || heads[1].Retired {
		t.Fatalf("heads after cleanup = %+v err=%v, want the log untouched", heads, err)
	}
}
