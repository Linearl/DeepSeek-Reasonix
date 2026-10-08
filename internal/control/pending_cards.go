package control

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/pendingcards"
)

// 任务 408（异步决策点回访）：把每个审批/提问决策点记入一张持久卡片，
// 用户不在场时决策点不再只活在内存的 prompt map 里——
//   - 入队：{会话, 时间, 动作摘要, 上下文指针}，不随 turn 滚动丢失；
//   - 回访：Notice（会话内显著标记）+ 运行态计数（"N 个待你决定"）+ 通知钩子；
//   - 三态闭环：批完（answered/allow/deny）、超时（wait 过期/TTL）、撤回（用户显式撤销）；
//   - 断点续跑：批完的答案沿既有 reply 通道回流，阻塞的 run 原地继续，不重启。
//
// 边界（任务书第 4 条）：不改审批语义本身——yolo/auto/autopilot 档位、
// 审批超时行为、225 级联全部保持原样；卡片队列是用户级记账，默认关闭
// （铁律 2），关闭时本文件所有入口都是零成本 no-op。

// pendingCardState owns the durable queue behind its own lock, off c.mu, so a
// card write never stalls an approval or status poll. probed remembers whether
// the one-shot startup reconciliation already ran for this session path: the
// runtime-state badge must show restart-left cards without a per-event config
// read or queue open on the refresh hot path.
type pendingCardState struct {
	mu     sync.Mutex
	queue  *pendingcards.Queue
	path   string // session path the queue is bound to ("" = never opened)
	probed bool
}

// pendingCardsLive resolves the switch and returns the bound queue (nil when
// the switch is off or the session has no path yet). Read per call so a
// settings change applies to newly arriving prompts without a restart.
func (c *Controller) pendingCardsLive() (*pendingcards.Queue, time.Duration) {
	enabled, ttl := config.PendingCardsLive()
	if !enabled {
		return nil, 0
	}
	return c.ensurePendingCards(), ttl
}

// ensurePendingCards lazily opens the queue for the current session path and
// rebinds it when the session rotates (same shape as rebindInbox, minus the
// copy semantics — a card belongs to the session it fired in; the old file
// stays on disk as the durable record).
func (c *Controller) ensurePendingCards() *pendingcards.Queue {
	s := &c.pendingCards
	s.mu.Lock()
	defer s.mu.Unlock()
	return c.ensurePendingCardsLocked()
}

// ensurePendingCardsLocked is ensurePendingCards with pendingCardState.mu
// already held (caller may chain it with a probe under the same lock).
func (c *Controller) ensurePendingCardsLocked() *pendingcards.Queue {
	s := &c.pendingCards
	path := c.SessionPath()
	if path == "" {
		return nil
	}
	if s.queue != nil && s.path == path {
		return s.queue
	}
	q, err := pendingcards.Open(path)
	if err != nil {
		if !errors.Is(err, pendingcards.ErrNoSession) {
			log.Printf("[pending-cards] open failed (cards stay off this session): %v", err)
		}
		return nil
	}
	s.queue, s.path = q, path
	return q
}

// notePendingCard records a decision point (enqueue + in-session flag + notify
// hook). Fire-and-forget: card bookkeeping must never fail a prompt.
func (c *Controller) notePendingCard(kind, promptID, turnID, summary string) {
	q, _ := c.pendingCardsLive()
	if q == nil {
		return
	}
	card := pendingcards.Card{
		ID:        promptID,
		Session:   c.SessionPath(),
		CreatedAt: time.Now(),
		Kind:      kind,
		Summary:   clipUTF8(summary, 240),
		TurnID:    turnID,
		State:     pendingcards.StatePending,
	}
	if err := q.Enqueue(card); err != nil {
		log.Printf("[pending-cards] enqueue %s %s: %v", kind, promptID, err)
		return
	}
	pending := q.CountPending()
	c.sink.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelInfo,
		Code:  event.NoticeCodePendingCardEnqueued,
		Text:  fmt.Sprintf("决策点已挂起：第 %d 项待你决定 — %s", pending, card.Summary),
	})
	// 回访通知：复用 permission_prompt 通知通道（hooks.Notification），与审批
	// 提示现有的系统通知同源；Ask 路径此前没有这一跳。
	hookCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		c.hooks.Notification(hookCtx, "Reasonix · 待你决定: "+card.Summary, "permission_prompt")
	}()
	c.refreshRuntimeState(event.Event{})
}

// settlePendingCard closes a card. Best-effort: an unknown id (switch flipped
// on mid-prompt) just means there was never a card.
func (c *Controller) settlePendingCard(promptID, state, outcome string) {
	q, _ := c.pendingCardsLive()
	if q == nil {
		return
	}
	if err := q.Settle(promptID, state, outcome); err != nil && !errors.Is(err, pendingcards.ErrNotFound) {
		log.Printf("[pending-cards] settle %s -> %s: %v", promptID, state, err)
		return
	}
	c.refreshRuntimeState(event.Event{})
}

