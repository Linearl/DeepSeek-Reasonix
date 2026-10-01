package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/billing"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Task 297: the policy knobs' file-value rules — zero means built-in default,
// never "off"; only the switch turns the pass off.
func TestNormalizeColdCacheCompactKnobs(t *testing.T) {
	got := normalizeColdCacheCompactKnobs(true, 0, 0)
	if !got.Enabled || got.MinBytes != config.ColdCacheCompactMinBytesDefault || got.IdleMinutes != config.ColdCacheCompactIdleMinutesDefault {
		t.Fatalf("zero values should fall back to defaults: %+v", got)
	}
	got = normalizeColdCacheCompactKnobs(false, -5, -1)
	if got.Enabled || got.MinBytes != config.ColdCacheCompactMinBytesDefault || got.IdleMinutes != config.ColdCacheCompactIdleMinutesDefault {
		t.Fatalf("negative values should fall back too (only the switch disables): %+v", got)
	}
	got = normalizeColdCacheCompactKnobs(true, 1<<20, 60)
	if got.MinBytes != 1<<20 || got.IdleMinutes != 60 {
		t.Fatalf("explicit values must pass through untouched: %+v", got)
	}
}

// The whole 297 policy: switch gate, idle floor, size floor, and the
// once-per-cooling-window guard keyed on the activity stamp the attempt was
// made for (a user touch re-arms eligibility; a retry loop never fires).
func TestColdCacheCompactDecision(t *testing.T) {
	const kb = int64(1024)
	now := time.UnixMilli(1790600000000)
	idleActivity := now.Add(-6 * time.Hour).UnixMilli()
	freshActivity := now.Add(-10 * time.Minute).UnixMilli()

	cases := []struct {
		name      string
		enabled   bool
		activity  int64
		openedAt  int64
		attempted int64
		parked    int64
		size      int64
		wantDo    bool
		wantWhy   string
	}{
		{"switch off is zero behaviour", false, idleActivity, 0, 0, 0, 1000 * kb, false, "switch off"},
		{"eligible: idle 6h, 700KB, no prior attempt", true, idleActivity, 0, 0, 0, 700 * kb, true, "eligible"},
		{"under floor skipped", true, idleActivity, 0, 0, 0, 599 * kb, false, "context under size floor"},
		{"recently active skipped", true, freshActivity, 0, 0, 0, 700 * kb, false, "not idle long enough"},
		{"same cooling window never twice", true, idleActivity, 0, idleActivity, 0, 700 * kb, false, "already compacted this cooling window"},
		{"user touched it again -> re-armed", true, idleActivity + 1, 0, idleActivity, 0, 700 * kb, true, "eligible"},
		{"no activity stamp is skipped", true, 0, 0, 0, 0, 700 * kb, false, "no activity stamp"},
		{"invalid config (zero idle) is skipped", true, idleActivity, 0, 0, 0, 700 * kb, false, "eligible"}, // idleMinutes supplied by caller below
		// Task 380-A: opening a cold session is a wake-up — the idle clock
		// restarts from the open even though LastActivityAt still reads old.
		{"recently opened cold session skipped (open exemption)", true, idleActivity, now.Add(-30 * time.Minute).UnixMilli(), 0, 0, 700 * kb, false, "not idle long enough"},
		{"old open does not shield a cold session", true, idleActivity, now.Add(-7 * time.Hour).UnixMilli(), 0, 0, 700 * kb, true, "eligible"},
		{"later of open and activity wins", true, now.Add(-2 * time.Hour).UnixMilli(), now.Add(-4 * time.Hour).UnixMilli(), 0, 0, 700 * kb, false, "not idle long enough"},
		// Task 424: the terminal no-foldable-region class parks the session
		// for its current activity stamp.
		{"parked for this cooling window is skipped", true, idleActivity, 0, 0, idleActivity, 700 * kb, false, "parked: no foldable region remains (re-arms on new activity)"},
		{"new activity re-arms a parked session", true, idleActivity + 1, 0, 0, idleActivity, 700 * kb, true, "eligible"},
		{"park stamp for an older window does not shield", true, idleActivity, 0, 0, idleActivity - 1000, 700 * kb, true, "eligible"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idleMinutes := 300
			if c.name == "invalid config (zero idle) is skipped" {
				idleMinutes = 0
				c.wantDo, c.wantWhy = false, "invalid config"
			}
			do, why := coldCacheCompactDecision(c.enabled, now, c.activity, c.openedAt, c.attempted, c.parked, c.size, 600*kb, idleMinutes)
			if do != c.wantDo || why != c.wantWhy {
				t.Fatalf("do=%v why=%q, want do=%v why=%q", do, why, c.wantDo, c.wantWhy)
			}
		})
	}
	// Exact-floor semantics: equal to the floor is eligible (size < floor is
	// the only rejection), pinned outside the table because 600*1024 is both
	// the default floor and the case's size.
	do, _ := coldCacheCompactDecision(true, now, idleActivity, 0, 0, 0, 600*kb, 600*kb, 300)
	if !do {
		t.Fatal("size equal to the floor must be eligible")
	}
}

