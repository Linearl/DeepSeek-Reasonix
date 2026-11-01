package provider

// 任务 tl1327（2026-11-01 路由器 NAT 打满事件根治件）：进程级拨号并发预算 +
// 断网熔断。每会话工厂（cli/acp.go newACPSubagentProviderResolver）为每个
// subagent 新建 provider 实例——各自独立的 http.Client + transport 池，断网时
// N 会话 × M provider 各自独立重连，本机曾同时挂 109 条 SYN_SENT 把路由器
// NAT 连接表打满（新 TCP 全断、老连接活着）。本文件的拨号门在三条聊天主力
// 链路（openai/anthropic/responses 的 transport DialContext）上统一收口：
//
// ① 并发预算：同时最多 MaxConcurrent 条新建 TCP 连接在途（SYN_SENT 有界），
//    其余排队 + 抖动指数退避，谁先拿到槽谁先拨。
// ② 断网熔断：连续 FailureThreshold 次真实拨号失败 → 判定网络不可达，暂停
//    （快速失败）全部 provider 拨号 Cooldown；冷却到期后仅放一条金丝雀
//    （下一个真实请求），金丝雀成功即统一放行、失败则续闭。熔断暂停期错误
//    是可重试的普通瞬态错误——上层 SendWithRetry 的既有退避继续工作，网络
//    恢复后自动放行，不新增任何不可达形态。
//
// fail-open 契约（可用性机制删前必须有替代判例）：门自身故障 = 行为退回现状。
// 全部状态是原子量（热路径无锁可卡死）；门内 panic 经 recover 直接落回原始
// 拨号；配置非法（预算 ≤0）视为门禁用；排队等待由调用方 ctx 与队列耐心上限
// 双重界定，无自造的无界等待。未接线（未调 ConfigureDialGate / 未过
// GateHTTPClient）的链路行为逐字节不变。

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// DefaultDialGateConcurrent is the process-wide cap on in-flight new TCP
// connections across all gated chat transports. ~16 covers every sane
// provider×session combination while keeping a NAT-table storm impossible:
// the 2026-11-01 incident ran 109 concurrent SYN_SENT.
const DefaultDialGateConcurrent = 16

// DefaultDialGateFailureThreshold consecutive real dial failures before the
// breaker judges the network unreachable. Three independent failures (across
// any providers/hosts) is strong evidence of a local-path outage and weak
// evidence of any single endpoint being down.
const DefaultDialGateFailureThreshold = 3

// DefaultDialGateCooldown is how long the breaker stays open per trip. Inside
// the task's 30-60s window: long enough that the retry storms of dozens of
// sessions collapse into one canary, short enough that recovery feels quick.
const DefaultDialGateCooldown = 45 * time.Second

// queuePatience bounds how long one dial may wait in the slot queue before it
// fails with a typed, retryable error instead of hanging invisibly. It only
// ever trips under pathological saturation (a healthy network drains the queue
// within one dial timeout), and a caller-supplied ctx cancels sooner anyway.
// A var so tests can shrink it.
var queuePatience = 90 * time.Second

// denyRecheck is the RetryAt advertised while a canary is still in flight: the
// verdict (and with it either a full release or a re-opened cooldown) lands
// within one dial timeout, so waiters re-check shortly instead of waiting out
// a whole cooldown that is no longer real.
const denyRecheck = 2 * time.Second

// DialGateSettings overrides the process gate's defaults. Nil/zero fields keep
// the defaults; Disabled=false is the zero value, so the explicit-off signal is
// a non-nil pointer (config `disabled = true` maps to it). Invalid values
// (concurrency ≤0) disable the gate entirely — fail-open beats fail-closed for
// an availability mechanism.
type DialGateSettings struct {
	// Disabled turns the whole gate off when non-nil and true: dials pass
	// through byte-for-byte as before tl1327 (the rollback path).
	Disabled *bool
	// MaxConcurrent overrides DefaultDialGateConcurrent when >0.
	MaxConcurrent int
	// FailureThreshold overrides DefaultDialGateFailureThreshold when >0.
	FailureThreshold int
	// Cooldown overrides DefaultDialGateCooldown when >0.
	Cooldown time.Duration
}

