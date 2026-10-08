package main

import (
	"fmt"
	"time"

	"reasonix/internal/pendingcards"
)

// 任务 408（异步决策点回访）：desktop 侧的待批卡片 API。
// PendingDecisionCardsForTab 供给回归汇总（"N 个待你决定"明细）；
// WithdrawPendingCardForTab 是三态闭环的撤回臂（审批→deny、提问→关闭面板语义）。

// PendingCardView is the Wails-safe pending-decision card row.
type PendingCardView struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	TurnID    string    `json:"turnId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	State     string    `json:"state"`
	Outcome   string    `json:"outcome,omitempty"`
}

func pendingCardView(c pendingcards.Card) PendingCardView {
	return PendingCardView{
		ID:        c.ID,
		Kind:      c.Kind,
		Summary:   c.Summary,
		TurnID:    c.TurnID,
		CreatedAt: c.CreatedAt,
		State:     c.State,
		Outcome:   c.Outcome,
	}
}

// PendingDecisionCardsForTab returns the tab session's pending decision cards.
// An empty list is the normal off-switch / nothing-pending answer.
func (a *App) PendingDecisionCardsForTab(tabID string) ([]PendingCardView, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return []PendingCardView{}, a.workspaceNotReadyErr(tab)
	}
	cards := ctrl.PendingDecisionCards()
	views := make([]PendingCardView, 0, len(cards))
	for _, c := range cards {
		views = append(views, pendingCardView(c))
	}
	return views, nil
}

// WithdrawPendingCardForTab resolves one pending card as withdrawn: the
// underlying prompt answers deny (approval) or dismiss (ask) through the
// ordinary resolution paths, and the durable card records the withdrawal.
func (a *App) WithdrawPendingCardForTab(tabID, promptID string) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	if err := ctrl.WithdrawPendingCard(promptID); err != nil {
		return fmt.Errorf("withdraw pending card %s: %w", promptID, err)
	}
	return nil
}
