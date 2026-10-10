package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// Slice S1c — process lifecycle (design §4/D5 + R3 matrix C6).
//
// The S1a manager was one-shot: it dialled once, decided inline or remote, and
// never looked at the subprocess again. This file turns that decision into a
// state machine owned by a Manager:
//
//	inline_warming → remote_ready → degraded_inline → remote_ready
//
// (decision D5, names verbatim). Views handed to boot follow the manager's
// state, so a dead or restarting subprocess degrades every consumer to the
// inline path at once (matrix C6) without any caller having to poll.
//
// Thresholds default from design §4 and are overridable through
// REASONIX_BASE_HEALTH_* environment variables (iron law 2: exposed, not
// hard-coded); Options fields exist so tests do not have to sleep on the
// production defaults.
const (
	defaultHealthInterval    = 15 * time.Second
	defaultHealthTimeout     = 5 * time.Second
	defaultHealthMaxMisses   = 2
	defaultRestartBaseDelay  = 1 * time.Second
	defaultRestartMaxDelay   = 30 * time.Second
	defaultRestartMaxFails   = 5
	defaultDegradedRetry     = 5 * time.Minute
	defaultShutdownRPCBudget = 500 * time.Millisecond
)

// lifecycleThresholds is the resolved configuration for one Manager: defaults
// < environment < Options (Options win so a test can drive the machine without
// exporting process-wide env vars).
type lifecycleThresholds struct {
	healthInterval     time.Duration
	healthTimeout      time.Duration
	healthMaxMisses    int
	restartBaseDelay   time.Duration
	restartMaxDelay    time.Duration
	restartMaxFailures int
	degradedRetry      time.Duration
	shutdownRPCBudget  time.Duration
}

func resolveThresholds(opts Options) lifecycleThresholds {
	th := lifecycleThresholds{
		healthInterval:     envDuration("REASONIX_BASE_HEALTH_INTERVAL", defaultHealthInterval),
		healthTimeout:      envDuration("REASONIX_BASE_HEALTH_TIMEOUT", defaultHealthTimeout),
		healthMaxMisses:    envInt("REASONIX_BASE_HEALTH_MAX_MISSES", defaultHealthMaxMisses),
		restartBaseDelay:   defaultRestartBaseDelay,
		restartMaxDelay:    defaultRestartMaxDelay,
		restartMaxFailures: defaultRestartMaxFails,
		degradedRetry:      defaultDegradedRetry,
		shutdownRPCBudget:  defaultShutdownRPCBudget,
	}
	if opts.HealthInterval > 0 {
		th.healthInterval = opts.HealthInterval
	}
	if opts.HealthTimeout > 0 {
		th.healthTimeout = opts.HealthTimeout
	}
	if opts.HealthMaxMisses > 0 {
		th.healthMaxMisses = opts.HealthMaxMisses
	}
	if opts.RestartBaseDelay > 0 {
		th.restartBaseDelay = opts.RestartBaseDelay
	}
	if opts.RestartMaxDelay > 0 {
		th.restartMaxDelay = opts.RestartMaxDelay
	}
	if opts.RestartMaxFailures > 0 {
		th.restartMaxFailures = opts.RestartMaxFailures
	}
	if opts.DegradedRetry > 0 {
		th.degradedRetry = opts.DegradedRetry
	}
	return th
}