type dialGate struct {
	disabled   atomic.Bool
	slots      chan struct{}
	threshold  int64
	cooldown   time.Duration
	failures   atomic.Int64
	openUntil  atomic.Int64 // unix nanos; 0 = breaker closed
	probing    atomic.Bool  // one canary at a time
	acquiring  atomic.Int64 // queued waiters (observability + tests)
	peakInline atomic.Int64 // observability: max slots held at once
	nowFn      func() time.Time
}

var currentGate atomic.Pointer[dialGate]

func init() { currentGate.Store(newDialGate(DialGateSettings{})) }

func newDialGate(s DialGateSettings) *dialGate {
	g := &dialGate{threshold: int64(DefaultDialGateFailureThreshold), cooldown: DefaultDialGateCooldown, nowFn: time.Now}
	if s.FailureThreshold > 0 {
		g.threshold = int64(s.FailureThreshold)
	}
	if s.Cooldown > 0 {
		g.cooldown = s.Cooldown
	}
	maxConc := DefaultDialGateConcurrent
	if s.MaxConcurrent > 0 {
		maxConc = s.MaxConcurrent
	}
	if s.Disabled != nil && *s.Disabled {
		// Rollback path: the gate exists (snapshots stay meaningful) but
		// admits everything — dial behaviour is byte-for-byte pre-tl1327.
		g.disabled.Store(true)
	}
	g.slots = make(chan struct{}, maxConc)
	return g
}

// ConfigureDialGate swaps the process gate to the given settings. Unknown
// fields keep their defaults; the swap is atomic and in-flight dials of the
// old gate simply release into the old channel — briefly the process may hold
// old-cap + new-cap dials, still bounded, never unbounded.
func ConfigureDialGate(s DialGateSettings) { currentGate.Store(newDialGate(s)) }

// dialGateForTest installs a test gate and returns a restore func.
// Package-internal only: production wiring goes through ConfigureDialGate.
func dialGateForTest(s DialGateSettings, now func() time.Time) (g *dialGate, restore func()) {
	g = newDialGate(s)
	if now != nil {
		g.nowFn = now
	}
	prev := currentGate.Load()
	currentGate.Store(g)
	return g, func() { currentGate.Store(prev) }
}

// ErrDialPaused is the sentinel every breaker-denied dial fails with. It is a
// plain transient error: SendWithRetry treats it like any other retryable
// transport failure and its existing backoff re-enters the gate later — after
// the cooldown the next request becomes the canary, so recovery needs no new
// machinery above this file.
var ErrDialPaused = errors.New("provider dials paused: network judged unreachable (circuit breaker open)")

// IsDialPaused reports whether err is a breaker-pause fast-fail (as opposed to
// a real dial outcome).
func IsDialPaused(err error) bool { return errors.Is(err, ErrDialPaused) }

// DialPausedError carries when the gate expects to admit the next dial: the
// breaker's openUntil while still cooling down, a near-term recheck while a
// canary probe is in flight.
type DialPausedError struct{ RetryAt time.Time }

func (e *DialPausedError) Error() string {
	return fmt.Sprintf("%s (next admission ~%s)", ErrDialPaused, e.RetryAt.Format("15:04:05"))
}

func (e *DialPausedError) Is(target error) bool { return target == ErrDialPaused }

// DialQueueTimeoutError is what a dial fails with when it waited past
// queuePatience for a slot. Retryable like any transient transport error.
type DialQueueTimeoutError struct{ Waited time.Duration }

func (e *DialQueueTimeoutError) Error() string {
	return fmt.Sprintf("dial queue timeout: no free connection slot after %s (process dial budget saturated)", e.Waited.Round(time.Second))
}

