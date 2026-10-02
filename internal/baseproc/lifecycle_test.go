package baseproc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// spawnRecorder is a Dial double for lifecycle tests: it serves a fresh v1
// server per spawn (so a restart really gets a new connection), records every
// attempt and every teardown, and can inject spawn failures or kill a live
// connection. Design §4's crash injection rides the seam Options.Dial already
// exposed in S1a.
type spawnRecorder struct {
	mu       sync.Mutex
	calls    int
	failures int // dial errors to inject on the first N attempts
	prepare  func(s *Server, n int)

	servers  []*Server
	spawns   []*spawnAttempt
	teardown int
}

type spawnAttempt struct {
	clientIn  io.ReadCloser // client read end (pipedRW.r)
	clientOut io.WriteCloser
	serverIn  io.ReadCloser
	serverOut io.WriteCloser
	done      chan error

	waitOnce sync.Once
	waitErr  error
}

// wait blocks until this spawn's Serve loop exits, at most timeout. It is
// idempotent: the result is memoised, so a teardown that already collected it
// and a later cleanup asking again do not each pay the timeout.
func (a *spawnAttempt) wait(timeout time.Duration) error {
	a.waitOnce.Do(func() {
		select {
		case err := <-a.done:
			a.waitErr = err
		case <-time.After(timeout):
			a.waitErr = fmt.Errorf("serve loop did not exit within %s", timeout)
		}
	})
	return a.waitErr
}

func (r *spawnRecorder) dial(ctx context.Context) (io.ReadWriteCloser, func(), error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	fail := n <= r.failures
	prepare := r.prepare
	r.mu.Unlock()
	if fail {
		return nil, nil, fmt.Errorf("spawn %d: injected failure", n)
	}

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	s := NewServer("spawn-recorder")
	if prepare != nil {
		prepare(s, n)
	}
	attempt := &spawnAttempt{
		clientIn:  clientIn,
		clientOut: clientOut,
		serverIn:  serverIn,
		serverOut: serverOut,
		done:      make(chan error, 1),
	}
	go func() { attempt.done <- s.Serve(context.Background(), serverIn, serverOut) }()

	r.mu.Lock()
	r.servers = append(r.servers, s)
	r.spawns = append(r.spawns, attempt)
	r.mu.Unlock()

	teardown := func() {
		r.mu.Lock()
		r.teardown++
		r.mu.Unlock()
		attempt.close()
		_ = attempt.wait(5 * time.Second)
	}
	return &pipedRW{r: clientIn, w: clientOut}, teardown, nil
}

func (a *spawnAttempt) close() {
	_ = a.clientOut.Close()
	_ = a.clientIn.Close()
	_ = a.serverIn.Close()
	_ = a.serverOut.Close()
}

// kill severs spawn n's pipes, which is what a crashed subprocess looks like
// from the client side: the read loop hits EOF and the channel finishes.
func (r *spawnRecorder) kill(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n-1 < len(r.spawns) {
		r.spawns[n-1].close()
	}
}

func (r *spawnRecorder) counts() (calls, teardowns int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.teardown
}

func (r *spawnRecorder) server(n int) *Server {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n-1 >= len(r.servers) {
		return nil
	}
	return r.servers[n-1]
}

// serveSurface makes every spawned server advertise CapTools backed by a
// one-tool registry, so a successful round trip proves the answer came from
// the subprocess rather than the client's inline surface.
func (r *spawnRecorder) serveSurface(name string) {
	r.prepare = func(s *Server, n int) {
		s.AttachToolSurface(&RegistrySurface{Reg: newStubRegistry(&stubTool{name: name, readOnly: true})})
	}
}

// cleanup closes every spawn still alive so the test cannot leak pipes or
// leave a Serve loop spinning after the assertions are done.
func (r *spawnRecorder) cleanup() {
	r.mu.Lock()
	spawns := append([]*spawnAttempt(nil), r.spawns...)
	r.mu.Unlock()
	for _, a := range spawns {
		a.close()
	}
	for _, a := range spawns {
		_ = a.wait(5 * time.Second)
	}
}

