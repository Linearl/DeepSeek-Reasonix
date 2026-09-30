package zcodebridge

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"reasonix/internal/proc"
)

// childProcess owns one spawned `app-server --stdio` child. Tree-kill reuses
// the same proc primitives the bus worker runs under (StartTracked Job Object
// with KILL_ON_JOB_CLOSE + TrackTree escapee watch + KillTracked), but without
// RunCommand's foreground wait: a bridge child is long-lived, so Wait happens
// on this side in one goroutine and the exit feeds back through waitCh.
type childProcess struct {
	cmd    *exec.Cmd
	job    uintptr
	tree   *proc.TreeTracker
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer

	waitOnce sync.Once
	waitCh   chan error
}

// startChild spawns the child. The context kills the tree on cancellation:
// a watcher converts ctx.Done into kill so a serve shutdown tears the child
// down even if nobody calls Bridge.Close.
func startChild(ctx context.Context, cfg Config) (*childProcess, error) {
	cmd := proc.Command(cfg.Command, cfg.Args...)
	if cfg.Workspace != "" {
		cmd.Dir = cfg.Workspace
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	p := &childProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: newTailBuffer(stderrTailCap),
		waitCh: make(chan error, 1),
	}
	cmd.Stderr = p.stderr

	job, err := proc.StartTracked(cmd)
	if err != nil {
		// Nothing started (or the fail-closed path already reaped it): release
		// the pipe ends this side holds so no descriptor leaks.
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	p.job = job
	p.tree = proc.TrackTree(cmd)
	go func() { p.waitCh <- cmd.Wait() }()
	go p.watch(ctx)
	return p, nil
}

// watch kills the tree when the owning context is canceled (serve shutdown).
func (p *childProcess) watch(ctx context.Context) {
	select {
	case <-ctx.Done():
		p.kill()
	case <-p.exited():
	}
}

// reap drains waitCh exactly once when the child exits on its own; the job
// handle is released so the OS object does not leak for the process lifetime.
func (p *childProcess) reap() {
	<-p.exited()
	p.treeStop()
}

func (p *childProcess) exited() <-chan error { return p.waitCh }

// kill terminates the process tree: Job Object first (KILL_ON_JOB_CLOSE),
// then the tracked-tree walk for anything that escaped assignment, then the
// direct child — the same layering proc.RunCommand uses.
func (p *childProcess) kill() {
	p.waitOnce.Do(func() {
		proc.KillTracked(p.cmd, p.job)
		if p.tree != nil {
			p.tree.Kill()
			p.treeStop()
		}
		_ = p.stdin.Close()
	})
}

func (p *childProcess) treeStop() {
	if p.tree != nil {
		p.tree.Stop()
	}
}

// tailBuffer keeps the LAST cap bytes of a stream — the region diagnostics
// care about — with bounded memory regardless of child chattiness. Same shape
// as the bus worker's capture tail.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newTailBuffer(capacity int) *tailBuffer { return &tailBuffer{cap: capacity} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.cap {
		t.buf = t.buf[len(t.buf)-t.cap:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
