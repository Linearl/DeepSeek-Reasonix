package control

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/pendingcards"
)

// 任务 408（异步决策点回访）验收锚：
//   - 决策点 → 卡片入队 + 通知（可测）
//   - 批完 → 断点续跑（不重启）
//   - 批完/超时/撤回三态闭环；默认关（铁律 2，关=今日行为逐字节等价）

// enablePendingCards arms the experimental switch in an isolated REASONIX_HOME
// (the same pattern as enableCascadeApproval) so a test never reads the
// developer's real config.
func enablePendingCards(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load isolated config: %v", err)
	}
	if err := cfg.SetExperimentalPendingCards(true); err != nil {
		t.Fatalf("arm pending cards: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save isolated config: %v", err)
	}
	if enabled, _ := config.PendingCardsLive(); !enabled {
		t.Fatal("pending cards switch did not take effect in the isolated home")
	}
}

// keepPendingCardsOff pins the switch off in an isolated home so the
// default-off equivalence never depends on the developer's real config.
func keepPendingCardsOff(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	if _, err := os.Stat(filepath.Join(home, "config.toml")); err == nil {
		t.Fatal("isolated home unexpectedly has a config")
	}
	if enabled, _ := config.PendingCardsLive(); enabled {
		t.Fatal("pending cards must default off in a clean home")
	}
}

// pendingCardsSessionPath mints a real session transcript path (the queue
// roots its file next to it, the same sibling-file convention as the inbox).
func pendingCardsSessionPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("mint session file: %v", err)
	}
	return path
}

func pendingCardsFixture(t *testing.T, sink event.Sink, timeout time.Duration) *Controller {
	t.Helper()
	return New(Options{
		Sink:            sink,
		SessionDir:      t.TempDir(),
		SessionPath:     pendingCardsSessionPath(t),
		ApprovalTimeout: timeout,
	})
}

// pendingCardsSink captures the prompts and the notices a decision point emits.
type pendingCardsSink struct {
	mu        sync.Mutex
	asks      []event.Ask
	approvals []event.Approval
	notices   []event.Event
}

func (s *pendingCardsSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.Kind {
	case event.AskRequest:
		s.asks = append(s.asks, e.Ask)
	case event.ApprovalRequest:
		s.approvals = append(s.approvals, e.Approval)
	case event.Notice:
		s.notices = append(s.notices, e)
	}
}