// lifecycleHarness builds a Manager with test-sized thresholds and a logger
// whose buffer captures every D5 decision line.
type lifecycleHarness struct {
	rec   *spawnRecorder
	mgr   *Manager
	opts  Options
	lines func() string
}

// newLifecycleHarness wires a recorder and fast thresholds, lets the test
// mutate both before the manager starts (so the very first spawn sees the
// setup), and guarantees teardown at test end.
func newLifecycleHarness(t *testing.T, setup func(rec *spawnRecorder, opts *Options)) *lifecycleHarness {
	t.Helper()
	log, buf := newTestLogger()
	rec := &spawnRecorder{}
	opts := Options{
		Enabled:          true,
		ServerVersion:    "v-parent",
		Log:              log,
		Dial:             rec.dial,
		HealthInterval:   50 * time.Millisecond,
		HealthTimeout:    20 * time.Millisecond,
		HealthMaxMisses:  2,
		RestartBaseDelay: 10 * time.Millisecond,
		RestartMaxDelay:  40 * time.Millisecond,
		DegradedRetry:    time.Hour, // never reached unless a test asks for it
	}
	if setup != nil {
		setup(rec, &opts)
	}
	h := &lifecycleHarness{
		rec:   rec,
		opts:  opts,
		lines: func() string { return buf.String() },
	}
	h.mgr = NewManager(context.Background(), opts)
	t.Cleanup(func() {
		_ = h.mgr.Close()
		rec.cleanup()
	})
	return h
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// firstTool returns the first tool name a ToolCatalog query answers with. It
// is the discriminator between the two surfaces: the subprocess serves
// "alpha", the inline fallback serves "inline-only".
func firstTool(t *testing.T, c BaseClient) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.ToolCatalog(ctx, ToolCatalogParams{Scope: ScopeAll})
	if err != nil {
		return "", err
	}
	if len(res.Tools) == 0 {
		return "", fmt.Errorf("catalog returned no tools")
	}
	return res.Tools[0].Name, nil
}

func inlineOnlySurface() ToolSurface {
	return &RegistrySurface{Reg: newStubRegistry(&stubTool{name: "inline-only"})}
}

func TestManagerStartsRemoteAndServesCatalogFromSubprocess(t *testing.T) {
	// 设计 §4 spawn 成功 → remote_ready；工具目录经通道由子进程回答（不是
	// 客户端自己的 inline 面），证明 Mode()==remote 时委托真的落到连接上。
	h := newLifecycleHarness(t, func(rec *spawnRecorder, opts *Options) {
		opts.Surface = inlineOnlySurface()
		rec.serveSurface("alpha")
	})

	if mode := h.mgr.Mode(); mode != ModeRemote {
		t.Fatalf("mode = %q, want remote (state %s)", mode, h.mgr.State())
	}
	if state := h.mgr.State(); state != "remote_ready" {
		t.Fatalf("state = %q, want remote_ready", state)
	}
	got, err := firstTool(t, h.mgr.Acquire())
	if err != nil {
		t.Fatalf("remote catalog: %v", err)
	}
	if got != "alpha" {
		t.Fatalf("catalog = %q, want alpha (the subprocess surface)", got)
	}
	if !strings.Contains(h.lines(), "boot: base remote") {
		t.Fatalf("log = %q, want boot: base remote", h.lines())
	}
}

