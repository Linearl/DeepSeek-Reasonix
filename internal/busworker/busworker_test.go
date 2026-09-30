package busworker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/safego"
	"reasonix/internal/sessioncollab"
)

func newTestWorker(t *testing.T, mutate func(*Config)) *Worker {
	t.Helper()
	cfg := Config{
		Enabled:      true,
		Command:      "zcode-stub",
		Mode:         "build",
		Concurrency:  2,
		Timeout:      "10s",
		PollInterval: "20ms",
		Contact:      "zcode-worker",
		MailDir:      t.TempDir(),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	w, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w
}

// deliverTask seeds an assignment mail exactly the way collab_spawn does —
// same machine contract, same fields — so the pool test exercises the real
// bus flow rather than a parallel one.
func deliverTask(t *testing.T, w *Worker, cardID, prompt, from string) (mailID string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"kind": "bus-task", "card_id": cardID, "prompt": prompt, "title": "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := w.mail.Deliver(sessioncollab.MailMessage{
		From: from, To: w.contact, Body: string(body), CardID: cardID, RequireReply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return msg.ID
}

func newCard(t *testing.T, w *Worker, assignee string) string {
	t.Helper()
	card, err := w.cards.Create(sessioncollab.Card{Title: "t", Initiator: "zcode-dev", Assignee: assignee})
	if err != nil {
		t.Fatal(err)
	}
	return card.ID
}

// waitFor polls until cond is true or the deadline passes — pool work is
// async by design (poller tick + lane), so tests assert on convergence.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestNewValidation(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(Config{Enabled: false, MailDir: dir}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: want ErrDisabled, got %v", err)
	}
	if _, err := New(Config{Enabled: true, Mode: "ask", MailDir: dir}); err == nil {
		t.Fatal("invalid mode: want error")
	}
	if _, err := New(Config{Enabled: true, Timeout: "soon", MailDir: dir}); err == nil {
		t.Fatal("invalid timeout: want error")
	}
	if _, err := New(Config{Enabled: true, PollInterval: "0", MailDir: dir}); err == nil {
		t.Fatal("invalid poll interval: want error")
	}
	w, err := New(Config{Enabled: true, MailDir: dir})
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if w.command != defaultCommand || w.mode != defaultMode || w.concur != defaultConcur ||
		w.timeout != defaultTimeout || w.poll != defaultPoll || w.contact != defaultContact {
		t.Fatalf("defaults not applied: %+v", w)
	}
}

func TestParseJob(t *testing.T) {
	good := sessioncollab.MailMessage{
		ID: "m1", From: "zcode-dev", ThreadID: "m1",
		Body: `{"kind":"bus-task","card_id":"card_x","prompt":"do it"}`,
	}
	j, ok := parseJob(good)
	if !ok || j.CardID != "card_x" || j.Prompt != "do it" || j.From != "zcode-dev" {
		t.Fatalf("good contract: %+v ok=%v", j, ok)
	}
	for name, body := range map[string]string{
		"not json":       `oops`,
		"wrong kind":     `{"kind":"bus-event","card_id":"c","prompt":"p"}`,
		"missing card":   `{"kind":"bus-task","prompt":"p"}`,
		"missing prompt": `{"kind":"bus-task","card_id":"c"}`,
	} {
		if _, ok := parseJob(sessioncollab.MailMessage{Body: body}); ok {
			t.Fatalf("%s: want drop", name)
		}
	}
}

// TestParseResultZcodeShape uses the exact terminator shape the headless CLI
// emits (prompt-command.ts: type/result + response + projection), preceded by
// stream noise the parser must ignore, and checks the "no result line" case.
func TestParseResultZcodeShape(t *testing.T) {
	w := newTestWorker(t, nil)
	stream := strings.Join([]string{
		`{"type":"turn.started","turnId":"t1"}`,
		`{"type":"part.delta","text":"working…"}`,
		`{"type":"result","sessionId":"s1","traceId":"tr1","response":"all green","eventCount":7,` +
			`"projection":{"status":"complete","turnCount":2,"totalTokenCount":4321,"contextUsed":12000,"contextWindow":200000}}`,
		"",
	}, "\n")
	res := w.parseResult(stream)
	if res.Response != "all green" {
		t.Fatalf("parse: %+v", res)
	}
	if !strings.Contains(string(res.Projection), `"totalTokenCount":4321`) {
		t.Fatalf("projection not carried: %s", res.Projection)
	}
	if got := w.parseResult(`{"type":"part.delta","text":"no terminator"}`); got.Response != "" {
		t.Fatalf("missing terminator: %+v", got)
	}
}

