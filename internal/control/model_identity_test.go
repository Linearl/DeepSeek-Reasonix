package control

import (
	"context"
	"fmt"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// overrideResolver resolves canned refs so the SetSessionModelOverride mirror
// can be observed through the resolver's last selection.
type overrideResolver struct {
	last provider.Selection
}

func (r *overrideResolver) Catalog() []provider.Descriptor { return nil }

func (r *overrideResolver) Resolve(sel provider.Selection) (provider.Provider, error) {
	r.last = sel
	return stubModelProvider{}, nil
}

type stubModelProvider struct{}

func (stubModelProvider) Name() string { return "stub" }

func (stubModelProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	return nil, fmt.Errorf("not used")
}

// TestSetModelIdentityRebindsActiveReads pins the task-602 hot-switch rebind:
// after SetModelIdentity, the canonical ref, label, balance endpoint, and the
// frozen image gate read from the swap; zero/nil fields fall back to the
// construction identity per field.
func TestSetModelIdentityRebindsActiveReads(t *testing.T) {
	on, off := true, false
	c := New(controlOptionsForIdentityTest("a/one", "one", "https://old.example/balance", "k1", &on))
	if got := c.activeModelRef(); got != "a/one" {
		t.Fatalf("construction modelRef = %q, want a/one", got)
	}
	if url, key := c.activeBalance(); url != "https://old.example/balance" || key != "k1" {
		t.Fatalf("construction balance = (%q, %q), want old endpoint", url, key)
	}

	c.SetModelIdentity("b/two", "two", "https://new.example/balance", "k2", &off)
	if got := c.activeModelRef(); got != "b/two" {
		t.Fatalf("activeModelRef = %q, want b/two after the identity swap", got)
	}
	if got := c.activeLabel(); got != "two" {
		t.Fatalf("activeLabel = %q, want two", got)
	}
	if got := c.ModelRef(); got != "b/two" {
		t.Fatalf("ModelRef() = %q, want b/two", got)
	}
	if got := c.Label(); got != "two" {
		t.Fatalf("Label() = %q, want two", got)
	}
	if url, key := c.activeBalance(); url != "https://new.example/balance" || key != "k2" {
		t.Fatalf("activeBalance = (%q, %q), want new endpoint", url, key)
	}
	if img := c.activeImageInput(); img == nil || *img {
		t.Fatal("activeImageInput = on, want the swapped false gate")
	}
	if c.ImageInputEnabled() {
		t.Fatal("ImageInputEnabled followed the construction provider, not the swap")
	}

	// Zero/nil fields fall back to the construction identity per field.
	c.SetModelIdentity("", "", "", "", nil)
	if got := c.activeModelRef(); got != "a/one" {
		t.Fatalf("activeModelRef after zero swap = %q, want construction a/one", got)
	}
	if got := c.activeLabel(); got != "one" {
		t.Fatalf("activeLabel after zero swap = %q, want construction one", got)
	}
	if url, key := c.activeBalance(); url != "https://old.example/balance" || key != "k1" {
		t.Fatalf("activeBalance after zero swap = (%q, %q), want construction endpoint", url, key)
	}
	if img := c.activeImageInput(); img == nil || !*img {
		t.Fatal("activeImageInput after zero swap = off, want construction true gate")
	}
}

// TestSetSessionModelOverrideMirrorCarriesExtras pins the controller mirror's
// pass-through: the ref reaches the executor's resolver selection and a
// resolvable target is accepted.
func TestSetSessionModelOverrideMirrorCarriesExtras(t *testing.T) {
	resolver := &overrideResolver{}
	ag := agent.New(stubModelProvider{}, tool.NewRegistry(), agent.NewSession("s"), agent.Options{ModelResolver: resolver}, event.Discard)
	c := New(Options{Executor: ag, Sink: event.Discard})

	price := &provider.Pricing{}
	if !c.SetSessionModelOverride("other/b", agent.ModelOverrideExtras{Pricing: price, ContextWindow: 200_000}) {
		t.Fatal("mirror declined a resolvable cross-family target; task 602 lifted the family gate")
	}
	if resolver.last.Ref != "other/b" {
		t.Fatalf("resolver selection = %q, want other/b", resolver.last.Ref)
	}
}

func controlOptionsForIdentityTest(ref, label, balanceURL, balanceKey string, image *bool) Options {
	ag := agent.New(stubModelProvider{}, tool.NewRegistry(), agent.NewSession("s"), agent.Options{}, event.Discard)
	return Options{
		Executor:         ag,
		Sink:             event.Discard,
		ModelRef:         ref,
		Label:            label,
		BalanceURL:       balanceURL,
		BalanceKey:       balanceKey,
		FrozenImageInput: image,
	}
}