func TestManagerChannelDeathDegradesViewsThenRespawns(t *testing.T) {
	// 矩阵 C6：子进程死 → 全部视图立即 inline（不等心跳）；退避后重启成功 →
	// remote_ready 恢复。目录来源随之切换，证明委托真的换了对象。
	h := newLifecycleHarness(t, func(rec *spawnRecorder, opts *Options) {
		opts.Surface = inlineOnlySurface()
		rec.serveSurface("alpha")
		opts.RestartBaseDelay = time.Second // keep the inline window observable
		opts.RestartMaxDelay = time.Second
	})

	view := h.mgr.Acquire()
	defer view.Close()
	if got, err := firstTool(t, view); err != nil || got != "alpha" {
		t.Fatalf("catalog before death = %q/%v, want alpha/nil", got, err)
	}

	h.rec.kill(1)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && h.mgr.Mode() != ModeInline {
		time.Sleep(5 * time.Millisecond)
	}
	if h.mgr.Mode() != ModeInline {
		t.Fatalf("mode = %q (state %s) 3s after killing the channel, want inline; log = %q",
			h.mgr.Mode(), h.mgr.State(), h.lines())
	}
	if got, err := firstTool(t, view); err != nil || got != "inline-only" {
		t.Fatalf("catalog while degraded = %q/%v, want inline-only/nil (C6 fallback)", got, err)
	}

	waitFor(t, 6*time.Second, "respawn back to remote_ready", func() bool {
		return h.mgr.State() == "remote_ready"
	})
	calls, _ := h.rec.counts()
	if calls < 2 {
		t.Fatalf("spawn attempts = %d, want >= 2 (death must trigger a restart)", calls)
	}
	if got, err := firstTool(t, view); err != nil || got != "alpha" {
		t.Fatalf("catalog after respawn = %q/%v, want alpha/nil", got, err)
	}
	if !strings.Contains(h.lines(), "boot: base fallback") {
		t.Fatalf("log = %q, want boot: base fallback on death", h.lines())
	}
}

func TestManagerGivesUpToDegradedAfterMaxFailures(t *testing.T) {
	// 设计 §4：连续 restartMaxFailures 次失败 → degraded_inline，转入低频再探，
	// 不再轰炸用户。
	h := newLifecycleHarness(t, func(rec *spawnRecorder, opts *Options) {
		opts.Surface = inlineOnlySurface()
		rec.failures = 2 // the initial spawn and the first restart both fail
		opts.RestartMaxFailures = 2
		opts.RestartBaseDelay = 5 * time.Millisecond
		opts.DegradedRetry = time.Hour
	})

	waitFor(t, 3*time.Second, "degraded_inline state", func() bool {
		return h.mgr.State() == "degraded_inline"
	})
	if mode := h.mgr.Mode(); mode != ModeInline {
		t.Fatalf("mode = %q while degraded, want inline", mode)
	}
	if !strings.Contains(h.lines(), "boot: base degraded") {
		t.Fatalf("log = %q, want boot: base degraded", h.lines())
	}
	calls, _ := h.rec.counts()
	time.Sleep(150 * time.Millisecond)
	after, _ := h.rec.counts()
	if after != calls {
		t.Fatalf("spawn attempts moved %d -> %d while degraded (retry_in must be %s, not immediate)",
			calls, after, h.opts.DegradedRetry)
	}
}

func TestManagerBaseDyingNotificationDegradesImmediately(t *testing.T) {
	// 设计 §5 base.dying = 子进程提前告警窗口（R1 缓解）：不必等心跳预算，
	// 收到即降级。
	h := newLifecycleHarness(t, func(rec *spawnRecorder, opts *Options) {
		opts.Surface = inlineOnlySurface()
		rec.serveSurface("alpha")
		opts.HealthInterval = time.Hour // the ping loop must not be what degrades
		opts.RestartBaseDelay = time.Second
		opts.RestartMaxDelay = time.Second
	})

	s := h.rec.server(1)
	if s == nil {
		t.Fatal("spawn 1 has no server handle")
	}
	if err := s.Notify(NotifyDying, DyingParams{Reason: "self test exit"}); err != nil {
		t.Fatalf("send base.dying: %v", err)
	}

	waitFor(t, 3*time.Second, "inline after base.dying", func() bool {
		return h.mgr.Mode() == ModeInline
	})
	if !strings.Contains(h.lines(), "boot: base dying") {
		t.Fatalf("log = %q, want boot: base dying", h.lines())
	}
	if !strings.Contains(h.lines(), "self test exit") {
		t.Fatalf("log = %q, want the dying reason carried through", h.lines())
	}
}

