package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"reasonix/internal/proc"
)

const (
	// defaultHandshakeTimeout bounds spawn + hello in Start. The 15s health
	// ping loop and its env-tunable thresholds are S1c; this only covers the
	// first handshake so a wedged spawn cannot stall a boot decision.
	defaultHandshakeTimeout = 10 * time.Second

	// subprocess close budgets, mirroring the plugin stdio transport's
	// graceful/kill split. The full §4 shutdown state machine (in-flight
	// ToolCall drain, orphan watchdog) is S1c; these bounds keep S1a Close
	// from wedging a caller.
	gracefulCloseWait = 750 * time.Millisecond
	killCloseWait     = 5 * time.Second
)

// Options configures Start.
type Options struct {
	// Enabled mirrors config experimental_base_process (design R4). false —
	// the default — returns an InlineBaseClient and touches nothing: the
	// pre-S1 behaviour byte for byte.
	Enabled bool
	// ServerVersion is the local build identity used by inline hello answers.
	ServerVersion string
	// Command spawns the subprocess (decision D4). Empty defaults to the
	// current executable with `base serve --stdio` — no new binary, no new
	// signing surface.
	Command []string
	// Env is the subprocess environment. Empty inherits os.Environ()
	// explicitly (design F1: REASONIX_HOME and friends must not depend on
	// implicit cwd propagation).
	Env []string
	// HandshakeTimeout bounds spawn + hello. Zero uses defaultHandshakeTimeout.
	HandshakeTimeout time.Duration
	// Surface backs the inline fallback's tool face (S1b): boot hands over the
	// registry it just built so an inline client answers toolCatalog/toolCall
	// with the same functions the pre-S1 path uses (R1). Nil keeps the S1a
	// ErrNotWired semantics.
	Surface ToolSurface
	// Log receives the fallback/remote decisions. Nil uses slog.Default().
	Log *slog.Logger
	// Notify receives server→client notifications from the remote path. Nil
	// drops them (the v1 core server emits none; base.dying handling is S1c).
	Notify func(method string, params json.RawMessage)
	// Dial, when set, replaces subprocess spawning entirely (tests inject
	// pipe pairs; S1c crash-injection reuses it). It returns the framed
	// transport and a teardown func that is idempotent.
	Dial func(ctx context.Context) (io.ReadWriteCloser, func(), error)
}

// Start returns the BaseClient per R1 and never fails:
//
//   - switch off (the default): inline client, zero side effects;
//   - switch on: spawn the subprocess, run the hello handshake; any failure
//     (spawn error, timeout, protocol/version mismatch) logs `boot: base
//     fallback` and returns inline — the pre-S1 behaviour;
//   - success logs `boot: base remote` (decision D5's grep-able family).
//
// Restart/backoff/degraded transitions and the periodic health loop are S1c
// (TODO); the S1a manager is one-shot: it decides once, inline or remote.
func Start(ctx context.Context, opts Options) BaseClient {
	inline := InlineBaseClient{ServerVersion: opts.ServerVersion, Surface: opts.Surface}
	if !opts.Enabled {
		return inline
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	timeout := opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	hsCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	remote, err := dialAndHello(hsCtx, opts)
	if err != nil {
		log.Warn("boot: base fallback", "reason", err.Error())
		return inline
	}
	log.Info("boot: base remote",
		"server_version", remote.hello.ServerVersion,
		"protocol", remote.hello.ProtocolVersion)
	return remote
}

// dialAndHello spawns (or dials) the base and completes the handshake. On any
// error the connection and subprocess are torn down before returning.
func dialAndHello(ctx context.Context, opts Options) (*RemoteBaseClient, error) {
	dial := opts.Dial
	if dial == nil {
		dial = subprocessDial(opts)
	}
	rw, closeProc, err := dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("spawn base subprocess: %w", err)
	}
	c := newConn(rw, opts.Notify)
	r := newRemoteClient(c, closeProc)
	if _, helloErr := r.Hello(ctx, HelloParams{
		ProtocolVersion: ProtocolVersion,
		ClientPID:       os.Getpid(),
	}); helloErr != nil {
		_ = r.Close() // tears down the channel and the subprocess
		return nil, fmt.Errorf("base handshake: %w", helloErr)
	}
	return r, nil
}

