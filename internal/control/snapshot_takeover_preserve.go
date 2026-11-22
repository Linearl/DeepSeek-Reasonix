package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1748: fork-line preservation and relay for the rev-lag takeover. The
// takeover (agent.SaveAdoptOverStaleDisk) resolves a dead-writer divergence by
// rewriting the transcript from the live snapshot — which silently destroys
// the disk side's unique messages. The 2026-11-21 22:59 field case made the
// cost concrete: disk 683 msgs vs snapshot 686, decision=rewrite, and a user
// decision message sent at 23:16 into the other version line was never found
// in any transcript, event log, or turn record again.
//
// Three obligations, all funneled through takeoverDivergedRevLag (the single
// seam shared by the mid-turn checkpoint escalation and the turn-boundary
// ladder), none of which change the takeover decision itself:
//
//  1. Observability — what was discarded (id/timestamp/role per message, user
//     messages flagged) and why (revision gap), in desktop.log and in the
//     per-session conflicts.jsonl diagnostic.
//  2. Injection consistency — the abandoned line's real user messages are
//     relayed into the active turn as one origin=host guidance message
//     (HostGeneratedUserMessage + AppendTurnMessage, the canonical write
//     funnel — same channel family as the task-172/530 nudges), so the model
//     never silently misses what the user said on the forked line. The
//     visible-guidance class is deliberate: protocol-classified host messages
//     are dropped by the interrupted-turn rebuild (tl1602), which would
//     re-loose exactly the content this file exists to keep.
//  3. Non-destruction — the abandoned side's unique messages land in full in
//     <stem>.conflict-preserved.jsonl before they can be considered gone; the
//     rewrite never destroys the only copy.
//
// Ordering contract: the disk capture runs BEFORE SaveAdoptOverStaleDisk
// (after the rewrite the bytes are gone), but nothing is persisted, injected,
// or logged unless the takeover actually commits — a declined takeover lost
// nothing, so it must not manufacture preserve files.

const (
	// takeoverPreserveMaxBytes bounds one preservation record. The record
	// holds message content by design (it is the recovery copy); data URLs
	// are stripped separately, so this cap only bites pathological lines.
	takeoverPreserveMaxBytes = 8 << 20
	// takeoverRelayMaxMessages / takeoverRelayMaxTotalChars bound the
	// origin=host relay so a pathological fork line cannot balloon the live
	// context; overflow reads back from the preserve file.
	takeoverRelayMaxMessages = 10
	takeoverRelayMaxPerMsg   = 4000
	takeoverRelayMaxTotal    = 16 << 10
	// takeoverDetailMaxListed bounds the per-message slog list; beyond it the
	// line carries "+N more" and the full list lives in the preserve file.
	takeoverDetailMaxListed = 10
	takeoverDetailMaxChars  = 160
)

// TakeoverRecoveryRelayMarker marks the origin=host message that relays the
// abandoned line's user messages into the live transcript. Recognizable in
// text so a relayed message that comes back as input (replay, quoted text) is
// identifiable, mirroring FeedbackNudgeMarker.
const TakeoverRecoveryRelayMarker = "[forked-line message recovery]"

// takeoverDiskSide is the pre-rewrite capture of the on-disk transcript.
// Capturing outside the save locks is safe: the takeover re-verifies the disk
// fingerprint under the locks and declines when the disk moved, and a capture
// failure only degrades observability — it must never block the takeover.
type takeoverDiskSide struct {
	messages []provider.Message
	failed   string // non-empty = why the capture is empty (log-only)
}

// takeoverPreserveOutcome is what the post-commit settle did with the
// captured side; every field lands in the fork trace / diagnostic record.
type takeoverPreserveOutcome struct {
	diskOnly       int    // captured messages the live line does not contain
	diskOnlyUser   int    // of those, real user-authored ones
	preserved      int    // messages written to the preserve log
	preserveRef    string // bare filename of the preserve log, "" on failure
	preserveErr    string // why preservation failed, "" on success
	injected       bool   // relay message landed in the live transcript
	injectErr      string // why the relay failed, "" on success
	truncated      int    // messages dropped from the preserve record by the byte cap
	relayTruncated int    // messages dropped from the relay by the message/char caps
}

