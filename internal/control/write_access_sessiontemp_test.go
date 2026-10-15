package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/permission"
	"reasonix/internal/sandbox"
	"reasonix/internal/sessiontemp"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

func TestSessionTempWriteDoesNotRequestScopeExpansion(t *testing.T) {
	workspace := canonicalWriteTestDir(t)
	m := sessiontemp.NewWithRoot(t.TempDir())
	c := New(Options{
		WorkspaceRoot: workspace,
		WriteRoots:    sandbox.NewWritableRootSet([]string{workspace}),
		SessionTemp:   m,
		Policy:        permission.New("allow", nil, nil, nil),
	})
	t.Cleanup(c.Close)
	lease, err := m.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	ownedRoot, err := sandbox.ResolveAbsPath(lease.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		decision, err := c.CheckWriteAccess(context.Background(), agent.WriteAccessCheck{
			Tool:       "write_file",
			Expandable: true,
			Declaration: tool.WriteAccessDeclaration{
				Directories: []string{lease.Dir()},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Allow || len(decision.PerCallRoots) != 1 || decision.PerCallRoots[0] != ownedRoot {
			t.Fatalf("session temp write = %+v, want per-call approval for owned temp root", decision)
		}
		var writer tool.Tool
		for _, candidate := range (builtin.Workspace{Dir: workspace, WriteRootSet: c.writeAccess.roots}).Tools("write_file") {
			if candidate.Name() == "write_file" {
				writer = candidate
			}
		}
		if writer == nil {
			t.Fatal("write_file missing")
		}
		path := filepath.Join(lease.Dir(), "validate.py")
		args, err := json.Marshal(map[string]string{"path": path, "content": "print('ok')\n"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Execute(sandbox.WithPerCallWriteRoots(context.Background(), decision.PerCallRoots), args); err != nil {
			t.Fatalf("owned session temp write: %v", err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "print('ok')\n" {
			t.Fatalf("written file = %q, %v", got, err)
		}
	}
	checkBlocked := func(dir string) {
		t.Helper()
		decision, err := c.CheckWriteAccess(context.Background(), agent.WriteAccessCheck{
			Tool: "write_file", Expandable: true,
			Declaration: tool.WriteAccessDeclaration{Directories: []string{dir}},
		})
		if err != nil || decision.Allow {
			t.Fatalf("unowned directory %q = %+v, %v; want denied", dir, decision, err)
		}
	}
	checkBlocked(filepath.Join(filepath.Dir(lease.Dir()), "reasonix-session-tmp-unowned"))
	if err := os.Symlink(t.TempDir(), filepath.Join(lease.Dir(), "escape")); err == nil {
		checkBlocked(filepath.Join(lease.Dir(), "escape"))
	}
	m.Rotate()
	checkBlocked(lease.Dir())
}
