package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/provider"
)

// pendingHandoffFile is the host-written handoff a loop guard leaves behind.
// It sits next to the session transcript: the guard stops a turn in which every
// legitimate call is blocked, and without this file the model's latest text
// would exist only inside a transcript a crash could still truncate (task 171).
// The name is deliberately not a .jsonl, so no session scanner adopts it.
const pendingHandoffFile = "pending-handoff.md"

// pendingHandoffMaxBytes bounds how much model text the handoff carries.
const pendingHandoffMaxBytes = 32 << 10

// writePendingHandoff persists the run's latest assistant text before a loop
// guard stops the turn. Best effort on purpose: a failed handoff must never
// block the guard that is trying to end a deadlock.
func (a *Agent) writePendingHandoff(constraintSurface bool, constraintSig string) {
	if a == nil || a.turn.handoffWritten {
		return
	}
	path := strings.TrimSpace(a.SessionPath())
	if path == "" {
		return
	}
	text := strings.TrimSpace(a.latestAssistantText())
	if text == "" {
		return
	}
	if len(text) > pendingHandoffMaxBytes {
		text = text[:pendingHandoffMaxBytes] + "\n\n[truncated by the host]"
	}
	reason := "a loop guard fired"
	if constraintSurface && constraintSig != "" {
		reason = fmt.Sprintf("every call was refused by the same host constraint (%s)", constraintSig)
	}
	body := fmt.Sprintf(
		"# Pending handoff\n\n_Written by the host when %s. The session transcript is authoritative; this file is safe to delete._\n\n## Latest model output\n\n%s\n",
		reason, text)
	target := filepath.Join(filepath.Dir(path), pendingHandoffFile)
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		slog.Warn("agent: pending handoff write failed", "path", target, "err", err)
		return
	}
	a.turn.handoffWritten = true
	slog.Info("agent: pending handoff written",
		"path", target, "reason", reason, "at", time.Now().UTC().Format(time.RFC3339))
}

// latestAssistantText returns the newest assistant text in the transcript, so
// the handoff carries what the run had actually established.
func (a *Agent) latestAssistantText() string {
	if a == nil {
		return ""
	}
	session := a.Session()
	if session == nil {
		return ""
	}
	msgs, _ := session.snapshotMessagesVersion()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != provider.RoleAssistant {
			continue
		}
		if text := strings.TrimSpace(msgs[i].Content); text != "" {
			return text
		}
	}
	return ""
}