// Task 424 acceptance: a session whose compact attempt failed with the
// terminal "no foldable region remains" class stops retrying for its current
// activity stamp (no more per-tick spam), and new session activity re-arms it
// for a fresh attempt. Exercises the park write + decision read as one cycle.
func TestColdCacheCompactParkRearmCycle(t *testing.T) {
	l := &coldCacheCompactLoop{
		a:         &App{},
		done:      make(map[string]int64),
		parked:    make(map[string]int64),
		firstSeen: make(map[string]int64),
		inFlight:  map[string]bool{},
	}
	const kb = int64(1024)
	now := time.UnixMilli(1790600000000)
	act := now.Add(-6 * time.Hour).UnixMilli()
	const path = `C:\sessions\over-limit.jsonl`

	// First pass: nothing parked yet, the session is eligible.
	if do, why := l.decisionFor(path, now, act, 700*kb); !do {
		t.Fatalf("first pass should be eligible, why=%q", why)
	}
	// The attempt fails with the terminal class -> parked for this stamp.
	l.parkNoFoldable(path, act)
	if got := l.parked[path]; got != act {
		t.Fatalf("parked stamp = %d, want %d", got, act)
	}
	// Every later tick (any number of them) skips: that is the no-spam pin.
	for i := range 3 {
		if do, why := l.decisionFor(path, now.Add(time.Duration(i+1)*coldCacheCompactTickInterval), act, 700*kb); do {
			t.Fatalf("tick %d after park must skip, why=%q", i+1, why)
		} else if why != "parked: no foldable region remains (re-arms on new activity)" {
			t.Fatalf("tick %d skip reason = %q", i+1, why)
		}
	}
	// The user writes a new message -> LastActivityAt bumps -> re-armed.
	newAct := act + int64(5*time.Minute/time.Millisecond)
	if do, why := l.decisionFor(path, now.Add(10*time.Minute), newAct, 700*kb); !do {
		t.Fatalf("new activity must re-arm the session, why=%q", why)
	}
}

// decisionFor is the exact read the tick performs, factored so the park cycle
// test exercises the production keying (mutex + maps) rather than a copy.
func (l *coldCacheCompactLoop) decisionFor(path string, now time.Time, lastActivityAt, size int64) (bool, string) {
	l.mu.Lock()
	attempted, parked := l.done[path], l.parked[path]
	l.mu.Unlock()
	return coldCacheCompactDecision(true, now, lastActivityAt, 0, attempted, parked, size, config.ColdCacheCompactMinBytesDefault, config.ColdCacheCompactIdleMinutesDefault)
}

// Task 424: only the terminal no-foldable-region class parks a session; every
// other failure class keeps the per-tick retry (zero regression pin).
func TestColdCacheCompactTerminalClass(t *testing.T) {
	terminal := fmt.Errorf("%w: %w", agent.ErrCompactionRequired, agent.ErrNoFoldableRegion)
	if !coldCacheCompactTerminal(terminal) {
		t.Fatal("wrapped no-foldable-region error must classify as terminal")
	}
	if !coldCacheCompactTerminal(agent.ErrNoFoldableRegion) {
		t.Fatal("bare sentinel must classify as terminal")
	}
	// The shape the truncation rescue produces when it fails on the same cause.
	rescued := fmt.Errorf("%w: %w (truncation: %w)", agent.ErrCompactionRequired, agent.ErrNoFoldableRegion, errors.New("disk"))
	if !coldCacheCompactTerminal(rescued) {
		t.Fatal("truncation-wrapped no-foldable-region must classify as terminal")
	}
	retryable := []error{
		nil,
		errors.New("connection reset"),
		fmt.Errorf("%w: %s", agent.ErrCompactionRequired, "truncated view still 300000 >= 200000"),
		context.DeadlineExceeded,
	}
	for _, err := range retryable {
		if coldCacheCompactTerminal(err) {
			t.Fatalf("error %v must stay retryable (not terminal)", err)
		}
	}
}

