package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/event"
)

// varyingProvider extends the shared fakeProvider with a request-scoped
// effort vocabulary, mirroring the openai adapter's PerRequestEfforts probe.
type varyingProvider struct {
	fakeProvider
	efforts []string
}

func (p *varyingProvider) PerRequestEfforts() []string { return p.efforts }

func TestSetSessionEffortOverrideVocabularyGate(t *testing.T) {
	newAgent := func(efforts []string) *Agent {
		return New(&varyingProvider{efforts: efforts}, nil, NewSession("s"), Options{}, event.Discard)
	}

	a := newAgent([]string{"low", "max"})
	if !a.SetSessionEffortOverride("max") {
		t.Fatal("vocabulary-listed level rejected")
	}
	if got := a.effortOverrideForRequest(); got != "max" {
		t.Fatalf("override = %q, want max", got)
	}
	if a.SetSessionEffortOverride("high") {
		t.Fatal("level outside the vocabulary accepted")
	}
	if got := a.effortOverrideForRequest(); got != "max" {
		t.Fatalf("override after rejected level = %q, want max", got)
	}
	if !a.SetSessionEffortOverride("") {
		t.Fatal("clearing the override must always succeed")
	}
	if got := a.effortOverrideForRequest(); got != "" {
		t.Fatalf("override after clear = %q, want empty", got)
	}
}

func TestSetSessionEffortOverrideNonVaryingProvider(t *testing.T) {
	a := New(&fakeProvider{}, nil, NewSession("s"), Options{}, event.Discard)
	if a.SetSessionEffortOverride("max") {
		t.Fatal("non-varying provider accepted a per-request override")
	}
	if got := a.effortOverrideForRequest(); got != "" {
		t.Fatalf("override = %q, want empty", got)
	}
}

// Task 1388: every decline line carries path=fallback so a slow switch
// reconciles against the desktop's runtime-build-end path=fallback line, and
// the vocabulary branch names what the probe saw — the gateway entries fixed
// by the protocol-scoped GLM probe declined as exactly this branch with an
// empty vocabulary.
func TestSetSessionEffortOverrideDeclineLogsCarryFallbackPath(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })

	// Decline ②: the provider exposes no per-request probe.
	a := New(&fakeProvider{}, nil, NewSession("s"), Options{}, event.Discard)
	if a.SetSessionEffortOverride("max") {
		t.Fatal("non-varying provider accepted a per-request override")
	}
	// Decline ③: the probe is present but the level missed it.
	v := New(&varyingProvider{efforts: []string{"low"}}, nil, NewSession("s"), Options{}, event.Discard)
	if v.SetSessionEffortOverride("max") {
		t.Fatal("level outside the vocabulary accepted")
	}

	logs := buf.String()
	for _, want := range []string{
		"reason=provider-not-effort-varying",
		"reason=level-not-in-vocabulary",
		"level=max",
		"vocabulary=low",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("decline log missing %q in:\n%s", want, logs)
		}
	}
	if strings.Count(logs, "path=fallback") != 2 {
		t.Fatalf("each decline log must carry path=fallback, got:\n%s", logs)
	}
}

func TestEffortOverrideForRequestGovernorPriority(t *testing.T) {
	a := New(&varyingProvider{efforts: []string{"low", "max"}}, nil, NewSession("s"), Options{}, event.Discard)
	if !a.SetSessionEffortOverride("max") {
		t.Fatal("set session override")
	}

	prevGovernorEnabled := governorEnabled
	governorEnabled = true
	t.Cleanup(func() { governorEnabled = prevGovernorEnabled })

	// Governor disengaged: the session override stands.
	if got := a.effortOverrideForRequest(); got != "max" {
		t.Fatalf("override = %q, want max", got)
	}
	// Governor engaged: the running guard must not be outbid by a session
	// depth bump.
	a.task.governor.engaged = true
	if got := a.effortOverrideForRequest(); got != governorEffort {
		t.Fatalf("override = %q, want governor effort %q", got, governorEffort)
	}
}
