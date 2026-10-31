package agent

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/event"
)

// subagentOpenTraceKey carries the open trace across RunProfileSpec's derived
// contexts (background jobs read it off the caller ctx they capture).
type subagentOpenTraceKey struct{}

// withSubagentOpenTrace attaches the trace to ctx (RunProfileSpec entry).
func withSubagentOpenTrace(ctx context.Context, tr *subagentOpenTrace) context.Context {
	return context.WithValue(ctx, subagentOpenTraceKey{}, tr)
}

// subagentOpenTraceFromContext returns the attached trace or nil. Nil-safe:
// entry points without a trace (fleet items, direct RunProfileSpec callers)
// simply skip instrumentation.
func subagentOpenTraceFromContext(ctx context.Context) *subagentOpenTrace {
	if ctx == nil {
		return nil
	}
	tr, _ := ctx.Value(subagentOpenTraceKey{}).(*subagentOpenTrace)
	return tr
}

// tl1293（只带仪器不带优化）：子代理打开链路分段计时。
//
// 用户 20261031 反馈「子智能体打开 ~3s 不算快」。该窗口此前没有任何打点：
// RunProfileSpec 内的本地装配段（registry/transcript/runtime/slot）与首轮
// 请求段（请求发出→响应头→首个内容块）全靠推测。本仪器把一次子代理打开
// 拆成可 grep 的分段读数，给「3s 构成拆解」提供 before 数据。
//
// 纪律与 boot 包 promptSegments（tl1229）完全同款（纯计时 + 一行日志，零
// 行为变更）：mark 不改任何控制流；汇总在首个内容事件或 runSession 收口处
// 发一条 "subagent: open segments" 行。sink 包装只观察事件，不改写、不丢
// 弃、不缓冲；CheckedSink 能力原样透传。
//
// 分段名即段尾标记（mark(X) = 自上一 mark 以来、以收口名 X 记账）：
//   registry    子代理工具注册表构建
//   transcript  转录 run 准备（fresh/continue/fork 元数据，内存操作）
//   runtime     provider/pricing/ctxWin 解析（无 override 时复用父 provider）
//   slot        调度槽获取（含排队等待；前台在 acquireSlot 后、后台在 job 内）
//   dispatch    runSession 闭包入口（前台≈0ms；后台=作业调度排队）
//   prepare     请求序列化 + agent 组装（turn_entry→请求发出）
//   rtt         请求发出→响应头到达（Connecting 清帧 attempt=0）
//   first_out   响应头→首个内容事件（服务端 prefill + 首 token）
// total = 整个窗口墙钟（发出时刻）。
type subagentOpenSegments struct {
	start  time.Time
	last   time.Time
	stages []subagentOpenSegment
}

type subagentOpenSegment struct {
	name string
	ms   int64
}

func newSubagentOpenSegments() *subagentOpenSegments {
	now := time.Now()
	return &subagentOpenSegments{start: now, last: now}
}

// mark closes the segment that just ended under name. Same shape as
// bootTiming.mark; never touches control flow.
func (s *subagentOpenSegments) mark(name string) {
	if s == nil {
		return
	}
	now := time.Now()
	s.markAt(name, now.Sub(s.last))
}

// markAt is the injectable-clock form of mark (tests pin the accounting
// instead of racing time.Since).
func (s *subagentOpenSegments) markAt(name string, d time.Duration) {
	if s == nil {
		return
	}
	s.stages = append(s.stages, subagentOpenSegment{name: name, ms: d.Milliseconds()})
	s.last = s.last.Add(d)
}

// summary renders one greppable line body: "total=…ms registry=2ms …". The
// total is the whole open window (wall clock to emit time).
func (s *subagentOpenSegments) summary() string {
	if s == nil {
		return ""
	}
	total := time.Since(s.start).Milliseconds()
	parts := make([]string, 0, len(s.stages)+1)
	parts = append(parts, "total="+strconv.FormatInt(total, 10)+"ms")
	for _, seg := range s.stages {
		parts = append(parts, seg.name+"="+strconv.FormatInt(seg.ms, 10)+"ms")
	}
	return strings.Join(parts, " ")
}