// TestPoolEndToEnd drives the real pool: assignment mail in → card pending →
// running → done, result file on disk, receipt mail back to the sender.
func TestPoolEndToEnd(t *testing.T) {
	w := newTestWorker(t, func(c *Config) { c.Concurrency = 1 })
	var calls atomic.Int32
	w.probe = func(ctx context.Context, cmd *exec.Cmd, j job) error {
		calls.Add(1)
		if cmd.Args[1] != "-p" || cmd.Args[3] != "--output-format" || cmd.Args[4] != "stream-json" {
			t.Errorf("unexpected argv: %v", cmd.Args)
		}
		// The child contract: stdout carries stream noise then the terminator.
		cmd.Stdout.Write([]byte(`{"type":"part.delta"}` + "\n"))
		cmd.Stdout.Write([]byte(`{"type":"result","response":"batch 8 done","projection":{"status":"complete"}}` + "\n"))
		return nil
	}

	cardID := newCard(t, w, w.contact)
	deliverTask(t, w, cardID, "run batch 8", "zcode-dev")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	safego.Go("test.pool", func() { w.Run(ctx) })

	waitFor(t, "card done", func() bool {
		card, err := w.cards.Get(cardID)
		return err == nil && card.Status == sessioncollab.StatusDone
	})
	if calls.Load() != 1 {
		t.Fatalf("probe calls: want 1, got %d", calls.Load())
	}
	card, _ := w.cards.Get(cardID)
	if !strings.Contains(card.Result, "batch 8 done") {
		t.Fatalf("card result: %q", card.Result)
	}
	refPath := filepath.Join(w.mail.Dir(), "bus-results", cardID+".json")
	b, err := os.ReadFile(refPath)
	if err != nil || !strings.Contains(string(b), `"ok": true`) {
		t.Fatalf("result file: %q (%v)", b, err)
	}
	// Receipt answers the sender on the same thread.
	waitFor(t, "receipt mail", func() bool {
		pending, _ := w.mail.Peek("zcode-dev")
		return len(pending) == 1 && strings.Contains(pending[0].Body, `"status":"done"`)
	})
}

// TestPoolDedupByCard: the same assignment delivered twice must execute once
// — the card CAS is the dedup point (package doc).
func TestPoolDedupByCard(t *testing.T) {
	w := newTestWorker(t, func(c *Config) { c.Concurrency = 2 })
	var calls atomic.Int32
	mu := sync.Mutex{}
	order := []string{}
	w.probe = func(ctx context.Context, cmd *exec.Cmd, j job) error {
		mu.Lock()
		order = append(order, j.CardID)
		mu.Unlock()
		calls.Add(1)
		time.Sleep(50 * time.Millisecond) // widen the race window
		cmd.Stdout.Write([]byte(`{"type":"result","response":"ok"}` + "\n"))
		return nil
	}
	cardID := newCard(t, w, w.contact)
	deliverTask(t, w, cardID, "same task", "zcode-dev")
	deliverTask(t, w, cardID, "same task", "zcode-dev") // duplicate mail, same card

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	safego.Go("test.pool", func() { w.Run(ctx) })
	waitFor(t, "card done", func() bool {
		card, err := w.cards.Get(cardID)
		return err == nil && card.Status == sessioncollab.StatusDone
	})
	time.Sleep(100 * time.Millisecond) // let a would-be second run surface
	if calls.Load() != 1 {
		t.Fatalf("duplicate executed: calls=%d order=%v", calls.Load(), order)
	}
}

// TestPoolTimeoutFailsCard: a child that hangs past the per-run budget gets
// its context canceled (real runs: proc tree kill) and the card lands failed.
func TestPoolTimeoutFailsCard(t *testing.T) {
	w := newTestWorker(t, func(c *Config) { c.Timeout = "150ms"; c.PollInterval = "20ms" })
	w.probe = func(ctx context.Context, cmd *exec.Cmd, j job) error {
		<-ctx.Done() // simulate a hung child: exit only when killed
		return context.Cause(ctx)
	}
	cardID := newCard(t, w, w.contact)
	deliverTask(t, w, cardID, "hang forever", "zcode-dev")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	safego.Go("test.pool", func() { w.Run(ctx) })
	waitFor(t, "card failed", func() bool {
		card, err := w.cards.Get(cardID)
		return err == nil && card.Status == sessioncollab.StatusFailed
	})
	card, _ := w.cards.Get(cardID)
	if card.Error == "" {
		t.Fatal("failed card must carry the error")
	}
	waitFor(t, "failure receipt", func() bool {
		pending, _ := w.mail.Peek("zcode-dev")
		return len(pending) == 1 && strings.Contains(pending[0].Body, `"status":"failed"`)
	})
}

// TestPoolDropsMalformedMail: a body that is not the machine contract is
// acked and dropped (audited), never executed, never retried forever.
func TestPoolDropsMalformedMail(t *testing.T) {
	w := newTestWorker(t, nil)
	probeCalled := false
	w.probe = func(ctx context.Context, cmd *exec.Cmd, j job) error {
		probeCalled = true
		return nil
	}
	if _, err := w.mail.Deliver(sessioncollab.MailMessage{
		From: "zcode-dev", To: w.contact, Body: "please look at this free-form text",
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	safego.Go("test.pool", func() { w.Run(ctx) })
	waitFor(t, "mail drained", func() bool {
		pending, _ := w.mail.Peek(w.contact)
		return len(pending) == 0
	})
	time.Sleep(50 * time.Millisecond)
	if probeCalled {
		t.Fatal("malformed mail must not execute")
	}
	b, err := os.ReadFile(filepath.Join(w.mail.Dir(), "bus-worker-audit.jsonl"))
	if err != nil || !strings.Contains(string(b), "malformed assignment mail") {
		t.Fatalf("drop not audited: %q (%v)", b, err)
	}
}

// TestReapStaleRunning: a card left "running" by a dead process is reaped to
// blocked at startup — visible, reopenable, never silently lost.
func TestReapStaleRunning(t *testing.T) {
	w := newTestWorker(t, func(c *Config) { c.Timeout = "1ms" })
	cardID := newCard(t, w, w.contact)
	if _, err := w.cards.Update(cardID, func(c *sessioncollab.Card) error {
		c.Status = sessioncollab.StatusRunning // Update stamps UpdatedAt=now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // now the card is older than horizon = 2×timeout
	w.reapStaleRunning()
	card, err := w.cards.Get(cardID)
	if err != nil || card.Status != sessioncollab.StatusBlocked {
		t.Fatalf("reap: %+v (%v)", card, err)
	}
}
