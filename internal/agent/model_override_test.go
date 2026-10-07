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
// exercise the same-family gate (Name equality) of the model override.
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

func TestSetSessionModelOverrideFamilyGate(t *testing.T) {
	home := &namedFakeProvider{name: "fake"}
	alt := &namedFakeProvider{name: "fake", fakeProvider: fakeProvider{reply: "alt"}}
	other := &namedFakeProvider{name: "other"}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"fake/a":  alt,
		"other/b": other,
	}}
	a := New(home, nil, NewSession("s"), Options{ModelResolver: resolver}, event.Discard)

	// Same-family target arms: the destination flips and the ref is observable.
	if !a.SetSessionModelOverride("fake/a") {
		t.Fatal("same-family override rejected")
	}
	if got := a.providerForRequest(); got != provider.Provider(alt) {
		t.Fatal("destination did not flip to the resolved override provider")
	}
	if got := a.sessionModelOverrideRef(); got != "fake/a" {
		t.Fatalf("override ref = %q, want fake/a", got)
	}

	// Cross-family target declines and leaves the armed destination intact.
	if a.SetSessionModelOverride("other/b") {
		t.Fatal("cross-family override accepted")
	}
	if got := a.providerForRequest(); got != provider.Provider(alt) {
		t.Fatal("declined cross-family switch disturbed the armed destination")
	}

	// Clearing always succeeds and restores the construction provider.
	if !a.SetSessionModelOverride("") {
		t.Fatal("clearing the override must always succeed")
	}
	if got := a.providerForRequest(); got != provider.Provider(home) {
		t.Fatal("destination after clear is not the construction provider")
	}
	if got := a.sessionModelOverrideRef(); got != "" {
		t.Fatalf("override ref after clear = %q, want empty", got)
	}

	// Resolve failures decline instead of arming a dead destination.
	resolver.err = fmt.Errorf("catalog unavailable")
	if a.SetSessionModelOverride("fake/a") {
		t.Fatal("resolve failure accepted")
	}
	if got := a.providerForRequest(); got != provider.Provider(home) {
		t.Fatal("declined resolve disturbed the construction destination")
	}
}

func TestSetSessionModelOverrideWithoutResolverSeam(t *testing.T) {
	a := New(&fakeProvider{}, nil, NewSession("s"), Options{}, event.Discard)
	if a.SetSessionModelOverride("fake/a") {
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
	if a.SetSessionModelOverride("fake/a") {
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
	if !a.SetSessionModelOverride("fake/a") {
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
	if !a.SetSessionModelOverride("fake/next") {
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
	if !a.SetSessionModelOverride("") {
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
	if !a.SetSessionModelOverride("fake/plain") {
		t.Fatal("same-family override rejected")
	}
	if a.SetSessionEffortOverride("low") {
		t.Fatal("effort override armed against a destination whose vocabulary is unknown")
	}
	if got := a.effortOverrideForRequest(); got != "max" {
		t.Fatalf("override after declined level = %q, want max", got)
	}
}
