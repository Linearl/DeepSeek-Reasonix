package collabinbox

// 任务 980 ③：信件详情全文读口。
//
// 用户验收实测（2026-10-18，0528 包协作中心信件面板）：点开信件无法查看完整
// 详情——列表条目只有 preview（首行截断，设计上「enough to triage, never a
// transcript dump」），全文此前没有任何读口。本文件补上按 id 的单封全文读：
// 列表保持 preview-only（性能与设计意图不变），详情弹窗按需取全文。
//
// 诚实性口径（与任务 587 空/败分离同源）：锁繁忙 / 信箱库不可读导致 History
// 降级为空行集时，必须上报错误而不是「not found」——否则详情弹窗会把「读不
// 到」渲染成空正文，正是 587 修掉的静默空同形。

import (
	"context"
	"errors"
	"fmt"
)

// MailDetail is the panel's on-demand full-body view of ONE archive entry.
// Metadata mirrors the list entry's triage line so the popup header renders
// without a second lookup; Body carries the complete original text.
type MailDetail struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	FromSession string `json:"fromSession,omitempty"`
	To          string `json:"to"`
	ToTitle     string `json:"toTitle,omitempty"`
	At          int64  `json:"at"`
	ThreadID    string `json:"threadId"`
	Bucket      string `json:"bucket"`
	Channel     string `json:"channel,omitempty"`
	Preview     string `json:"preview"`
	// RefPath mirrors the list entry's file reference (任务 1016 增强项②) —
	// an 意见箱 notification's authoritative md, absolute path.
	RefPath string `json:"refPath,omitempty"`
	// Body is the complete mail body, untruncated — the reason this read path
	// exists (任务 980 ③: 弹窗展示全文).
	Body string `json:"body"`
}

// ReadMail returns the full body of one entry by id (任务 980 ③). Read-only:
// no state writes, no retention side effects — the panel's list path owns
// those. The transport read follows the same lock chain as List (store read
// lock → MailStore lock), and a degraded transport read surfaces as an error.
func (s *Store) ReadMail(ctx context.Context, id string) (MailDetail, error) {
	if id == "" {
		return MailDetail{}, errors.New("collab mail detail: empty mail id")
	}
	unlock, _, err := s.lockRead(ctx)
	if err != nil {
		return MailDetail{}, err
	}
	if unlock != nil {
		defer unlock()
	}
	rows, degraded := s.mail.History(ctx)
	for _, row := range rows {
		m := row.Mail
		if m.ID != id {
			continue
		}
		return MailDetail{
			ID:          m.ID,
			From:        m.From,
			FromSession: m.FromSession,
			To:          m.To,
			ToTitle:     m.ToTitle,
			At:          m.At,
			ThreadID:    firstNonEmpty(m.ThreadID, m.ID),
			Bucket:      s.classify(m),
			Channel:     m.Channel,
			RefPath:     feedbackRefOf(m),
			Preview:     previewOf(m.Body),
			Body:        m.Body,
		}, nil
	}
	if degraded {
		// 锁繁忙 / 库不可读：History 返回空行集 ≠ 这封信不存在。报错让弹窗显示
		// 失败态（可重试），绝不渲染成空正文。
		return MailDetail{}, errors.New("collab mail detail: mail store busy (lock contention) — cannot read the full body, retry shortly")
	}
	return MailDetail{}, fmt.Errorf("collab mail detail: mail %q not found", id)
}