func envDuration(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func envInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// baseState is one node of the D5 state machine.
type baseState int

const (
	// stateWarming: spawn/restart pending — views answer inline (R1).
	stateWarming baseState = iota
	// stateRemoteReady: the subprocess is up and answering health checks.
	stateRemoteReady
	// stateDegraded: restartMaxFailures consecutive failures — long-term
	// inline with a low-frequency probe (design §4: 不再重试打扰用户).
	stateDegraded
	// stateStopped: terminal (design F3 version mismatch) — never retried, so
	// a protocol-skewed pair cannot become a retry storm.
	stateStopped
)

// String returns the design §4/D5 greppable state name.
func (s baseState) String() string {
	switch s {
	case stateRemoteReady:
		return "remote_ready"
	case stateDegraded:
		return "degraded_inline"
	case stateStopped:
		return "inline_stopped"
	default:
		return "inline_warming"
	}
}

// errClientClosed is returned by a ManagedClient after its own Close: the
// manager may still be alive (shared), but this view has released its claim.
var errClientClosed = errors.New("baseproc: client closed")

// Manager owns ONE resident subprocess and its lifecycle (design §4): spawn,
// the health ping loop, exponential-backoff restart, degraded fallback, and
// the shutdown/orphan teardown. Views (ManagedClient) are cheap refcounted
// facades over it, so N consumers can share a single base process — the S1b
// audit note "manager one-shot: N boots = N subprocesses" converges here.
//
// A Manager never fails its caller: construction always yields a usable
// client (inline while not ready), which is R1 restated for the lifecycle
// slice.
type Manager struct {
	opts Options
	log  *slog.Logger
	th   lifecycleThresholds

	mu            sync.Mutex
	state         baseState
	remote        *RemoteBaseClient
	healthySince  time.Time
	nextAttemptAt time.Time
	misses        int
	failures      int
	views         int
	closed        bool
	shutdownSent  bool

	// baseCtx is cancelled by Close so a restart attempt already in flight
	// (dial + handshake, bounded by HandshakeTimeout) aborts instead of
	// holding the shutdown hostage.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	stopOnce sync.Once
	stopCh   chan struct{}
	kickCh   chan struct{}
	doneCh   chan struct{}
}

// NewManager resolves thresholds, performs the initial spawn synchronously
// (bounded by HandshakeTimeout — a wedged spawn must not stall a boot
// decision), and starts the supervision loop. The initial attempt decides the
// starting state; later attempts belong to the loop.
func NewManager(ctx context.Context, opts Options) *Manager {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	baseCtx, baseCancel := context.WithCancel(context.Background())
	m := &Manager{
		opts:       opts,
		log:        log,
		th:         resolveThresholds(opts),
		state:      stateWarming,
		baseCtx:    baseCtx,
		baseCancel: baseCancel,
		stopCh:     make(chan struct{}),
		kickCh:     make(chan struct{}, 1),
		doneCh:     make(chan struct{}),
	}
	// The supervisor must see base.dying itself (R1's early-warning window),
	// so the caller's notification callback is chained behind ours.
	outer := opts.Notify
	m.opts.Notify = func(method string, params json.RawMessage) {
		if method == NotifyDying {
			m.handleDying(params)
			return
		}
		if outer != nil {
			outer(method, params)
		}
	}
	if err := m.spawn(ctx); err != nil {
		log.Warn("boot: base fallback", "reason", err.Error())
		if isVersionMismatch(err) {
			// Design F3: protocol skew is terminal for this process — no
			// retry storm, the caller just stays inline.
			m.setState(stateStopped)
		}
	}
	go m.loop()
	return m
}

// Acquire returns a refcounted view carrying THIS caller's inline fallback
// surface. That split matters once managers are shared (S1c: N boots, one
// subprocess): the subprocess is process-wide, but each boot built its own
// registry for its own workspace root, so the fallback answer a view gives
// when the base is down must be the caller's own — never another tab's.
//
// Every view must be released with ManagedClient.Close.
func (m *Manager) Acquire(opts Options) *ManagedClient {
	m.mu.Lock()
	m.views++
	m.mu.Unlock()
	return &ManagedClient{
		m:      m,
		inline: InlineBaseClient{ServerVersion: opts.ServerVersion, Surface: opts.Surface},
	}
}

// Closed reports whether this manager has been shut down — the shared pool in
// boot uses it to drop a finished manager instead of handing out views on a
// corpse.
func (m *Manager) Closed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// State reports the current D5 node (inline_warming / remote_ready /
// degraded_inline / inline_stopped) — tests and装机读数 read this directly.
func (m *Manager) State() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.String()
}

