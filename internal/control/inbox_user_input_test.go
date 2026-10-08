package control

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// 任务580: a durable inbox item consumed as a NEW turn's input must announce
// itself to frontends as a UserInput event (Text = display text, ItemID = the
// durable item id). The wire protocol has no other user-message channel —
// composer submissions render their row optimistically — so without this event
// the transcript stayed without the consumed message until a history reload
// (the 2026-10-07 "到达不渲染 / 切回不刷新" report). Both consumption paths are
// pinned: the guarded dispatcher (TrySubmitInboxItem → submitPreparedInboxTurn)
// and the synchronous bridge (RunInboxTurn → runSynchronousTurn).

const inboxUserInputTestTimeout = 15 * time.Second

type inboxUserInputRecorder struct {
	mu    sync.Mutex
	seen  []event.Event
	done  chan struct{}
	doneN int
}

func (r *inboxUserInputRecorder) record(e event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, e)
	if e.Kind == event.TurnDone {
		r.doneN++
		if r.done == nil {
			r.done = make(chan struct{})
		}
		if r.doneN == 1 {
			close(r.done)
		}
	}
}

func (r *inboxUserInputRecorder) userInputEvents() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]event.Event, 0, 2)
	for _, e := range r.seen {
		if e.Kind == event.UserInput {
			out = append(out, e)
		}
	}
	return out
}

func (r *inboxUserInputRecorder) waitTurnDone(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(inboxUserInputTestTimeout)
	for {
		r.mu.Lock()
		n := r.doneN
		r.mu.Unlock()
		if n >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d turn completions, saw %d", want, n)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type noopInboxRunner struct{}

func (noopInboxRunner) Run(_ context.Context, _ string) error { return nil }

func newInboxUserInputController(t *testing.T, rec *inboxUserInputRecorder) *Controller {
	t.Helper()
	dir := t.TempDir()
	c := New(Options{
		Runner:      noopInboxRunner{},
		Sink:        event.FuncSink(rec.record),
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		c.Close()
		c.autosaveWG.Wait()
	})
	return c
}

func TestTrySubmitInboxItemEmitsUserInput(t *testing.T) {
	rec := &inboxUserInputRecorder{}
	c := newInboxUserInputController(t, rec)
	snap, err := c.EnqueueInbox(InboxRequest{
		Intent:  sessioninbox.IntentFollowup,
		Display: "[跨会话消息] 来自 contact_id=alpha → 发至 contact_id=beta\n帮忙确认 644 的验收矩阵",
		Submit:  "帮忙确认 644 的验收矩阵",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.TrySubmitInboxItem(snap.ItemID); err != nil {
		t.Fatal(err)
	}
	rec.waitTurnDone(t, 1)

	events := rec.userInputEvents()
	if len(events) != 1 {
		t.Fatalf("UserInput events = %d, want 1 (seen kinds: %+v)", len(events), rec.seen)
	}
	if events[0].Text != "[跨会话消息] 来自 contact_id=alpha → 发至 contact_id=beta\n帮忙确认 644 的验收矩阵" {
		t.Fatalf("UserInput display text = %q", events[0].Text)
	}
	if events[0].ItemID != snap.ItemID {
		t.Fatalf("UserInput itemId = %q, want durable item %q", events[0].ItemID, snap.ItemID)
	}
}

func TestRunInboxTurnEmitsUserInput(t *testing.T) {
	rec := &inboxUserInputRecorder{}
	c := newInboxUserInputController(t, rec)
	snap, err := c.EnqueueInbox(InboxRequest{
		Intent:  sessioninbox.IntentFollowup,
		Display: "空闲开轮桥消费的条目",
		Submit:  "空闲开轮桥消费的条目",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RunInboxTurn(context.Background(), snap.ItemID); err != nil {
		t.Fatal(err)
	}
	// RunInboxTurn is synchronous for the whole turn: on return the events are
	// already recorded.
	events := rec.userInputEvents()
	if len(events) != 1 {
		t.Fatalf("UserInput events = %d, want 1", len(events))
	}
	if events[0].Text != "空闲开轮桥消费的条目" || events[0].ItemID != snap.ItemID {
		t.Fatalf("UserInput = (text=%q, item=%q), want (text=%q, item=%q)",
			events[0].Text, events[0].ItemID, "空闲开轮桥消费的条目", snap.ItemID)
	}
}

func TestRunInboxTurnRejectedReplayEmitsNoSecondUserInput(t *testing.T) {
	rec := &inboxUserInputRecorder{}
	c := newInboxUserInputController(t, rec)
	snap, err := c.EnqueueInbox(InboxRequest{
		Intent:  sessioninbox.IntentFollowup,
		Display: "只消费一次的条目",
		Submit:  "只消费一次的条目",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RunInboxTurn(context.Background(), snap.ItemID); err != nil {
		t.Fatal(err)
	}
	// A replay (mirror queue redelivery, late dispatcher kick) must not mint a
	// second announcement: the item is no longer queued, so the run errors
	// before admission and the frontend's itemId dedupe never gets stressed.
	if err := c.RunInboxTurn(context.Background(), snap.ItemID); err == nil {
		t.Fatal("replayed RunInboxTurn succeeded; want ErrInvalidState-class failure")
	}
	if events := rec.userInputEvents(); len(events) != 1 {
		t.Fatalf("UserInput events after replay = %d, want 1", len(events))
	}
}

func TestEmitInboxUserInputSkipsBlankDisplay(t *testing.T) {
	rec := &inboxUserInputRecorder{}
	c := newInboxUserInputController(t, rec)
	c.emitInboxUserInput("item-1", "   ")
	if events := rec.userInputEvents(); len(events) != 0 {
		t.Fatalf("blank display emitted %d UserInput events, want 0", len(events))
	}
}