func TestManagerTwoHealthMissesRetireConnection(t *testing.T) {
	// 设计 §4 health-check：连续 healthMaxMisses 次超时判死。这里让子进程的
	// ping 永不回答，两个预算窗口后必须降级（装机上对应「进程活着但卡住」）。
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	h := newLifecycleHarness(t, func(rec *spawnRecorder, opts *Options) {
		opts.Surface = inlineOnlySurface()
		opts.HealthInterval = 10 * time.Millisecond
		opts.HealthTimeout = 15 * time.Millisecond
		opts.HealthMaxMisses = 2
		opts.RestartBaseDelay = 250 * time.Millisecond // keep the inline window wide
		opts.RestartMaxDelay = 250 * time.Millisecond
		rec.prepare = func(s *Server, n int) {
			s.AttachToolSurface(&RegistrySurface{Reg: newStubRegistry(&stubTool{name: "alpha"})})
			s.Register(MethodPing, func(ctx context.Context, _ json.RawMessage) (any, error) {
				select {
				case <-block:
					return nil, fmt.Errorf("blocked ping released")
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
		}
	})

	waitFor(t, 3*time.Second, "degradation after two health misses", func() bool {
		return h.mgr.Mode() == ModeInline
	})
	if !strings.Contains(h.lines(), "boot: base health miss") {
		t.Fatalf("log = %q, want boot: base health miss", h.lines())
	}
	if !strings.Contains(h.lines(), "health:") {
		t.Fatalf("log = %q, want the health reason on the fallback line", h.lines())
	}
}

func TestManagerSharedSubprocessTearsDownOnLastViewOnly(t *testing.T) {
	// S1b 遗留收敛：N 视图共享一个子进程，最后一个视图关闭才拆除（§4）。
	h := newLifecycleHarness(t, func(rec *spawnRecorder, opts *Options) {
		opts.Surface = inlineOnlySurface()
		rec.serveSurface("alpha")
	})

	first := h.mgr.Acquire()
	second := h.mgr.Acquire()

	if err := first.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if _, teardowns := h.rec.counts(); teardowns != 0 {
		t.Fatalf("teardowns = %d after first view closed, want 0 (still one view)", teardowns)
	}
	if mode := h.mgr.Mode(); mode != ModeRemote {
		t.Fatalf("mode = %q after first view closed, want remote", mode)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second close of the same view: %v", err)
	}

	if err := second.Close(); err != nil {
		t.Fatalf("second view close: %v", err)
	}
	waitFor(t, 3*time.Second, "teardown after the last view", func() bool {
		_, teardowns := h.rec.counts()
		return teardowns == 1
	})
	if state := h.mgr.State(); state != "inline_stopped" {
		t.Fatalf("state = %q after last close, want inline_stopped", state)
	}
	if _, teardowns := h.rec.counts(); teardowns != 1 {
		t.Fatalf("teardowns = %d, want exactly 1 (Close is idempotent)", teardowns)
	}
	if err := second.Ping(context.Background()); err == nil {
		t.Fatal("ping on a closed view succeeded, want errClientClosed")
	}
}

func TestManagerVersionMismatchStopsRetrying(t *testing.T) {
	// 设计 F3：协议错配 fallback 后不重试轰炸——拨号必须恰好一次，且状态
	// 落在终态（不是 warming，否则退避循环还在跑）。
	log, buf := newTestLogger()
	rec := &spawnRecorder{prepare: func(s *Server, n int) {
		s.Register(MethodHello, func(context.Context, json.RawMessage) (any, error) {
			return nil, &RPCError{Code: CodeVersionMismatch, Message: "protocol too old"}
		})
	}}
	mgr := NewManager(context.Background(), Options{
		Enabled:          true,
		ServerVersion:    "v-parent",
		Log:              log,
		Dial:             rec.dial,
		RestartBaseDelay: 5 * time.Millisecond,
		RestartMaxDelay:  10 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = mgr.Close()
		rec.cleanup()
	})

	if mode := mgr.Mode(); mode != ModeInline {
		t.Fatalf("mode = %q, want inline on version mismatch", mode)
	}
	if state := mgr.State(); state != "inline_stopped" {
		t.Fatalf("state = %q, want inline_stopped (terminal)", state)
	}
	if n := strings.Count(buf.String(), "boot: base fallback"); n != 1 {
		t.Fatalf("fallback logged %d times, want exactly 1", n)
	}
	time.Sleep(200 * time.Millisecond) // 20x the backoff: nothing may retry
	if calls, _ := rec.counts(); calls != 1 {
		t.Fatalf("dial attempts = %d after 200ms, want 1 (F3: no retry storm)", calls)
	}
	if n := strings.Count(buf.String(), "boot: base fallback"); n != 1 {
		t.Fatalf("fallback logged %d times after the wait, want exactly 1", n)
	}
}

func TestManagerBackoffCurveAndDegradedProbe(t *testing.T) {
	// 纯函数钉死设计 §4 的退避曲线：1s → 2s → 4s → … 封顶 30s，达阈值改走
	// 低频再探。时序不由真实 sleep 验证（会 flaky），由曲线本身验证。
	m := &Manager{th: lifecycleThresholds{
		restartBaseDelay:   time.Second,
		restartMaxDelay:    30 * time.Second,
		restartMaxFailures: 10,
		degradedRetry:      5 * time.Minute,
	}}
	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	for i, expected := range want {
		if got := m.backoffLocked(i + 1); got != expected {
			t.Fatalf("backoff(%d) = %s, want %s", i+1, got, expected)
		}
	}
	if got := m.backoffLocked(m.th.restartMaxFailures); got != m.th.degradedRetry {
		t.Fatalf("backoff at the failure floor = %s, want the degraded probe %s", got, m.th.degradedRetry)
	}
	m.state = stateDegraded
	if got := m.backoffLocked(1); got != m.th.degradedRetry {
		t.Fatalf("backoff while degraded = %s, want %s", got, m.th.degradedRetry)
	}
}

func TestThresholdsResolveEnvironmentOverDefaults(t *testing.T) {
	// 铁律 2：阈值不是硬编码——env 覆盖默认，Options 覆盖 env（测试可驱动）。
	t.Setenv("REASONIX_BASE_HEALTH_INTERVAL", "3s")
	t.Setenv("REASONIX_BASE_HEALTH_MAX_MISSES", "7")
	th := resolveThresholds(Options{})
	if th.healthInterval != 3*time.Second {
		t.Fatalf("healthInterval = %s, want 3s from REASONIX_BASE_HEALTH_INTERVAL", th.healthInterval)
	}
	if th.healthMaxMisses != 7 {
		t.Fatalf("healthMaxMisses = %d, want 7 from REASONIX_BASE_HEALTH_MAX_MISSES", th.healthMaxMisses)
	}
	// A malformed value must not poison the default.
	t.Setenv("REASONIX_BASE_HEALTH_TIMEOUT", "not-a-duration")
	if got := resolveThresholds(Options{}).healthTimeout; got != defaultHealthTimeout {
		t.Fatalf("healthTimeout = %s, want the default %s", got, defaultHealthTimeout)
	}
	th = resolveThresholds(Options{HealthInterval: 42 * time.Millisecond, HealthMaxMisses: 3})
	if th.healthInterval != 42*time.Millisecond || th.healthMaxMisses != 3 {
		t.Fatalf("Options must outrank env, got %s/%d", th.healthInterval, th.healthMaxMisses)
	}
}

func TestDisabledStartNeverBuildsAManager(t *testing.T) {
	// R4 默认态：开关 off = 纯 inline，无管理器、无 goroutine、零日志。
	log, buf := newTestLogger()
	calls := 0
	client := Start(context.Background(), Options{
		ServerVersion: "v-test",
		Log:           log,
		Dial: func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
			calls++
			return nil, nil, fmt.Errorf("must not dial while disabled")
		},
	})
	if client.Mode() != ModeInline {
		t.Fatalf("mode = %q, want inline", client.Mode())
	}
	if calls != 0 {
		t.Fatalf("dial invoked %d times while disabled", calls)
	}
	if buf.Len() != 0 {
		t.Fatalf("disabled start logged %q, want silence", buf.String())
	}
	if _, ok := client.(*ManagedClient); ok {
		t.Fatal("disabled start returned a managed view, want the bare inline client")
	}
}
