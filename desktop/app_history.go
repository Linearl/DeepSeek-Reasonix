package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// HistoryMessage is one prior turn, for the frontend to repopulate its transcript
// after a reload.
type HistoryMessage struct {
	CompletionReceipt  *eventwire.CompletionReceipt `json:"completionReceipt,omitempty"`
	CompletionSummary  *eventwire.CompletionSummary `json:"completionSummary,omitempty"`
	TurnID             string                       `json:"turnId,omitempty"`
	Role               string                       `json:"role"`
	Content            string                       `json:"content"`
	Detail             string                       `json:"detail,omitempty"`
	Code               string                       `json:"code,omitempty"`
	SubmitText         string                       `json:"submitText,omitempty"`
	CheckpointTurn     *int                         `json:"checkpointTurn,omitempty"`
	CreatedAt          int64                        `json:"createdAt,omitempty"`
	Reasoning          string                       `json:"reasoning,omitempty"`
	MemoryCitations    []provider.MemoryCitation    `json:"memoryCitations,omitempty"`
	WorkDurationMs     int64                        `json:"workDurationMs,omitempty"`
	Level              string                       `json:"level,omitempty"`
	ToolCalls          []HistoryToolCall            `json:"toolCalls,omitempty"`
	ToolCallID         string                       `json:"toolCallId,omitempty"`
	ToolName           string                       `json:"toolName,omitempty"`
	ToolResultArchived bool                         `json:"toolResultArchived,omitempty"`
	ToolResultError    string                       `json:"toolResultError,omitempty"`
	// Execution is local shell metadata restored onto ToolCards after history
	// reload. Omitted when absent so older frontends ignore it safely.
	Execution        *provider.ToolExecution          `json:"execution,omitempty"`
	Pending          bool                             `json:"pending,omitempty"`
	Trigger          string                           `json:"trigger,omitempty"`
	Messages         int                              `json:"messages,omitempty"`
	Summary          string                           `json:"summary,omitempty"`
	Archive          string                           `json:"archive,omitempty"`
	DecisionReceipt  *provider.DecisionReceipt        `json:"decisionReceipt,omitempty"`
	Readiness        *event.FinalReadiness            `json:"readiness,omitempty"`
	ReadPause        *provider.ReadPause              `json:"readPause,omitempty"`
	ReadCompletion   *provider.ReadCompletion         `json:"readCompletion,omitempty"`
	ProtocolRecovery *provider.ProtocolRecoveryAction `json:"protocolRecovery,omitempty"`
	Diagnostic       *provider.FailureDiagnostic      `json:"diagnostic,omitempty"`
	ServerSearch     []provider.ServerSearchCall      `json:"serverSearch,omitempty"`
}

func interruptedTurnHistoryNotice(recovery *provider.InterruptedTurnRecovery) HistoryMessage {
	if recovery != nil && recovery.TerminalStatus == "failed" {
		diagnostic := recovery.FailureDiagnostic
		message := "The provider request failed. Check the connection settings and try again."
		detail := provider.FailureDiagnosticDetail(diagnostic)
		// Task 340 (upstream #10778): the summary carries the real provider
		// error (status line + scrubbed response-body snippet); without it a
		// reload names only the status class and drops the actual cause.
		if recovery.FailureSummary != "" {
			if detail != "" {
				detail += "\n"
			}
			detail += recovery.FailureSummary
		}
		if diagnostic != nil {
			if statusMessage := i18n.M.ProviderStatusMessage(diagnostic.Status); statusMessage != "" {
				message = statusMessage
			} else if diagnostic.Status > 0 {
				message = fmt.Sprintf("Provider request failed (HTTP %d).", diagnostic.Status)
			}
			label := provider.ProviderDisplayLabel(diagnostic.ProviderID, diagnostic.ProviderDisplayName, diagnostic.Protocol)
			if label != "" {
				message = label + ": " + message
			}
		}
		return HistoryMessage{Role: "notice", Level: "warn", Code: event.NoticeCodeProviderRequestFailed, Content: message, Detail: detail, Diagnostic: diagnostic}
	}
	return HistoryMessage{
		Role: "notice", Level: "info", Code: event.NoticeCodeCancelledTurn,
		Content: "This turn was interrupted. Partial output is kept for reference; only completed tool pairs and a bounded recovery summary enter the next model turn. Inspect the workspace before continuing or reverting changes.",
	}
}

