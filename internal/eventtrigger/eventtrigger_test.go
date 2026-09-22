package eventtrigger

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	eng := NewEngine(func(ctx context.Context, c Checker) (string, int, error) {
		return "", -1, nil
	})
	return eng
}

// TestWhitelistRejectsUnknownAndReadOnlyEnforced pins the v1 security
// boundary: only registered read-only tools and registered builtins may be
// checkers; a non-read-only registration and an unregistered name are both
// refused (acceptance: "白名单外 checker 被拒绝").
func TestWhitelistRejectsUnknownAndReadOnlyEnforced(t *testing.T) {
	eng := newTestEngine(t)
	if err := eng.RegisterTool("write_file", false); err == nil || !errors.Is(err, ErrWhitelist) {
		t.Fatalf("non-read-only tool must be refused: %v", err)
	}
	if err := eng.RegisterTool("read_file", true); err != nil {
		t.Fatal(err)
	}
	bad := Trigger{
		ID: "t1", IntervalS: 60,
		Checker: Checker{Kind: CheckerTool, Name: "bash"},
		Match:   Match{Kind: MatchExit0},
	}
	if err := eng.Register(bad); err == nil || !errors.Is(err, ErrWhitelist) {
		t.Fatalf("unregistered tool must be refused: %v", err)
	}
	bad.Checker = Checker{Kind: CheckerTool, Name: "read_file"}
	bad.Match = Match{Kind: "rm-rf"}
	if err := eng.Register(bad); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported match kind must be refused: %v", err)
	}
	// Registered read-only tool + valid match passes.
	bad.Match = Match{Kind: MatchExit0}
	if err := eng.Register(bad); err != nil {
		t.Fatalf("valid trigger refused: %v", err)
	}
	unknownBuiltin := Trigger{ID: "t2", IntervalS: 60, Checker: Checker{Kind: CheckerBuiltin, Name: "session_status"}, Match: Match{Kind: MatchExit0}}
	if err := eng.Register(unknownBuiltin); err == nil || !errors.Is(err, ErrWhitelist) {
		t.Fatalf("unregistered builtin must be refused: %v", err)
	}
}