// acquireBackoffBase/cap shape the queue's retry cadence: waiters poll for a
// freed slot with jittered exponential backoff instead of all parking on one
// channel wake (the release wake would otherwise stampede every queued
// session at once — exactly the herd tl1327 exists to break).
const (
	acquireBackoffBase = 50 * time.Millisecond
	acquireBackoffCap  = time.Second
)

// DialFunc is the raw transport dial signature.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// gatedDialContext marks an already-gated DialContext so GateHTTPClient stays
// idempotent.
type gatedDialContext DialFunc

// chatMaxIdleConnsPerHost bounds how many established connections stay warm
// per host between requests. net/http's default 2 is sized for short page
// fetches; a chat session routinely overlaps its main stream with auxiliary
// calls on the same host, and every evicted idle connection is a fresh TCP
// dial (through the NAT table this task exists to protect).
const chatMaxIdleConnsPerHost = 8

// GateHTTPClient prepares a chat-mainline HTTP client in place:
//   - it installs the process dial gate on the transport's DialContext, so all
//     new TCP connections this client ever opens count against the process
//     dial budget and feed the outage breaker (tl1327 ①②);
//   - it raises MaxIdleConnsPerHost from net/http's default 2 to
//     chatMaxIdleConnsPerHost, so an idle provider connection survives between
//     turns instead of being closed and re-dialed on the next request
//     (tl1327 ③ connection-reuse tuning; the webfetch tl1319 precedent).
//
// It is idempotent and leaves non-*http.Transport transports (test doubles,
// the credential-proxy tunnel client) untouched. Returns c for chaining.
func GateHTTPClient(c *http.Client) *http.Client {
	if c == nil {
		return nil
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr == nil {
		return c
	}
	if tr.MaxIdleConnsPerHost < chatMaxIdleConnsPerHost {
		tr.MaxIdleConnsPerHost = chatMaxIdleConnsPerHost
	}
	inner := tr.DialContext
	if _, already := any(inner).(gatedDialContext); already {
		return c
	}
	if inner == nil {
		// http.DefaultTransport's dialer is 30s/30s; mirror it so gating does
		// not change dial timeouts for transports that never set one.
		inner = (&net.Dialer{Timeout: DefaultDialTimeout, KeepAlive: 30 * time.Second}).DialContext
	}
	tr.DialContext = gatedDialContext(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return gatedDial(ctx, DialFunc(inner), network, addr)
	})
	return c
}

// gatedDial is the one seam every gated dial flows through. Fail-open: any
// panic inside the gate machinery falls back to the plain dial — the gate must
// never become a new way for the network to be unreachable.
func gatedDial(ctx context.Context, inner DialFunc, network, addr string) (conn net.Conn, err error) {
	g := currentGate.Load()
	if g == nil || g.disabled.Load() {
		return inner(ctx, network, addr)
	}
	defer func() {
		if r := recover(); r != nil {
			conn, err = inner(ctx, network, addr)
		}
	}()
	return g.dial(ctx, inner, network, addr)
}

func (g *dialGate) dial(ctx context.Context, inner DialFunc, network, addr string) (net.Conn, error) {
	if err := g.enter(ctx); err != nil {
		return nil, err
	}
	if err := g.acquire(ctx); err != nil {
		return nil, err
	}
	// Slot held from here to the inner dial's completion (defer release: a
	// panicking transport must not leak budget).
	defer func() { <-g.slots }()
	g.notePeak()
	conn, err := inner(ctx, network, addr)
	g.record(err)
	return conn, err
}

// notePeak keeps the process-wide "max in-flight dials" high-water mark for
// diagnostics and the tl1327 ④ baseline work.
func (g *dialGate) notePeak() {
	n := int64(len(g.slots))
	for {
		cur := g.peakInline.Load()
		if n <= cur || g.peakInline.CompareAndSwap(cur, n) {
			return
		}
	}
}

