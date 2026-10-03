package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/eventtrigger"
	"reasonix/internal/sessioncollab"
)

// Task 284: cross-session subscriptions — the push half of collaboration.
// 218/224/235 are pull (ask, poll, drain); this registers a watching session
// and actively pushes its state changes / anomaly events to the subscriber's
// own conversation through the existing steer delivery (221 merge semantics),
// so a manager no longer hand-polls every 5 minutes (user directive
// 2026-09-24, machine-ised).
//
// Relationship with 228/230 (audited before building, dispatch requirement):
//   - 228 event_wait = WAIT by occupying the turn (one call, one verdict).
//   - 230 eventtrigger = the generic trigger engine: whitelist-validated
//     checkers, one match DSL, one PollLoop. This service REUSES that verdict
//     path — every subscription is evaluated through engine.Evaluate with a
//     registered builtin checker (session_events), so 230 and 284 can never
//     disagree about what "matched" means (same acceptance as 228: one
//     judgement, two consumers). What 284 adds on top is the subscription
//     lifecycle the engine deliberately does not model: TTL expiry, durable
//     records, edge-triggered de-duplication, and a push action.
//   - Push channel: MailStore.Deliver with Delivery=steer — the exact channel
//     talk_to_session uses, so the arrival side (control inbox → TrySteer →
//     221 merge) is reused unchanged. With the panel's AllowSteer off it
//     degrades to followup exactly like talk_to_session does (task 173 ④).
//
// Polling-footprint rule (dispatch acceptance "不新增轮询面"): ONE process-wide
// loop, and it blocks on a wake channel while no subscription exists — zero
// polling when nobody subscribed. Never one timer per subscription.
const (
	SubscribeStateChange  = "state_change"   // running/queued/idle flip (edge)
	SubscribeToolError    = "tool_error"     // status stream: tool failure (273 family)
	SubscribeBlockingWait = "blocking_wait"  // status stream: gate blocking a turn (283 family)
	SubscribeNeedsDecision = "needs_decision" // status stream: human decision required (283 family)
	SubscribeStuck        = "stuck"          // running with no turn progress for stuck_after_s
	// SubscribeTurnAbnormalEnd (task 319) fires when the watched peer's turn
	// reaches a NON-completed terminal state (failed/interrupted/
	// recovery_required — the event.TurnStatus set): the peer died instead of
	// finishing, which is how a dispatched task goes silent. paired with the
	// watcher notification carrying the recovery guidance.
	SubscribeTurnAbnormalEnd = "turn_abnormal_end"
)

// subscribeEventKinds is the v1 whitelist: anomalies only, no arbitrary events.
var subscribeEventKinds = map[string]bool{
	SubscribeStateChange:      true,
	SubscribeToolError:        true,
	SubscribeBlockingWait:     true,
	SubscribeNeedsDecision:    true,
	SubscribeStuck:            true,
	SubscribeTurnAbnormalEnd:  true,
}