// subagentOpenTrace observes one subagent open window. All methods are
// nil-receiver safe so call sites never need a nil check.
type subagentOpenTrace struct {
	mu   sync.Mutex
	segs *subagentOpenSegments

	ref  string
	name string
	mode string // "fg" | "bg"

	emitted        bool
	sawRequestOut  bool
	sawHeaders     bool
	sawFirstOutput bool
}

func newSubagentOpenTrace(mode string) *subagentOpenTrace {
	return &subagentOpenTrace{segs: newSubagentOpenSegments(), mode: mode}
}

// mark closes the just-ended local segment under name.
func (tr *subagentOpenTrace) mark(name string) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.segs.mark(name)
}

// describe attaches the run identity used by the summary line (ref may be
// empty for ephemeral runs).
func (tr *subagentOpenTrace) describe(name, ref string) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if strings.TrimSpace(tr.name) == "" {
		tr.name = strings.TrimSpace(name)
	}
	tr.ref = strings.TrimSpace(ref)
}

// observe classifies one child event and closes the matching request-phase
// segment. Pure accounting: the event is forwarded unchanged by the sink.
func (tr *subagentOpenTrace) observe(ev event.Event) {
	if tr == nil {
		return
	}
	switch ev.Kind {
	case event.Connecting:
		// attempt>=1: the HTTP attempt went out; attempt==0: the clear frame
		// (headers arrived or start failed). First ones win — retries after
		// the initial attempt are the provider layer's business, not part of
		// the open window's first-pass shape.
		//
		// Race note: on a fast connect the attempt-1 notice can sit in the
		// turn loop's buffered channel and get drained by the clear-frame
		// path without ever reaching this sink. The clear frame therefore
		// back-fills "prepare" (at zero cost) before closing "rtt", so
		// prepare+rtt always sum to the request window even when the
		// attempt-1 frame never fires.
		tr.mu.Lock()
		defer tr.mu.Unlock()
		if ev.RetryAttempt >= 1 {
			if !tr.sawRequestOut {
				tr.sawRequestOut = true
				tr.segs.mark("prepare")
			}
			return
		}
		if !tr.sawHeaders && tr.sawRequestOut {
			tr.sawHeaders = true
			tr.segs.mark("rtt")
			return
		}
		if !tr.sawHeaders && !tr.sawRequestOut {
			// Fast-connect drain: synthesize the prepare boundary so the
			// rtt segment still owns the whole request window.
			tr.sawRequestOut = true
			tr.segs.markAt("prepare", 0)
			tr.sawHeaders = true
			tr.segs.mark("rtt")
		}
	case event.Reasoning, event.Text, event.Message, event.ToolDispatch, event.ToolProgress, event.ToolResult:
		tr.mu.Lock()
		defer tr.mu.Unlock()
		if tr.sawFirstOutput {
			return
		}
		tr.sawFirstOutput = true
		tr.segs.mark("first_out")
		tr.emitLocked()
	default:
	}
}

// emitLocked emits the one summary line (caller holds mu). Idempotent.
func (tr *subagentOpenTrace) emitLocked() {
	if tr.emitted {
		return
	}
	tr.emitted = true
	slog.Info("subagent: open segments",
		"name", tr.name,
		"ref", tr.ref,
		"mode", tr.mode,
		"summary", tr.segs.summary())
}

// finish emits the summary when the window ends without a first output
// (error paths, empty runs). The first-output path emits earlier and this is
// a no-op.
func (tr *subagentOpenTrace) finish() {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.emitLocked()
}

// subagentOpenTraceSink forwards every event through the trace observation
// before handing it to the real sink. CheckedSink capability is preserved so
// durability-aware consumers upstream keep their acknowledgement path.
type subagentOpenTraceSink struct {
	inner event.Sink
	tr    *subagentOpenTrace
}

func (tr *subagentOpenTrace) wrapSink(inner event.Sink) event.Sink {
	if tr == nil {
		return inner
	}
	return subagentOpenTraceSink{inner: inner, tr: tr}
}

func (s subagentOpenTraceSink) Emit(e event.Event) {
	s.tr.observe(e)
	s.inner.Emit(e)
}

func (s subagentOpenTraceSink) EmitChecked(e event.Event) error {
	s.tr.observe(e)
	return event.EmitChecked(s.inner, e)
}