// Task 380 sixth-acceptance accounting: the headless capture sums the pass's
// billable usage and the catalog-priced cost quote; non-usage events are
// dropped like the event.Discard it replaces, and a nil capture (live path)
// renders no fields instead of double-counting the session's own ledger.
func TestColdCacheUsageCapture(t *testing.T) {
	c := &coldCacheUsageCapture{}
	c.Emit(event.Event{Kind: event.CompactionStarted}) // non-usage: dropped
	c.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{
		PromptTokens: 1200, CompletionTokens: 90, CacheHitTokens: 800, TotalTokens: 1290, RequestCount: 1,
	}, CostQuote: &billing.CostQuote{Original: billing.MoneyOf(amountMustParse(t, "0.0042"), "CNY")}})
	c.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{
		PromptTokens: 300, CompletionTokens: 10, CacheHitTokens: 0, TotalTokens: 310, RequestCount: 1,
	}, CostQuote: &billing.CostQuote{Original: billing.MoneyOf(amountMustParse(t, "0.0008"), "CNY")}})

	fields := c.logFields()
	got := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		got[fields[i].(string)] = fields[i+1]
	}
	if got["usagePromptTokens"] != 1500 || got["usageCompletionTokens"] != 100 ||
		got["usageCacheHitTokens"] != 800 || got["usageRequests"] != 2 {
		t.Fatalf("token sums drifted: %+v", got)
	}
	if got["costAmount"] != 0.005 || got["costCurrency"] != "CNY" {
		t.Fatalf("cost sum = %v %v, want 0.005 CNY", got["costAmount"], got["costCurrency"])
	}
	if _, ok := got["costMixedCurrency"]; ok {
		t.Fatal("same-currency sums must not flag mixed currency")
	}

	// Mixed currencies cannot be summed honestly: tokens still add up, the
	// cost is flagged instead of silently wrong.
	m := &coldCacheUsageCapture{}
	m.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 10, TotalTokens: 10, RequestCount: 1},
		CostQuote: &billing.CostQuote{Original: billing.MoneyOf(amountMustParse(t, "0.01"), "CNY")}})
	m.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 20, TotalTokens: 20, RequestCount: 1},
		CostQuote: &billing.CostQuote{Original: billing.MoneyOf(amountMustParse(t, "0.01"), "USD")}})
	mf := map[string]any{}
	fields = m.logFields()
	for i := 0; i+1 < len(fields); i += 2 {
		mf[fields[i].(string)] = fields[i+1]
	}
	if mf["costMixedCurrency"] != true {
		t.Fatalf("mixed-currency capture must flag costMixedCurrency: %+v", mf)
	}
	if _, ok := mf["costAmount"]; ok {
		t.Fatal("mixed-currency capture must not log a summed costAmount")
	}

	// The live path has no capture: no fields, no panic.
	var nilCapture *coldCacheUsageCapture
	if f := nilCapture.logFields(); f != nil {
		t.Fatalf("nil capture must render no fields, got %v", f)
	}
	nilCapture.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 1}})
}

func amountMustParse(t *testing.T, s string) billing.Amount {
	t.Helper()
	a, err := billing.ParseAmount(s)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", s, err)
	}
	return a
}

// sessionContextBytes counts live transcript + events, missing files as zero.
func TestSessionContextBytes(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(live, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	got := sessionContextBytes(live)
	if got < 1000 {
		t.Fatalf("bytes = %d, want at least the live file size", got)
	}
	missing := sessionContextBytes(filepath.Join(dir, "nope.jsonl"))
	if missing != 0 {
		t.Fatalf("missing file should read as zero, got %d", missing)
	}
}

// The tick with the switch off must be a no-op on an uninitialised App: the
// live config read decides before any session state is touched (iron rule 2
// zero-behaviour pin).
func TestColdCacheTickOffIsZeroBehaviour(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	knobs := loadColdCacheCompactConfig()
	if knobs.Enabled {
		t.Fatalf("fresh REASONIX_HOME should read the switch off: %+v", knobs)
	}
	// A zero-value App has no sessions dir wiring; tick must return before
	// touching it.
	l := &coldCacheCompactLoop{a: &App{}, done: map[string]int64{}, parked: map[string]int64{}}
	l.tick(time.Now())
}