// captureTakeoverDiskSide reads the current on-disk transcript. Best-effort:
// a failure here is recorded and the takeover proceeds unchanged.
func captureTakeoverDiskSide(path string) takeoverDiskSide {
	session, err := agent.LoadSession(path)
	if err != nil {
		return takeoverDiskSide{failed: "load: " + err.Error()}
	}
	if session == nil {
		return takeoverDiskSide{failed: "load returned nil session"}
	}
	return takeoverDiskSide{messages: session.Snapshot()}
}

// takeoverMessageKey is the cross-line identity used to decide which captured
// messages the live transcript already covers. Deliberately content-based,
// NOT message-ID-based: each writer line mints its own IDs (Session.Add), so
// the same sentence on two lines carries two different IDs and an ID key
// would miscount genuinely-covered content as lost. Presence semantics:
// duplicates share a key, which is the right answer to "is this content
// still reachable on the live line?".
func takeoverMessageKey(m provider.Message) string {
	// User-role messages are compared on the user-authored view (RawContent
	// first): a fork-line copy carrying only the wire-truncated Content must
	// still match when the live line holds the full original text.
	text := m.Content
	if m.Role == provider.RoleUser {
		text = agent.UserMessageText(m)
	}
	sum := sha256.Sum256([]byte(text))
	return string(m.Role) + "\x00" + fmt.Sprintf("%x", sum[:16])
}

// takeoverUserMessage reports whether a message is real user-authored input —
// the class the fork-line relay and the preservation counters exist for.
// Host-generated user-role protocol messages are machine noise here.
func takeoverUserMessage(m provider.Message) bool {
	return m.Role == provider.RoleUser &&
		m.Origin != provider.MessageOriginHost &&
		strings.TrimSpace(agent.UserMessageText(m)) != ""
}

// settleTakeoverDiskSide runs after the takeover committed: computes the
// abandoned line's unique messages, preserves them in full, relays the user
// ones into the active turn, and attaches the evidence to the fork trace.
// Called on the takeover's committed path only; errors never fail the
// takeover — they are logged and recorded so the loss is visible.
func (c *Controller) settleTakeoverDiskSide(trace *forkTrace, path, mode string, live *agent.Session, captured takeoverDiskSide, conflict *agent.SessionSnapshotConflictError) takeoverPreserveOutcome {
	out := takeoverPreserveOutcome{}
	if len(captured.messages) == 0 {
		if captured.failed != "" {
			slog.Warn("controller: rev-lag takeover committed; abandoned-line capture failed (preserve/relay skipped)",
				"mode", mode, "session", agent.BranchID(path), "err", captured.failed)
		}
		return out
	}
	liveSet := make(map[string]struct{}, live.Len())
	for _, m := range live.Snapshot() {
		liveSet[takeoverMessageKey(m)] = struct{}{}
	}
	var abandoned []provider.Message
	for _, m := range captured.messages {
		if _, ok := liveSet[takeoverMessageKey(m)]; !ok {
			abandoned = append(abandoned, m)
		}
	}
	out.diskOnly = len(abandoned)
	trace.takeoverDiskOnly = len(abandoned)
	if len(abandoned) == 0 {
		// Superset rewrite: the live line already contains everything the disk
		// had (the 22:59 field shape) — lossless, but say so once.
		slog.Info("controller: rev-lag takeover committed; abandoned disk side fully covered by the live transcript (lossless rewrite)",
			"mode", mode, "session", agent.BranchID(path),
			"base_revision", conflict.BaseRevision, "disk_revision", conflict.DiskRevision,
			"rev_lag", conflict.DiskRevision-conflict.BaseRevision,
			"disk_messages", conflict.ExistingMessages, "snapshot_messages", conflict.SnapshotMessages)
		return out
	}

	users := make([]provider.Message, 0, 2)
	for _, m := range abandoned {
		if takeoverUserMessage(m) {
			users = append(users, m)
		}
	}
	out.diskOnlyUser = len(users)
	trace.takeoverDiskOnlyUser = len(users)

	// ③ Non-destruction: the full unique set lands in the preserve log before
	// anything else can consider it gone.
	out.writePreserveLog(path, mode, captured, abandoned, conflict)
	trace.takeoverPreserved = out.preserved
	trace.takeoverPreserveRef = out.preserveRef
	trace.takeoverPreserveErr = out.preserveErr

	// ② Injection consistency: relay the user-authored messages into the
	// active turn through the canonical write funnel (origin=host guidance —
	// persists, reaches the model, survives interrupted-turn rebuild).
	out.relayAbandonedUsers(c, path, users)
	trace.takeoverInjected = out.injected
	trace.takeoverInjectErr = out.injectErr

	// ① Observability: one decision line carrying what was discarded.
	slog.Warn("controller: rev-lag takeover committed; abandoned disk side had unique messages (preserved + relayed)",
		"mode", mode, "session", agent.BranchID(path),
		"base_revision", conflict.BaseRevision, "disk_revision", conflict.DiskRevision,
		"rev_lag", conflict.DiskRevision-conflict.BaseRevision,
		"disk_messages", conflict.ExistingMessages, "snapshot_messages", conflict.SnapshotMessages,
		"disk_only_messages", out.diskOnly, "disk_only_user_messages", out.diskOnlyUser,
		"disk_only_summary", takeoverDiscardSummary(abandoned),
		"preserved_messages", out.preserved, "preserved_ref", out.preserveRef, "preserved_err", out.preserveErr,
		"injected", out.injected, "inject_err", out.injectErr)
	return out
}