// SubscribeEventKinds returns the sorted v1 whitelist (tool schema help).
func SubscribeEventKinds() []string {
	out := make([]string, 0, len(subscribeEventKinds))
	for k := range subscribeEventKinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const (
	subscribeDefaultTTL   = time.Hour
	subscribeMinTTL       = 30 * time.Second
	subscribeMaxTTL       = 7 * 24 * time.Hour
	subscribeDefaultEvery = 30 * time.Second
	subscribeMinEvery     = 5 * time.Second
	subscribeMaxEvery     = 300 * time.Second
	subscribeDefaultStuck = 600 * time.Second
	subscribeMinStuck     = 60 * time.Second
	subscribeMaxStuck     = 24 * time.Hour
	// subscribeMessagePrefix tags every push so the receiving turn can tell a
	// subscription notification (and drop it) without parsing free text.
	subscribeMessagePrefix = "[订阅推送]"
)

// Subscription is one durable watch record (task 284). ID is the resolved
// target contact id — same target re-subscribes overwrite (renew), which is
// what makes subscribe idempotent and repeat-subscribe single-push.
type Subscription struct {
	ID          string    `json:"id"`
	Target      string    `json:"target"`
	Events      []string  `json:"events"`
	EveryS      int       `json:"every_s"`
	StuckAfterS int       `json:"stuck_after_s"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// subscribeCursor is the non-persisted edge/dedup state per subscription.
// Stream offsets and state baselines intentionally reset on restart: replay
// of historical events must never push old news (first observation records
// without pushing).
type subscribeCursor struct {
	baseline    bool              // first state snapshot recorded
	lastStates  map[string]string // contact -> state
	streamOff   int64             // bytes consumed from the status stream
	streamReady bool              // initial park done (off==0 alone cannot say: an empty file parks at 0)
	stuck       bool              // stuck edge latched (push once per episode)
	// abnormalLatched (task 319) edge-latches turn_abnormal_end: the dead
	// turn pages once per episode, cleared when the peer returns healthy.
	abnormalLatched bool
}

// SubscribeService owns the registry, cursors, and the single push loop.
// Deliver and Now are injectable for tests; production wiring fills them from
// SessionCollabConfig (Deliver = MailStore, Now = time.Now).
type SubscribeService struct {
	mu      sync.Mutex
	cfg     SessionCollabConfig
	path    string
	subs    map[string]*Subscription
	cursors map[string]*subscribeCursor
	engine  *eventtrigger.Engine

	// Deliver sends one push message to the subscriber (task 284 wires the
	// mailbox steer delivery). Tests replace it to capture pushes.
	Deliver func(msg sessioncollab.MailMessage) error
	// Now is the clock (tests freeze it).
	Now func() time.Time

	wake    chan struct{}
	started bool
	stop    context.CancelFunc
}

// NewSubscribeService builds a service persisted under the collab mail dir.
func NewSubscribeService(cfg SessionCollabConfig) *SubscribeService {
	mailDir := cfg.MailDir
	if mailDir == "" {
		mailDir = sessioncollabMailDirFallback()
	}
	path := ""
	if mailDir != "" {
		path = filepath.Join(mailDir, "session-subscriptions.json")
	}
	return &SubscribeService{
		cfg:     cfg,
		path:    path,
		subs:    map[string]*Subscription{},
		cursors: map[string]*subscribeCursor{},
		engine:  eventtrigger.NewEngine(nil),
		Deliver: nil, // wired by EnsureStarted / tests
		Now:     time.Now,
		wake:    make(chan struct{}, 1),
	}
}

// sessioncollabMailDirFallback mirrors the tool side's default so the
// persistence file lands next to the mailbox even when cfg.MailDir is empty.
func sessioncollabMailDirFallback() string {
	return config.SessionCollabMailDir()
}

// ---- registry ----

// Subscribe registers or renews a subscription for a resolved contact.
// Same target overwrites (idempotent, single push per event), events are
// validated against the v1 whitelist, and TTL is clamped. It persists the
// registry and nudges the loop.
func (s *SubscribeService) Subscribe(id, target string, events []string, ttl, every, stuckAfter time.Duration) (Subscription, error) {
	if strings.TrimSpace(id) == "" {
		return Subscription{}, fmt.Errorf("subscribe_session: target does not resolve to a contact id")
	}
	if len(events) == 0 {
		events = []string{SubscribeStateChange}
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(events))
	for _, e := range events {
		e = strings.ToLower(strings.TrimSpace(e))
		if !subscribeEventKinds[e] {
			return Subscription{}, fmt.Errorf("subscribe_session: unknown event kind %q (whitelist: %s)",
				e, strings.Join(SubscribeEventKinds(), ","))
		}
		if !seen[e] {
			seen[e] = true
			clean = append(clean, e)
		}
	}
	ttl = clampSubDuration(ttl, subscribeMinTTL, subscribeMaxTTL, subscribeDefaultTTL)
	every = clampSubDuration(every, subscribeMinEvery, subscribeMaxEvery, subscribeDefaultEvery)
	stuckAfter = clampSubDuration(stuckAfter, subscribeMinStuck, subscribeMaxStuck, subscribeDefaultStuck)

	s.mu.Lock()
	// Renew keeps the cursor: an already-observed baseline must not re-push
	// state edges just because the TTL was extended (acceptance: repeated
	// subscribe to one topic never double-pushes).
	_, renew := s.subs[id]
	sub := &Subscription{
		ID:          id,
		Target:      target,
		Events:      clean,
		EveryS:      int(every / time.Second),
		StuckAfterS: int(stuckAfter / time.Second),
		ExpiresAt:   s.now().Add(ttl),
		CreatedAt:   s.now(),
	}
	s.subs[id] = sub
	if _, ok := s.cursors[id]; !ok {
		s.cursors[id] = &subscribeCursor{lastStates: map[string]string{}}
	}
	s.persistLocked()
	out := *sub
	s.mu.Unlock()
	s.nudge()
	slog.Info("agent: session subscription registered",
		"id", id, "events", clean, "ttl", ttl, "every", every, "renewed", renew)
	return out, nil
}

// Unsubscribe removes a subscription (idempotent: unknown ids no-op and
// report removed=false).
func (s *SubscribeService) Unsubscribe(id string) bool {
	s.mu.Lock()
	_, ok := s.subs[id]
	delete(s.subs, id)
	delete(s.cursors, id)
	s.persistLocked()
	s.mu.Unlock()
	if ok {
		slog.Info("agent: session subscription removed", "id", id)
	}
	return ok
}

// List returns a snapshot of active (non-expired) subscriptions.
func (s *SubscribeService) List() []Subscription {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		if now.After(sub.ExpiresAt) {
			continue
		}
		out = append(out, *sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// pruneExpiredLocked drops TTL-expired subscriptions (silent expiry, task 284
// TTL semantics: expired means no more pushes; re-subscribe to renew).
func (s *SubscribeService) pruneExpiredLocked() []string {
	now := s.now()
	var gone []string
	for id, sub := range s.subs {
		if now.After(sub.ExpiresAt) {
			delete(s.subs, id)
			delete(s.cursors, id)
			gone = append(gone, id)
		}
	}
	if len(gone) > 0 {
		s.persistLocked()
	}
	return gone
}

// persistLocked writes the registry (best effort — a write failure must never
// break a turn; the next change retries).
func (s *SubscribeService) persistLocked() {
	if s.path == "" {
		return
	}
	b, err := json.MarshalIndent(struct {
		Subs []Subscription `json:"subs"`
	}{Subs: subList(s.subs)}, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

func subList(m map[string]*Subscription) []Subscription {
	out := make([]Subscription, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// load reads persisted subscriptions back (restart survival, task 284 倾向持久).
// Expired records are dropped on load.
func (s *SubscribeService) load() {
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var doc struct {
		Subs []Subscription `json:"subs"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		slog.Warn("agent: subscription store unreadable — starting empty", "err", err)
		return
	}
	s.mu.Lock()
	now := s.now()
	for i := range doc.Subs {
		sub := doc.Subs[i]
		if sub.ID == "" || now.After(sub.ExpiresAt) {
			continue
		}
		s.subs[sub.ID] = &sub
		s.cursors[sub.ID] = &subscribeCursor{lastStates: map[string]string{}}
	}
	s.mu.Unlock()
}

