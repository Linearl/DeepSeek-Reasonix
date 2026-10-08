package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// countingFakeProvider counts sampling/summary Stream calls so the hot-switch
// tests can prove a model switch triggers zero compaction calls (任务638
// acceptance: the only allowed cost of a switch is a prompt-cache miss).
type countingFakeProvider struct {
	fakeProvider
	calls int
}

func (p *countingFakeProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.calls++
	return p.fakeProvider.Stream(ctx, req)
}

// hotSwitchFixture installs a hand-built v4 sidecar (sys + digest covering the
// first five canonical messages) and boots an agent whose destination is
// "fake/a", with "other/b" resolvable for the switch. No provider call is
// needed: the sidecar restores directly, so call counters start at zero.
type hotSwitchFixture struct {
	agent     *Agent
	home      *countingFakeProvider
	other     *countingFakeProvider
	path      string
	canonical []provider.Message
	baseKey   string
	switchKey string
}

func newHotSwitchFixture(t *testing.T) *hotSwitchFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	// Covered turns are long enough that replacing them with a digest really
	// saves tokens, so the panel-ratio assertion is meaningful.
	canonical := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: strings.Repeat("task-one-body-", 30)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("done-one-body-", 30)},
		{Role: provider.RoleUser, Content: strings.Repeat("task-two-body-", 30)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("done-two-body-", 30)},
		{Role: provider.RoleUser, Content: "tail-user"},
		{Role: provider.RoleAssistant, Content: "tail-reply"},
	}
	const covered = 5
	projection := ContextProjection{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			formatSummaryMessage("earlier rounds digested"),
		},
		TranscriptVersion: uint64(len(canonical)),
		CoveredCount:      covered,
		CoveredPrefixHash: coveredPrefixHash(canonical, covered),
		PinnedContextHash: pinnedContextCoverageHash(canonical, covered),
	}
	home := &countingFakeProvider{fakeProvider: fakeProvider{reply: "digest"}}
	other := &countingFakeProvider{fakeProvider: fakeProvider{reply: "digest"}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"other/b": other,
	}}
	// Save before New so LoadProjectionSidecar restores (no sidecar_missing
	// kick, no background rebuild racing the call counters).
	if err := SaveCompactionState(path, CompactionState{
		SchemaVersion:     compactionStateSchemaV1, // Save forces the current schema on write
		PromptCacheKey:    promptCacheKey("ws", BranchID(path), "fake/a"),
		TranscriptVersion: uint64(len(canonical)),
		Projection:        projection,
	}); err != nil {
		t.Fatal(err)
	}
	sess := &Session{Messages: append([]provider.Message(nil), canonical...)}
	a := New(home, nil, sess, Options{
		SessionPath:   path,
		WorkspaceID:   "ws",
		ModelRef:      "fake/a",
		ModelResolver: resolver,
	}, event.Discard)
	if a.sess.checkpointState != "restored" {
		t.Fatalf("checkpointState = %q, want restored sidecar", a.sess.checkpointState)
	}
	return &hotSwitchFixture{
		agent:     a,
		home:      home,
		other:     other,
		path:      path,
		canonical: canonical,
		baseKey:   promptCacheKey("ws", BranchID(path), "fake/a"),
		switchKey: promptCacheKey("ws", BranchID(path), "other/b"),
	}
}

// assertProjectedWire pins the wire shape of the folded view: 2 body messages
// (live system + digest) + the uncovered tail, never the full canonical.
func assertProjectedWire(t *testing.T, wire, canonical []provider.Message) {
	t.Helper()
	if len(wire) != 4 {
		t.Fatalf("wire view len = %d (%+v), want the projected 4-message view, not the %d-message canonical", len(wire), wire, len(canonical))
	}
	if wire[0].Content != "sys" || !isCompactionSummary(wire[1]) {
		t.Fatalf("wire prefix = %+v, want live system + digest", wire[:2])
	}
	if wire[2].Content != "tail-user" || wire[3].Content != "tail-reply" {
		t.Fatalf("wire tail = %+v, want the uncovered canonical tail", wire[2:])
	}
}