// takeoverDiscardSummary renders the abandoned messages for the decision log:
// one line per message with id/timestamp/role, user messages flagged. Bounded
// to takeoverDetailMaxListed entries; the full list is in the preserve log.
func takeoverDiscardSummary(abandoned []provider.Message) string {
	lines := make([]string, 0, min(len(abandoned), takeoverDetailMaxListed))
	for i, m := range abandoned {
		if i == takeoverDetailMaxListed {
			lines = append(lines, fmt.Sprintf("+%d more (see preserve log)", len(abandoned)-i))
			break
		}
		lines = append(lines, takeoverMessageLogLine(i, m))
	}
	return strings.Join(lines, " | ")
}

func takeoverMessageLogLine(i int, m provider.Message) string {
	role := string(m.Role)
	if takeoverUserMessage(m) {
		role = "USER" // 用户消息单独标注
	}
	at := "-"
	if m.CreatedAt > 0 {
		at = time.UnixMilli(m.CreatedAt).Format("2006-01-02T15:04:05")
	}
	text := agent.UserMessageText(m)
	if m.Role != provider.RoleUser {
		text = m.Content
	}
	if runes := []rune(text); len(runes) > takeoverDetailMaxChars {
		text = string(runes[:takeoverDetailMaxChars]) + "…"
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\n", "\\n"), "|", "\\|")
	return fmt.Sprintf("#%d role=%s id=%s at=%s chars=%d text=%q", i, role, orDash(m.ID), at, len([]rune(text)), text)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// takeoverPreserveRecord is one appended line of <stem>.conflict-preserved.jsonl:
// everything the live line did not contain from the abandoned disk side, in
// full. Image/audio data URLs are stripped (placeholder kept) so a screenshot
// on the fork line cannot write gigabytes.
type takeoverPreserveRecord struct {
	At           time.Time           `json:"at"`
	Reason       string              `json:"reason"`
	Mode         string              `json:"mode"`
	BranchID     string              `json:"branch_id"`
	BaseRevision int64               `json:"base_revision"`
	DiskRevision int64               `json:"disk_revision"`
	DiskMessages int                 `json:"disk_messages"`
	LiveMessages int                 `json:"live_messages"`
	DiskOnlyUser int                 `json:"disk_only_user_messages"`
	Messages     []provider.Message  `json:"messages"`
	Truncated    int                 `json:"truncated_messages,omitempty"`
}

// writePreserveLog appends the preservation record. Failure keeps the takeover
// healthy but is loud: at that moment the relay is the only copy left.
func (o *takeoverPreserveOutcome) writePreserveLog(path, mode string, captured takeoverDiskSide, abandoned []provider.Message, conflict *agent.SessionSnapshotConflictError) {
	logPath := store.SessionConflictPreserveLog(path)
	if logPath == "" {
		o.preserveErr = "empty preserve log path"
		return
	}
	keep := abandoned
	for i := range keep {
		keep[i].Images = strippedMediaRefs(keep[i].Images, "images")
		keep[i].Audio = strippedMediaRefs(keep[i].Audio, "audio")
	}
	rec := takeoverPreserveRecord{
		At:           time.Now(),
		Reason:       forkReasonRevLagTakeover,
		Mode:         mode,
		BranchID:     agent.BranchID(path),
		BaseRevision: conflict.BaseRevision,
		DiskRevision: conflict.DiskRevision,
		DiskMessages: conflict.ExistingMessages,
		LiveMessages: conflict.SnapshotMessages,
		DiskOnlyUser: o.diskOnlyUser,
		Messages:     keep,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		o.preserveErr = "marshal: " + err.Error()
		return
	}
	for len(data) > takeoverPreserveMaxBytes && len(rec.Messages) > 0 {
		// Byte-cap: drop the tail one message at a time; the count stays honest
		// and the head remains the recovery copy. Rare by construction (data
		// URLs are already stripped), so the re-marshal cost is acceptable.
		rec.Messages = rec.Messages[:len(rec.Messages)-1]
		rec.Truncated++
		data, err = json.Marshal(rec)
		if err != nil {
			o.preserveErr = "marshal: " + err.Error()
			return
		}
	}
	o.truncated = rec.Truncated
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		o.preserveErr = "mkdir: " + err.Error()
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		o.preserveErr = "open: " + err.Error()
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		o.preserveErr = "write: " + err.Error()
		return
	}
	o.preserved = len(rec.Messages)
	o.preserveRef = filepath.Base(logPath)
}

func strippedMediaRefs(refs []string, kind string) []string {
	if len(refs) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("<%d %s data URL(s) stripped, tl1748>", len(refs), kind)}
}