// TestMatchDSLThreeForms pins the minimal verdict DSL (exit0 / jsonpath /
// regex) — the three starter shapes of task 230's acceptance.
func TestMatchDSLThreeForms(t *testing.T) {
	eng := NewEngine(func(ctx context.Context, c Checker) (string, int, error) {
		switch c.Name {
		case "ok":
			return `{"paths":["a","b"],"count":3}`, 0, nil
		case "fail":
			return "", 3, nil
		case "boom":
			return "", -1, errors.New("exec failed")
		}
		return "", -1, nil
	})
	if err := eng.RegisterTool("ok", true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fail", "boom"} {
		if err := eng.RegisterTool(name, true); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(name string, m Match) Trigger {
		return Trigger{ID: "x", IntervalS: 60, Checker: Checker{Kind: CheckerTool, Name: name}, Match: m}
	}
	ctx := context.Background()

	// exit0
	hit, _, err := eng.Evaluate(ctx, mk("ok", Match{Kind: MatchExit0}))
	if err != nil || !hit {
		t.Fatalf("exit0 on success: hit=%v err=%v", hit, err)
	}
	hit, _, _ = eng.Evaluate(ctx, mk("fail", Match{Kind: MatchExit0}))
	if hit {
		t.Fatal("exit0 must miss on non-zero exit")
	}
	hit, _, _ = eng.Evaluate(ctx, mk("boom", Match{Kind: MatchExit0}))
	if hit {
		t.Fatal("exit0 must miss on error")
	}
	// jsonpath truthy + comparison + miss
	hit, _, err = eng.Evaluate(ctx, mk("ok", Match{Kind: MatchJSONPath, Expr: "count"}))
	if err != nil || !hit {
		t.Fatalf("jsonpath truthy: hit=%v err=%v", hit, err)
	}
	hit, _, _ = eng.Evaluate(ctx, mk("ok", Match{Kind: MatchJSONPath, Expr: "paths==[a b]"}))
	// "%v" of a []any renders "[a b]" — document the minimal formatter.
	if !hit {
		t.Fatal("jsonpath == comparison missed on rendered list")
	}
	hit, _, _ = eng.Evaluate(ctx, mk("ok", Match{Kind: MatchJSONPath, Expr: "missing"}))
	if hit {
		t.Fatal("jsonpath must miss on absent path")
	}
	// regex
	hit, _, err = eng.Evaluate(ctx, mk("ok", Match{Kind: MatchRegex, Expr: `"paths"`}))
	if err != nil || !hit {
		t.Fatalf("regex: hit=%v err=%v", hit, err)
	}
	if hit, _, _ = eng.Evaluate(ctx, mk("ok", Match{Kind: MatchRegex, Expr: "nope-not-there"})); hit {
		t.Fatal("regex must miss on non-matching output")
	}
	// invalid regex is rejected at validation time
	if err := eng.Validate(mk("ok", Match{Kind: MatchRegex, Expr: "("})); err == nil {
		t.Fatal("invalid regex must be refused by Validate")
	}
	// jsonpath against non-JSON errors instead of guessing
	if _, _, err := eng.Evaluate(ctx, mk("boom", Match{Kind: MatchJSONPath, Expr: "a"})); err == nil {
		t.Fatal("jsonpath on non-JSON output must error, not miss silently")
	}
}

// TestPollLoopSharedSemantics pins the loop contract event_wait (228) and the
// trigger engine share: immediate first tick, timeout settle, cancellation.
func TestPollLoopSharedSemantics(t *testing.T) {
	// Already-true condition returns without sleeping.
	start := time.Now()
	out := PollLoop(context.Background(), PollSpec{Interval: time.Hour, Timeout: time.Hour}, func() bool { return true })
	if !out.Satisfied || out.Elapsed > time.Second {
		t.Fatalf("immediate-first-tick broken: %+v", out)
	}
	_ = start

	// Timeout settles unsatisfied (short windows to keep the test fast).
	out = PollLoop(context.Background(), PollSpec{Interval: 5 * time.Millisecond, Timeout: 25 * time.Millisecond}, func() bool { return false })
	if out.Satisfied || !out.TimedOut || out.Interrupted {
		t.Fatalf("timeout settle broken: %+v", out)
	}

	// Cancellation wins over sleep and reports interrupted.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	out = PollLoop(ctx, PollSpec{Interval: 50 * time.Millisecond, Timeout: time.Hour}, func() bool { return false })
	if !out.Interrupted || out.Satisfied {
		t.Fatalf("cancellation settle broken: %+v", out)
	}

	// A condition that turns true on a later tick wakes the loop.
	ticks := 0
	out = PollLoop(context.Background(), PollSpec{Interval: 5 * time.Millisecond, Timeout: 2 * time.Second}, func() bool {
		ticks++
		return ticks >= 3
	})
	if !out.Satisfied || ticks < 3 {
		t.Fatalf("late satisfaction broken: ticks=%d out=%+v", ticks, out)
	}
}

// TestRunFiresEventOnSatisfiedTrigger: register → poll → event, plus
// unregister idempotence (acceptance: 注册/注销 + 命中产生事件).
func TestRunFiresEventOnSatisfiedTrigger(t *testing.T) {
	eng := NewEngine(func(ctx context.Context, c Checker) (string, int, error) { return "ok", 0, nil })
	if err := eng.RegisterTool("status_probe", true); err != nil {
		t.Fatal(err)
	}
	if err := eng.Register(Trigger{ID: "go", IntervalS: MinIntervalS, Checker: Checker{Kind: CheckerTool, Name: "status_probe"}, Match: Match{Kind: MatchExit0}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fired := make(chan Trigger, 4)
	go eng.Run(ctx, func(t Trigger, verdict string) { fired <- t })
	select {
	case got := <-fired:
		if got.ID != "go" {
			t.Fatalf("wrong trigger fired: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event fired within 5s")
	}
	eng.Unregister("go")
	eng.Unregister("go") // idempotent
	if n := len(eng.Triggers()); n != 0 {
		t.Fatalf("unregister left %d triggers", n)
	}
}

// TestIntervalBoundsRejected: out-of-range schedule numbers fail validation.
func TestIntervalBoundsRejected(t *testing.T) {
	eng := newTestEngine(t)
	_ = eng.RegisterTool("r", true)
	err := eng.Validate(Trigger{ID: "x", IntervalS: 1, Checker: Checker{Kind: CheckerTool, Name: "r"}, Match: Match{Kind: MatchExit0}})
	if err == nil || !strings.Contains(err.Error(), "interval_s") {
		t.Fatalf("interval below min must be refused: %v", err)
	}
	err = eng.Validate(Trigger{ID: "x", IntervalS: 9999, TimeoutS: 50000, Checker: Checker{Kind: CheckerTool, Name: "r"}, Match: Match{Kind: MatchExit0}})
	if err == nil {
		t.Fatal("interval/timeout above max must be refused")
	}
}