type HistoryToolCall struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Arguments         string `json:"arguments"`
	ResolvedName      string `json:"resolvedName,omitempty"`
	CapabilityID      string `json:"capabilityId,omitempty"`
	ResolvedReadOnly  *bool  `json:"resolvedReadOnly,omitempty"`
	Subject           string `json:"subject,omitempty"`
	Summary           string `json:"summary,omitempty"`
	Diff              string `json:"diff,omitempty"`
	Added             int    `json:"added,omitempty"`
	Removed           int    `json:"removed,omitempty"`
	ArgumentsArchived bool   `json:"argumentsArchived,omitempty"`
}

const (
	defaultHistoryPageTurns = 60
	maxHistoryPageTurns     = 200
)

type HistoryPage struct {
	Messages   []HistoryMessage `json:"messages"`
	StartTurn  int              `json:"startTurn"`
	EndTurn    int              `json:"endTurn"`
	TotalTurns int              `json:"totalTurns"`
	HasOlder   bool             `json:"hasOlder"`
	Revision   int64            `json:"revision,omitempty"`
	Digest     string           `json:"digest,omitempty"`
}

// historyProviderMessagesWithPersistedTimes overlays legacy event-record
// timestamps onto a copy for display. It deliberately leaves the controller's
// provider transcript untouched: timestamp migration must not change session
// digests, conflict detection, or model-request cache prefixes.
func historyProviderMessagesWithPersistedTimes(msgs []provider.Message, sessionPath string) []provider.Message {
	if len(msgs) == 0 || strings.TrimSpace(sessionPath) == "" {
		return msgs
	}
	needsPersistedTime := false
	for _, msg := range msgs {
		if msg.CreatedAt <= 0 && agent.IsUserAuthoredTurnMessage(msg) {
			needsPersistedTime = true
			break
		}
	}
	if !needsPersistedTime {
		return msgs
	}
	tr := beginSilentLoader("history_time_backfill", sessionPath)
	users, err := agent.LoadSessionUserMessages(sessionPath)
	if err != nil {
		tr.finish(err, -1)
		return msgs
	}
	if len(users) == 0 {
		tr.finish(nil, 0)
		return msgs
	}
	tr.finish(nil, len(users))
	out := append([]provider.Message(nil), msgs...)
	userIndex := 0
	for i := range out {
		if out[i].Role != provider.RoleUser || agent.IsPinnedContextRevision(out[i]) {
			continue
		}
		if userIndex >= len(users) {
			break
		}
		user := users[userIndex]
		userIndex++
		if out[i].CreatedAt <= 0 && !user.At.IsZero() {
			out[i].CreatedAt = user.At.UnixMilli()
		}
	}
	return out
}

// History returns the session's message log.
func (a *App) History() []HistoryMessage {
	return a.HistoryForTab("")
}

func (a *App) HistoryPage(beforeTurn, limit int) HistoryPage {
	return a.HistoryPageForTab("", beforeTurn, limit)
}