// relayAbandonedUsers injects one origin=host guidance message carrying the
// abandoned line's user messages into the live transcript, through the same
// canonical funnel the nudges use (persists; reaches the model; survives the
// interrupted-turn rebuild because it is NOT protocol-classified).
func (o *takeoverPreserveOutcome) relayAbandonedUsers(c *Controller, path string, users []provider.Message) {
	if len(users) == 0 {
		return
	}
	if c == nil || c.executor == nil {
		o.injectErr = "no executor"
		return
	}
	msg := agent.HostGeneratedUserMessage(o.renderRelay(users))
	c.executor.AppendTurnMessage(context.Background(), msg)
	o.injected = true
}

// renderRelay builds the relay text: full user-authored content (bounded),
// provenance header, and a pointer to the preserve log for the overflow and
// the non-user remainder of the abandoned line.
func (o *takeoverPreserveOutcome) renderRelay(users []provider.Message) string {
	var b strings.Builder
	b.WriteString(TakeoverRecoveryRelayMarker)
	b.WriteString(" The session file had diverged and the live transcript took over. ")
	b.WriteString(strconv.Itoa(len(users)))
	b.WriteString(" user message(s) from the abandoned version line are relayed below so they are not lost — treat them as the user's own words:\n")
	total := 0
	kept := 0
	for i, m := range users {
		if kept == takeoverRelayMaxMessages || total >= takeoverRelayMaxTotal {
			o.relayTruncated = len(users) - kept
			b.WriteString(fmt.Sprintf("…%d more user message(s) omitted; full abandoned-line transcript is in %s\n",
				o.relayTruncated, orDash(o.preserveRef)))
			break
		}
		text := agent.UserMessageText(m)
		if runes := []rune(text); len(runes) > takeoverRelayMaxPerMsg {
			text = string(runes[:takeoverRelayMaxPerMsg]) + "…(truncated, full text in " + orDash(o.preserveRef) + ")"
		}
		entry := fmt.Sprintf("%d. %s\n", i+1, takeoverRelayStamped(m, text))
		total += len(entry)
		kept++
		b.WriteString(entry)
	}
	if o.preserveRef != "" {
		b.WriteString("Full abandoned-line transcript preserved in " + o.preserveRef + ".\n")
	}
	if o.preserveRef == "" && o.preserveErr != "" {
		b.WriteString("WARNING: the preserve log could not be written (" + o.preserveErr + "); this relay is the only copy.\n")
	}
	return b.String()
}

// takeoverRelayStamped renders one relayed user message with its best-known
// timestamp so the model can place it in time.
func takeoverRelayStamped(m provider.Message, text string) string {
	stamp := "time unknown"
	if m.CreatedAt > 0 {
		stamp = time.UnixMilli(m.CreatedAt).Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("[sent at %s] %s", stamp, text)
}
