package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// namedFakeProvider carries a configurable provider family name so tests can
// exercise cross-family destinations of the model override (task 602 lifted
// the task-148 same-family gate).
type namedFakeProvider struct {
	fakeProvider
	name string
}

func (p *namedFakeProvider) Name() string { return p.name }

// fakeModelResolver resolves canned refs to canned providers — the test stand-in
// for the boot-wired provider.Resolver seam (LocalProviderResolver or merged).
type fakeModelResolver struct {
	providers map[string]provider.Provider
	err       error
	last      provider.Selection
}

func (r *fakeModelResolver) Catalog() []provider.Descriptor { return nil }

func (r *fakeModelResolver) Resolve(sel provider.Selection) (provider.Provider, error) {
	r.last = sel
	if r.err != nil {
		return nil, r.err
	}
	if p, ok := r.providers[sel.Ref]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("unknown model %q", sel.Ref)
}

func TestSetSessionModelOverrideAcceptsCrossFamily(t *testing.T) {
	home := &namedFakeProvider{name: "fake"}
	alt := &namedFakeProvider{name: "fake", fakeProvider: fakeProvider{reply: "alt"}}
	other := &namedFakeProvider{name: "other"}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"fake/a":  alt,
		"other/b": other,
	}}
	price := &provider.Pricing{}
	a := New(home, nil, NewSession("s"), Options{ModelResolver: resolver, ContextWindow: 128_000, MaxOutputTokens: 8_192}, event.Discard)

	// Same-family target arms: the destination flips and the ref is observable.
	if !a.SetSessionModelOverride("fake/a", ModelOverrideExtras{}) {
		t.Fatal("same-family override rejected")
	}
	if got := a.providerForRequest(); got != provider.Provider(alt) {
		t.Fatal("destination did not flip to the resolved override provider")
	}
	if got := a.sessionModelOverrideRef(); got != "fake/a" {
		t.Fatalf("override ref = %q, want fake/a", got)
	}

	// Task 602: cross-family targets arm too — the destination is a fully
	// constructed adapter and every destination-scoped surface follows it.
	if !a.SetSessionModelOverride("other/b", ModelOverrideExtras{Pricing: price, ContextWindow: 256_000, MaxOutputTokens: 16_384, HighSpeedModels: []string{"other/b"}}) {
		t.Fatal("cross-family override rejected; task 602 lifted the family gate")
	}
	if got := a.providerForRequest(); got != provider.Provider(other) {
		t.Fatal("destination did not flip to the cross-family provider")
	}
	if got := a.sessionModelOverrideRef(); got != "other/b" {
		t.Fatalf("override ref = %q, want other/b", got)
	}
	// The entry-derived scalars follow the destination.
	if got := a.destinationModelRef(); got != "other/b" {
		t.Fatalf("destinationModelRef = %q, want other/b", got)
	}
	if got := a.destinationPricing(); got != price {
		t.Fatal("destinationPricing did not follow the override extras")
	}
	if got := a.destinationContextWindow(); got != 256_000 {
		t.Fatalf("destinationContextWindow = %d, want 256000", got)
	}
	if got := a.destinationMaxOutputTokens(); got != 16_384 {
		t.Fatalf("destinationMaxOutputTokens = %d, want 16384", got)
	}
	if got := a.destinationHighSpeedModels(); len(got) != 1 || got[0] != "other/b" {
		t.Fatalf("destinationHighSpeedModels = %v, want [other/b]", got)
	}
	// The learned-window composition takes the destination as configured input.
	if got := a.effectiveContextWindow(); got != 256_000 {
		t.Fatalf("effectiveContextWindow = %d, want 256000 (destination as configured input)", got)
	}

	// Zero extras keep the construction scalars (same-family callers without
	// an entry at hand stay conservative).
	if !a.SetSessionModelOverride("fake/a", ModelOverrideExtras{}) {
		t.Fatal("same-family override with zero extras rejected")
	}
	if got := a.destinationPricing(); got != nil {
		t.Fatalf("destinationPricing with zero extras = %v, want nil (construction)", got)
	}
	if got := a.destinationContextWindow(); got != 128_000 {
		t.Fatalf("destinationContextWindow with zero extras = %d, want 128000", got)
	}
	if got := a.destinationMaxOutputTokens(); got != 8_192 {
		t.Fatalf("destinationMaxOutputTokens with zero extras = %d, want 8192", got)
	}

	// Clearing always succeeds and restores the construction destination.
	if !a.SetSessionModelOverride("", ModelOverrideExtras{}) {
		t.Fatal("clearing the override must always succeed")
	}
	if got := a.providerForRequest(); got != provider.Provider(home) {
		t.Fatal("destination after clear is not the construction provider")
	}
	if got := a.sessionModelOverrideRef(); got != "" {
		t.Fatalf("override ref after clear = %q, want empty", got)
	}
	if got := a.destinationModelRef(); got != "" {
		t.Fatalf("destinationModelRef after clear = %q, want construction (empty opts)", got)
	}

	// Resolve failures decline instead of arming a dead destination.
	resolver.err = fmt.Errorf("catalog unavailable")
	if a.SetSessionModelOverride("fake/a", ModelOverrideExtras{}) {
		t.Fatal("resolve failure accepted")
	}
	if got := a.providerForRequest(); got != provider.Provider(home) {
		t.Fatal("declined resolve disturbed the construction destination")
	}
}

