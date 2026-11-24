package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
)

// tl1742: passive side-channel capture of provider content_filter rejections.
// When the recovery ladder arms (run_loop.go isKnownProviderRejection branch),
// the host records the rejected round's full context to
// <stateRoot>/rejection-cases/ so field rejections can later be batch-analyzed
// (topic/language/quotation-density clustering, and cross-checking which
// rewrite rounds passed — "same content, different shape" = shape sensitivity).
//
// Non-goals (task book tl1742): no local filter simulation, no change to the
// recovery-ladder behavior, no live alerting. The capture is best-effort and
// silent: every failure is swallowed with a system log line and never blocks
// or perturbs the ladder (guarded by TestProviderRejectionCaptureFailureDoesNotBlockLadder).
//
// Privacy: a case file contains real conversation content (the trigger user
// message, context previews, the rejected/rewritten replies). Files land in
// the user's own state root (never inside a repo, never synced), with 0600
// file mode and a 0700 directory; the file name carries only a timestamp and
// a short transport-session tag, no content-derived words.

const (
	// rejectionCaseSchemaVersion bumps whenever the JSON shape changes so the
	// batch-analysis script can skip files it cannot parse.
	rejectionCaseSchemaVersion = 1
	// Preview / truncation budgets: the file is for human analysis, not a
	// transcript replica. A-segment history is previews; B is a fixed short
	// template sentence by construction; C is a fixed host instruction; only
	// D (the rewritten reply) can be long, so it alone gets a generous cap.
	rejectionCasePreviewChars     = 400
	rejectionCaseTriggerChars     = 2000
	rejectionCaseReplyChars       = 4000
	rejectionCaseRecentMessages   = 12
	rejectionCaseOutcomePending   = ""
	rejectionCaseOutcomeRewrite   = "rewrite_passed"
	rejectionCaseOutcomeDegrade   = "degrade_passed"
	rejectionCaseOutcomeExhausted = "exhausted"
	rejectionCaseOutcomeReset     = "streak_reset_by_toolwork"
)

// rejectionCaseRound is one rejected round: B (the rejected reply) plus the C
// instruction the host injected in response, with the finish_reason the
// provider reported for that round.
type rejectionCaseRound struct {
	Stage              int    `json:"stage"`
	RejectedReply      string `json:"rejected_reply"`
	RewriteInstruction string `json:"rewrite_instruction"`
	FinishReason       string `json:"finish_reason"`
	RejectedAt         string `json:"rejected_at"`
}

// rejectionCaseMessage is one A-segment history entry: role, origin, length,
// and a bounded preview. Host-generated protocol messages keep their origin
// tag so analysis can tell real user input from injected ladder instructions.
type rejectionCaseMessage struct {
	Role    string `json:"role"`
	Origin  string `json:"origin,omitempty"`
	Length  int    `json:"length"`
	Preview string `json:"preview"`
}

// rejectionCaseContext is the A segment: a bounded digest of the conversation
// the provider rejected against. The full canonical list is summarized (count
// + per-message previews) instead of copied verbatim; the last real user
// message (the trigger) is preserved with a wider budget because it is the
// single most likely rejection cause.
type rejectionCaseContext struct {
	MessageCount       int                    `json:"message_count"`
	VisibleCharsTotal  int                    `json:"visible_chars_total"`
	RecentMessages     []rejectionCaseMessage `json:"recent_messages"`
	TriggerUserMessage string                 `json:"trigger_user_message"`
}

// rejectionCaseCapture is the pending case while the ladder is mid-flight:
// armed at rejection #1, extended at rejection #2, closed either by the first
// non-rejection final (D segment) or by the exhausted error. It lives on the
// turnRuntime, so a fresh Run starts with no pending case.
type rejectionCaseCapture struct {
	Meta               rejectionCaseMeta    `json:"-"`
	Context            rejectionCaseContext `json:"context_before"`
	Rounds             []rejectionCaseRound `json:"rounds"`
	Outcome            string               `json:"outcome"`
	RewrittenReply     string               `json:"rewritten_reply,omitempty"`
	RewrittenTruncated bool                 `json:"rewritten_reply_truncated,omitempty"`
}

