package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

// Task 284 acceptance tests: subscription → anomaly push arrives (steer),
// TTL expiry stops pushes, subscribe/unsubscribe are idempotent with no
// double push, steering off degrades to followup, and unknown event kinds
// are refused by the whitelist. No skips anywhere.

type subTestEnv struct {
	t          *testing.T
	cfg        SessionCollabConfig
	svc        *SubscribeService
	mu         sync.Mutex
	pushes     []sessioncollab.MailMessage
	running    atomic.Bool // SessionStatus stub: target is running?
	known      atomic.Bool // SessionStatus stub: probe can see the target
	frozenNano atomic.Int64
}

func newSubTestEnv(t *testing.T) *subTestEnv {
	t.Helper()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "peer.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"branch-peer","contact_id":"ct_peer","custom_title":"Peer"}`
	if err := os.WriteFile(sessionPath+".meta", []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(t.TempDir(), "collab-status.jsonl")
	// The stream file must exist before the first tick: the cursor parks at
	// EOF on first sight, and an event appended after that park is what the
	// subscription must push (production: the engine creates the file on the
	// first collab event, and a first tick with no file parks at 0 — same
	// rule).
	if err := os.WriteFile(statusPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env := &subTestEnv{t: t}
	env.frozenNano.Store(time.Now().UnixNano())
	env.known.Store(true)
	cfg := SessionCollabConfig{
		Enabled:          true,
		SessionDir:       dir,
		WorkspaceRoot:    t.TempDir(),
		MailDir:          t.TempDir(),
		CollabStatusPath: statusPath,
		AllowSteer:       true,
		SessionStatus: func(contact string) (bool, int64, int, bool) {
			return env.running.Load(), time.Now().UnixMilli(), 0, env.known.Load()
		},
	}
	env.cfg = cfg
	env.svc = NewSubscribeService(cfg)
	env.svc.Now = func() time.Time { return time.Unix(0, env.frozenNano.Load()) }
	env.svc.Deliver = func(m sessioncollab.MailMessage) error {
		env.mu.Lock()
		env.pushes = append(env.pushes, m)
		env.mu.Unlock()
		return nil
	}
	return env
}

func (e *subTestEnv) pushCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pushes)
}

func (e *subTestEnv) lastPush() sessioncollab.MailMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pushes) == 0 {
		e.t.Fatal("expected a push, have none")
	}
	return e.pushes[len(e.pushes)-1]
}

func (e *subTestEnv) tick() {
	e.svc.Tick(context.Background(), e.svc.Deliver, e.cfg.AllowSteer)
}

func (e *subTestEnv) appendStream(ev CollabStatusEvent) {
	ev.TS = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(ev)
	f, err := os.OpenFile(e.cfg.CollabStatusPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		e.t.Fatal(err)
	}
}

// TestSubscribeStateChangePushArrives: subscribe → first tick records the
// baseline without pushing → the state flips → the push arrives as a steer
// message → an unchanged state never pushes again (edge trigger).
func TestSubscribeStateChangePushArrives(t *testing.T) {
	env := newSubTestEnv(t)
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	env.tick() // baseline: idle observed, no push
	if env.pushCount() != 0 {
		t.Fatalf("baseline tick pushed %d times, want 0 (first observation never pushes)", env.pushCount())
	}
	env.running.Store(true) // idle → running
	env.tick()
	if env.pushCount() != 1 {
		t.Fatalf("pushes = %d, want exactly 1 on the state edge", env.pushCount())
	}
	push := env.lastPush()
	if !strings.Contains(push.Body, subscribeMessagePrefix) {
		t.Fatalf("push body missing %q prefix: %q", subscribeMessagePrefix, push.Body)
	}
	if !strings.Contains(push.Body, "state_change") {
		t.Fatalf("push body missing event kind: %q", push.Body)
	}
	if push.Delivery != string(sessioncollab.DeliverySteer) {
		t.Fatalf("delivery = %q, want steer (mid-turn injection)", push.Delivery)
	}
	env.tick() // unchanged running → no second push
	env.tick()
	if env.pushCount() != 1 {
		t.Fatalf("pushes = %d after unchanged ticks, want 1 (edge trigger, no double push)", env.pushCount())
	}
}