func TestSetSessionModelOverrideWithoutResolverSeam(t *testing.T) {
	a := New(&fakeProvider{}, nil, NewSession("s"), Options{}, event.Discard)
	if a.SetSessionModelOverride("fake/a", ModelOverrideExtras{}) {
		t.Fatal("override accepted without a resolver seam")
	}
	if got := a.sessionModelOverrideRef(); got != "" {
		t.Fatalf("override ref = %q, want empty", got)
	}
}

func TestSetSessionModelOverrideRecoveryForkDeclined(t *testing.T) {
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"fake/a": &namedFakeProvider{name: "fake"},
	}}
	a := New(&fakeProvider{}, nil, NewSession("s"), Options{ModelResolver: resolver}, event.Discard)
	path := filepath.Join(t.TempDir(), "session.json")
	if err := SaveBranchMeta(path, BranchMeta{ID: BranchID(path), Recovered: true}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	a.sess.path = path
	if a.SetSessionModelOverride("fake/a", ModelOverrideExtras{}) {
		t.Fatal("recovery-forked session accepted a model override; reanchor semantics live on the rebuild path")
	}
	if got := a.providerForRequest(); got == nil {
		t.Fatal("destination lost")
	}
}

func TestSetSessionModelOverrideCarriesSessionEffort(t *testing.T) {
	varying := &varyingProvider{efforts: []string{"low", "max"}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"fake/a": &namedFakeProvider{name: "fake"},
	}}
	a := New(varying, nil, NewSession("s"), Options{ModelResolver: resolver}, event.Discard)
	if !a.SetSessionEffortOverride("max") {
		t.Fatal("set session effort")
	}
	if !a.SetSessionModelOverride("fake/a", ModelOverrideExtras{}) {
		t.Fatal("same-family override rejected")
	}
	if resolver.last.Ref != "fake/a" {
		t.Fatalf("resolver ref = %q, want fake/a", resolver.last.Ref)
	}
	if resolver.last.Effort == nil || *resolver.last.Effort != "max" {
		t.Fatalf("resolver effort = %v, want max", resolver.last.Effort)
	}
}

func TestSamplingRequestDestinationSticksAcrossOverrideFlip(t *testing.T) {
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"fake/next": &namedFakeProvider{name: "fake"},
	}}
	a := New(&fakeProvider{}, nil, NewSession("s"), Options{ModelResolver: resolver}, event.Discard)
	captured := &namedFakeProvider{name: "fake"}
	frozen := samplingRequest{prov: captured}
	// A switch landing after the freeze must not re-route the frozen round:
	// the destination was captured at freeze, mirroring EffortOverride riding
	// inside the frozen payload.
	if !a.SetSessionModelOverride("fake/next", ModelOverrideExtras{}) {
		t.Fatal("arm override")
	}
	if got := frozen.destination(a); got != provider.Provider(captured) {
		t.Fatal("frozen round re-routed after a post-freeze override flip")
	}
	// An uncaptured request follows the effective destination.
	var unfrozen samplingRequest
	if got := unfrozen.destination(a); got == nil || got == provider.Provider(a.svc.prov) && got != a.providerForRequest() {
		t.Fatal("unfrozen request did not follow the effective destination")
	}
	if !a.SetSessionModelOverride("", ModelOverrideExtras{}) {
		t.Fatal("clear override")
	}
	if got := unfrozen.destination(a); got != provider.Provider(a.svc.prov) {
		t.Fatal("unfrozen request after clear is not the construction provider")
	}
}