// rejectionCaseMeta is metadata split out of the JSON marshal: the file body
// gets flattened fields, this struct only carries them through the runtime.
type rejectionCaseMeta struct {
	Provider      string
	ModelRef      string
	SessionRef    string
	WorkspaceRoot string
	CreatedAt     string
	// FileStamp is the collision-avoiding file-name fragment derived from
	// CreatedAt (millisecond precision), kept separate so the JSON timestamp
	// stays standard RFC3339.
	FileStamp    string
	FinishReason string
}

// rejectionCaseFile is the on-disk JSON shape (flattened meta + capture).
type rejectionCaseFile struct {
	SchemaVersion      int                  `json:"schema_version"`
	CreatedAt          string               `json:"created_at"`
	CollectedAt        string               `json:"collected_at"`
	SessionRef         string               `json:"session_ref"`
	Provider           string               `json:"provider"`
	ModelRef           string               `json:"model_ref"`
	WorkspaceRoot      string               `json:"workspace_root"`
	Outcome            string               `json:"outcome"`
	Rounds             []rejectionCaseRound `json:"rounds"`
	Context            rejectionCaseContext `json:"context_before"`
	RewrittenReply     string               `json:"rewritten_reply,omitempty"`
	RewrittenTruncated bool                 `json:"rewritten_reply_truncated,omitempty"`
}

// rejectionCasesDirOverride pins the capture directory (tests only). Empty
// defers to the live state root, mirroring the feedback-inbox override
// pattern (feedback_mail.go).
var rejectionCasesDirOverride string

// SetRejectionCasesDir overrides the rejection-cases directory (tests only).
func SetRejectionCasesDir(dir string) {
	rejectionCasesDirOverride = dir
}

// rejectionCasesDir resolves the capture directory: the state root's
// rejection-cases subdir in production, the override in tests. The state root
// may legitimately be empty in bare test Agents, so the fallback chain ends at
// the OS config dir rather than panicking.
func (a *Agent) rejectionCasesDir() string {
	if rejectionCasesDirOverride != "" {
		return rejectionCasesDirOverride
	}
	root := a.stateRoot()
	if root == "" {
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
			root = filepath.Join(cfg, "reasonix")
		} else {
			root = os.TempDir()
		}
	}
	return filepath.Join(root, "rejection-cases")
}

// captureProviderRejection arms or extends the pending case at a rejection
// hit. Called right after state.terminal.rejectedTemplateBlocks++ in the
// isKnownProviderRejection branch; blocks>max is the exhausted terminal, whose
// close-out is handled by closeRejectionCaptureExhausted.
func (a *Agent) captureProviderRejection(state *turnRuntime, rejectedText string, usage *provider.Usage) {
	if state == nil {
		return
	}
	blocks := state.terminal.rejectedTemplateBlocks
	if blocks > maxRejectedRecoveryRounds {
		return
	}
	if blocks == 1 {
		// A tool round resets the streak counter (run_loop.go handleToolRound)
		// without closing a pending case; if one is still armed here, the old
		// streak ended through tool work — finalize it before opening the new
		// one so both cases survive.
		a.finalizeRejectionCapture(state, rejectionCaseOutcomeReset)
		state.rejectionCapture = a.newRejectionCaseCapture(rejectedText, usage, 1, providerRejectionRewriteMessage())
		return
	}
	// blocks == 2: same streak, second rung. The stage-1 rewrite was rejected
	// too — append the second round with the degrade instruction as its C.
	if state.rejectionCapture == nil {
		// Defensive: the capture should exist whenever blocks==2, but a nil
		// here must never wedge the ladder — rebuild it as a full 2-round case.
		state.rejectionCapture = a.newRejectionCaseCapture("", nil, 2, "")
	}
	state.rejectionCapture.appendRound(rejectedText, providerRejectionDegradeMessage(), 2, usage)
}

