package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// quotaProvider fails every sampling attempt with a quota-class error — the
// coding-plan 5h exhaustion shape (HTTP 429 credits/subscription).
type quotaProvider struct {
	call int
}

func (p *quotaProvider) Name() string { return "quota-provider" }

func (p *quotaProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.call++
	return nil, &provider.QuotaError{Status: 429, Code: "insufficient_quota"}
}

// Task 242 B1: WITH a fallback target stamped, quota errors join the fast
// retry loop instead of failing on the first response — the loop exhausts the
// sampling budget and then hands the error up for the controller to switch
// (the sleep is already a no-op in main_test, so this is instant).
func TestQuotaRetriesWhenFallbackConfigured(t *testing.T) {
	prov := &quotaProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("sys"), Options{}, &incompleteReadEventSink{})

	err := a.Run(WithModelFallback(context.Background(), "backup/model-b"), "list my tasks")
	if err == nil {
		t.Fatal("exhausted quota must end the run with an error")
	}
	if !strings.Contains(err.Error(), "quota") {
		t.Fatalf("error = %v, want the quota classification", err)
	}
	if prov.call < 2 {
		t.Fatalf("provider calls = %d, want the retry loop to run more than once when a fallback is configured", prov.call)
	}
}

// Task 845 (user-ratified semantics ②) updated the pre-242 default: WITHOUT a
// stamped target quota no longer dies on the first failure — it enters the
// long-wait lane, probes until the quota window rolls over (bounded by the
// quota wait budget), and only then surfaces. The 242 fallback contract above
// is untouched; this test pins the new default lane and its UI face.
func TestQuotaLongWaitsWhenFallbackOff(t *testing.T) {
	prov := &quotaProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("sys"), Options{
		QuotaWaitInterval: time.Millisecond,
		QuotaWaitBudget:   5 * time.Millisecond,
	}, &incompleteReadEventSink{})

	err := a.Run(context.Background(), "list my tasks")
	if err == nil {
		t.Fatal("exhausted quota must still end the run once the wait budget is gone")
	}
	if prov.call <= 1 {
		t.Fatalf("provider calls = %d, want the long-wait lane to probe beyond the first failure", prov.call)
	}
	var exhausted *provider.RecoveryWaitExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Phase != "quota" {
		t.Fatalf("err = %v, want a quota wait-exhausted termination", err)
	}
	if !strings.Contains(err.Error(), "quota") {
		t.Fatalf("error = %v, want the quota classification preserved", err)
	}
}
