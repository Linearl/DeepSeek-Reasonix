package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/skill"
)

// tl1479 — the capsule's bash output detail binding: tab routing, guard
// rails, and the end-to-end positive path through a real jobs manager.

func newTestJobOutputApp(t *testing.T) (*App, *jobs.Manager) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	st := skill.New(skill.Options{HomeDir: home})
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{AllSkillStore: st, SkillStore: st, Jobs: jm})
	a := NewApp()
	a.setTestCtrl(ctrl, "")
	t.Cleanup(func() { a.activeCtrl().Close() })
	return a, jm
}

func TestJobOutputForTabPositivePath(t *testing.T) {
	a, jm := newTestJobOutputApp(t)
	j := jm.Start("bash", "tail demo", func(ctx context.Context, out io.Writer) (string, error) {
		if _, err := out.Write([]byte("hello-from-job\n")); err != nil {
			return "", err
		}
		return "", nil
	})
	// Job.done is unexported outside the jobs package; the public Wait blocks
	// until the job reaches a terminal state.
	if results := jm.Wait(context.Background(), []string{j.ID}, 5); len(results) != 1 || results[0].Status != jobs.Done {
		t.Fatalf("job did not finish cleanly: %+v", results)
	}

	for _, tabID := range []string{"", "test"} { // "" = active tab, "test" = the registered runtime tab
		view, err := a.JobOutputForTab(tabID, j.ID)
		if err != nil {
			t.Fatalf("JobOutputForTab(%q): %v", tabID, err)
		}
		if !view.Found {
			t.Fatalf("JobOutputForTab(%q): job %q not found", tabID, j.ID)
		}
		if view.Status != string(jobs.Done) {
			t.Fatalf("JobOutputForTab(%q): status = %q, want done", tabID, view.Status)
		}
		if !strings.Contains(view.Text, "hello-from-job") {
			t.Fatalf("JobOutputForTab(%q): text %q lost the job output", tabID, view.Text)
		}
	}
}

func TestJobOutputForTabGuards(t *testing.T) {
	a, _ := newTestJobOutputApp(t)

	if _, err := a.JobOutputForTab("", "   "); err == nil {
		t.Fatal("empty job id must error")
	}
	view, err := a.JobOutputForTab("", "bash-404")
	if err != nil {
		t.Fatalf("unknown job on a live controller must not error: %v", err)
	}
	if view.Found {
		t.Fatal("unknown job must report found=false")
	}
	if view.Text != "" {
		t.Fatalf("unknown job must not carry text, got %q", view.Text)
	}
	// A tab without a controller resolves to no reader: found=false, no error
	// (the capsule treats it as "job already gone" and closes quietly).
	view, err = a.JobOutputForTab("no-such-tab", "bash-1")
	if err != nil {
		t.Fatalf("unknown tab must not error: %v", err)
	}
	if view.Found {
		t.Fatal("controller-less tab must report found=false")
	}
}
