package agent

import (
	"context"
	"strings"
	"testing"

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

// WITHOUT a stamped target (switch off — the default) quota keeps its
// pre-242 contract: the very first failure surfaces, no retry burn.
func TestQuotaFailsFastWhenFallbackOff(t *testing.T) {
	prov := &quotaProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("sys"), Options{}, &incompleteReadEventSink{})

	err := a.Run(context.Background(), "list my tasks")
	if err == nil {
		t.Fatal("quota error must surface")
	}
	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want 1 (default-off must not retry, zero regression)", prov.call)
	}
}