// Mode is the view-facing answer: remote only while the subprocess is
// actually ready, inline otherwise (matrix C6 — every consumer degrades
// together).
func (m *Manager) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == stateRemoteReady && m.remote != nil {
		return ModeRemote
	}
	return ModeInline
}

func (m *Manager) setState(s baseState) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
}

// spawn opens one connection and completes the handshake. Success installs
// the connection as current; failure leaves the previous state alone and
// counts one failed attempt.
func (m *Manager) spawn(ctx context.Context) error {
	timeout := m.opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	hsCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	remote, err := dialAndHello(hsCtx, m.opts)
	if err != nil {
		if !m.isClosed() {
			// Bookkeeping is skipped when Close raced the attempt: a shutdown
			// is not a spawn failure worth counting or logging.
			if degraded := m.recordFailure(); degraded {
				m.setState(stateDegraded)
			}
		}
		return err
	}
	m.mu.Lock()
	m.remote = remote
	m.state = stateRemoteReady
	m.healthySince = time.Now()
	m.misses = 0
	m.mu.Unlock()
	m.log.Info("boot: base remote",
		"server_version", remote.hello.ServerVersion,
		"protocol", remote.hello.ProtocolVersion)
	go m.watchDead(remote)
	return nil
}

// watchDead reacts the moment the channel dies instead of waiting for the
// next health tick — a crashed subprocess must degrade its consumers now, not
// after 15s of failing calls.
func (m *Manager) watchDead(r *RemoteBaseClient) {
	<-r.c.done
	m.markDead(r, "channel closed")
}

// markDead retires the given connection (only if it is still the current
// one), degrades every view, schedules the restart, and tears the process
// down. Idempotent: the health loop, the death watcher and base.dying can all
// race here.
func (m *Manager) markDead(r *RemoteBaseClient, reason string) {
	m.mu.Lock()
	if m.closed || r == nil || m.remote != r {
		m.mu.Unlock()
		return
	}
	m.remote = nil
	// A connection that survived a full health interval is a healthy base
	// that died once: restart its budget from scratch. A connection that died
	// early is a crash loop: accumulate so the degraded floor is reachable.
	if time.Since(m.healthySince) >= m.th.healthInterval {
		m.failures = 0
	}
	degraded := m.recordFailureLocked()
	m.state = stateWarming
	if degraded {
		m.state = stateDegraded
	}
	m.mu.Unlock()

	m.log.Warn("boot: base fallback", "reason", reason)
	if degraded {
		m.log.Warn("boot: base degraded",
			"failures", m.th.restartMaxFailures,
			"retry_in", m.th.degradedRetry)
	}
	_ = r.Close() // transport first, then the spawner's process teardown
	m.kick()
}

// recordFailure counts one failed spawn/early death and schedules the next
// attempt on the §4 curve (1s → 2s → 4s → … capped at 30s), degrading to the
// low-frequency probe after restartMaxFailures consecutive failures. It
// reports whether that floor was reached (the caller flips the state).
func (m *Manager) recordFailure() bool {
	m.mu.Lock()
	degraded := m.recordFailureLocked()
	m.mu.Unlock()
	if degraded {
		m.log.Warn("boot: base degraded",
			"failures", m.th.restartMaxFailures,
			"retry_in", m.th.degradedRetry)
	}
	return degraded
}

func (m *Manager) recordFailureLocked() bool {
	m.failures++
	m.nextAttemptAt = time.Now().Add(m.backoffLocked(m.failures))
	return m.failures >= m.th.restartMaxFailures
}

// backoffLocked is the exponential curve: base × 2^(n-1), capped, with the
// degraded probe replacing the curve once the failure floor is reached.
func (m *Manager) backoffLocked(failures int) time.Duration {
	if m.state == stateDegraded {
		return m.th.degradedRetry
	}
	if failures < 1 {
		failures = 1
	}
	if failures >= m.th.restartMaxFailures {
		return m.th.degradedRetry
	}
	d := m.th.restartBaseDelay
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= m.th.restartMaxDelay {
			return m.th.restartMaxDelay
		}
	}
	if d > m.th.restartMaxDelay {
		d = m.th.restartMaxDelay
	}
	return d
}