// expirePendingCard is the timeout arm of the closure: the prompt wait died
// (approval timeout / autopilot grace) and the card records it.
func (c *Controller) expirePendingCard(promptID string) {
	c.settlePendingCard(promptID, pendingcards.StateTimeout, "wait_expired")
}

// expirePendingCardErr distinguishes a real wait expiry from a turn cancel
// (both end the decision window without a user decision; the outcome keeps
// them diagnosable apart).
func (c *Controller) expirePendingCardErr(promptID string, err error) {
	outcome := "wait_expired"
	if errors.Is(err, context.Canceled) {
		outcome = "turn_cancelled"
	}
	c.settlePendingCard(promptID, pendingcards.StateTimeout, outcome)
}

// pendingDecisionCount is the cheap runtime-state badge count. The queue opens
// once per session path on the first refresh after construction (one config
// read, one file read — restart-left cards then show without waiting for a new
// decision point); afterwards counting is in-memory only, zero IO per event.
func (c *Controller) pendingDecisionCount() int {
	s := &c.pendingCards
	s.mu.Lock()
	q, probed := s.queue, s.probed
	s.mu.Unlock()
	if q != nil {
		return q.CountPending()
	}
	if probed {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queue != nil {
		return s.queue.CountPending()
	}
	if s.probed {
		return 0
	}
	s.probed = true
	// The probe sweeps TTL expiry while counting: restart-orphaned cards must
	// not inflate the badge until some later list call happens to sweep them.
	enabled, ttl := config.PendingCardsLive()
	if !enabled {
		return 0
	}
	queue := c.ensurePendingCardsLocked()
	if queue == nil {
		return 0
	}
	return len(queue.Pending(time.Now(), ttl))
}

// PendingDecisionCards returns the pending cards for the 回归汇总 view
// ("N 个待你决定"), sweeping TTL expiry first so restart-orphaned cards close
// instead of accumulating. Callers get copies; the queue stays authoritative.
func (c *Controller) PendingDecisionCards() []pendingcards.Card {
	q, ttl := c.pendingCardsLive()
	if q == nil {
		return nil
	}
	return q.Pending(time.Now(), ttl)
}

// WithdrawPendingCard is the 撤回 arm: the user explicitly declines to decide.
// The underlying prompt resolves through the ordinary paths (approval → deny,
// ask → the explicit no-selection dismissal), and the card records withdrawn.
func (c *Controller) WithdrawPendingCard(promptID string) error {
	return c.WithdrawPendingCardChecked(promptID)
}

// WithdrawPendingCardChecked settles the card withdrawn FIRST (first
// settlement wins, so the ordinary resolution path below cannot overwrite the
// user's explicit withdrawal with a generic resolved outcome), then resolves
// the prompt through the ordinary paths: approval → deny, ask → the explicit
// no-selection dismissal.
func (c *Controller) WithdrawPendingCardChecked(promptID string) error {
	promptID = strings.TrimSpace(promptID)
	if promptID == "" {
		return fmt.Errorf("empty prompt id")
	}
	c.settlePendingCard(promptID, pendingcards.StateWithdrawn, "user_withdrew")
	var resolveErr error
	if _, isAsk := c.pendingAsk(promptID); isAsk {
		// Ask withdrawal = the explicit dismiss path (same as closing the
		// panel): an empty answer batch ends the turn without feeding a
		// fabricated choice to the model.
		resolveErr = c.AnswerQuestionChecked(promptID, emptyAskAnswers(c.pendingAskQuestions(promptID)))
	} else {
		resolveErr = c.approveChecked(promptID, false, false, false)
	}
	return resolveErr
}

// pendingAsk reports whether promptID is a live ask, with its questions.
func (c *Controller) pendingAsk(promptID string) (pendingAsk, bool) {
	c.approval.mu.Lock()
	defer c.approval.mu.Unlock()
	p, ok := c.approval.asks[promptID]
	return p, ok
}

// pendingAskQuestions snapshots a live ask's questions for the dismiss batch.
func (c *Controller) pendingAskQuestions(promptID string) []event.AskQuestion {
	p, ok := c.pendingAsk(promptID)
	if !ok {
		return nil
	}
	return p.questions
}

func emptyAskAnswers(questions []event.AskQuestion) []event.AskAnswer {
	out := make([]event.AskAnswer, 0, len(questions))
	for _, q := range questions {
		out = append(out, event.AskAnswer{QuestionID: q.ID})
	}
	return out
}

// pendingCardSummaryForAsk renders the 动作摘要 for an ask card.
func pendingCardSummaryForAsk(questions []event.AskQuestion) string {
	if len(questions) == 0 {
		return "question"
	}
	q := questions[0]
	if q.Header != "" && q.Prompt != "" {
		return q.Header + ": " + q.Prompt
	}
	if q.Prompt != "" {
		return q.Prompt
	}
	return q.Header
}

// pendingCardSummaryForApproval renders the 动作摘要 for an approval card.
func pendingCardSummaryForApproval(tool, subject string) string {
	if subject == "" {
		return tool
	}
	return tool + ": " + subject
}
