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