// plan decides how long the loop sleeps before its next action.
func (m *Manager) plan() (baseState, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case stateStopped:
		return stateStopped, 0
	case stateRemoteReady:
		return stateRemoteReady, m.th.healthInterval
	default:
		d := time.Until(m.nextAttemptAt)
		if d < 0 {
			d = 0
		}
		return m.state, d
	}
}

// loop is the single supervision goroutine: it waits out the current state's
// delay (health tick when ready, restart attempt when not) and is woken early
// by base.dying or a channel death via kickCh. Waiting is always re-planned
// after a kick, so an early wake-up can never skip the backoff.
func (m *Manager) loop() {
	defer close(m.doneCh)
	for {
		st, delay := m.plan()
		if st == stateStopped {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-m.stopCh:
			timer.Stop()
			return
		case <-m.kickCh:
			// An early wake-up (base.dying, channel death) only re-plans: the
			// remaining backoff must still elapse before a respawn, or a dying
			// base would be hammered instead of paced.
			timer.Stop()
			continue
		case <-timer.C:
		}
		st, _ = m.plan()
		if st == stateStopped {
			return
		}
		if st == stateRemoteReady {
			m.healthTick()
			continue
		}
		m.restartTick()
	}
}

// healthTick is the design §4 probe: ping under a bounded context;
// healthMaxMisses consecutive misses (default 2) retire the connection.
func (m *Manager) healthTick() {
	m.mu.Lock()
	r := m.remote
	m.mu.Unlock()
	if r == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.th.healthTimeout)
	defer cancel()
	if err := r.Ping(ctx); err != nil {
		m.mu.Lock()
		m.misses++
		misses := m.misses
		m.mu.Unlock()
		m.log.Warn("boot: base health miss", "misses", misses, "reason", err.Error())
		if misses >= m.th.healthMaxMisses {
			m.markDead(r, "health: "+err.Error())
		}
		return
	}
	m.mu.Lock()
	m.misses = 0
	if time.Since(m.healthySince) >= m.th.healthInterval {
		m.failures = 0 // stability credit: the base proved it can live
	}
	m.mu.Unlock()
}

// restartTick attempts one spawn for a non-ready state. The delay before it
// was already enforced by plan(); here the outcome decides the next one.
func (m *Manager) restartTick() {
	m.mu.Lock()
	closed := m.closed
	attempt := m.failures + 1
	m.mu.Unlock()
	if closed {
		return
	}
	m.log.Info("boot: base restart attempt", "attempt", attempt)
	if err := m.spawn(m.baseCtx); err != nil {
		if m.isClosed() {
			return // shutdown raced the attempt: not a failure worth logging
		}
		m.log.Warn("boot: base fallback", "reason", err.Error())
		if isVersionMismatch(err) {
			// F3 during a restart: the pair is skewed, stop trying.
			m.setState(stateStopped)
		}
	}
}

// isClosed reports whether Close already ran.
func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// handleDying consumes base.dying (design §5): the subprocess is announcing
// its own exit, so degrade immediately instead of waiting out the ping budget
// — the window that lets consumers fall back before calls start failing.
func (m *Manager) handleDying(params json.RawMessage) {
	var p DyingParams
	reason := "server announced shutdown"
	if err := json.Unmarshal(params, &p); err == nil && p.Reason != "" {
		reason = p.Reason
	}
	m.log.Warn("boot: base dying", "reason", reason)
	m.mu.Lock()
	r := m.remote
	m.mu.Unlock()
	m.markDead(r, "dying: "+reason)
}

func (m *Manager) kick() {
	select {
	case m.kickCh <- struct{}{}:
	default:
	}
}

// markShutdownSent records that a base.shutdown was already answered, so a
// later Close never repeats it (the server answers -32002 after the first
// one, and the second call would only burn the shutdown budget).
func (m *Manager) markShutdownSent() {
	m.mu.Lock()
	m.shutdownSent = true
	m.mu.Unlock()
}

