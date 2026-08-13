package control

import (
	"context"
	"testing"

	"reasonix/internal/event"
)

func TestRequestDetachRequiresRunningTurn(t *testing.T) {
	c := New(Options{Sink: event.FuncSink(func(event.Event) {})})
	if err := c.RequestDetach(); err == nil {
		t.Fatal("RequestDetach on idle controller should fail")
	}
}

func TestRequestDetachSetsFlagWhileRunning(t *testing.T) {
	c := New(Options{Sink: event.FuncSink(func(event.Event) {})})
	c.mu.Lock()
	c.running = true
	c.mu.Unlock()
	if err := c.RequestDetach(); err != nil {
		t.Fatalf("RequestDetach: %v", err)
	}
	if !c.detachRequestedSignal() {
		t.Fatal("detach flag not set after RequestDetach")
	}
	c.clearDetachRequested()
	if c.detachRequestedSignal() {
		t.Fatal("detach flag not cleared by clearDetachRequested")
	}
}

func TestPerformDetachInvokesRunnerAndEmitsNotice(t *testing.T) {
	var gotPath string
	var gotSpec DetachSpec
	var noticeText string
	c := New(Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice && noticeText == "" {
				noticeText = e.Text
			}
		}),
		DetachRunner: func(ctx context.Context, path string, spec DetachSpec) (string, error) {
			gotPath = path
			gotSpec = spec
			return "detach-1", nil
		},
	})
	c.mu.Lock()
	c.running = true
	c.workspaceRoot = t.TempDir()
	c.mu.Unlock()
	c.setSessionPath(t.TempDir()+"/session.jsonl", false)
	if err := c.performDetach(context.Background(), 0); err != nil {
		t.Fatalf("performDetach: %v", err)
	}
	if gotPath == "" {
		t.Fatal("detach runner was not invoked with a snapshot path")
	}
	if gotSpec.WorkspaceRoot != c.workspaceRoot {
		t.Fatalf("spec.WorkspaceRoot = %q, want %q", gotSpec.WorkspaceRoot, c.workspaceRoot)
	}
	if gotSpec.ParentSession == "" {
		t.Fatal("spec.ParentSession is empty")
	}
	if noticeText == "" {
		t.Fatal("expected a notice event naming the background job")
	}
}

func TestPerformDetachFailsWithoutRunner(t *testing.T) {
	c := New(Options{Sink: event.FuncSink(func(event.Event) {})})
	c.mu.Lock()
	c.running = true
	c.mu.Unlock()
	if err := c.performDetach(context.Background(), 0); err == nil {
		t.Fatal("performDetach without DetachRunner should fail")
	}
}