func (s *pendingCardsSink) hasNotice(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.notices {
		if n.Code == code {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !ready() {
		select {
		case <-deadline:
			t.Fatalf("%s never happened", what)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func cardStateFor(t *testing.T, sessionPath, promptID string) pendingcards.Card {
	t.Helper()
	q, err := pendingcards.Open(sessionPath)
	if err != nil {
		t.Fatalf("open card queue: %v", err)
	}
	for _, c := range q.All() {
		if c.ID == promptID {
			return c
		}
	}
	t.Fatalf("card %s not found in %s", promptID, pendingcards.FileNameFor(sessionPath))
	return pendingcards.Card{}
}

// waitForCard polls until the card is durable. The prompt event reaches the
// sink a few statements before notePendingCard persists, so decision-side
// assertions must wait for the card instead of racing the emit.
func waitForCard(t *testing.T, sessionPath, promptID string) pendingcards.Card {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		q, err := pendingcards.Open(sessionPath)
		if err != nil {
			t.Fatalf("open card queue: %v", err)
		}
		for _, c := range q.All() {
			if c.ID == promptID {
				return c
			}
		}
		select {
		case <-deadline:
			t.Fatalf("card %s never became durable in %s", promptID, pendingcards.FileNameFor(sessionPath))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// 决策点 → 卡片入队 + 会话内显著标记（可测）。
func TestPendingCardAskEnqueuesCardAndFlagsSession(t *testing.T) {
	enablePendingCards(t)
	sink := &pendingCardsSink{}
	c := pendingCardsFixture(t, sink, 2*time.Second)

	type askResult struct {
		answers []event.AskAnswer
		err     error
	}
	done := make(chan askResult, 1)
	go func() {
		answers, err := c.Ask(context.Background(), askProbeQuestions())
		done <- askResult{answers, err}
	}()

	waitFor(t, "ask request", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.asks) == 1
	})
	promptID := sink.asks[0].ID

	// 入队：持久卡片就位（kind=ask、摘要非空、状态 pending）。
	card := waitForCard(t, c.SessionPath(), promptID)
	if card.Kind != pendingcards.KindAsk || card.State != pendingcards.StatePending {
		t.Fatalf("card = %+v, want pending ask card", card)
	}
	if card.Summary == "" {
		t.Fatal("card summary is empty; the 动作摘要 must name the decision")
	}
	// 通知：会话内显著标记（pending_card_enqueued notice）。
	if !sink.hasNotice(event.NoticeCodePendingCardEnqueued) {
		t.Fatal("enqueue notice was not emitted")
	}
	// 汇总面：pending list 与运行态计数都看得到这一项。
	if got := len(c.PendingDecisionCards()); got != 1 {
		t.Fatalf("PendingDecisionCards = %d, want 1", got)
	}
	if got := c.RuntimeStateSnapshot().PendingCards; got != 1 {
		t.Fatalf("runtime state PendingCards = %d, want 1", got)
	}

	// 批完 → 断点续跑：答案沿既有 reply 通道回流，Ask 原地返回，不重启。
	answers := []event.AskAnswer{{QuestionID: "q1", Selected: []string{"A"}}}
	if err := c.AnswerQuestionChecked(promptID, answers); err != nil {
		t.Fatalf("answer: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("ask failed: %v", got.err)
		}
		if len(got.answers) != 1 || len(got.answers[0].Selected) != 1 || got.answers[0].Selected[0] != "A" {
			t.Fatalf("ask returned %+v, want the user's selection", got.answers)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("answered ask never resumed the blocked run")
	}
	card = cardStateFor(t, c.SessionPath(), promptID)
	if card.State != pendingcards.StateResolved || card.Outcome != "answered" {
		t.Fatalf("card after answer = %+v, want resolved/answered", card)
	}
	if got := c.RuntimeStateSnapshot().PendingCards; got != 0 {
		t.Fatalf("runtime state PendingCards = %d after resolution, want 0", got)
	}
}

// 审批决策点同样入队；deny 结算为批完。
func TestPendingCardApprovalEnqueuesAndSettlesOnDecision(t *testing.T) {
	enablePendingCards(t)
	sink := &pendingCardsSink{}
	c := pendingCardsFixture(t, sink, 2*time.Second)

	type approvalResult struct {
		allow bool
		err   error
	}
	done := make(chan approvalResult, 1)
	go func() {
		allow, _, err := c.requestApprovalWithReason(context.Background(), "bash", "rm -rf /", nil, "test")
		done <- approvalResult{allow, err}
	}()

	waitFor(t, "approval request", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.approvals) == 1
	})
	promptID := sink.approvals[0].ID

	card := waitForCard(t, c.SessionPath(), promptID)
	if card.Kind != pendingcards.KindApproval || card.State != pendingcards.StatePending {
		t.Fatalf("card = %+v, want pending approval card", card)
	}
	if card.Summary != "bash: rm -rf /" {
		t.Fatalf("card summary = %q, want tool+subject", card.Summary)
	}

	if err := c.ResolveApproval(promptID, false, "once"); err != nil {
		t.Fatalf("resolve deny: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("approval wait failed: %v", got.err)
		}
		if got.allow {
			t.Fatal("denied approval returned allow=true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("denied approval never resumed the blocked run")
	}
	card = cardStateFor(t, c.SessionPath(), promptID)
	if card.State != pendingcards.StateResolved || card.Outcome != "deny" {
		t.Fatalf("card after deny = %+v, want resolved/deny", card)
	}
}

// 撤回：提问卡撤回走面板关闭语义（空答案批 → 关当前轮），卡片记 withdrawn，
// 提问不再处于待答集合。
func TestPendingCardWithdrawAsk(t *testing.T) {
	enablePendingCards(t)
	sink := &pendingCardsSink{}
	c := pendingCardsFixture(t, sink, 2*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = c.Ask(ctx, askProbeQuestions())
	}()
	waitFor(t, "ask request", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.asks) == 1
	})
	promptID := sink.asks[0].ID
	waitForCard(t, c.SessionPath(), promptID)

	if err := c.WithdrawPendingCard(promptID); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	card := cardStateFor(t, c.SessionPath(), promptID)
	if card.State != pendingcards.StateWithdrawn || card.Outcome != "user_withdrew" {
		t.Fatalf("card after withdraw = %+v, want withdrawn/user_withdrew", card)
	}
	// 提问已离开待答集合（关闭面板语义消费了它，而不是留一个死面板）。
	if _, asks := c.approval.snapshotPrompts(); len(asks) != 0 {
		t.Fatalf("pending asks after withdraw = %d, want 0", len(asks))
	}
}

// 撤回：审批卡撤回即 deny，卡片记 withdrawn（首次结算获胜，不被 deny 覆盖）。
func TestPendingCardWithdrawApproval(t *testing.T) {
	enablePendingCards(t)
	sink := &pendingCardsSink{}
	c := pendingCardsFixture(t, sink, 2*time.Second)

	allowed := make(chan bool, 1)
	go func() {
		allow, _, _ := c.requestApprovalWithReason(context.Background(), "write_file", "/tmp/x", nil, "test")
		allowed <- allow
	}()
	waitFor(t, "approval request", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.approvals) == 1
	})
	promptID := sink.approvals[0].ID
	waitForCard(t, c.SessionPath(), promptID)

	if err := c.WithdrawPendingCard(promptID); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	select {
	case allow := <-allowed:
		if allow {
			t.Fatal("withdrawn approval must deny, not allow")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("withdrawn approval never released the blocked run")
	}
	card := cardStateFor(t, c.SessionPath(), promptID)
	if card.State != pendingcards.StateWithdrawn {
		t.Fatalf("card after withdraw = %+v, want withdrawn (first settlement wins over deny)", card)
	}
}

// 超时：等待过期 → 卡片记 timeout（wait_expired），等待方照常拿到错误。
func TestPendingCardTimeoutClosesCard(t *testing.T) {
	enablePendingCards(t)
	sink := &pendingCardsSink{}
	c := pendingCardsFixture(t, sink, 80*time.Millisecond)

	errc := make(chan error, 1)
	go func() {
		_, err := c.Ask(context.Background(), askProbeQuestions())
		errc <- err
	}()
	waitFor(t, "ask request", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.asks) == 1
	})
	promptID := sink.asks[0].ID

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expired ask returned answers instead of an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expired ask never unblocked")
	}
	card := cardStateFor(t, c.SessionPath(), promptID)
	if card.State != pendingcards.StateTimeout || card.Outcome != "wait_expired" {
		t.Fatalf("card after expiry = %+v, want timeout/wait_expired", card)
	}
}