func TestStreamProviderRequestRoutesToDestination(t *testing.T) {
	home := &fakeProvider{}
	alt := &namedFakeProvider{name: "fake"}
	a := New(home, nil, NewSession("s"), Options{}, event.Discard)
	req := provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}
	if _, err := a.streamProviderRequest(context.Background(), alt, req); err != nil {
		t.Fatalf("stream via destination: %v", err)
	}
	if len(alt.got) == 0 {
		t.Fatal("destination provider did not receive the request")
	}
	if len(home.got) != 0 {
		t.Fatal("construction provider received a request routed elsewhere")
	}
	// nil destination falls back to the agent's effective destination.
	if _, err := a.streamProviderRequest(context.Background(), nil, req); err != nil {
		t.Fatalf("stream via fallback: %v", err)
	}
	if len(home.got) == 0 {
		t.Fatal("fallback did not reach the construction provider")
	}
}

// TestEffortVocabularyProbeFollowsModelDestination pins the task-148 change to
// SetSessionEffortOverride: after a model override arms, the per-request depth
// vocabulary is probed on the destination the requests actually reach, not on
// the construction provider.
func TestEffortVocabularyProbeFollowsModelDestination(t *testing.T) {
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"fake/plain": &namedFakeProvider{name: "fake"}, // not effort-varying
	}}
	a := New(&varyingProvider{efforts: []string{"low", "max"}}, nil, NewSession("s"), Options{ModelResolver: resolver}, event.Discard)
	if !a.SetSessionEffortOverride("max") {
		t.Fatal("base provider vocabulary rejected a listed level")
	}
	if !a.SetSessionModelOverride("fake/plain", ModelOverrideExtras{}) {
		t.Fatal("same-family override rejected")
	}
	if a.SetSessionEffortOverride("low") {
		t.Fatal("effort override armed against a destination whose vocabulary is unknown")
	}
	if got := a.effortOverrideForRequest(); got != "max" {
		t.Fatalf("override after declined level = %q, want max", got)
	}
}

// TestReplaySensitivityFollowsDestination pins the task-602 protocol-scoped
// conversion: the sampling-attempt buffering decision probes the destination's
// reasoning contract, so a hot switch to a protocol that owns its reasoning
// keeps streaming live instead of inheriting the construction provider's
// buffering.
func TestReplaySensitivityFollowsDestination(t *testing.T) {
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"other/b": &namedFakeProvider{name: "other"},
	}}
	a := New(&reasoningSensitiveProvider{}, nil, NewSession("s"), Options{ModelResolver: resolver}, event.Discard)
	if buffered, _ := a.samplingAttemptSinks(); buffered == nil {
		t.Fatal("replay-sensitive construction provider did not buffer the stream sink")
	}
	if !a.SetSessionModelOverride("other/b", ModelOverrideExtras{}) {
		t.Fatal("cross-family override rejected")
	}
	if buffered, _ := a.samplingAttemptSinks(); buffered != nil {
		t.Fatal("destination followed the construction provider's replay sensitivity after a hot switch")
	}
}

// reasoningSensitiveProvider advertises the strict reasoning contract
// (tool-call reasoning + round trip, no empty fallback) the buffering branch
// keys on.
type reasoningSensitiveProvider struct {
	fakeProvider
}

func (p *reasoningSensitiveProvider) Name() string { return "strict" }

func (p *reasoningSensitiveProvider) RequiresToolCallReasoning() bool { return true }

func (p *reasoningSensitiveProvider) RequiresReasoningRoundTrip() bool { return true }