// ---- loop ----

// EnsureStarted loads the store and starts the single process-wide push loop.
// Idempotent; when no subscription exists the loop blocks on the wake channel
// (zero polling footprint). Tests drive Tick directly instead.
func (s *SubscribeService) EnsureStarted() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	s.load()
	s.mu.Lock()
	deliver := s.Deliver
	steerGate := s.cfg.AllowSteer
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	if deliver == nil {
		mailDir := s.cfg.MailDir
		if mailDir == "" {
			mailDir = config.SessionCollabMailDir()
		}
		mail := sessioncollab.NewMailStoreWithHopLimit(mailDir, s.cfg.hopLimit())
		// task 461 P1: the push loop's lifecycle ctx rides into the lock wait —
		// Stop() ends a contended acquire instead of leaving it to the budget.
		deliver = func(msg sessioncollab.MailMessage) error {
			_, err := mail.Deliver(ctx, msg)
			return err
		}
	}
	s.mu.Lock()
	s.stop = cancel
	s.mu.Unlock()
	go s.run(ctx, deliver, steerGate)
	slog.Info("agent: subscription push loop started", "store", s.path)
}

// Stop terminates the loop (tests / teardown).
func (s *SubscribeService) Stop() {
	s.mu.Lock()
	cancel := s.stop
	s.started = false
	s.stop = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *SubscribeService) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// run is THE loop (no per-subscription timers): wake on subscription change,