// serveArgv resolves the spawn argv: an explicit command passes through; the
// default is the current executable re-run as `base serve --stdio` (decision
// D4 — same binary, no new build/signing surface).
func serveArgv(command []string) ([]string, error) {
	if len(command) > 0 {
		return command, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	return []string{exe, "base", "serve", "--stdio"}, nil
}

// subprocessDial builds the real spawn path: the current (or configured)
// binary run as `base serve --stdio` (decision D4), env explicitly inherited
// (F1), window hidden, process tracked so teardown can kill the tree.
func subprocessDial(opts Options) func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
	return func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
		argv, err := serveArgv(opts.Command)
		if err != nil {
			return nil, nil, err
		}
		env := opts.Env
		if env == nil {
			env = os.Environ()
		}
		// The exec context must NOT be the handshake context: Start cancels
		// that one right after the handshake, which would kill a healthy
		// subprocess. The process lifetime belongs to RemoteBaseClient.Close
		// (graceful stdin EOF, bounded kill) plus the pipe-EOF orphan path
		// (decision D4); ctx-cleanup hardening is S1c.
		cmd := proc.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...)
		cmd.Env = env
		cmd.Stderr = os.Stderr // S1a: inherit; logs/base.log (F2) is S1c

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, fmt.Errorf("stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, nil, fmt.Errorf("stdout pipe: %w", err)
		}
		job, err := proc.StartTracked(cmd)
		if err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, nil, fmt.Errorf("start %s: %w", argv[0], err)
		}
		rw := &procRW{r: stdout, w: stdin}
		return rw, func() { teardownSubprocess(cmd, job, stdin, stdout) }, nil
	}
}

// teardownSubprocess closes the protocol channel (stdin EOF is the graceful
// signal the serve loop exits on), then bounds the wait and hard-kills the
// tracked tree — the plugin stdio transport's proven split.
func teardownSubprocess(cmd *exec.Cmd, job uintptr, stdin, stdout io.Closer) {
	_ = stdin.Close()
	waited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(gracefulCloseWait):
		proc.KillTracked(cmd, job)
		select {
		case <-waited:
		case <-time.After(killCloseWait):
			// Leave the reaper goroutine to finish on its own; never wedge a
			// caller on a wedged child.
		}
	}
	proc.FinishTracked(job)
	_ = stdout.Close()
}

// procRW adapts the subprocess pipe pair to the framed channel.
type procRW struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p *procRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *procRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *procRW) Close() error {
	return errors.Join(p.r.Close(), p.w.Close())
}

// RunStdioServer is the subprocess main loop behind `reasonix base serve
// --stdio` (decision D4): no GUI registration, no session locks, the v1 core
// protocol on the given streams until EOF. stdout is protocol-pure — any
// stray write corrupts framing — so diagnostics go to errw only.
//
// Parent death surfaces as stdin EOF (decision D4's Windows orphan path),
// which Serve already answers with a clean nil and exit code 0.
//
// TODO(S1b): host the heavy base here — MCP connections, plugin/tool
// registries, builtin registration, provider factory — and register the tool
// surface handlers + capabilities.
// TODO(S1c): logs/base.log (F2, subprocess panic must land in a file),
// liveness tick, and the parent-liveness watchdog beyond pipe EOF.
func RunStdioServer(ctx context.Context, version string, in io.Reader, out io.Writer, errw io.Writer) int {
	s := NewServer(version)
	if err := s.Serve(ctx, in, out); err != nil {
		fmt.Fprintf(errw, "base serve: %v\n", err)
		return 1
	}
	return 0
}