// newRejectionCaseCapture builds the pending case at rejection #1: metadata,
// the A-segment context digest, and round 1 (B + C).
func (a *Agent) newRejectionCaseCapture(rejectedText string, usage *provider.Usage, stage int, instruction string) *rejectionCaseCapture {
	now := time.Now().UTC()
	rc := &rejectionCaseCapture{
		Meta: rejectionCaseMeta{
			Provider:      a.currentProviderName(),
			ModelRef:      strings.TrimSpace(a.modelRef),
			SessionRef:    a.captureSessionRef(),
			WorkspaceRoot: a.workspaceRoot(),
			CreatedAt:     now.Format(time.RFC3339),
			FileStamp:     now.Format("20060102-150405.000"),
			FinishReason:  usageFinishReason(usage),
		},
		Context: a.captureContextDigest(),
	}
	rc.appendRound(rejectedText, instruction, stage, usage)
	return rc
}

// appendRound adds one rejected round (B + C + finish_reason) to the case.
func (c *rejectionCaseCapture) appendRound(rejectedText, instruction string, stage int, usage *provider.Usage) {
	c.Rounds = append(c.Rounds, rejectionCaseRound{
		Stage:              stage,
		RejectedReply:      strings.TrimSpace(rejectedText),
		RewriteInstruction: instruction,
		FinishReason:       usageFinishReason(usage),
		RejectedAt:         time.Now().UTC().Format(time.RFC3339),
	})
}

// closeOnRecoveredFinal records the D segment: the first final answer that is
// NOT a rejection after the ladder armed. Outcome follows the last injected
// stage (rewrite passed vs degrade passed).
func (c *rejectionCaseCapture) closeOnRecoveredFinal(text string) {
	truncated := len(text) > rejectionCaseReplyChars
	if truncated {
		text = text[:rejectionCaseReplyChars]
	}
	c.RewrittenReply = text
	c.RewrittenTruncated = truncated
	if len(c.Rounds) > 0 && c.Rounds[len(c.Rounds)-1].Stage >= 2 {
		c.Outcome = rejectionCaseOutcomeDegrade
	} else {
		c.Outcome = rejectionCaseOutcomeRewrite
	}
}

// closeRejectionCaptureOnRecoveredFinal is the run-loop hook: called on every
// final that did NOT match the rejection template while a case is pending, it
// completes the D segment and flushes the file.
func (a *Agent) closeRejectionCaptureOnRecoveredFinal(state *turnRuntime, text string) {
	if state == nil || state.rejectionCapture == nil {
		return
	}
	cap := state.rejectionCapture
	cap.closeOnRecoveredFinal(text)
	a.finalizeRejectionCapture(state, cap.Outcome)
	state.rejectionCapture = nil
}

// closeRejectionCaptureExhausted flushes the pending case when the ladder
// caps out (third rejection): the run ends as an error either way, but the
// case file must still land so the failed ladder is analyzable.
func (a *Agent) closeRejectionCaptureExhausted(state *turnRuntime) {
	if state == nil || state.rejectionCapture == nil {
		return
	}
	state.rejectionCapture.Outcome = rejectionCaseOutcomeExhausted
	a.finalizeRejectionCapture(state, rejectionCaseOutcomeExhausted)
	state.rejectionCapture = nil
}