// else sleep to the nearest per-subscription interval; block forever while
// empty.
func (s *SubscribeService) run(ctx context.Context, deliver func(sessioncollab.MailMessage) error, steerGate bool) {
	for {
		interval := s.nextInterval()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
		s.Tick(ctx, deliver, steerGate)
	}
}

// nextInterval is the soonest per-subscription poll; a generous floor keeps
// an empty store from ever waking (blocks until nudge).
func (s *SubscribeService) nextInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	best := time.Duration(0)
	now := s.now()
	for _, sub := range s.subs {
		if now.After(sub.ExpiresAt) {
			continue
		}
		d := time.Duration(sub.EveryS) * time.Second
		if best == 0 || d < best {
			best = d
		}
	}
	if best == 0 {
		return 24 * time.Hour // empty: effectively blocked until a nudge
	}
	return best
}

// Tick evaluates every live subscription once. Exported for tests: the
// acceptance (push arrives / TTL stops pushes / no double push) is asserted
// against Tick, not against wall-clock sleeps.
func (s *SubscribeService) Tick(ctx context.Context, deliver func(sessioncollab.MailMessage) error, steerGate bool) {
	s.mu.Lock()
	for _, id := range s.pruneExpiredLocked() {
		slog.Info("agent: session subscription expired", "id", id)
	}
	type work struct {
		sub    Subscription
		cursor *subscribeCursor
	}
	jobs := make([]work, 0, len(s.subs))
	for id, sub := range s.subs {
		jobs = append(jobs, work{sub: *sub, cursor: s.cursors[id]})
	}
	s.mu.Unlock()

	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		hit, kinds, detail := s.evaluateSubscription(ctx, job.sub, job.cursor)
		if !hit {
			continue
		}
		s.pushSubscription(job.sub, kinds, detail, deliver, steerGate)
	}
}

