// Package eventtrigger is the generic event-trigger engine (task 230).
//
// Boundary with the neighbouring mechanisms (task 230 dispatch card):
//   - 202 / read_collab_status + status stream  = 数供给：只读快照，谁问谁答。
//   - 228 / event_wait                          = 等待：占用当前 turn，阻塞到
//     条件成立（或超时带快照返回）。它的轮询循环与 session_status 判定已收口
//     到本包（PollLoop + builtin checker），所以两处结论由同一实现给出。
//   - 230 / this engine                         = 触发：注册 Trigger，独立于
//     任何一次调用按 interval 轮询 checker，命中产生事件（一期 = 进程内回调；
//     二期 = 唤醒目标对话，后续任务承接）。
//     228 是本机制的首个消费者/预设，不是被替代对象。
//
// Core abstraction: an event source is one tool call plus a verdict over its
// result (task 230). The checker whitelist is v1's security boundary: only
// registered READ-ONLY host tools and explicitly registered builtin
// predicates may run as checkers — arbitrary command execution stays closed.
// The match DSL starts minimal: exit0 / jsonpath / regex.
package eventtrigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Match kinds — the minimal-enough DSL (task 230 phase 1).
const (
	MatchExit0    = "exit0"    // checker ran without error and (when known) exit code 0
	MatchJSONPath = "jsonpath" // output JSON resolves the path to a truthy value, or "a.b==literal"
	MatchRegex    = "regex"    // output matches the regex
)

// Checker kinds.
const (
	CheckerTool    = "tool"    // a registered read-only host tool (whitelist)
	CheckerBuiltin = "builtin" // an explicitly registered engine predicate
)

// DefaultIntervalS / timeout bounds mirror the event_wait clamps so a
// trigger and a wait behave the same way under the same numbers.
const (
	DefaultIntervalS = 60
	MinIntervalS     = 5
	MaxIntervalS     = 600
	MinTimeoutS      = 10
	MaxTimeoutS      = 7200
)

// Match is the verdict over one checker execution.
type Match struct {
	Kind string `json:"kind"`
	Expr string `json:"expr"`
}