// release drops one view; the last release performs the §4 shutdown.
func (m *Manager) release() {
	m.mu.Lock()
	if m.views > 0 {
		m.views--
	}
	last := m.views == 0
	m.mu.Unlock()
	if last {
		_ = m.Close()
	}
}

// Close stops supervision and tears the subprocess down: best-effort
// base.shutdown (design §4 — in-flight work gets the server's drain budget),
// then the bounded graceful/kill teardown. Safe to call more than once.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	r := m.remote
	m.remote = nil
	alreadySent := m.shutdownSent
	m.state = stateStopped
	m.mu.Unlock()

	m.baseCancel() // abort a restart attempt already in flight
	m.stopOnce.Do(func() { close(m.stopCh) })

	var err error
	if r != nil {
		if !alreadySent {
			ctx, cancel := context.WithTimeout(context.Background(), m.th.shutdownRPCBudget)
			if shutdownErr := r.Shutdown(ctx); shutdownErr != nil {
				// A base that will not acknowledge still gets torn down; the
				// teardown below is the authority, not this nicety.
				m.log.Debug("boot: base shutdown no-ack", "reason", shutdownErr.Error())
			} else {
				m.markShutdownSent()
			}
			cancel()
		}
		err = r.Close()
	}
	<-m.doneCh
	return err
}

// isVersionMismatch reports the F3 trigger: the handshake failed because the
// peers share no protocol version (or the server negotiated above us).
func isVersionMismatch(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == CodeVersionMismatch
}

// ManagedClient is the refcounted view boot receives from Start: every call
// resolves the manager's current target (remote while ready, the inline
// fallback otherwise) so a caller holds one stable BaseClient reference
// across restarts. Closing releases the view; the subprocess lives until the
// last view closes.
type ManagedClient struct {
	m *Manager
	// inline is THIS view's fallback: the registry its own boot built for its
	// own workspace root. Shared managers must never answer from another
	// view's surface.
	inline InlineBaseClient

	once sync.Once
	mu   sync.Mutex
	done bool
}

// target resolves the client one call delegates to. The pointer is captured
// once per call so an in-flight call keeps talking to the connection it
// started on (a mid-call swap would risk a double execution).
//
// 任务521 nil 防线：任何 nil 组合都不得 panic，且一律 fail-closed 回落
// inline/本地路径。逐组合矩阵（inline = c.inline，各态指 manager 字段）：
//
//	#  receiver  m       done   state          remote    返回
//	1  nil       —       —      —              —         (nil, errClientClosed)：无本地面可回落，错误向上传播
//	2  非 nil    nil     false  —              —         (inline, nil)：半构造视图回落本地（修复前 panic）
//	3  非 nil    nil     true   —              —         (nil, errClientClosed)
//	4  非 nil    非 nil  true   —              —         (nil, errClientClosed)
//	5  非 nil    非 nil  false  remote_ready   非 nil    (remote, nil)：正常远端
//	6  非 nil    非 nil  false  remote_ready   nil       (inline, nil)：markDead 竞态窗口，回落本地
//	7  非 nil    非 nil  false  其余任意态      任意      (inline, nil)
//
// 回落语义：inline 为值类型，装箱后接口恒非 nil——target() 绝不返回
// (nil, nil)。零值 inline（Surface nil）的工具面答 ErrNotWired，消费方
// （agent baseToolCall / control baseCatalogEntries）经既有 ErrNotWired
// 分支转本地注册表路径；带 Surface 就地本地执行。不设 recover：确定性
// 守卫逐组合可证，recover 会把未来真实缺陷静默成降级。
func (c *ManagedClient) target() (BaseClient, error) {
	if c == nil {
		return nil, errClientClosed
	}
	c.mu.Lock()
	closed := c.done
	inline := c.inline
	c.mu.Unlock()
	if closed {
		return nil, errClientClosed
	}
	m := c.m
	if m == nil {
		// 任务521：半构造视图（manager 缺失）——fail-closed 回落本地面，
		// 绝不 panic（修复前此处 c.m.mu.Lock() nil 解引用）。
		return inline, nil
	}
	// One critical section decides remote vs inline so the answer cannot go
	// stale between the check and the hand-off.
	m.mu.Lock()
	if m.state == stateRemoteReady && m.remote != nil {
		remote := m.remote
		m.mu.Unlock()
		return remote, nil
	}
	m.mu.Unlock()
	return inline, nil
}