// finalizeRejectionCapture serializes the pending case and writes it to disk.
// outcome overrides the capture's own outcome for the close-out paths that
// end without a recovered final (exhausted / streak reset). Best-effort: any
// error is logged and dropped — never propagated to the ladder.
func (a *Agent) finalizeRejectionCapture(state *turnRuntime, outcome string) {
	if state == nil || state.rejectionCapture == nil {
		return
	}
	rc := state.rejectionCapture
	if rc.Outcome == rejectionCaseOutcomePending || outcome != rc.Outcome {
		rc.Outcome = outcome
	}
	defer func() { state.rejectionCapture = nil }()

	file := rejectionCaseFile{
		SchemaVersion:      rejectionCaseSchemaVersion,
		CreatedAt:          rc.Meta.CreatedAt,
		CollectedAt:        time.Now().UTC().Format(time.RFC3339),
		SessionRef:         rc.Meta.SessionRef,
		Provider:           rc.Meta.Provider,
		ModelRef:           rc.Meta.ModelRef,
		WorkspaceRoot:      rc.Meta.WorkspaceRoot,
		Outcome:            rc.Outcome,
		Rounds:             rc.Rounds,
		Context:            rc.Context,
		RewrittenReply:     rc.RewrittenReply,
		RewrittenTruncated: rc.RewrittenTruncated,
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		slog.Warn("agent: rejection-case capture marshal failed", "err", err)
		return
	}
	dir := a.rejectionCasesDir()
	name := fmt.Sprintf("rej-%s-%s.json", rc.Meta.FileStamp, shortHash8(rc.Meta.SessionRef))
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("agent: rejection-case capture mkdir failed", "dir", dir, "err", err)
		return
	}
	if err := fileutil.AtomicWriteFile(path, raw, 0o600); err != nil {
		slog.Warn("agent: rejection-case capture write failed", "path", path, "err", err)
	}
}

// captureContextDigest builds the A segment from the canonical conversation
// snapshot: bounded per-message previews plus the widened trigger user
// message. Never panics on a nil session — a missing digest must not wedge
// the ladder either.
func (a *Agent) captureContextDigest() rejectionCaseContext {
	digest := rejectionCaseContext{RecentMessages: []rejectionCaseMessage{}}
	if a == nil || a.sess.conversation == nil {
		return digest
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	digest.MessageCount = len(msgs)
	for _, m := range msgs {
		digest.VisibleCharsTotal += len([]rune(m.Content))
	}
	// Keep only the trailing window; system entries still count toward the
	// totals above but their preview budget stays the same (the system prompt
	// is boilerplate, not a rejection trigger).
	if len(msgs) > rejectionCaseRecentMessages {
		msgs = msgs[len(msgs)-rejectionCaseRecentMessages:]
	}
	for _, m := range msgs {
		digest.RecentMessages = append(digest.RecentMessages, rejectionCaseMessage{
			Role:    string(m.Role),
			Origin:  string(m.Origin),
			Length:  len([]rune(m.Content)),
			Preview: truncateRunes(m.Content, rejectionCasePreviewChars),
		})
	}
	// The trigger: the last message that originated from the real user (host
	// guidance messages carry Origin=host and are excluded by design — the
	// ladder's own instructions must never masquerade as the trigger).
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == provider.RoleUser && m.Origin == provider.MessageOriginUser {
			digest.TriggerUserMessage = truncateRunes(m.Content, rejectionCaseTriggerChars)
			break
		}
	}
	return digest
}

// captureSessionRef is the short transport-session tag for the file name and
// metadata. The agent layer has no user-facing session id; the cache session
// id is the stable per-conversation transport identity it does have.
func (a *Agent) captureSessionRef() string {
	if a == nil || a.sess.conversation == nil || a.sess.conversation.cacheSessionID == "" {
		return "nosession"
	}
	return a.sess.conversation.cacheSessionID
}

func (a *Agent) currentProviderName() string {
	if dest := a.providerForRequest(); dest != nil {
		return dest.Name()
	}
	return ""
}

func usageFinishReason(usage *provider.Usage) string {
	if usage == nil {
		return ""
	}
	return usage.FinishReason
}

// truncateRunes cuts s to at most n runes, avoiding a mid-rune split.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