// Checker names what to run. Args are the tool call's arguments (tool kind)
// or the predicate's parameters (builtin kind).
type Checker struct {
	Kind string          `json:"kind"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Trigger is one registered event source.
type Trigger struct {
	ID        string  `json:"id"`
	IntervalS int     `json:"interval_s"`
	TimeoutS  int     `json:"timeout_s"`
	Checker   Checker `json:"checker"`
	Match     Match   `json:"match"`
}

// ToolEntry is the whitelist record: registered read-only host tools.
type ToolEntry struct {
	ReadOnly bool
}

// Executor runs a checker once: output text, optional exit code (-1 unknown),
// and error. The constructing side injects it (agent wires real tool
// execution plus builtin predicates) so this package stays import-cycle-free.
type Executor func(ctx context.Context, c Checker) (output string, exitCode int, err error)

// ErrWhitelist rejects any checker outside the registered read-only set.
var ErrWhitelist = errors.New("eventtrigger: checker rejected by whitelist (only registered read-only tools and registered builtin predicates are allowed)")

type Engine struct {
	mu       sync.Mutex
	triggers map[string]Trigger
	tools    map[string]ToolEntry
	builtins map[string]func(ctx context.Context, args json.RawMessage) (string, error)
	exec     Executor
}

// NewEngine builds an engine with an empty whitelist; RegisterTool /
// RegisterBuiltin fill it before any Trigger validates.
func NewEngine(exec Executor) *Engine {
	return &Engine{
		triggers: map[string]Trigger{},
		tools:    map[string]ToolEntry{},
		builtins: map[string]func(ctx context.Context, args json.RawMessage) (string, error){},
		exec:     exec,
	}
}

// RegisterTool adds a host tool to the checker whitelist. Non-read-only
// tools are refused: a checker must not mutate state (v1 security boundary).
func (e *Engine) RegisterTool(name string, readOnly bool) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("eventtrigger: tool name required")
	}
	if !readOnly {
		return fmt.Errorf("%w: tool %q is not read-only", ErrWhitelist, name)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tools[name] = ToolEntry{ReadOnly: true}
	return nil
}

// RegisterBuiltin adds an engine-local predicate (e.g. session_status).
func (e *Engine) RegisterBuiltin(name string, fn func(context.Context, json.RawMessage) (string, error)) error {
	if strings.TrimSpace(name) == "" || fn == nil {
		return fmt.Errorf("eventtrigger: builtin name and function required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.builtins[name] = fn
	return nil
}

// Validate checks a trigger before it is accepted: whitelist membership,
// match DSL shape, and clamped scheduling numbers. It never executes.
func (e *Engine) Validate(t Trigger) error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("eventtrigger: trigger id required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch t.Checker.Kind {
	case CheckerTool:
		if entry, ok := e.tools[t.Checker.Name]; !ok || !entry.ReadOnly {
			return fmt.Errorf("%w: tool %q", ErrWhitelist, t.Checker.Name)
		}
	case CheckerBuiltin:
		if _, ok := e.builtins[t.Checker.Name]; !ok {
			return fmt.Errorf("%w: builtin %q", ErrWhitelist, t.Checker.Name)
		}
	default:
		return fmt.Errorf("eventtrigger: checker kind %q must be %q or %q", t.Checker.Kind, CheckerTool, CheckerBuiltin)
	}
	if err := validateMatch(t.Match); err != nil {
		return err
	}
	if clampInterval(t.IntervalS) == 0 {
		return fmt.Errorf("eventtrigger: interval_s out of range [%d..%d]", MinIntervalS, MaxIntervalS)
	}
	if t.TimeoutS != 0 && clampTimeout(t.TimeoutS) == 0 {
		return fmt.Errorf("eventtrigger: timeout_s out of range [%d..%d] or 0 (no timeout)", MaxTimeoutS, MinTimeoutS)
	}
	return nil
}

func validateMatch(m Match) error {
	switch m.Kind {
	case MatchExit0:
		return nil
	case MatchJSONPath:
		if strings.TrimSpace(m.Expr) == "" {
			return fmt.Errorf("eventtrigger: jsonpath match needs an expression (\"a.b.c\" or \"a.b==literal\")")
		}
	case MatchRegex:
		if strings.TrimSpace(m.Expr) == "" {
			return fmt.Errorf("eventtrigger: regex match needs a pattern")
		}
		if _, err := regexp.Compile(m.Expr); err != nil {
			return fmt.Errorf("eventtrigger: invalid regex: %w", err)
		}
	default:
		return fmt.Errorf("eventtrigger: match kind %q unsupported (exit0/jsonpath/regex)", m.Kind)
	}
	return nil
}

// Register validates and stores a trigger (replacing the same id).
func (e *Engine) Register(t Trigger) error {
	if err := e.Validate(t); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.IntervalS == 0 {
		t.IntervalS = DefaultIntervalS
	}
	e.triggers[t.ID] = t
	return nil
}

// Unregister removes a trigger; unknown ids are a no-op (idempotent).
func (e *Engine) Unregister(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.triggers, id)
}

// Triggers returns a snapshot of registered triggers.
func (e *Engine) Triggers() []Trigger {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Trigger, 0, len(e.triggers))
	for _, t := range e.triggers {
		out = append(out, t)
	}
	return out
}

// Evaluate runs the checker once and applies the match — the single verdict
// path both the trigger loop and event_wait use (task 230 acceptance: one
// judgement implementation, two consumers).
func (e *Engine) Evaluate(ctx context.Context, t Trigger) (bool, string, error) {
	if err := e.Validate(t); err != nil {
		return false, "", err
	}
	var output string
	var exitCode int
	var err error
	switch t.Checker.Kind {
	case CheckerBuiltin:
		e.mu.Lock()
		fn := e.builtins[t.Checker.Name]
		e.mu.Unlock()
		if fn == nil {
			return false, "", fmt.Errorf("%w: builtin %q", ErrWhitelist, t.Checker.Name)
		}
		output, err = fn(ctx, t.Checker.Args)
		exitCode = 0
		if err != nil {
			exitCode = 1
		}
	default:
		output, exitCode, err = e.exec(ctx, t.Checker)
	}
	hit, verdict, matchErr := matchResult(t.Match, output, exitCode, err)
	return hit, verdict, matchErr
}

// matchResult applies the DSL. verdict is a short human-readable line for
// event records (never the raw output — keep events compact and free of
// argument payloads beyond what the match itself used).
func matchResult(m Match, output string, exitCode int, execErr error) (bool, string, error) {
	switch m.Kind {
	case MatchExit0:
		ok := execErr == nil && (exitCode == 0 || exitCode == -1)
		return ok, fmt.Sprintf("exit0=%v", ok), nil
	case MatchRegex:
		re, err := regexp.Compile(m.Expr)
		if err != nil {
			return false, "", fmt.Errorf("eventtrigger: invalid regex: %w", err)
		}
		ok := re.MatchString(output)
		return ok, fmt.Sprintf("regex=%v", ok), nil
	case MatchJSONPath:
		ok, err := jsonPathMatches(output, m.Expr)
		if err != nil {
			return false, "", err
		}
		return ok, fmt.Sprintf("jsonpath(%s)=%v", m.Expr, ok), nil
	}
	return false, "", fmt.Errorf("eventtrigger: match kind %q unsupported", m.Kind)
}

// jsonPathMatches evaluates "a.b.c" (truthy resolved value) or the explicit
// comparison form "a.b==literal" (string compare after fmt of the resolved
// value). Minimal on purpose — no JSON-path library, no traversal of arrays
// by index (task 230 待核实 item 1: decide the boundary, don't overdesign).
func jsonPathMatches(output, expr string) (bool, error) {
	path, want, hasWant := strings.Cut(expr, "==")
	path = strings.TrimSpace(path)
	if path == "" {
		return false, fmt.Errorf("eventtrigger: empty json path in %q", expr)
	}
	var doc any
	if err := json.Unmarshal([]byte(output), &doc); err != nil {
		return false, fmt.Errorf("eventtrigger: output is not JSON (jsonpath needs a JSON result): %w", err)
	}
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return false, nil
		}
		cur, ok = obj[seg]
		if !ok {
			return false, nil
		}
	}
	if hasWant {
		return fmt.Sprintf("%v", cur) == strings.TrimSpace(want), nil
	}
	return truthy(cur), nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case json.Number:
		f, err := x.Float64()
		return err == nil && f != 0
	}
	return true
}

// PollSpec carries the shared clamped schedule.
type PollSpec struct {
	Interval time.Duration
	Timeout  time.Duration // 0 = no timeout
}

// PollOutcome reports how a loop ended.
type PollOutcome struct {
	Satisfied   bool
	Elapsed     time.Duration
	Interrupted bool // ctx cancelled (turn stop) before satisfied/timeout
	TimedOut    bool
}

// PollLoop is the shared polling semantics of event_wait (228) and the
// trigger engine (230): first tick runs immediately (an already-true
// condition must not sleep), then interval sleeps bounded by the deadline;
// ctx cancellation settles cleanly with the last observation.
func PollLoop(ctx context.Context, spec PollSpec, tick func() bool) PollOutcome {
	started := time.Now()
	deadline := time.Time{}
	if spec.Timeout > 0 {
		deadline = started.Add(spec.Timeout)
	}
	interval := spec.Interval
	if interval <= 0 {
		interval = time.Duration(DefaultIntervalS) * time.Second
	}
	if tick() {
		return PollOutcome{Satisfied: true, Elapsed: time.Since(started)}
	}
	for {
		now := time.Now()
		if !deadline.IsZero() && !now.Before(deadline) {
			return PollOutcome{Elapsed: now.Sub(started), TimedOut: true}
		}
		sleep := interval
		if !deadline.IsZero() {
			if remaining := deadline.Sub(now); remaining < sleep {
				sleep = remaining
			}
		}
		select {
		case <-ctx.Done():
			return PollOutcome{Elapsed: time.Since(started), Interrupted: true}
		case <-time.After(sleep):
		}
		if tick() {
			return PollOutcome{Satisfied: true, Elapsed: time.Since(started)}
		}
	}
}

// Run polls every registered trigger on its own interval; a satisfied
// trigger fires onEvent (id stays registered — one-shot semantics are the
// consumer's choice via Unregister in the callback) and restarts its
// schedule. It exits when ctx is done. Callers gate it behind the
// experimental switch (default off, fork rule 2).
func (e *Engine) Run(ctx context.Context, onEvent func(t Trigger, verdict string)) {
	for {
		if ctx.Err() != nil {
			return
		}
		for _, t := range e.Triggers() {
			if ctx.Err() != nil {
				return
			}
			spec := PollSpec{Interval: time.Duration(clampInterval(t.IntervalS)) * time.Second}
			out := PollLoop(ctx, spec, func() bool {
				hit, verdict, err := e.Evaluate(ctx, t)
				if err != nil || !hit {
					return false
				}
				if onEvent != nil {
					onEvent(t, verdict)
				}
				return hit
			})
			if out.Interrupted && ctx.Err() != nil {
				return
			}
		}
		// Idle gap before rescanning: never spin when no trigger exists.
		if len(e.Triggers()) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(MinIntervalS) * time.Second):
			}
		}
	}
}

func clampInterval(v int) int {
	if v == 0 {
		return DefaultIntervalS
	}
	if v < MinIntervalS || v > MaxIntervalS {
		return 0 // caller treats 0 as invalid
	}
	return v
}

func clampTimeout(v int) int {
	if v < MinTimeoutS || v > MaxTimeoutS {
		return 0
	}
	return v
}
