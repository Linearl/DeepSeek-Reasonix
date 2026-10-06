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
	// defaultHandshakeTimeout bounds spawn + hello in Start. The health loop's
	// own ping budget is separate (Options.HealthTimeout); this only covers
	// the first handshake so a wedged spawn cannot stall a boot decision.
	defaultHandshakeTimeout = 10 * time.Second

	// subprocess close budgets, mirroring the plugin stdio transport's
	// graceful/kill split. gracefulCloseWait matches the server's own
	// gracefulDrainBudget (design §4: 在途 ≤5s then kill) so an acknowledged
	// base.shutdown gets its full drain before the tree is killed; the kill
	// budget then bounds a wedged child so Close can never hang a caller.
	gracefulCloseWait = 5 * time.Second
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
	// pipe pairs; crash-injection reuses it). It returns the framed
	// transport and a teardown func that is idempotent.
	Dial func(ctx context.Context) (io.ReadWriteCloser, func(), error)

	// LogFile is the subprocess log destination (design F2: logs/base.log).
	// Empty resolves to <REASONIX_HOME>/logs/base/base.log. The child's fd 2
	// becomes that file, so its slog lines and any panic land on disk instead
	// of a console nobody has.
	LogFile string
	// Stderr overrides where the subprocess's stderr goes — tests inject a
	// buffer. Nil resolves LogFile (the production path).
	Stderr io.Writer

	// Lifecycle thresholds (design §4). Zero falls back to the
	// REASONIX_BASE_HEALTH_* environment variable, then to the design
	// defaults; the fields exist so tests can drive the state machine without
	// exporting process-wide env vars.
	HealthInterval     time.Duration // ping period while ready (default 15s)
	HealthTimeout      time.Duration // one ping's budget (default 5s)
	HealthMaxMisses    int           // consecutive misses that kill it (default 2)
	RestartBaseDelay   time.Duration // first backoff step (default 1s)
	RestartMaxDelay    time.Duration // backoff cap (default 30s)
	RestartMaxFailures int           // failures before degraded_inline (default 5)
	DegradedRetry      time.Duration // probe period while degraded (default 5m)
}

// Start returns the BaseClient per R1 and never fails:
//
//   - switch off (the default): inline client, zero side effects (no manager,
//     no goroutine, no log line);
//   - switch on: a Manager spawns the subprocess, runs the hello handshake,
//     and then supervises it (design §4/D5) — health pings, exponential-backoff
//     restart, degraded fallback — returning a refcounted view whose Mode
//     follows the manager's state;
//   - any spawn/handshake failure logs `boot: base fallback` and leaves the
//     view inline (the pre-S1 behaviour); success logs `boot: base remote`.
//
// The view owns one Manager reference: its Close tears the subprocess down
// (design §4 shutdown). Callers that want to share one subprocess across N
// builds hold a Manager and Acquire views from it instead (S1c: the S1b
// "N boots = N subprocesses" convergence).
func Start(ctx context.Context, opts Options) BaseClient {
	if !opts.Enabled {
		return InlineBaseClient{ServerVersion: opts.ServerVersion, Surface: opts.Surface}
	}
	return NewManager(ctx, opts).Acquire(opts)
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
		// F2: the subprocess writes into a dedicated log file, and F1: it is
		// told which one. The path travels in the environment rather than
		// being re-derived so the child never guesses its own log location.
		stderr := resolveStderr(opts)
		env = withBaseLogEnv(env, stderr.path)
		// The exec context must NOT be the handshake context: Start cancels
		// that one right after the handshake, which would kill a healthy
		// subprocess. The process lifetime belongs to RemoteBaseClient.Close
		// (base.shutdown → graceful stdin EOF → bounded kill, design §4) plus
		// the pipe-EOF orphan path (decision D4); Manager.Close also cancels
		// its own baseCtx so an in-flight restart attempt aborts with it.
		cmd := proc.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...)
		cmd.Env = env
		cmd.Stderr = stderr.w

		stdin, err := cmd.StdinPipe()
		if err != nil {
			stderr.cleanup()
			return nil, nil, fmt.Errorf("stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			stderr.cleanup()
			_ = stdin.Close()
			return nil, nil, fmt.Errorf("stdout pipe: %w", err)
		}
		job, err := proc.StartTracked(cmd)
		if err != nil {
			stderr.cleanup()
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, nil, fmt.Errorf("start %s: %w", argv[0], err)
		}
		rw := &procRW{r: stdout, w: stdin}
		return rw, func() {
			// The log file outlives the process only long enough for the
			// reaper to finish: closing it first would swallow a dying
			// child's last lines (and its panic).
			teardownSubprocess(cmd, job, stdin, stdout)
			stderr.cleanup()
		}, nil
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
// S1c status of this process:
//
//   - session lease accounting is ON (AttachSessionAccounting): base.attach
//     now records session_id + root + workspace_scope against the declaring
//     client pid, base.detach releases it, and a later hello sweeps orphans
//     (matrix C3/C4). The root is carried but not yet CONSUMED — see below.
//   - logs/base.log (F2) is owned by the PARENT: cmd.Stderr points at the
//     file, so this process's fd 2 already is the log, including a panic.
//   - parent liveness beyond pipe EOF needs no watchdog: design §4 picks pipe
//     EOF as the Windows detector, and Serve returns a clean nil on it.
//   - NOT DONE: hosting the heavy base (MCP connections, plugin/tool
//     registries, builtin registration, provider factory) behind a
//     workspace-bound surface, and with it the CapTools advertisement. That
//     needs boot's registry construction inside this process plus the
//     base-side ownership review S1b listed (ctx-bound tools, typed errors) —
//     reported as remaining, not attempted here. Until then this process
//     advertises no tools capability and every client gate falls back inline
//     per R1: an empty serve process never serves a wrong catalog.
func RunStdioServer(ctx context.Context, version string, in io.Reader, out io.Writer, errw io.Writer) int {
	s := NewServer(version)
	// The real subprocess always carries the session face: attach/detach lease
	// accounting (design §6, C3/C4) is what tells the base which workspace
	// roots are live. Tests that want the bare S1a core use NewServer directly.
	s.AttachSessionAccounting()
	// 任务 478（F2 落地）：base.log 成为存活记录——就绪一行、退出一行；健康
	// ping 与普通请求不落盘。接线前「0 字节」无法区分「没跑起来」与「跑着但
	// 安静」，这两行把两者分开。
	log := newServeLogger(errw)
	log.ready(version)
	if err := s.Serve(ctx, in, out); err != nil {
		log.exit(err, s.ShutdownRequested())
		fmt.Fprintf(errw, "base serve: %v\n", err)
		return 1
	}
	log.exit(nil, s.ShutdownRequested())
	return 0
}