// 任务638 acceptance 1+2+3 (report section 5): after a model hot switch the
// projection stays (no full-canonical fallback on the wire), no summary call
// fires, and the cache namespace rebinds to the destination — the runtime twin
// of the LoadProjectionSidecar fallback. The reasoning-replay overlay clears
// with the namespace while the model-agnostic projection body stays.
func TestModelHotSwitchKeepsProjectedViewAndRebinds(t *testing.T) {
	fx := newHotSwitchFixture(t)
	a := fx.agent

	wireBefore := a.modelVisibleMessages()
	assertProjectedWire(t, wireBefore, fx.canonical)
	if got := a.sess.compactionState.PromptCacheKey; got != fx.baseKey {
		t.Fatalf("initial key = %q, want %q", got, fx.baseKey)
	}
	if fx.home.calls != 0 || fx.other.calls != 0 {
		t.Fatalf("fixture leaked provider calls: home=%d other=%d", fx.home.calls, fx.other.calls)
	}

	// Arm the thinking-repair overlay: a namespace change is a model change,
	// so the overlay must not govern the destination's requests.
	a.sess.reasoningReplayStrongProjection = 7
	a.sess.reasoningReplayStrongProjectionAnchor = "anchor-638"

	if !a.SetSessionModelOverride("other/b", ModelOverrideExtras{}) {
		t.Fatal("override declined")
	}

	wireAfter := a.modelVisibleMessages()
	assertProjectedWire(t, wireAfter, fx.canonical) // no fallback to the 7-message canonical
	if got := a.sess.compactionState.PromptCacheKey; got != fx.switchKey {
		t.Fatalf("PromptCacheKey after switch = %q, want rebind to %q", got, fx.switchKey)
	}
	if a.sess.reasoningReplayStrongProjection != 0 || a.sess.reasoningReplayStrongProjectionAnchor != "" {
		t.Fatal("reasoning-replay overlay survived the model namespace change")
	}
	// The sidecar follows the destination so a restart keeps the new namespace.
	disk, ok, err := LoadCompactionState(fx.path)
	if err != nil || !ok {
		t.Fatalf("reload sidecar: ok=%v err=%v", ok, err)
	}
	if disk.PromptCacheKey != fx.switchKey {
		t.Fatalf("persisted key after switch = %q, want %q", disk.PromptCacheKey, fx.switchKey)
	}
	if len(disk.Projection.Messages) == 0 {
		t.Fatal("rebind dropped the projection body")
	}

	// A full maintenance round on the switched destination must stay silent:
	// the projected view is below the trigger, so no summary is requested.
	if _, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatalf("prepare after switch: %v", err)
	}
	if fx.home.calls != 0 || fx.other.calls != 0 {
		t.Fatalf("model switch triggered summary calls: home=%d other=%d", fx.home.calls, fx.other.calls)
	}

	// The panel readout agrees: projected, and smaller than canonical.
	snap := a.ContextMaintenanceSnapshot()
	if !snap.ProjectionValid {
		t.Fatal("ContextMaintenanceSnapshot reports the projection invalid after a pure model switch")
	}
	if snap.ProjectedTokens >= snap.CanonicalTokens {
		t.Fatalf("view did not stay folded: projected=%d canonical=%d", snap.ProjectedTokens, snap.CanonicalTokens)
	}
}

// 任务638 acceptance 3: switching back to the original model keeps the fold
// and rebinds the namespace back — a switch in either direction only moves
// the cache namespace.
func TestModelHotSwitchBackRestoresOriginalNamespace(t *testing.T) {
	fx := newHotSwitchFixture(t)
	a := fx.agent

	if !a.SetSessionModelOverride("other/b", ModelOverrideExtras{}) {
		t.Fatal("override declined")
	}
	if wire := a.modelVisibleMessages(); len(wire) != 4 {
		t.Fatalf("post-switch wire len = %d, want 4", len(wire))
	}
	if got := a.sess.compactionState.PromptCacheKey; got != fx.switchKey {
		t.Fatalf("post-switch key = %q, want %q", got, fx.switchKey)
	}

	// Clear the override: the construction destination ("fake/a") takes over.
	if !a.SetSessionModelOverride("", ModelOverrideExtras{}) {
		t.Fatal("clearing the override must succeed")
	}
	wireBack := a.modelVisibleMessages()
	assertProjectedWire(t, wireBack, fx.canonical)
	if got := a.sess.compactionState.PromptCacheKey; got != fx.baseKey {
		t.Fatalf("key after switching back = %q, want %q", got, fx.baseKey)
	}
	disk, ok, err := LoadCompactionState(fx.path)
	if err != nil || !ok {
		t.Fatalf("reload sidecar: ok=%v err=%v", ok, err)
	}
	if disk.PromptCacheKey != fx.baseKey {
		t.Fatalf("persisted key after switching back = %q, want %q", disk.PromptCacheKey, fx.baseKey)
	}
	if fx.home.calls != 0 || fx.other.calls != 0 {
		t.Fatalf("switch-back triggered summary calls: home=%d other=%d", fx.home.calls, fx.other.calls)
	}
}

// 任务638 acceptance 4: real content drift still fails closed — the view
// falls back to the full canonical transcript (with or without a model
// switch) and the namespace is NOT rewritten over a stale fold.
func TestModelHotSwitchStillFailsClosedOnContentDrift(t *testing.T) {
	fx := newHotSwitchFixture(t)
	a := fx.agent

	// Rewrite a covered user turn behind the fold: the covered-prefix hash
	// must reject the fold regardless of which model asks.
	a.sess.conversation.Messages[1].Content = "task-one-EDITED"

	wire := a.modelVisibleMessages()
	if len(wire) != len(fx.canonical) {
		t.Fatalf("drifted wire len = %d, want the full canonical %d (fail closed)", len(wire), len(fx.canonical))
	}
	if wire[1].Content != "task-one-EDITED" {
		t.Fatalf("wire[1] = %q, want the edited canonical turn", wire[1].Content)
	}
	if got := a.sess.compactionState.PromptCacheKey; got != fx.baseKey {
		t.Fatalf("key drifted to %q without a valid fold; want %q untouched", got, fx.baseKey)
	}
	if snap := a.ContextMaintenanceSnapshot(); snap.ProjectionValid {
		t.Fatal("content drift must report the projection invalid")
	}

	// The same drift under a model switch must not rebind either.
	if !a.SetSessionModelOverride("other/b", ModelOverrideExtras{}) {
		t.Fatal("override declined")
	}
	if wire := a.modelVisibleMessages(); len(wire) != len(fx.canonical) {
		t.Fatalf("drifted wire under switch len = %d, want full canonical", len(wire))
	}
	if got := a.sess.compactionState.PromptCacheKey; got != fx.baseKey {
		t.Fatalf("key rebound over stale content: %q, want %q", got, fx.baseKey)
	}
}