// evaluateSubscription runs the subscription's verdict through the 230
// engine's Evaluate (whitelist + DSL in one place). The builtin checker
// session_events returns {fired, kinds, detail}; match is jsonpath fired==true.
// The builtin closure harvests kinds/detail into locals — Evaluate returns
// only the match verdict, and sessionEventsVerdict consumes edges/cursors, so
// it must run EXACTLY once per evaluation or the push payload is lost.
func (s *SubscribeService) evaluateSubscription(ctx context.Context, sub Subscription, cur *subscribeCursor) (bool, []string, string) {
	args, _ := json.Marshal(map[string]any{"events": sub.Events, "stuck_after_s": sub.StuckAfterS})
	var (
		fired  bool
		kinds  []string
		detail string
	)
	// RegisterBuiltin is per-call idempotent by name (last write wins); the
	// closure captures this subscription's cursor so one shared engine can
	// evaluate every subscription with the same whitelist machinery.
	_ = s.engine.RegisterBuiltin("session_events", func(ctx context.Context, raw json.RawMessage) (string, error) {
		out, err := s.sessionEventsVerdict(sub, cur)
		if err != nil {
			return out, err
		}
		var doc struct {
			Fired  bool     `json:"fired"`
			Kinds  []string `json:"kinds"`
			Detail string   `json:"detail"`
		}
		if json.Unmarshal([]byte(out), &doc) == nil {
			fired, kinds, detail = doc.Fired, doc.Kinds, doc.Detail
		}
		return out, err
	})
	trigger := eventtrigger.Trigger{
		ID:        "subscribe:" + sub.ID,
		IntervalS: sub.EveryS,
		Checker:   eventtrigger.Checker{Kind: eventtrigger.CheckerBuiltin, Name: "session_events", Args: args},
		Match:     eventtrigger.Match{Kind: eventtrigger.MatchJSONPath, Expr: "fired==true"},
	}
	if err := s.engine.Validate(trigger); err != nil {
		slog.Warn("agent: subscription trigger invalid", "id", sub.ID, "err", err)
		return false, nil, ""
	}
	hit, _, err := s.engine.Evaluate(ctx, trigger)
	if err != nil || !hit || !fired {
		return false, nil, ""
	}
	return true, kinds, detail
}

// sessionEventsVerdict is the whitelist event-source judgement for one
// subscription. Edge rules (no double push):
//   - state_change: fires only on a transition; the first observation after
//     registration/restart records the baseline WITHOUT firing.
//   - stream kinds (tool_error/blocking_wait/needs_decision): consume the
//     status stream incrementally from the cursor — consumed bytes never
//     re-fire.
//   - stuck: running with no turn progress past stuck_after_s; latched until
//     the target stops being stuck.
func (s *SubscribeService) sessionEventsVerdict(sub Subscription, cur *subscribeCursor) (string, error) {
	want := map[string]bool{}
	for _, e := range sub.Events {
		want[e] = true
	}
	var kinds []string
	var details []string

	if want[SubscribeStateChange] || want[SubscribeStuck] {
		records, _, _ := collabStatusRecords(s.cfg, []string{sub.Target})
		now := s.now()
		for _, rec := range records {
			contact, _ := rec["contactId"].(string)
			if contact == "" {
				if t, _ := rec["title"].(string); t != "" {
					contact = t
				}
			}
			state, _ := rec["state"].(string)
			if want[SubscribeStateChange] {
				if !cur.baseline {
					cur.lastStates[contact] = state
				} else if prev, ok := cur.lastStates[contact]; !ok || prev != state {
					kinds = append(kinds, SubscribeStateChange)
					details = append(details, fmt.Sprintf("%s: %s→%s", contact, prev, state))
					cur.lastStates[contact] = state
				}
			}
			if want[SubscribeStuck] {
				stuck := s.stuckDetected(rec, sub, now)
				if stuck && !cur.stuck {
					kinds = append(kinds, SubscribeStuck)
					details = append(details, fmt.Sprintf("%s: running without turn progress > %ds", contact, sub.StuckAfterS))
				}
				cur.stuck = stuck
			}
		}
		if !cur.baseline {
			cur.baseline = true
		}
	}

	if want[SubscribeToolError] || want[SubscribeBlockingWait] || want[SubscribeNeedsDecision] {
		streamPath := s.cfg.CollabStatusPath
		if streamPath == "" {
			streamPath = ResolveCollabStatusPath("", s.cfg.MailDir, s.cfg.WorkspaceRoot)
		}
		if newKinds, newDetails := consumeStatusStream(streamPath, sub, cur, want); len(newKinds) > 0 {
			kinds = append(kinds, newKinds...)
			details = append(details, newDetails...)
		}
	}

	// Task 319: the peer turn that died instead of completing. The authoritative
	// signal is event.TurnStatus's non-completed terminal set — the same enum
	// the controller publishes on RuntimeStatus — surfaced through the injected
	// SessionTurnStatus probe (nil probe = unknown = never fires; a guess here
	// would page the watcher for a healthy peer). Edge-latched like stuck: the
	// abnormal state fires ONCE per episode and clears when the peer returns to
	// a non-terminal state, so a watchdog redraw does not re-page.
	if want[SubscribeTurnAbnormalEnd] {
		if s.cfg.SessionTurnStatus != nil {
			status, known := s.cfg.SessionTurnStatus(sub.Target)
			if known && turnStatusIsAbnormalEnd(status) {
				if !cur.abnormalLatched {
					kinds = append(kinds, SubscribeTurnAbnormalEnd)
					details = append(details, fmt.Sprintf("%s: turn ended abnormally (status=%s)", sub.Target, status))
					slog.Warn("agent: abnormal turn end detected on watched session",
						"target", sub.Target, "status", status, "death_class", classifyTurnDeath(status),
						"at", s.now().UTC().Format(time.RFC3339))
				}
				cur.abnormalLatched = true
			} else {
				cur.abnormalLatched = false
			}
		}
	}

	fired := len(kinds) > 0
	out, _ := json.Marshal(map[string]any{
		"fired":  fired,
		"kinds":  kinds,
		"detail": strings.Join(details, "; "),
	})
	return string(out), nil
}

