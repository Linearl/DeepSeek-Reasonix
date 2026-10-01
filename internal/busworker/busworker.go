// Package busworker runs unattended bus task assignments through an external
// headless agent CLI. It drains ONE mailbox contact (default "zcode-worker"),
// so any bus participant can hand work to the pool with the same durable
// primitives everything else on the bus already uses: a zcode role via
// collab_spawn (card + assignment mail in one step), or a Reasonix session
// via talk_to_session to the pool contact (bus #1) — the mail body must be
// the kind=bus-task contract over a pending card, which is exactly what
// collab_spawn produces and what parseJob below demands. Nothing here touches
// controller/session state: a run is one tracked child process plus a result
// file plus a card update plus a receipt mail.
//
// # Concurrency model (review-facing, per the M3 audit request)
//
// Goroutine lifecycle — exactly two kinds, all bounded by the Run ctx:
//
//	1 poller goroutine: ticker loop, the ONLY Drain caller for the worker
//	    contact (single consumer by construction, matching sessioncollab's
//	    two-phase Claim→Ack contract). Claims a batch and hands accepted
//	    jobs to the lane channel; exits on ctx.Done and closes the channel.
//	N lane goroutines: one per configured lane, `for job := range lane`.
//	    Each job = one tracked child process (proc.RunCommand: Windows Job
//	    Object + tree kill, bounded cancel-retry) under a per-job timeout
//	    context derived from Run's ctx. Lanes share no mutable state.
//
// Lock hierarchy (outermost first; never taken in the reverse order):
//
//  1. sessioncollab MailStore file lock — held inside Drain/Deliver only
//  2. sessioncollab CardStore file lock — held inside Create/Update only
//  3. busworker holds NO in-process mutex: shared state is exclusively
//     channel- and file-store-mediated, so a lane crash cannot poison a
//     lock and no lock order exists to invert.
//
// Delivery guarantee: Claim→Ack is at-least-once INTO the lane channel; the
// CARD is the dedup point — a job whose card is no longer "pending" is
// acked and skipped without a rerun (claimCard CAS via CardStore's file
// lock), so duplicate mail or a replayed claim executes at most once per
// card. Backpressure is lossless: Drain only acks jobs the lane channel
// actually accepted; the rest stay queued in the mailbox file. Shutdown is
// lossy by declaration: in-flight runs get their timeout context canceled
// and settle as failed; queued-but-unstarted jobs are dropped after being
// acked (their cards stay pending, so re-sending re-runs them). Startup
// reaps cards left "running" by a dead process to "blocked".
package busworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
	"reasonix/internal/proc"
	"reasonix/internal/safego"
	"reasonix/internal/sessioncollab"
)

const (
	defaultCommand     = "zcode"
	defaultMode        = "build"
	defaultConcur      = 2
	defaultTimeout     = 30 * time.Minute
	defaultPoll        = 5 * time.Second
	defaultContact     = "zcode-worker"
	resultTailCap      = 2 << 20 // keep the last 2 MiB of child stdout: the terminator lives at the end, memory stays bounded
	receiptBodyCap     = 4000    // receipts stay small; the result file carries the payload
	clipLineCap        = 200
	staleRunningFactor = 2 // reap horizon = 2 × per-run timeout
)

// job is one claimed assignment, decoded from the mail-body contract
// (parseJob). Mail without a valid contract is acked, audited, dropped —
// the pool never guesses.
type job struct {
	MessageID string
	From      string
	ThreadID  string
	CardID    string
	Prompt    string
	Title     string
	Workspace string
}