// Mode implements BaseClient (delegates to the manager's D5 state).
// 任务521：nil receiver / nil manager 答 inline——消费门（先 Mode 后调用）
// 在第一个接触点就 fail-closed 走本地路径，而非 panic。
func (c *ManagedClient) Mode() Mode {
	if c == nil || c.m == nil {
		return ModeInline
	}
	return c.m.Mode()
}

// State exposes the manager state for logs/tests.
// 任务521：nil receiver / nil manager 答 inline_warming（读数面，不 panic）。
func (c *ManagedClient) State() string {
	if c == nil || c.m == nil {
		return stateWarming.String()
	}
	return c.m.State()
}

// Hello implements BaseClient.
func (c *ManagedClient) Hello(ctx context.Context, params HelloParams) (HelloResult, error) {
	t, err := c.target()
	if err != nil {
		return HelloResult{}, err
	}
	return t.Hello(ctx, params)
}

// Ping implements BaseClient.
func (c *ManagedClient) Ping(ctx context.Context) error {
	t, err := c.target()
	if err != nil {
		return err
	}
	return t.Ping(ctx)
}

// Attach implements BaseClient.
func (c *ManagedClient) Attach(ctx context.Context, params AttachParams) (AttachResult, error) {
	t, err := c.target()
	if err != nil {
		return AttachResult{}, err
	}
	return t.Attach(ctx, params)
}

// Detach implements BaseClient.
func (c *ManagedClient) Detach(ctx context.Context, params DetachParams) (DetachResult, error) {
	t, err := c.target()
	if err != nil {
		return DetachResult{}, err
	}
	return t.Detach(ctx, params)
}

// ToolCatalog implements BaseClient.
func (c *ManagedClient) ToolCatalog(ctx context.Context, params ToolCatalogParams) (ToolCatalogResult, error) {
	t, err := c.target()
	if err != nil {
		return ToolCatalogResult{}, err
	}
	return t.ToolCatalog(ctx, params)
}

// ToolCall implements BaseClient. The target is captured once: a call already
// on the wire never reruns against the inline fallback (no double execution),
// it just fails if that connection dies mid-flight.
func (c *ManagedClient) ToolCall(ctx context.Context, params ToolCallParams) (ToolCallResult, error) {
	t, err := c.target()
	if err != nil {
		return ToolCallResult{}, err
	}
	return t.ToolCall(ctx, params)
}

// ProviderResolve implements BaseClient.
func (c *ManagedClient) ProviderResolve(ctx context.Context, params ProviderResolveParams) (ProviderResolveResult, error) {
	t, err := c.target()
	if err != nil {
		return ProviderResolveResult{}, err
	}
	return t.ProviderResolve(ctx, params)
}

// Shutdown implements BaseClient: an explicit base.shutdown is recorded so
// the later Close does not repeat it.
func (c *ManagedClient) Shutdown(ctx context.Context) error {
	t, err := c.target()
	if err != nil {
		return err
	}
	if err := t.Shutdown(ctx); err != nil {
		return err
	}
	// 任务521：nil manager 视图无记账可做（记账依赖 manager 存活）——跳过，
	// 不解引用（修复 target 后此处会暴露为新的 panic 点）。
	if c.m != nil {
		c.m.markShutdownSent()
	}
	return nil
}

// Close implements BaseClient: idempotent release of this view. The
// subprocess is torn down when the last view closes.
// 任务521：nil receiver / nil manager 视图无引用可释放——幂等空操作。
func (c *ManagedClient) Close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		c.mu.Lock()
		c.done = true
		c.mu.Unlock()
		if c.m != nil {
			c.m.release()
		}
	})
	return nil
}

var _ BaseClient = (*ManagedClient)(nil)