// turnStatusIsAbnormalEnd is the task 319 classifier over event.TurnStatus:
// completed is the only healthy terminal; cancelled-by-user reads as
// interrupted (a watcher still needs to know the task did not finish), and
// queued/in_progress/waiting_user/cancelling are not terminals at all.
func turnStatusIsAbnormalEnd(status string) bool {
	switch status {
	case "failed", "interrupted", "recovery_required", "protocol_failed":
		return true
	default:
		return false
	}
}

// classifyTurnDeath buckets the status for the death-cause log line (task 319
// ④, in the 304 logging family): a coarse but honest class so "mimo died
// again" becomes countable instead of anecdotal. The finer provider-level
// reason (model interrupt vs hook reject) rides the log line's own err text
// when the runtime exposes it.
func classifyTurnDeath(status string) string {
	switch status {
	case "failed", "protocol_failed":
		return "provider_or_turn_failure"
	case "interrupted":
		return "interrupted"
	case "recovery_required":
		return "recovery_required"
	default:
		return "unknown"
	}
}

// stuckDetected mirrors get_session_status's live probe: running with the
// last turn start older than the threshold. known=false never guesses stuck.
func (s *SubscribeService) stuckDetected(rec map[string]any, sub Subscription, now time.Time) bool {
	if rec["state"] != "running" {
		return false
	}
	contact, _ := rec["contactId"].(string)
	if contact == "" {
		return false
	}
	if s.cfg.SessionStatus == nil {
		return false
	}
	running, lastTurnAtMS, _, known := s.cfg.SessionStatus(contact)
	if !known || !running || lastTurnAtMS <= 0 {
		return false
	}
	threshold := time.Duration(sub.StuckAfterS) * time.Second
	return now.Sub(time.UnixMilli(lastTurnAtMS)) > threshold
}