func (a *App) HistoryPageForTab(tabID string, beforeTurn, limit int) HistoryPage {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	var sessionDir, sessionPath string
	if tab != nil {
		ctrl = tab.Ctrl
		sessionDir = tabSessionDir(tab)
		sessionPath = tab.currentSessionPath()
	}
	a.mu.RUnlock()
	if ctrl == nil {
		if strings.TrimSpace(sessionPath) == "" {
			return HistoryPage{Messages: []HistoryMessage{}}
		}
		page, err := previewSessionPage(sessionDir, sessionPath, beforeTurn, limit)
		if err != nil {
			return HistoryPage{Messages: []HistoryMessage{}}
		}
		return page
	}
	dir := controllerSessionDir(ctrl)
	path := ctrl.SessionPath()
	msgs := ctrl.History()
	status := ctrl.RuntimeStatus()
	idle := !status.Running && !status.PendingPrompt && !ctrl.SessionHasUnsavedChanges()
	// Session-v4 experiment: when idle, prefer the durable v4 projection via
	// Query so history browsing exercises the v4 store. Live turns keep the
	// agent in-memory history until the next Snapshot mirrors into v4.
	if idle && strings.TrimSpace(path) != "" {
		if c, ok := ctrl.(*control.Controller); ok {
			if v4 := c.SessionV4(); v4 != nil {
				if v4Msgs, hit := v4.HistoryMessages(context.Background(), path); hit {
					msgs = v4Msgs
				} else if loaded, ok := silentLoadSessionSnapshot("history_page_v4_fallback", path); ok {
					msgs = loaded
				}
			} else if loaded, ok := silentLoadSessionSnapshot("history_page_v4_fallback", path); ok {
				msgs = loaded
			}
		} else if loaded, ok := silentLoadSessionSnapshot("history_page_v4_fallback", path); ok {
			msgs = loaded
		}
	}
	page := historyPageFromProviderMessages(
		msgs,
		sessionDisplayResolver(dir, path),
		sessionPlannerDisplayTurns(dir, path),
		ctrl.CheckpointTurnsByMessageIndex(),
		beforeTurn,
		limit,
	)
	digest, _ := agent.ContentDigestForMessages(msgs)
	return historyPageWithFingerprint(page, path, digest)
}

func historyPageWithFingerprint(page HistoryPage, sessionPath, contentDigest string) HistoryPage {
	contentDigest = strings.TrimSpace(contentDigest)
	if strings.TrimSpace(sessionPath) == "" || contentDigest == "" {
		return page
	}
	// Digest is derived from the exact full transcript used to build the page.
	// Never copy a newer sidecar digest onto older page content.
	page.Digest = contentDigest
	if meta, ok, err := agent.LoadBranchMeta(sessionPath); err == nil && ok {
		if strings.TrimSpace(meta.ContentDigest) == contentDigest {
			page.Revision = meta.Revision
		}
	}
	return page
}

func normalizeHistoryPageLimit(limit int) int {
	if limit <= 0 {
		return defaultHistoryPageTurns
	}
	if limit > maxHistoryPageTurns {
		return maxHistoryPageTurns
	}
	return limit
}

func historyPageFromMessages(messages []HistoryMessage, beforeTurn, limit int) HistoryPage {
	limit = normalizeHistoryPageLimit(limit)
	totalTurns := 0
	for _, msg := range messages {
		if msg.Role == "user" {
			totalTurns++
		}
	}
	if beforeTurn <= 0 || beforeTurn > totalTurns {
		beforeTurn = totalTurns
	}
	startTurn := max(beforeTurn-limit, 0)
	page := HistoryPage{
		StartTurn:  startTurn,
		EndTurn:    beforeTurn,
		TotalTurns: totalTurns,
		HasOlder:   startTurn > 0,
	}
	if len(messages) == 0 || startTurn >= beforeTurn {
		page.Messages = []HistoryMessage{}
		return page
	}
	page.Messages = historyMessagesForTurnRange(messages, startTurn, beforeTurn)
	return page
}

func historyMessagesForTurnRange(messages []HistoryMessage, startTurn, endTurn int) []HistoryMessage {
	out := make([]HistoryMessage, 0, len(messages))
	turn := -1
	for _, msg := range messages {
		if msg.Role == "user" {
			turn++
		}
		if turn < 0 {
			if startTurn == 0 {
				out = append(out, msg)
			}
			continue
		}
		if turn >= startTurn && turn < endTurn {
			out = append(out, msg)
		}
	}
	return out
}

func (a *App) HistoryForTab(tabID string) []HistoryMessage {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	var sessionDir, sessionPath string
	if tab != nil {
		ctrl = tab.Ctrl
		sessionDir = tabSessionDir(tab)
		sessionPath = tab.currentSessionPath()
	}
	a.mu.RUnlock()
	if ctrl == nil {
		if strings.TrimSpace(sessionPath) == "" {
			return []HistoryMessage{}
		}
		messages, err := previewSessionMessages(sessionDir, sessionPath)
		if err != nil {
			return []HistoryMessage{}
		}
		return messages
	}
	dir := controllerSessionDir(ctrl)
	path := ctrl.SessionPath()
	msgs := historyProviderMessagesWithPersistedTimes(ctrl.History(), path)
	return historyMessagesWithPlannerDisplays(
		msgs,
		sessionDisplayResolver(dir, path),
		sessionPlannerDisplayTurns(dir, path),
		ctrl.CheckpointTurnsByMessageIndex(),
	)
}

