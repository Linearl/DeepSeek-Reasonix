package guardian

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务 990 — 门禁异常分支测试矩阵（guardian 部分）。
//
// 架构决策 msg_7a50d8fd1325a810：本矩阵钉住现状、不动语义。真缺陷点 =
// recordDenial() 无差别计数——transport 失败 / 30s 超时 / 解析失败三态被折叠成
// DENY 后与真实评审拒绝混入同一连拒计数，连续 3 次 provider 抖动即触发
// cbInterrupt 跳闸打断 turn。修熔断语义（届时方向=失败不计连拒、连续失败降级
// parent/human 而非打断 turn）等真实样本，不在本件。
//
// 三接口防错配（详见 guardian.go 各接口 doc）：
//   - Review        — 失败静默折叠成 deny，err 恒 nil（交互链 legacy 契约）；
//   - ReviewVerdict — 失败返回 err，供 auto_review 降级人工；
//   - ReviewAction  — 无人值守 fail-closed，且不参与熔断簿记。
//
// 本文件测试名即缺陷观测点：语义修复时这些测试应当被有意重写，而不是悄悄变绿。

// failureState 三态标识，与 guardian.go review() 的两条失败分支一一对应。
const (
	failureTransport  = "transport"   // agent.Run 返回 provider 传输错误
	failureTimeout    = "timeout"     // reviewTimeout（30s）压垮 review ctx
	failureUnparseable = "unparseable" // 模型输出无法 ParseAssessment
)

// deadlineProvider blocks every stream until the review context expires, then
// surfaces the deadline error — the same shape a real 30s reviewTimeout takes.
// The tests pass a shorter parent deadline so the suite does not wait 30s.
type deadlineProvider struct{}

func (deadlineProvider) Name() string { return "guardian-deadline" }