// consumeStatusStream reads only bytes past the cursor (187 rule: never
// rescan), filters by the subscription target and the whitelist, and advances
// the cursor so the same event cannot fire twice.
func consumeStatusStream(path string, sub Subscription, cur *subscribeCursor, want map[string]bool) (kinds, details []string) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil
	}
	size := st.Size()
	if !cur.streamReady {
		// First sight of the stream (fresh subscription or restart): park at
		// EOF — historical events are old news, never a push. streamReady
		// exists because an empty file parks at off==0, which off alone can
		// no longer distinguish from "not parked yet" (a file that appears
		// later would be re-parked forever and every event swallowed).
		cur.streamOff = size
		cur.streamReady = true
		return nil, nil
	}
	if size <= cur.streamOff {
		return nil, nil
	}
	if _, err := f.Seek(cur.streamOff, 0); err != nil {
		return nil, nil
	}
	buf := make([]byte, size-cur.streamOff)
	if _, err := f.Read(buf); err != nil && len(buf) == 0 {
		return nil, nil
	}
	cur.streamOff = size
	targetID := sub.ID
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev CollabStatusEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue // interleaved/partial line: skip, count nothing (stream contract)
		}
		if ev.Session != targetID && !strings.HasSuffix(ev.Session, string(filepath.Separator)+targetID) {
			// Also accept a basename match: the engine stamps contact id OR
			// path basename (collabStatusSessionID contract).
			if base := filepath.Base(ev.Session); base != targetID {
				continue
			}
		}
		if !want[ev.Event] {
			continue
		}
		kinds = append(kinds, ev.Event)
		details = append(details, strings.TrimSpace(ev.Summary))
	}
	return kinds, details
}

// pushSubscription delivers one steer-grade push through the reused mailbox
// channel (221 merge on arrival). Steering off degrades to followup exactly
// like talk_to_session (task 173 ④) — the message still lands.
func (s *SubscribeService) pushSubscription(sub Subscription, kinds []string, detail string, deliver func(sessioncollab.MailMessage) error, steerGate bool) {
	delivery := sessioncollab.DeliverySteer
	if !steerGate {
		delivery = sessioncollab.DeliveryFollowup
	}
	body := fmt.Sprintf("%s %s 事件=%s 细节=%s（订阅 %s，剩余 %s，可 unsubscribe 退订）",
		subscribeMessagePrefix, sub.Target, strings.Join(kinds, ","), detail,
		sub.ID, sub.ExpiresAt.Sub(s.now()).Round(time.Second))
	// Task 319 (recovery leg): a dead or stalled peer means the dispatched
	// work may be unfinished — say so EXPLICITLY so the task never disappears
	// silently. v1 recovery is report-style (the honest, non-duplicating
	// choice): the watcher decides between re-dispatch (progress document
	// present = continue, not redo) and rebuilding the session. Automatic
	// re-dispatch is deferred until its idempotency ledger exists — a blind
	// resend would double-run work that is still in flight.
	if slices.Contains(kinds, SubscribeTurnAbnormalEnd) || slices.Contains(kinds, SubscribeStuck) {
		body += fmt.Sprintf("\n⚠️ 任务状态提示：目标 %s 的 turn 已异常终止/长时间无进展——派发给它的任务可能未完成（任务不无声丢失）。请核对：1) 其进展文档（tasks/批*-进展-*.md）是否已交付——在则续跑（重派同任务即可，勿重做已完成部分）；2) 会话是否需要人工重建后重派；3) 本机日志 death_class 分类（slog: abnormal turn end detected）。", sub.Target)
	}
	msg := sessioncollab.MailMessage{
		From:     "",
		To:       s.selfContact(),
		Body:     body,
		Delivery: string(delivery),
	}
	if deliver == nil {
		slog.Warn("agent: subscription push dropped — no delivery channel", "id", sub.ID, "kinds", kinds)
		return
	}
	if err := deliver(msg); err != nil {
		slog.Warn("agent: subscription push failed", "id", sub.ID, "kinds", kinds, "err", err)
		return
	}
	slog.Info("agent: subscription pushed to subscriber",
		"id", sub.ID, "kinds", kinds, "delivery", delivery)
}

// selfContact is the subscriber: the push returns to THIS session (the
// watcher), so To is the caller's own contact id.
func (s *SubscribeService) selfContact() string {
	if s.cfg.CurrentContactID != "" {
		return s.cfg.CurrentContactID
	}
	if s.cfg.ResolveSessionPath != nil {
		return collabStatusSessionID(s.cfg.ResolveSessionPath(), "")
	}
	return collabStatusSessionID(s.cfg.CurrentSessionPath, "")
}

func (s *SubscribeService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func clampSubDuration(v, lo, hi, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