// enter decides admission before any slot is consumed: breaker closed → pass;
// open → deny fast (fail fast is the whole point: dozens of retrying sessions
// must not touch the NAT table while the network is down), except exactly one
// canary once the cooldown has elapsed.
func (g *dialGate) enter(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := g.nowFn()
	// nanos==0 means closed; time.Unix(0,0) would be 1970, not the zero Time,
	// so the closed check must look at the raw nanos, never at IsZero.
	if nanos := g.openUntil.Load(); nanos != 0 {
		until := time.Unix(0, nanos)
		if now.Before(until) {
			return &DialPausedError{RetryAt: until}
		}
		// Cooldown expired: exactly one waiting dial becomes the canary. The
		// CAS loses the race for everyone else — they re-enter on their next
		// retry and either deny again (canary still in flight) or pass (the
		// canary succeeded and closed the breaker).
		if g.probing.CompareAndSwap(false, true) {
			return nil
		}
		return &DialPausedError{RetryAt: now.Add(denyRecheck)}
	}
	return nil
}

// acquire takes a dial slot with jittered exponential backoff (排队+退避).
// Bounded by the caller's ctx and by queuePatience — no self-made unbounded
// wait. Caller cancellation is returned as-is; patience expiry becomes the
// typed, retryable DialQueueTimeoutError.
func (g *dialGate) acquire(ctx context.Context) error {
	g.acquiring.Add(1)
	defer g.acquiring.Add(-1)
	started := g.nowFn()
	delay := acquireBackoffBase
	for {
		select {
		case g.slots <- struct{}{}:
			return nil
		default:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if waited := g.nowFn().Sub(started); waited >= queuePatience {
			return &DialQueueTimeoutError{Waited: waited}
		}
		sleep := delay + time.Duration(rand.Int63n(int64(delay/2)+1))
		select {
		case <-time.After(sleep):
		case <-ctx.Done():
			return ctx.Err()
		}
		if delay < acquireBackoffCap {
			delay = min(delay*2, acquireBackoffCap)
		}
	}
}

// record turns a real dial outcome into breaker evidence. Caller-initiated
// cancellation is not network evidence and never counts (a user canceling a
// turn must not trip the breaker); the dialer's own timeout — 30s of unacked
// SYN — is.
func (g *dialGate) record(err error) {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}
	if err != nil {
		f := g.failures.Add(1)
		g.probing.Store(false)
		if f >= g.threshold {
			g.openUntil.Store(g.nowFn().Add(g.cooldown).UnixNano())
		}
		return
	}
	// Any success anywhere proves the path is back: release everything.
	g.failures.Store(0)
	g.openUntil.Store(0)
	g.probing.Store(false)
}

// DialGateSnapshot is read-only observability for diagnostics (task ④ baseline
// work and future incident triage).
type DialGateSnapshot struct {
	Disabled         bool
	Capacity         int
	InFlight         int
	Waiting          int
	ConsecutiveFails int
	BreakerOpenUntil time.Time
	ProbeInFlight    bool
	PeakInFlight     int
}

func DialGateSnapshotNow() DialGateSnapshot {
	g := currentGate.Load()
	if g == nil {
		return DialGateSnapshot{Disabled: true}
	}
	var openUntil time.Time
	if nanos := g.openUntil.Load(); nanos != 0 {
		openUntil = time.Unix(0, nanos)
	}
	return DialGateSnapshot{
		Disabled:         g.disabled.Load(),
		Capacity:         cap(g.slots),
		InFlight:         len(g.slots),
		Waiting:          int(g.acquiring.Load()),
		ConsecutiveFails: int(g.failures.Load()),
		BreakerOpenUntil: openUntil,
		ProbeInFlight:    g.probing.Load(),
		PeakInFlight:     int(g.peakInline.Load()),
	}
}