func (a *App) HistoryCheckpointTurnsForTab(tabID string) []int {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	if tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return []int{}
	}
	return historyCheckpointTurns(
		ctrl.History(),
		sessionDisplayResolver(controllerSessionDir(ctrl), ctrl.SessionPath()),
		ctrl.CheckpointTurnsByMessageIndex(),
	)
}

var pastedTextDisplayLabelPattern = regexp.MustCompile(`^\[(?:已粘贴文本|已貼上文字|Pasted text) #[0-9]+ · [0-9]+ (?:行|lines)\]$`)

// historyReplayUserContent keeps only user-authored replay data. Provider-facing
// capability, goal, hook, and resolved-reference context must not be resubmitted.
func historyReplayUserContent(content string) string {
	return control.StripReferencedContextPrefix(control.StripComposePrefixes(content))
}

// collapseLegacyExpandedPasteDisplay repairs sessions whose user-authored replay
// source still contains an expanded pasted-text block. This includes transcripts
// written before RawContent existed. The expanded block remains in SubmitText so
// edit replay can still reconstruct the card and recover its full payload.
func collapseLegacyExpandedPasteDisplay(content string) string {
	const beginPrefix = "--- Begin "
	for scan := 0; scan < len(content); {
		beginOffset := strings.Index(content[scan:], beginPrefix)
		if beginOffset < 0 {
			break
		}
		begin := scan + beginOffset
		labelStart := begin + len(beginPrefix)
		labelEndOffset := strings.Index(content[labelStart:], " ---")
		if labelEndOffset < 0 {
			break
		}
		labelEnd := labelStart + labelEndOffset
		label := content[labelStart:labelEnd]
		beginEnd := labelEnd + len(" ---")
		if !pastedTextDisplayLabelPattern.MatchString(label) {
			scan = beginEnd
			continue
		}
		endMarker := "--- End " + label + " ---"
		endOffset := strings.Index(content[beginEnd:], endMarker)
		if endOffset < 0 {
			scan = beginEnd
			continue
		}
		labelCopy := strings.LastIndex(content[:begin], label)
		if labelCopy < 0 || strings.TrimSpace(content[labelCopy+len(label):begin]) != "" {
			scan = beginEnd
			continue
		}
		end := beginEnd + endOffset + len(endMarker)
		content = content[:labelCopy+len(label)] + content[end:]
		scan = labelCopy + len(label)
	}
	return strings.TrimSpace(content)
}

// historyUserDisplayContent prefers a persisted display sidecar when one exists.
// Comparing it with the deterministic fallback distinguishes a sidecar hit
// without changing the resolver API used throughout history pagination.
func historyUserDisplayContent(msg provider.Message, resolveUserContent func(string) string) string {
	resolved := strings.TrimSpace(resolveUserContent(msg.Content))
	fallback := strings.TrimSpace(historyReplayUserContent(msg.Content))
	if resolved != "" && resolved != fallback {
		return resolved
	}
	replaySource := agent.UserMessageText(msg)
	if msg.RawContent == "" {
		replaySource = fallback
	}
	return collapseLegacyExpandedPasteDisplay(replaySource)
}

func historyCheckpointTurns(msgs []provider.Message, resolveUserContent func(string) string, checkpointTurns map[int]int) []int {
	out := make([]int, 0)
	for index, msg := range msgs {
		if !agent.IsUserAuthoredTurnMessage(msg) {
			continue
		}
		turn, ok := checkpointTurns[index]
		if !ok {
			turn = -1
		}
		out = append(out, turn)
	}
	return out
}

func historyMessages(msgs []provider.Message, resolveUserContent func(string) string) []HistoryMessage {
	return historyMessagesWithPlannerDisplays(msgs, resolveUserContent, nil, nil)
}