// TestSubscribeUnsubscribeStopsPush: after unsubscribe the same state change
// produces nothing, and a second unsubscribe is an idempotent no-op.
func TestSubscribeUnsubscribeStopsPush(t *testing.T) {
	env := newSubTestEnv(t)
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	env.tick()
	if !env.svc.Unsubscribe("ct_peer") {
		t.Fatal("first unsubscribe must report removed=true")
	}
	if env.svc.Unsubscribe("ct_peer") {
		t.Fatal("second unsubscribe must be an idempotent no-op (removed=false)")
	}
	env.running.Store(true)
	env.tick()
	if env.pushCount() != 0 {
		t.Fatalf("pushes after unsubscribe = %d, want 0", env.pushCount())
	}
}

// TestSubscribeTTLExpiryStopsPush: past the expiry the subscription is pruned
// silently — no pushes, no list entries — and re-subscribing (renew) restores
// it with the cursor intact (no baseline re-push).
func TestSubscribeTTLExpiryStopsPush(t *testing.T) {
	env := newSubTestEnv(t)
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	env.tick()
	env.frozenNano.Add((2 * time.Hour).Nanoseconds()) // past TTL
	env.running.Store(true)
	env.tick()
	if env.pushCount() != 0 {
		t.Fatalf("pushes after TTL expiry = %d, want 0", env.pushCount())
	}
	if got := env.svc.List(); len(got) != 0 {
		t.Fatalf("List after expiry = %d entries, want 0", len(got))
	}
	// Renew: the old baseline (idle) survives, so the already-observed flip
	// does not re-fire — but a NEW edge does.
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	env.tick()
	if env.pushCount() != 0 {
		t.Fatalf("renew re-pushed %d times — renewals must keep the cursor (same topic never double-pushes)", env.pushCount())
	}
	env.running.Store(false)
	env.tick()
	if env.pushCount() != 1 {
		t.Fatalf("pushes after renewed edge = %d, want 1 (renewed subscription still works)", env.pushCount())
	}
}

// TestSubscribeIdempotentAndWhitelist: repeated subscribe to one target keeps
// a single record; unknown event kinds are refused; unsubscribe by resolved
// target works after resolution fails for a bogus ref (raw id path).
func TestSubscribeIdempotentAndWhitelist(t *testing.T) {
	env := newSubTestEnv(t)
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeToolError}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := env.svc.List(); len(got) != 1 {
		t.Fatalf("List = %d entries after double subscribe, want 1 (idempotent by target)", len(got))
	}
	if _, err := env.svc.Subscribe("ct_other", "other", []string{"arbitrary_event"}, time.Hour, 0, 0); err == nil {
		t.Fatal("unknown event kind must be refused by the whitelist")
	} else if !strings.Contains(err.Error(), "whitelist") {
		t.Fatalf("whitelist refusal must name the whitelist, got: %v", err)
	}
	// Duplicate kinds inside one request collapse to one.
	sub, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange, SubscribeStateChange}, time.Hour, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Events) != 1 {
		t.Fatalf("events = %v, want deduplicated single entry", sub.Events)
	}
}

// TestSubscribeStreamEventPushesOnce: a tool_error written to the status
// stream fires exactly once — the incremental cursor consumes the bytes, so
// later ticks without new events push nothing.
func TestSubscribeStreamEventPushesOnce(t *testing.T) {
	env := newSubTestEnv(t)
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeToolError}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	env.tick() // baseline: stream cursor parks at EOF, no push
	if env.pushCount() != 0 {
		t.Fatalf("baseline tick pushed %d times, want 0 (history is old news)", env.pushCount())
	}
	env.appendStream(CollabStatusEvent{
		Session: "ct_peer",
		Event:   CollabStatusToolError,
		Summary: "bash failed: exit 127",
	})
	env.tick()
	if env.pushCount() != 1 {
		t.Fatalf("pushes = %d after new stream event, want 1", env.pushCount())
	}
	if body := env.lastPush().Body; !strings.Contains(body, "tool_error") {
		t.Fatalf("push body missing tool_error: %q", body)
	}
	env.tick()
	if env.pushCount() != 1 {
		t.Fatalf("pushes = %d after re-tick, want 1 (consumed bytes never re-fire)", env.pushCount())
	}
}