// 重启等价：磁盘上的 pending 卡片重新可见；TTL 过期的孤儿卡结算为 timeout
// （不随 turn 滚动丢失，也不会无限积压）。
func TestPendingCardsSurviveRestartAndSweepExpired(t *testing.T) {
	enablePendingCards(t)
	sessionPath := pendingCardsSessionPath(t)

	// 前一进程留下：一张新卡 + 一张已过 TTL 的孤儿卡。
	stale := time.Now().Add(-2 * time.Hour)
	q, err := pendingcards.Open(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh := pendingcards.Card{ID: "fresh", Session: sessionPath, CreatedAt: time.Now(), Kind: pendingcards.KindApproval, Summary: "bash: go test", State: pendingcards.StatePending}
	if err := q.Enqueue(fresh); err != nil {
		t.Fatal(err)
	}
	old := pendingcards.Card{ID: "orphan", Session: sessionPath, CreatedAt: stale, Kind: pendingcards.KindAsk, Summary: "选哪个库", State: pendingcards.StatePending}
	if err := q.Enqueue(old); err != nil {
		t.Fatal(err)
	}

	// 重启：新控制器在同一 session path 上打开队列。
	c := New(Options{Sink: event.Discard, SessionDir: t.TempDir(), SessionPath: sessionPath})
	cards := c.PendingDecisionCards()
	if len(cards) != 1 || cards[0].ID != "fresh" {
		t.Fatalf("pending after restart = %+v, want only the fresh card", cards)
	}
	card := cardStateFor(t, sessionPath, "orphan")
	if card.State != pendingcards.StateTimeout || card.Outcome != "ttl_expired" {
		t.Fatalf("orphan card = %+v, want timeout/ttl_expired", card)
	}
	if got := c.RuntimeStateSnapshot().PendingCards; got != 1 {
		t.Fatalf("runtime state PendingCards = %d after restart, want 1", got)
	}
}

// 默认关：开关关时决策点不落卡、运行态计数恒 0（今日行为逐字节等价）。
func TestPendingCardsOffKeepsLegacyBehavior(t *testing.T) {
	keepPendingCardsOff(t)
	sink := &pendingCardsSink{}
	c := pendingCardsFixture(t, sink, 2*time.Second)

	done := make(chan error, 1)
	go func() {
		_, err := c.Ask(context.Background(), askProbeQuestions())
		done <- err
	}()
	waitFor(t, "ask request", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.asks) == 1
	})
	promptID := sink.asks[0].ID

	if _, err := os.Stat(pendingcards.FileNameFor(c.SessionPath())); !os.IsNotExist(err) {
		t.Fatalf("card file exists while the switch is off: %v", err)
	}
	if got := len(c.PendingDecisionCards()); got != 0 {
		t.Fatalf("PendingDecisionCards = %d while off, want nil/0", got)
	}
	if got := c.RuntimeStateSnapshot().PendingCards; got != 0 {
		t.Fatalf("runtime state PendingCards = %d while off, want 0", got)
	}
	if sink.hasNotice(event.NoticeCodePendingCardEnqueued) {
		t.Fatal("enqueue notice emitted while the switch is off")
	}

	answers := []event.AskAnswer{{QuestionID: "q1", Selected: []string{"B"}}}
	if err := c.AnswerQuestionChecked(promptID, answers); err != nil {
		t.Fatalf("answer while off: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ask while off failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ask while off never resumed")
	}
}