type taskContract struct {
	Kind      string `json:"kind"`
	CardID    string `json:"card_id"`
	Prompt    string `json:"prompt"`
	Title     string `json:"title,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

// Worker owns the pool. Constructed once per serve process when enabled.
type Worker struct {
	mail      *sessioncollab.MailStore
	cards     *sessioncollab.CardStore
	command   string
	mode      string
	workspace string
	concur    int
	timeout   time.Duration
	poll      time.Duration
	contact   string
	resultDir string
	auditPath string
	// lane is the single hand-off channel poller → lanes, buffered so the
	// poller can apply backpressure without blocking under the mailbox lock.
	lane chan job
	// probe, when set (tests), replaces the real child-process execution.
	probe func(ctx context.Context, cmd *exec.Cmd, j job) error
}

// runResult is what one child run yielded.
type runResult struct {
	OK         bool
	Response   string
	ExitErr    string
	Projection json.RawMessage
	Usage      json.RawMessage
}

// Config mirrors [config.BusWorkerConfig]; normalized in New. Local type so
// this package does not import config for its inputs.
type Config struct {
	Enabled      bool
	Command      string
	Mode         string
	Workspace    string
	Concurrency  int
	Timeout      string
	PollInterval string
	Contact      string
	MailDir      string
	ResultDir    string
}

// ErrDisabled mirrors busmcp: a New error means "do not start the pool".
var ErrDisabled = errors.New("busworker: not enabled")

// New validates and normalizes cfg. A returned error means the pool must
// not start; serve logs and continues.
func New(cfg Config) (*Worker, error) {
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	w := &Worker{
		command:   strings.TrimSpace(cfg.Command),
		mode:      strings.TrimSpace(cfg.Mode),
		workspace: strings.TrimSpace(cfg.Workspace),
		contact:   strings.TrimSpace(cfg.Contact),
	}
	if w.command == "" {
		w.command = defaultCommand
	}
	if w.mode == "" {
		w.mode = defaultMode
	}
	if w.mode != "build" && w.mode != "yolo" {
		return nil, fmt.Errorf("busworker: mode must be build|yolo, got %q", w.mode)
	}
	w.concur = cfg.Concurrency
	if w.concur <= 0 {
		w.concur = defaultConcur
	}
	w.timeout = defaultTimeout
	if strings.TrimSpace(cfg.Timeout) != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("busworker: invalid timeout %q", cfg.Timeout)
		}
		w.timeout = d
	}
	w.poll = defaultPoll
	if strings.TrimSpace(cfg.PollInterval) != "" {
		d, err := time.ParseDuration(cfg.PollInterval)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("busworker: invalid poll_interval %q", cfg.PollInterval)
		}
		w.poll = d
	}
	if w.contact == "" {
		w.contact = defaultContact
	}
	mailDir := cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	if strings.TrimSpace(mailDir) == "" {
		return nil, errors.New("busworker: no mailbox directory available")
	}
	w.mail = sessioncollab.NewMailStore(mailDir)
	w.cards = sessioncollab.NewCardStore(mailDir)
	w.resultDir = cfg.ResultDir
	if w.resultDir == "" {
		w.resultDir = filepath.Join(mailDir, "bus-results")
	}
	w.auditPath = filepath.Join(mailDir, "bus-worker-audit.jsonl")
	w.lane = make(chan job, w.concur*4)
	return w, nil
}

// Run blocks until ctx is done: one poller, N lanes, per the concurrency
// model in the package doc.
func (w *Worker) Run(ctx context.Context) {
	w.reapStaleRunning()
	var lanes sync.WaitGroup
	for i := 0; i < w.concur; i++ {
		lanes.Add(1)
		safego.Go("busworker.lane", func() {
			defer lanes.Done()
			for j := range w.lane {
				w.execute(ctx, j)
			}
		})
	}
	safego.Go("busworker.poller", func() {
		defer close(w.lane)
		ticker := time.NewTicker(w.poll)
		defer ticker.Stop()
		w.drainOnce() // first pass immediately so a queued task starts without waiting a tick
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.drainOnce()
			}
		}
	})
	lanes.Wait()
}

// drainOnce claims one batch. Jobs enter the lane channel only when there
// is capacity; anything not accepted is NOT acked and stays in the mailbox
// (lossless backpressure). Malformed mail is acked and dropped — the pool
// never guesses at a body, and the audit line records it.
func (w *Worker) drainOnce() {
	_ = w.mail.Drain(w.contact, func(pending, refused []sessioncollab.MailMessage) []string {
		settled := make([]string, 0, len(pending)+len(refused))
		for _, m := range refused {
			settled = append(settled, m.ID) // over the hop ceiling: gone for good
		}
		for _, m := range pending {
			j, ok := parseJob(m)
			if !ok {
				w.audit("drop", "malformed assignment mail "+m.ID+" from "+m.From)
				settled = append(settled, m.ID)
				continue
			}
			select {
			case w.lane <- j:
				settled = append(settled, m.ID)
			default:
				// Lane channel full: return without acking the rest so they
				// stay queued for the next tick (Claim is not exclusive; the
				// card CAS makes a re-claim idempotent).
				return settled
			}
		}
		return settled
	})
}

// parseJob decodes the assignment contract. Only kind=bus-task with a card
// and a prompt is a job.
func parseJob(m sessioncollab.MailMessage) (job, bool) {
	var c taskContract
	if err := json.Unmarshal([]byte(m.Body), &c); err != nil {
		return job{}, false
	}
	if c.Kind != "bus-task" || strings.TrimSpace(c.CardID) == "" || strings.TrimSpace(c.Prompt) == "" {
		return job{}, false
	}
	return job{
		MessageID: m.ID,
		From:      m.From,
		ThreadID:  m.ThreadID,
		CardID:    c.CardID,
		Prompt:    c.Prompt,
		Title:     c.Title,
		Workspace: c.Workspace,
	}, true
}

// execute runs one job on a lane. Every exit path lands in exactly one
// terminal card state (done | failed) plus a receipt mail, so a sender is
// never left waiting without an answer.
func (w *Worker) execute(ctx context.Context, j job) {
	// Card CAS is the dedup point (package doc): only the lane that moves
	// the card pending→running runs it; everyone else no-ops.
	if err := w.claimCard(j.CardID); err != nil {
		w.audit("skip", "card "+j.CardID+" not claimable: "+err.Error())
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	dir := j.Workspace
	if dir == "" {
		dir = w.workspace
	}
	// Construct through internal/proc (desktop background-process gate): argv
	// values stay separate and Windows hides the child console window.
	cmd := proc.Command(w.command, "-p", j.Prompt, "--output-format", "stream-json", "--mode", w.mode)
	cmd.Dir = dir
	stdoutTail := newTail(resultTailCap)
	stderrTail := newTail(16 << 10)
	cmd.Stdout = stdoutTail
	cmd.Stderr = stderrTail

	runErr := w.runChild(runCtx, cmd, j)
	res := w.parseResult(stdoutTail.String())
	if runErr != nil {
		res.OK = false
		if res.ExitErr == "" {
			res.ExitErr = runErr.Error()
		}
	} else if res.Response == "" {
		// Exit 0 but no terminator line: treat as a failure so a silent
		// child can never masquerade as a finished task.
		res.OK = false
		res.ExitErr = "child produced no result line"
	} else {
		res.OK = true
	}

	resultRef := ""
	if path, err := w.writeResultFile(j, res, stderrTail.String()); err != nil {
		w.audit("result-file", "card "+j.CardID+": "+err.Error())
	} else {
		resultRef = path
	}
	w.finishCard(j, res, resultRef)
	w.sendReceipt(j, res, resultRef)
}

// runChild is the process execution seam; tests replace it via w.probe.
func (w *Worker) runChild(ctx context.Context, cmd *exec.Cmd, j job) error {
	if w.probe != nil {
		return w.probe(ctx, cmd, j)
	}
	_, err := proc.RunCommand(ctx, cmd, proc.RunOptions{
		Track:          true,
		Source:         "busworker",
		ShellKind:      "headless-agent",
		CommandPreview: fmt.Sprintf("%s -p <prompt:%d chars> --output-format stream-json --mode %s", w.command, len(j.Prompt), w.mode),
	})
	return err
}

// claimCard CASes the card into running. pending→running is the only legal
// entry; anything else (running/done/failed/blocked/missing) refuses so a
// duplicate mail or a reaped card cannot double-execute.
func (w *Worker) claimCard(cardID string) error {
	_, err := w.cards.Update(cardID, func(c *sessioncollab.Card) error {
		if c.Status != sessioncollab.StatusPending {
			return fmt.Errorf("status %s", c.Status)
		}
		c.Status = sessioncollab.StatusRunning
		c.Nodes = append(c.Nodes, sessioncollab.CardNode{
			ContactID: w.contact,
			Role:      "worker",
			At:        time.Now().UnixMilli(),
			Note:      "worker claimed assignment",
		})
		return nil
	})
	return err
}

// finishCard settles the terminal state and stamps the result chain.
func (w *Worker) finishCard(j job, res runResult, resultRef string) {
	_, err := w.cards.Update(j.CardID, func(c *sessioncollab.Card) error {
		if c.Status != sessioncollab.StatusRunning {
			return fmt.Errorf("card left running by another writer: %s", c.Status)
		}
		if res.OK {
			c.Status = sessioncollab.StatusDone
			c.Result = clipReceipt(res.Response)
			c.Error = ""
		} else {
			c.Status = sessioncollab.StatusFailed
			c.Error = clipReceipt(res.ExitErr)
		}
		if resultRef != "" {
			c.Result = strings.TrimSpace(c.Result + "\n[result_ref] " + resultRef)
		}
		note := "run ok"
		if !res.OK {
			note = "run failed: " + clipOneLine(res.ExitErr)
		}
		c.Nodes = append(c.Nodes, sessioncollab.CardNode{
			ContactID: w.contact,
			Role:      "worker",
			At:        time.Now().UnixMilli(),
			Note:      note,
		})
		return nil
	})
	if err != nil {
		w.audit("finish", "card "+j.CardID+": "+err.Error())
	}
}

// sendReceipt answers the sender on the same thread. Receipts never demand
// a reply (that would loop) and carry the result_ref, not the payload.
func (w *Worker) sendReceipt(j job, res runResult, resultRef string) {
	status := "done"
	if !res.OK {
		status = "failed"
	}
	body, err := json.Marshal(map[string]string{
		"kind":       "bus-receipt",
		"card_id":    j.CardID,
		"status":     status,
		"result_ref": resultRef,
		"summary":    clipReceipt(res.Response),
		"error":      clipOneLine(res.ExitErr),
	})
	if err != nil {
		w.audit("receipt", "card "+j.CardID+": "+err.Error())
		return
	}
	target := j.From
	if strings.TrimSpace(target) == "" {
		target = w.contact // sender unknown: keep the receipt on our own inbox for the record
	}
	if _, err := w.mail.Deliver(sessioncollab.MailMessage{
		From:     w.contact,
		To:       target,
		Body:     string(body),
		CardID:   j.CardID,
		ThreadID: j.ThreadID,
	}); err != nil {
		w.audit("receipt", "card "+j.CardID+": "+err.Error())
	}
}

// reapStaleRunning recovers cards the previous process died mid-run on.
// Stale means: assigned to this contact, still "running", not updated within
// two run timeouts. They go to blocked — reopenable, never silently lost.
func (w *Worker) reapStaleRunning() {
	cards, err := w.cards.List()
	if err != nil {
		return
	}
	horizon := time.Now().Add(-staleRunningFactor * w.timeout).UnixMilli()
	for _, c := range cards {
		if c.Status != sessioncollab.StatusRunning || c.Assignee != w.contact || c.UpdatedAt > horizon {
			continue
		}
		if _, err := w.cards.Update(c.ID, func(card *sessioncollab.Card) error {
			if card.Status != sessioncollab.StatusRunning {
				return nil
			}
			card.Status = sessioncollab.StatusBlocked
			card.Nodes = append(card.Nodes, sessioncollab.CardNode{
				ContactID: w.contact,
				Role:      "worker",
				At:        time.Now().UnixMilli(),
				Note:      "reaped at worker start: run outlived the previous process",
			})
			return nil
		}); err == nil {
			w.audit("reap", "card "+c.ID+" → blocked")
		}
	}
}

// resultFile is the durable payload a receipt points at.
type resultFile struct {
	CardID     string          `json:"card_id"`
	MessageID  string          `json:"message_id"`
	OK         bool            `json:"ok"`
	Response   string          `json:"response,omitempty"`
	ExitErr    string          `json:"exit_error,omitempty"`
	Projection json.RawMessage `json:"projection,omitempty"`
	Usage      json.RawMessage `json:"usage,omitempty"`
	StderrTail string          `json:"stderr_tail,omitempty"`
	FinishedAt int64           `json:"finished_at"`
}

func (w *Worker) writeResultFile(j job, res runResult, stderrTail string) (string, error) {
	payload, err := json.MarshalIndent(resultFile{
		CardID:     j.CardID,
		MessageID:  j.MessageID,
		OK:         res.OK,
		Response:   res.Response,
		ExitErr:    res.ExitErr,
		Projection: res.Projection,
		Usage:      res.Usage,
		StderrTail: clipReceipt(stderrTail),
		FinishedAt: time.Now().UnixMilli(),
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(w.resultDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(w.resultDir, j.CardID+".json")
	if err := fileutil.AtomicWriteFile(path, payload, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// parseResult scans the captured stdout tail for the stream-json terminator:
// the last line that parses as {"type":"result", ...}. The headless CLI
// guarantees the result line is final ("结果行之后绝不能再冒出事件行"), so a
// backward scan over the tail is correct and cheap; everything before it is
// stream noise this pool deliberately does not interpret.
func (w *Worker) parseResult(stdoutTail string) runResult {
	var res runResult
	lines := strings.Split(stdoutTail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, `"type": "result"`) && !strings.Contains(line, `"type":"result"`) {
			continue
		}
		var parsed struct {
			Response   string          `json:"response"`
			Projection json.RawMessage `json:"projection"`
			Usage      json.RawMessage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			continue
		}
		res.Response = parsed.Response
		res.Projection = parsed.Projection
		res.Usage = parsed.Usage
		break
	}
	return res
}

// ── small helpers ────────────────────────────────────────────────────────────

// tail is a bounded stdout collector: it keeps the LAST cap bytes — exactly
// the region the stream-json terminator lives in — and bounds memory no
// matter how chatty a run is.
type tail struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newTail(capacity int) *tail { return &tail{cap: capacity} }

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.cap {
		t.buf = t.buf[len(t.buf)-t.cap:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func (w *Worker) audit(kind, detail string) {
	b, err := json.Marshal(map[string]any{
		"at":    time.Now().UnixMilli(),
		"kind":  kind,
		"event": detail,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(w.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func clipReceipt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= receiptBodyCap {
		return s
	}
	return s[:receiptBodyCap] + "…"
}

func clipOneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= clipLineCap {
		return s
	}
	return s[:clipLineCap] + "…"
}