func (deadlineProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// newFailureSession builds a guardian session scripted to fail `state` on every
// review. For the timeout state the returned ctx must be used for the review
// call (its parent deadline is shorter than reviewTimeout); the other states
// ignore it. wantErr is the state marker each ReviewVerdict error must carry.
func newFailureSession(t *testing.T, state string) (gs *Session, reviewCtx context.Context, wantErr string) {
	t.Helper()
	var prov provider.Provider
	switch state {
	case failureTransport:
		p := &scriptedProvider{}
		for range 6 {
			p.responses = append(p.responses, scriptedResponse{err: errors.New("provider unavailable")})
		}
		prov = p
		wantErr = "provider unavailable"
		reviewCtx = context.Background()
	case failureTimeout:
		prov = deadlineProvider{}
		wantErr = "deadline"
		var cancel context.CancelFunc
		reviewCtx, cancel = context.WithTimeout(context.Background(), 120*time.Millisecond)
		t.Cleanup(cancel)
	case failureUnparseable:
		p := &scriptedProvider{}
		for range 6 {
			p.responses = append(p.responses, scriptedResponse{text: "no structured verdict in this reply"})
		}
		prov = p
		wantErr = "unparseable"
		reviewCtx = context.Background()
	default:
		t.Fatalf("unknown failure state %q", state)
	}
	gs = NewSession(prov, tool.NewRegistry(), PolicyPrompt(), "guardian-test", 0, nil, &captureSink{})
	return gs, reviewCtx, wantErr
}

func matrixParentSession() *agent.Session {
	parent := agent.NewSession("sys")
	parent.Add(provider.Message{Role: provider.RoleUser, Content: "do the thing"})
	return parent
}

// TestGuardianFailureStateMatrixTripsBreaker pins, per failure state: the review
// folds to a fail-closed deny carrying a state-identifiable error, every failure
// counts toward the breaker, and the third consecutive failure of THAT state
// alone trips cbInterrupt — i.e. 3 provider hiccups of one kind interrupt the
// turn even with zero authentic reviewer denials (the defect this matrix pins).
func TestGuardianFailureStateMatrixTripsBreaker(t *testing.T) {
	for _, state := range []string{failureTransport, failureTimeout, failureUnparseable} {
		t.Run(state, func(t *testing.T) {
			gs, reviewCtx, wantErr := newFailureSession(t, state)
			parent := matrixParentSession()

			// Reviews 1-2: folded deny carrying the state marker, counted.
			for i := 1; i <= 2; i++ {
				allow, reason, err := gs.ReviewVerdict(reviewCtx, "bash", json.RawMessage(`{"command":"rm -rf build"}`), parent)
				if err == nil {
					t.Fatalf("review %d: err = nil, want %s failure surfaced", i, state)
				}
				if !strings.Contains(err.Error(), wantErr) {
					t.Fatalf("review %d: err = %q, want marker %q", i, err, wantErr)
				}
				if allow {
					t.Fatalf("review %d: allow = true, want fail-closed deny", i)
				}
				if !strings.Contains(reason, "guardian denied") {
					t.Fatalf("review %d: reason = %q, want failure folded into deny reason", i, reason)
				}
				if gs.consecutiveDenials != i {
					t.Fatalf("review %d: consecutiveDenials = %d, want %d (failure counted)", i, gs.consecutiveDenials, i)
				}
			}

			// Third consecutive failure of this single state trips the breaker:
			// the model-facing reason switches from the per-denial text to the
			// circuit-breaker instruction.
			allow, reason, err := gs.ReviewVerdict(reviewCtx, "bash", json.RawMessage(`{"command":"rm -rf dist"}`), parent)
			if err == nil || allow {
				t.Fatalf("third failure: allow=%v err=%v, want fail-closed deny", allow, err)
			}
			if !strings.Contains(reason, "Guardian auto-review has denied too many requests") {
				t.Fatalf("third failure reason = %q, want circuit-breaker interruption", reason)
			}
			if !gs.interruptTriggered || gs.consecutiveDenials != 3 {
				t.Fatalf("breaker state = {triggered:%v consecutive:%d}, want {true 3}", gs.interruptTriggered, gs.consecutiveDenials)
			}
			if got := len(gs.recentDenials); got != 3 {
				t.Fatalf("recentDenials = %d, want 3", got)
			}

			// Fourth failure: the interrupt fires once per turn — latched, so the
			// reason reverts to the per-denial text even though the streak keeps
			// growing. Pin that too (a repeated interruption every round would
			// spam the model).
			allow, reason, err = gs.ReviewVerdict(reviewCtx, "bash", json.RawMessage(`{"command":"rm -rf /tmp"}`), parent)
			if err == nil || allow {
				t.Fatalf("fourth failure: allow=%v err=%v, want fail-closed deny", allow, err)
			}
			if strings.Contains(reason, "Guardian auto-review has denied too many requests") {
				t.Fatalf("fourth failure re-tripped the breaker: %q", reason)
			}
			if gs.consecutiveDenials != 4 {
				t.Fatalf("fourth failure: consecutiveDenials = %d, want 4", gs.consecutiveDenials)
			}
		})
	}
}

// TestGuardianMixedFailureStatesFoldIndistinguishably pins the core defect:
// transport, timeout and unparseable failures are recorded by the same
// recordDenial() as authentic denials, so a mix of three DIFFERENT failures
// trips the breaker exactly like three identical ones — the count carries no
// signal about what went wrong.
func TestGuardianMixedFailureStatesFoldIndistinguishably(t *testing.T) {
	// One session must observe the whole mix: script a single provider to fail
	// with a different state each round and drive all three through one breaker.
	p := &scriptedProvider{responses: []scriptedResponse{
		{err: errors.New("provider unavailable")},
		{text: "no structured verdict in this reply"},
		{err: errors.New("provider unavailable")},
	}}
	gs := NewSession(p, tool.NewRegistry(), PolicyPrompt(), "guardian-test", 0, nil, &captureSink{})
	parent := matrixParentSession()

	// Round 1: transport failure — folded deny, counted.
	allow, _, err := gs.ReviewVerdict(context.Background(), "bash", json.RawMessage(`{"command":"a"}`), parent)
	if err == nil || allow || gs.consecutiveDenials != 1 {
		t.Fatalf("transport round: allow=%v err=%v consecutive=%d, want deny/err/1", allow, err, gs.consecutiveDenials)
	}
	// Round 2: unparseable output — folded through the same counter.
	allow, _, err = gs.ReviewVerdict(context.Background(), "bash", json.RawMessage(`{"command":"b"}`), parent)
	if err == nil || allow || gs.consecutiveDenials != 2 {
		t.Fatalf("unparseable round: allow=%v err=%v consecutive=%d, want deny/err/2", allow, err, gs.consecutiveDenials)
	}
	// Round 3: transport failure again — mixed streak reaches 3 and trips,
	// despite none of the three being an authentic reviewer denial.
	allow, reason, err := gs.ReviewVerdict(context.Background(), "bash", json.RawMessage(`{"command":"c"}`), parent)
	if err == nil || allow {
		t.Fatalf("mixed third: allow=%v err=%v, want fail-closed deny", allow, err)
	}
	if !strings.Contains(reason, "Guardian auto-review has denied too many requests") {
		t.Fatalf("mixed third reason = %q, want breaker interruption", reason)
	}
	if !gs.interruptTriggered {
		t.Fatal("mixed failure streak did not trip the breaker")
	}
}

// TestGuardianResetTurnClearsFailureFoldedDenials pins the turn boundary: the
// failure-folded denials do not survive ResetTurn, so each new turn starts with
// a full three-failure budget (this is why provider flakiness costs exactly one
// turn, not the session).
func TestGuardianResetTurnClearsFailureFoldedDenials(t *testing.T) {
	gs, reviewCtx, _ := newFailureSession(t, failureTransport)
	parent := matrixParentSession()

	for i := 1; i <= 2; i++ {
		if _, _, err := gs.ReviewVerdict(reviewCtx, "bash", json.RawMessage(`{"command":"a"}`), parent); err == nil {
			t.Fatalf("review %d: want failure", i)
		}
	}
	if gs.consecutiveDenials != 2 {
		t.Fatalf("pre-reset consecutive = %d, want 2", gs.consecutiveDenials)
	}

	gs.ResetTurn()
	if gs.consecutiveDenials != 0 || gs.recentDenials != nil || gs.interruptTriggered {
		t.Fatalf("after ResetTurn: consecutive=%d recent=%v triggered=%v, want all cleared",
			gs.consecutiveDenials, gs.recentDenials, gs.interruptTriggered)
	}

	// Two failures in the new turn: without the reset this would have been the
	// 4th/5th and the breaker would already be latched.
	for i := 1; i <= 2; i++ {
		allow, reason, err := gs.ReviewVerdict(reviewCtx, "bash", json.RawMessage(`{"command":"b"}`), parent)
		if err == nil || allow {
			t.Fatalf("post-reset review %d: allow=%v err=%v, want fail-closed deny", i, allow, err)
		}
		if strings.Contains(reason, "denied too many requests") {
			t.Fatalf("post-reset review %d tripped breaker early: %q", i, reason)
		}
	}
	if gs.consecutiveDenials != 2 || gs.interruptTriggered {
		t.Fatalf("post-reset state: consecutive=%d triggered=%v, want 2/false", gs.consecutiveDenials, gs.interruptTriggered)
	}
	// Third post-reset failure trips again — the budget is per turn, not once.
	if _, reason, err := gs.ReviewVerdict(reviewCtx, "bash", json.RawMessage(`{"command":"c"}`), parent); err == nil {
		t.Fatal("third post-reset review: want failure")
	} else if !strings.Contains(reason, "denied too many requests") {
		t.Fatalf("third post-reset reason = %q, want breaker interruption", reason)
	}
}

// TestGuardianAuthenticAllowResetsFailureFoldedDenials pins the counter's other
// edge: a genuine allow clears the failure streak (recordAllow), so scattered
// failures separated by successful reviews never trip — only a run of three.
func TestGuardianAuthenticAllowResetsFailureFoldedDenials(t *testing.T) {
	p := &scriptedProvider{responses: []scriptedResponse{
		{err: errors.New("provider unavailable")},
		okAllow(),
		{err: errors.New("provider unavailable")},
		okAllow(),
		{err: errors.New("provider unavailable")},
	}}
	gs := NewSession(p, tool.NewRegistry(), PolicyPrompt(), "guardian-test", 0, nil, &captureSink{})
	parent := matrixParentSession()

	steps := []struct {
		wantFailure      bool // odd steps fail (scripted transport error), even steps allow
		wantConsecutive  int
		wantTriggered    bool
	}{
		{true, 1, false},
		{false, 0, false},
		{true, 1, false},
		{false, 0, false},
		{true, 1, false},
	}
	for i, step := range steps {
		allow, _, err := gs.ReviewVerdict(context.Background(), "read_file", json.RawMessage(`{"file_path":"a.txt"}`), parent)
		if step.wantFailure {
			if err == nil || allow {
				t.Fatalf("step %d: allow=%v err=%v, want failure-folded deny", i+1, allow, err)
			}
		} else {
			if err != nil {
				t.Fatalf("step %d: unexpected failure: %v", i+1, err)
			}
			if !allow {
				t.Fatalf("step %d: allow = false, want authentic allow", i+1)
			}
		}
		if gs.consecutiveDenials != step.wantConsecutive || gs.interruptTriggered != step.wantTriggered {
			t.Fatalf("step %d: consecutive=%d triggered=%v, want %d/%v",
				i+1, gs.consecutiveDenials, gs.interruptTriggered, step.wantConsecutive, step.wantTriggered)
		}
	}
	// A breaker trip is impossible here without three in a row: three failures
	// and two allows interleave to max streak 1. The failures still land in the
	// recent window — 10 of those in 50 reviews would trip on the window rule.
	if len(gs.recentDenials) != 5 || gs.countRecentDenials() != 3 {
		t.Fatalf("recent window = %d entries / %d denials, want 5 / 3", len(gs.recentDenials), gs.countRecentDenials())
	}
}

func okAllow() scriptedResponse {
	return scriptedResponse{text: `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"ok"}`}
}

// TestGuardianReviewActionFailureBypassesBreaker pins the third interface's
// bookkeeping: ReviewAction fails closed on a broken reviewer but never feeds
// recordDenial — three unattended failures do NOT interrupt anything. Callers
// picking the wrong interface therefore get different breaker behaviour for
// the identical provider outage (the mismatch this comment family guards).
func TestGuardianReviewActionFailureBypassesBreaker(t *testing.T) {
	p := &scriptedProvider{}
	for range 4 {
		p.responses = append(p.responses, scriptedResponse{err: errors.New("provider unavailable")})
	}
	gs := NewSession(p, tool.NewRegistry(), PolicyPrompt(), "guardian-test", 0, nil, &captureSink{})

	for i := 1; i <= 4; i++ {
		allow, _, failure := gs.ReviewAction(context.Background(), "bash", json.RawMessage(`{"command":"rm -rf /"}`), "unattended cleanup")
		if allow {
			t.Fatalf("action %d: allow = true, want fail-closed deny", i)
		}
		if failure == nil || !strings.Contains(failure.Error(), "provider unavailable") {
			t.Fatalf("action %d: failure = %v, want transport error surfaced", i, failure)
		}
	}
	if gs.consecutiveDenials != 0 || len(gs.recentDenials) != 0 || gs.interruptTriggered {
		t.Fatalf("after 4 failed actions: consecutive=%d recent=%v triggered=%v, want breaker untouched",
			gs.consecutiveDenials, gs.recentDenials, gs.interruptTriggered)
	}
}

// TestGuardianReviewFoldsFailureWithoutError pins the legacy interface: the same
// transport failure through Review returns err=nil (documented contract), so a
// caller cannot tell a provider outage from a reviewer deny by error alone —
// only ReviewVerdict exposes the difference.
func TestGuardianReviewFoldsFailureWithoutError(t *testing.T) {
	gs, reviewCtx, _ := newFailureSession(t, failureTransport)
	parent := matrixParentSession()

	allow, reason, err := gs.Review(reviewCtx, "bash", json.RawMessage(`{"command":"a"}`), parent)
	if err != nil {
		t.Fatalf("Review err = %v, want nil (legacy contract)", err)
	}
	if allow || !strings.Contains(reason, "guardian review failed") {
		t.Fatalf("Review = allow %v reason %q, want folded deny with failure rationale", allow, reason)
	}
	// The failure still counts — Review and ReviewVerdict share review().
	if gs.consecutiveDenials != 1 {
		t.Fatalf("consecutiveDenials = %d, want 1 (failure counted by Review too)", gs.consecutiveDenials)
	}
}
