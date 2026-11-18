package main

// tl1692: automatic AI session naming. When the user configures
// `agent.title_model` (a canonical provider/model ref), the desktop fires ONE
// parallel bounded title request per session — on the first autosave pass that
// sees a user turn, i.e. while the first answer is still streaming, mirroring
// the torch reference semantics ("ask the title model in parallel the moment
// the first message goes out; the sidebar shows the real name before the
// answer finishes").
//
// Zero-distraction default: with `agent.title_model` unset (the shipped
// default) every gate below closes before anything runs — no goroutine, no
// provider call, no UI change; behaviour is byte-identical to the pre-tl1692
// heuristic-only chain. A manual rename always wins: the manual-title guard
// here mirrors the heuristic chain's, and the result commits through the
// auto-title registry channel (source=auto, never manual).
//
// Non-goals honored: no batch renaming, no remote title service, no prompt
// into the conversation — the request rides the bounded no-tool title face
// (control.GenerateSessionTitle) the manual AI-rename action already uses.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// autoAITitleStage is the proposal stage automatic AI titles commit with. The
// registry's anti-regression gate (shouldApplyAutoTopicTitle) only accepts
// proposals at a stage >= the recorded one, and the heuristic chain uses
// stages 1/3 — so a committed AI name (stage 9) is never silently overwritten
// by a later heuristic proposal of the same session.
const autoAITitleStage = 9

// autoAITitleSeam indirection so tests pin the title generation without a
// provider (the manual AI-rename face runs through the same method).
var autoAITitleGenerate = func(ctx context.Context, ctrl *control.Controller, transcript string) (string, error) {
	return ctrl.GenerateSessionTitle(ctx, transcript)
}

// autoAITitleDone records the sessions the automatic AI naming already ran
// for — one title request per session per process, success or failure (a
// failed request must not retry-storm the title model on every autosave).
// In-process only: after a restart the first pass re-arms once, matching the
// heuristic identity memo's restart semantics.
var (
	autoAITitleMu   sync.Mutex
	autoAITitleSeen = map[string]struct{}{}
)

func autoAITitleAlreadyDone(sessionPath string) bool {
	autoAITitleMu.Lock()
	defer autoAITitleMu.Unlock()
	_, ok := autoAITitleSeen[sessionPath]
	return ok
}

func rememberAutoAITitleDone(sessionPath string) {
	autoAITitleMu.Lock()
	autoAITitleSeen[sessionPath] = struct{}{}
	autoAITitleMu.Unlock()
}

// titleModelConfigured reports whether agent.title_model is set — the single
// on/off switch for the whole automatic naming chain. The value is resolved
// once per process: the switch gates a controller-snapshot-based face (the
// title model itself binds at boot), so a config edit takes effect on restart
// — the same restart semantics as agent.title_model's model resolution — and
// the autosave title path (which runs on every snapshot pass) must not pay a
// config.Load (disk migration included) per pass. A broken config reads as
// OFF (availability first). A var so tests pin the gate without touching the
// user's real config.
var (
	titleModelResolvedOnce sync.Once
	titleModelResolved     bool
	titleModelConfigured   = func() bool {
		titleModelResolvedOnce.Do(func() {
			cfg, err := config.Load()
			if err == nil && cfg != nil {
				titleModelResolved = strings.TrimSpace(cfg.Agent.TitleModel) != ""
			}
		})
		return titleModelResolved
	}
)

// maybeKickAutoAITopicTitle is the fire-and-forget entry the autosave title
// path calls after the heuristic pass. Every gate runs on the caller's
// goroutine (cheap, lock-free sidecar reads); only the actual generation runs
// in parallel, so the autosave loop's pacing is untouched.
func (a *App) maybeKickAutoAITopicTitle(tab *WorkspaceTab, topicID, workspaceRoot, sessionPath string) {
	if tab == nil || strings.TrimSpace(topicID) == "" || strings.TrimSpace(sessionPath) == "" {
		return
	}
	if !titleModelConfigured() {
		return
	}
	if source := loadTopicTitleSource(workspaceRoot, topicID); source != topicTitleSourceAuto {
		return
	}
	if sessionHasManualDisplayTitle(sessionPath) {
		return
	}
	if autoAITitleAlreadyDone(sessionPath) {
		return
	}
	ctrl, ok := tab.Ctrl.(*control.Controller)
	if !ok || ctrl == nil {
		return
	}
	users := topicTitleUserTurnsFromSession(sessionPath)
	if len(users) == 0 {
		return
	}
	transcript := sessionTitleTranscript(users)
	rememberAutoAITitleDone(sessionPath)

	go func() {
		// The bounded title face carries its own 30s deadline; the boot
		// context just scopes the app lifetime.
		title, err := autoAITitleGenerate(a.bootContext(), ctrl, transcript)
		if err != nil {
			// Silent: a failed or empty title is an expected environment
			// condition, never a UI error (the check-face lesson, same shape).
			slog.Info("title: automatic AI naming skipped", "err", err)
			return
		}
		title = strings.TrimSpace(title)
		if title == "" {
			return
		}
		a.commitAutoAITopicTitle(topicID, workspaceRoot, sessionPath, title, transcript, len(users))
	}()
}

// commitAutoAITopicTitle lands the generated name through the auto-title
// registry channel (source=auto, stage 9) and republishes the sidebar exactly
// the way the heuristic commit does. Re-reads the manual guard first: the
// generation ran in parallel, and a rename that landed meanwhile wins.
func (a *App) commitAutoAITopicTitle(topicID, workspaceRoot, sessionPath, title, transcript string, userTurns int) {
	if sessionHasManualDisplayTitle(sessionPath) {
		return
	}
	if meta, ok, err := agent.LoadBranchMeta(sessionPath); err == nil && ok && strings.TrimSpace(meta.CustomTitle) != "" {
		return
	}
	proposal := autoTopicTitleProposal{
		Title:     title,
		Stage:     autoAITitleStage,
		UserTurns: userTurns,
		BasisHash: fmt.Sprintf("ai-%x", titleBasisDigest(transcript)),
	}
	applied, err := applyAutoTopicTitle(workspaceRoot, topicID, title, proposal)
	if err != nil || !applied {
		return
	}
	if topicAutoTitleCommittedHookForTest != nil {
		topicAutoTitleCommittedHookForTest()
	}
	a.updateOpenTopicTitle(topicID, title, topicTitleSourceAuto)
	changedDirs := a.updateTopicSessionTitles(topicID, title)
	if len(changedDirs) > 0 {
		a.emitProjectTreeChangedForSessionDirs(changedDirs...)
	} else {
		a.emitProjectTreeMetadataChanged()
	}
}

// titleBasisDigest is the basis hash input for automatic AI titles: a stable
// digest of the transcript the title was generated from (same transcript =>
// same proposal => the registry's BasisHash gate deduplicates).
func titleBasisDigest(transcript string) []byte {
	sum := sha256.Sum256([]byte("ai-title v1: " + transcript))
	return sum[:8]
}
