package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/subagentmailbox"
)

// SubagentSendReceiptView is the whitelisted delivery outcome for one
// subagent mailbox send (task 616). Disposition is one of:
//
//	steered  — the running sub-agent admitted the message mid-turn;
//	queued   — the handle was live but admission lost the terminal race; the
//	           message stays pending for a later continue_from;
//	parked   — the sub-agent is not running; the message stays pending;
//	disabled — experimental_subagent_messaging is off (nothing was written).
//
// Not-found and malformed requests surface as errors, matching the other
// subagents_app bindings.
type SubagentSendReceiptView struct {
	MessageID   string `json:"messageId"`
	Disposition string `json:"disposition"`
}

// SendSubagentMessage persists one user message in the target sub-agent's
// mailbox and, when that sub-agent is running in this process, injects it at
// the child's next tool-round gap. The ref must appear in the parent
// session's own artifact list — the same membership proof ReadSubagentSession
// uses — so a traversal-shaped ref can only fail with "does not belong".
//
// The channel deliberately bypasses the controller turn-admission path: the
// parent tab being busy or rotating never blocks a send, and a send never
// queues a parent turn. Delivery order is persist-first: every receipt has
// the message durable on disk.
func (a *App) SendSubagentMessage(sessionPath, ref, summary, text string) (SubagentSendReceiptView, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	ref = strings.TrimSpace(ref)
	summary = strings.TrimSpace(summary)
	text = strings.TrimSpace(text)
	if sessionPath == "" {
		return SubagentSendReceiptView{}, fmt.Errorf("empty session path")
	}
	if ref == "" {
		return SubagentSendReceiptView{}, fmt.Errorf("empty subagent reference")
	}
	if text == "" {
		return SubagentSendReceiptView{}, fmt.Errorf("empty message text")
	}
	// Switch gate first: off means nothing is written and nothing is sent.
	cfg, err := config.Load()
	if err != nil {
		return SubagentSendReceiptView{}, fmt.Errorf("config unavailable: %w", err)
	}
	if !cfg.SubagentMessagingEnabled() {
		return SubagentSendReceiptView{Disposition: subagentmailbox.DispositionDisabled}, nil
	}
	dir, validated, err := a.sessionDirForPath(sessionPath)
	if err != nil {
		return SubagentSendReceiptView{}, err
	}
	// Membership proof: the ref must be listed in this parent's artifacts.
	artifacts, err := agent.ListSubagentsByParent(dir, agent.BranchID(validated))
	if err != nil {
		return SubagentSendReceiptView{}, err
	}
	known := false
	for _, artifact := range artifacts {
		if artifact.Ref == ref {
			known = true
			break
		}
	}
	if !known {
		return SubagentSendReceiptView{}, fmt.Errorf("subagent %q does not belong to this session", ref)
	}
	hub := &subagentmailbox.Hub{Dir: filepath.Join(dir, "subagents")}
	receipt, err := hub.Deliver(ref, subagentmailbox.FromUser, summary, text)
	if err != nil {
		return SubagentSendReceiptView{}, err
	}
	return SubagentSendReceiptView{
		MessageID:   receipt.Entry.ID,
		Disposition: receipt.Disposition,
	}, nil
}

// tl1447ContinueSummary / tl1447ContinueText 是「继续」续轮的固定载荷：发进
// 静默子代理的信箱（活性处置通道，非用户消息面），运行中即 steer 注入运行
// 轮（子代理下个工具间隙收到），已停靠则排队等下次 continue_from 续轮送达。
const (
	tl1447ContinueSummary = "continue"
	// 载荷面向子代理模型（不是 UI 文案）：给一次自救机会——继续推进；确实
	// 卡死就报状态与阻塞点，让调用方拿到可处置的信息。
	tl1447ContinueText = "[heartbeat-continue] Continue the current task from where it stopped. " +
		"If you are genuinely stuck, reply with your current status, the last action, and the exact blocker."
)

// ContinueSubagentForSession（tl1447 缺陷②）向一个子代理发「继续」续轮唤醒。
// 与 SendSubagentMessage 同一条持久先行投递链（persist-first：回执即落盘），
// 但不挂在 experimental_subagent_messaging 开关下——续轮是静默子代理的活性
// 处置入口（先确认后继续由面板两步确认承担），不是可选消息特性；开关关闭
// 只停用户消息面，不停处置面。归属证明与 SendSubagentMessage 同一道闸：
// ref 必须列在本会话 artifact 清单内，traversal 形 ref 只能落在 not-found。
func (a *App) ContinueSubagentForSession(sessionPath, ref string) (SubagentSendReceiptView, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	ref = strings.TrimSpace(ref)
	if sessionPath == "" {
		return SubagentSendReceiptView{}, fmt.Errorf("empty session path")
	}
	if ref == "" {
		return SubagentSendReceiptView{}, fmt.Errorf("empty subagent reference")
	}
	dir, validated, err := a.sessionDirForPath(sessionPath)
	if err != nil {
		return SubagentSendReceiptView{}, err
	}
	artifacts, err := agent.ListSubagentsByParent(dir, agent.BranchID(validated))
	if err != nil {
		return SubagentSendReceiptView{}, err
	}
	known := false
	for _, artifact := range artifacts {
		if artifact.Ref == ref {
			known = true
			break
		}
	}
	if !known {
		return SubagentSendReceiptView{}, fmt.Errorf("subagent %q does not belong to this session", ref)
	}
	hub := &subagentmailbox.Hub{Dir: filepath.Join(dir, "subagents")}
	receipt, err := hub.Deliver(ref, subagentmailbox.FromUser, tl1447ContinueSummary, tl1447ContinueText)
	if err != nil {
		return SubagentSendReceiptView{}, err
	}
	return SubagentSendReceiptView{
		MessageID:   receipt.Entry.ID,
		Disposition: receipt.Disposition,
	}, nil
}