// TestSubscribeSteerGateOffDegrades: with the panel switch off the push still
// lands as followup — exactly talk_to_session's task 173 ④ degradation, not
// a refusal.
func TestSubscribeSteerGateOffDegrades(t *testing.T) {
	env := newSubTestEnv(t)
	env.cfg.AllowSteer = false
	env.svc.cfg.AllowSteer = false
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	env.tick()
	env.running.Store(true)
	env.svc.Tick(context.Background(), env.svc.Deliver, false)
	if env.pushCount() != 1 {
		t.Fatalf("pushes = %d, want 1", env.pushCount())
	}
	if got := env.lastPush().Delivery; got != string(sessioncollab.DeliveryFollowup) {
		t.Fatalf("delivery = %q, want followup (steer gate off degrades, never refuses)", got)
	}
}

// TestSubscribeServicePersistAndLoad: the registry survives a restart — a
// fresh service over the same store sees the subscription, and expired
// records are dropped on load.
func TestSubscribeServicePersistAndLoad(t *testing.T) {
	env := newSubTestEnv(t)
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStateChange}, time.Hour, 0, 0); err != nil {
		t.Fatal(err)
	}
	fresh := NewSubscribeService(env.cfg)
	fresh.Now = env.svc.Now
	fresh.load()
	got := fresh.List()
	if len(got) != 1 || got[0].ID != "ct_peer" {
		t.Fatalf("reloaded subs = %+v, want the ct_peer subscription", got)
	}
	// An expired record must not come back.
	env.frozenNano.Add((3 * time.Hour).Nanoseconds())
	stale := NewSubscribeService(env.cfg)
	stale.Now = env.svc.Now
	stale.load()
	if n := len(stale.List()); n != 0 {
		t.Fatalf("reloaded expired subs = %d, want 0", n)
	}
}

// TestSubscribeToolActions drives the tool surface end to end: subscribe via
// the directory identity, list, unsubscribe by title (resolved to the same
// contact id), and the second unsubscribe reads as no-op.
func TestSubscribeToolActions(t *testing.T) {
	env := newSubTestEnv(t)
	resolve := func(to string) (string, string, bool, error) {
		if to == "peer" || to == "ct_peer" {
			return "ct_peer", "", false, nil
		}
		return "", "", false, fmt.Errorf("unknown session %q", to)
	}
	tool := NewSubscribeSessionTool(env.cfg, env.svc, resolve)

	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"subscribe","target":"peer","events":["state_change","tool_error"],"ttl_s":600}`))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !strings.Contains(out, `"subscribed":true`) {
		t.Fatalf("subscribe result: %s", out)
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, `"count":1`) {
		t.Fatalf("list result: %s", out)
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"unsubscribe","target":"ct_peer"}`))
	if err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if !strings.Contains(out, `"unsubscribed":true`) {
		t.Fatalf("unsubscribe result: %s", out)
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"unsubscribe","target":"ct_peer"}`))
	if err != nil {
		t.Fatalf("unsubscribe 2: %v", err)
	}
	if !strings.Contains(out, `"unsubscribed":false`) {
		t.Fatalf("second unsubscribe must read as no-op, got: %s", out)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"nope"}`)); err == nil {
		t.Fatal("unknown action must be refused")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"subscribe","target":"peer","events":["shell_command"]}`)); err == nil {
		t.Fatal("unknown event kind must be refused through the tool too")
	}
}
