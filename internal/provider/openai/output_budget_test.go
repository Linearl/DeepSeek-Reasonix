package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/provider"
)

func TestSharedWindowOutputBudgetCapability(t *testing.T) {
	deepseek := &client{deepseek: true, maxOutputTokens: 32 * 1024}
	if !deepseek.SharesContextWindow() || deepseek.OutputBudget() != 32*1024 {
		t.Fatalf("DeepSeek capability = shared:%v budget:%d", deepseek.SharesContextWindow(), deepseek.OutputBudget())
	}
	openai := &client{maxOutputTokens: 32 * 1024}
	if openai.SharesContextWindow() {
		t.Fatal("ordinary OpenAI mode must keep its independent output ceiling")
	}
}

func TestOfficialDeepSeekPolicyOmitsWhenSafe(t *testing.T) {
	p, err := New(provider.Config{Name: "ds", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro"})
	if err != nil {
		t.Fatal(err)
	}
	got := p.(provider.ContextBudgetPolicyProvider).ContextBudgetPolicy()
	if got.WindowMode != provider.ContextWindowShared || got.AutoOutputTokens != provider.DeepSeekMaxOutputTokens || got.LimitMode != provider.OutputLimitOmitWhenSafe {
		t.Fatalf("official DeepSeek policy = %+v", got)
	}
	req := p.(*client).buildRequest(provider.Request{})
	if req.MaxTokens != 0 {
		t.Fatalf("safe official DeepSeek should omit max_tokens, got %d", req.MaxTokens)
	}
	clipped := p.(*client).buildRequest(provider.Request{MaxTokens: 229_502})
	if clipped.MaxTokens != 229_502 {
		t.Fatalf("clipped official DeepSeek max_tokens = %d", clipped.MaxTokens)
	}
}

func TestOpenCodeGoChatPolicySendsGenericMaxTokens(t *testing.T) {
	p, err := New(provider.Config{Name: "og", BaseURL: "https://opencode.ai/zen/go/v1", Model: "kimi-k3"})
	if err != nil {
		t.Fatal(err)
	}
	got := p.(provider.ContextBudgetPolicyProvider).ContextBudgetPolicy()
	if got.WindowMode != provider.ContextWindowShared || got.MaxOutputTokens != 131_072 || got.LimitMode != provider.OutputLimitAlways {
		t.Fatalf("OpenCode Go kimi-k3 policy = %+v", got)
	}
	req := p.(*client).buildRequest(provider.Request{MaxTokens: 131_072})
	if req.MaxTokens != 131_072 || req.MaxCompletionTokens != 0 {
		t.Fatalf("OpenCode Go Kimi must keep generic max_tokens: %+v", req)
	}
}

func TestOfficialKimiK3KeepsMaxCompletionTokens(t *testing.T) {
	p, err := New(provider.Config{
		Name: "kimi", BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-k3",
		Extra: map[string]any{"reasoning_protocol": "kimi-k3", "max_output_tokens": 131_072},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := p.(provider.ContextBudgetPolicyProvider).ContextBudgetPolicy()
	if got.WindowMode != provider.ContextWindowShared || got.AutoOutputTokens != 131_072 {
		t.Fatalf("official Kimi K3 policy = %+v", got)
	}
	req := p.(*client).buildRequest(provider.Request{MaxTokens: 131_072})
	if req.MaxTokens != 0 || req.MaxCompletionTokens != 131_072 {
		t.Fatalf("official Kimi K3 wire = max_tokens %d max_completion_tokens %d", req.MaxTokens, req.MaxCompletionTokens)
	}
}

func TestSharedWindowInputPolicyCountsReplayedReasoning(t *testing.T) {
	if !(&client{deepseek: true}).SharedWindowInputPolicy().ReplaysOrdinaryReasoning {
		t.Fatal("DeepSeek replays reasoning_content on every assistant turn that carries it; admission must count it")
	}
	if (&client{}).SharedWindowInputPolicy().ReplaysOrdinaryReasoning {
		t.Fatal("ordinary OpenAI mode strips history reasoning and must not count it")
	}
}

// TestUnknownGatewayWireOmitsUnsetMaxTokens pins the wire half of task 752:
// after a fallback switch the request reaches the destination adapter with
// MaxTokens unset (the agent no longer leaks the previous provider's cap), so
// the mimo-class wire must carry NO max_tokens — the server default applies.
// An over-limit value that does arrive (a pre-fix request, or a user-configured
// cap) is passed through untouched: the adapter has no ceiling metadata for
// unknown gateways, clamping is the agent's admission job.
func TestUnknownGatewayWireOmitsUnsetMaxTokens(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	// The fallback destination's exact shape: unknown gateway, no entry cap.
	p, err := New(provider.Config{Name: "mimo", BaseURL: srv.URL, Model: "mimo-v2.6-flash", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	policy := p.(provider.ContextBudgetPolicyProvider).ContextBudgetPolicy()
	if policy.MaxOutputTokens != 0 {
		t.Fatalf("unknown-gateway policy MaxOutputTokens = %d, want 0 (no metadata to clamp with)", policy.MaxOutputTokens)
	}

	ch, err := p.Stream(context.Background(), provider.Request{
		Messages:  []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		MaxTokens: 0, // what the agent sends after a task-752 cross-provider switch
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for chunk := range ch {
		if chunk.Type == provider.ChunkError {
			t.Fatalf("stream error: %v", chunk.Err)
		}
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		if v, exists := gotReq[field]; exists {
			t.Fatalf("unset request must omit %q on an unknown gateway, got %v: %+v", field, v, gotReq)
		}
	}

	// An entry-configured cap fills the unset request value — destination-owned
	// metadata (the destination adapter is built from the destination entry), so
	// this fallback can never resurrect the previous provider's cap.
	capped, err := New(provider.Config{Name: "mimo", BaseURL: srv.URL, Model: "mimo-v2.6-flash", APIKey: "k",
		Extra: map[string]any{"max_output_tokens": 65_536}})
	if err != nil {
		t.Fatal(err)
	}
	if got := capped.(*client).buildRequest(provider.Request{}).MaxTokens; got != 65_536 {
		t.Fatalf("entry cap backfill = %d, want 65536", got)
	}
	// A raw over-limit value rides as-is (documented: no adapter-side clamp).
	if got := p.(*client).buildRequest(provider.Request{MaxTokens: 384_000}).MaxTokens; got != 384_000 {
		t.Fatalf("raw passthrough = %d, want 384000 (clamping lives in agent admission)", got)
	}
}
