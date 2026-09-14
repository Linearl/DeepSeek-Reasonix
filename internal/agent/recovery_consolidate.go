package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/store"
)

// Recovery-copy consolidation: the manual entry point behind the desktop
// "merge recovery copies" session action. Long sessions touched by a stale
// runtime can fork repeatedly, leaving several *-recovery-* transcripts whose
// identity drifts from the main file — the root cause behind the permanent
// "earlier conversation could not be loaded" failures (#9468/#9470).
//
// Consolidation picks the fullest loadable copy as the canonical transcript,
// swaps it into the main identity (the previous main is archived whole under
// the recoverable .trash), and folds every fully covered loser into the same
// trash. Copies that still hold unique content are preserved untouched and
// reported, so the operation can never destroy the only copy of a turn.

var (
	// ErrNoRecoveryBranches means the session has no recovery copies to merge.
	ErrNoRecoveryBranches = errors.New("session has no recovery copies to consolidate")
	// ErrSessionLeaseHeldForConsolidation means a live runtime still holds the
	// session; consolidation needs the transcript at rest.
	ErrSessionLeaseHeldForConsolidation = errors.New("session is open in a running runtime; close it before consolidating recovery copies")
	// ErrMainNotCoveredByWinner means the fullest copy does not contain the
	// whole current main transcript. Swapping anyway would push main-only
	// turns into the trash archive, so the caller is asked to decide first.
	ErrMainNotCoveredByWinner = errors.New("fullest recovery copy does not contain the current main transcript; refusing the swap to avoid data loss")
)

// RecoveryBranchCandidate is one member of a session's recovery lineage as
// seen by a cold, conservative load.
type RecoveryBranchCandidate struct {
	Path         string
	MessageCount int
	Turns        int
	Revision     int64
	UpdatedAt    time.Time
	Loadable     bool
	IsMain       bool
}

// ConsolidationReport summarizes one consolidation run for the UI.
//
// The json tags are not decoration: this struct crosses the Wails bridge, and the
// frontend reads it by lowerCamelCase name (`report.blockedByDivergence`,
// `report.mainMessageCount`). Without tags Go emits the Go field names
// ("BlockedByDivergence"), every frontend read yields undefined, and the two
// consequences are severe rather than cosmetic - a divergence-blocked merge looks
// like a successful one, and the confirmation that would let the user choose a
// winner never appears.
type ConsolidationReport struct {
	MainPath       string `json:"mainPath"`
	WinnerPath     string `json:"winnerPath"` // "" when the main transcript already was the winner
	Promoted       bool   `json:"promoted"`
	NormalizedMain bool   `json:"normalizedMain"` // an older-format main was rewritten in place first
	// BlockedByDivergence reports that the fullest copy and the main
	// transcript each hold turns the other lacks (typical after a main-side
	// compaction). Nothing was merged; the caller may retry with Force.
	BlockedByDivergence bool     `json:"blockedByDivergence"`
	MainMessageCount    int      `json:"mainMessageCount"`
	WinnerMessageCount  int      `json:"winnerMessageCount"`
	Trashed             []string `json:"trashed"`
	SkippedNotCovered   []string `json:"skippedNotCovered"`
	// NotCoveredDetail explains each entry of SkippedNotCovered: the copy`s own
	// event count and how much of it the canonical transcript already holds. A copy
	// whose Unique is small is duplication; one with a large Unique is work the user
	// would lose by ignoring it, and the UI has to say which is which.
	NotCoveredDetail  []CopyOverlapDetail `json:"notCoveredDetail"`
	SkippedUnloadable []string            `json:"skippedUnloadable"`
	// Prefixed counts the losing chain's turns grafted onto the new main because
	// the winner did not cover them (task 90). ForkIndex is where the losing
	// chain rejoined, so the UI can say which part was kept.
	Prefixed  int `json:"prefixed,omitempty"`
	ForkIndex int `json:"forkIndex,omitempty"`
}

// CopyOverlapDetail is one skipped copy, described by how much of it is already
// present and how much is its own.
type CopyOverlapDetail struct {
	Path   string `json:"path"`
	Shared int    `json:"shared"`
	Unique int    `json:"unique"`
	// Reason is why this copy could not be archived (lease held, parent guard
	// refused, ...). Empty means the copy simply was not covered by the winner.
	Reason string `json:"reason,omitempty"`
}

// validateConsolidationTarget rejects paths that cannot be a consolidation
// target: non-transcripts, event logs, and recovery copies themselves.
func validateConsolidationTarget(mainPath string) error {
	mainPath = filepath.Clean(strings.TrimSpace(mainPath))
	if !strings.HasSuffix(mainPath, ".jsonl") ||
		strings.HasSuffix(mainPath, ".events.jsonl") ||
		strings.HasSuffix(mainPath, ".conflicts.jsonl") {
		return fmt.Errorf("consolidation targets a session transcript, got %s", mainPath)
	}
	if strings.Contains(filepath.Base(mainPath), "-recovery-") {
		return fmt.Errorf("consolidation targets the main transcript, not a recovery copy: %s", mainPath)
	}
	return nil
}

// normalizeTranscriptInPlaceIfDirty rewrites an older-format transcript in
// place with its normalized view — exactly the rewrite the session's next
// successful save would perform. Returns whether a rewrite happened. Load
// failures other than "needs normalization" are returned untouched.
func normalizeTranscriptInPlaceIfDirty(path string) (bool, error) {
	s, err := LoadSession(path)
	if err != nil || s == nil {
		return false, err
	}
	if !s.normalizedDirty {
		return false, nil
	}
	if err := s.SaveRewrite(path); err != nil {
		return false, err
	}
	return true, nil
}

// recoveryCopiesForMain lists the recovery copies that belong to mainPath by
// the stable <stem>-recovery-* naming convention in the same directory.
func recoveryCopiesForMain(mainPath string) ([]string, error) {
	dir := filepath.Dir(mainPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	stem := strings.TrimSuffix(filepath.Base(mainPath), ".jsonl")
	prefix := stem + "-recovery-"
	var out []string
	for _, e := range entries {
		// Sidecars share the transcript stem, so a copy's turns/events files
		// carry the same -recovery- prefix and would otherwise be enumerated as
		// copies of the copy: fake chain rows, previews that fail to load, and
		// promotes that die on "meta is missing". Only the transcript itself is
		// a copy.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") ||
			strings.HasSuffix(e.Name(), ".events.jsonl") ||
			strings.HasSuffix(e.Name(), ".turns.jsonl") ||
			strings.HasSuffix(e.Name(), ".conflicts.jsonl") {
			continue
		}
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !IsVisibleSession(path) {
			continue
		}
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

// recoveryConsolidationCandidate cold-loads one transcript for lineage
// analysis. Damaged or unnormalizable files are reported as unloadable rather
// than guessed at.
func recoveryConsolidationCandidate(path string, isMain bool) (RecoveryBranchCandidate, bool) {
	snap, ok := LoadSessionContentSnapshot(path)
	if !ok {
		return RecoveryBranchCandidate{Path: path, IsMain: isMain, Loadable: false}, false
	}
	meta, _, err := LoadBranchMeta(path)
	if err != nil {
		meta = BranchMeta{}
	}
	turns := 0
	for _, msg := range snap.messages {
		if IsUserAuthoredTurnMessage(msg) {
			turns++
		}
	}
	return RecoveryBranchCandidate{
		Path:         path,
		MessageCount: snap.Len(),
		Turns:        turns,
		Revision:     meta.Revision,
		UpdatedAt:    meta.UpdatedAt,
		Loadable:     true,
		IsMain:       isMain,
	}, true
}

// consolidationCandidateBeats reports whether a is a better canonical than b:
// most messages first, then the highest revision, then the newest update.
// stillUnloadable keeps only the unloadable copies the sweep did not archive,
// so the report lists what genuinely needs attention instead of entries the
// user already sent to the recoverable trash.
func stillUnloadable(unloadable, trashed []string) []string {
	if len(unloadable) == 0 {
		return unloadable
	}
	set := make(map[string]struct{}, len(trashed))
	for _, path := range trashed {
		set[path] = struct{}{}
	}
	out := unloadable[:0]
	for _, path := range unloadable {
		if _, ok := set[path]; !ok {
			out = append(out, path)
		}
	}
	return out
}

func consolidationCandidateBeats(a, b RecoveryBranchCandidate) bool {
	if a.Turns != b.Turns {
		return a.Turns > b.Turns
	}
	if a.MessageCount != b.MessageCount {
		return a.MessageCount > b.MessageCount
	}
	if a.Revision != b.Revision {
		return a.Revision > b.Revision
	}
	return a.UpdatedAt.After(b.UpdatedAt)
}

// ListSessionRecoveryBranches enumerates the recovery copies of a session
// with their cold-load stats. UI callers use it for pre-flight checks; it
// never mutates anything.
func ListSessionRecoveryBranches(mainPath string) ([]RecoveryBranchCandidate, error) {
	mainPath = filepath.Clean(strings.TrimSpace(mainPath))
	if err := validateConsolidationTarget(mainPath); err != nil {
		return nil, err
	}
	copies, err := recoveryCopiesForMain(mainPath)
	if err != nil {
		return nil, err
	}
	out := make([]RecoveryBranchCandidate, 0, len(copies)+1)
	if main, ok := recoveryConsolidationCandidate(mainPath, true); ok {
		out = append(out, main)
	} else {
		out = append(out, RecoveryBranchCandidate{Path: mainPath, IsMain: true, Loadable: false})
	}
	for _, copy := range copies {
		if cand, ok := recoveryConsolidationCandidate(copy, false); ok {
			out = append(out, cand)
		} else {
			out = append(out, RecoveryBranchCandidate{Path: copy, IsMain: false, Loadable: false})
		}
	}
	return out, nil
}

// ConsolidateOptions tunes one consolidation run.
type ConsolidateOptions struct {
	// Force lets a winner that does NOT cover the current main transcript
	// still be promoted. The previous main is archived whole under the
	// recoverable .trash, so the user can always roll back; nothing is
	// hard-deleted. Meant for an explicit user confirmation after the
	// engine reported BlockedByDivergence.
	Force bool
	// ArchiveLeftovers also archives the copies the winner does not cover, which
	// a forced swap routinely leaves behind. Their unique turns are what the user
	// chose against by picking a winner, and the archive is recoverable.
	ArchiveLeftovers bool
	// WinnerPath lets the user pick which chain wins instead of letting the
	// engine take the fullest one. The path must be one of this session's
	// loadable candidates (the main itself or a recovery copy). Naming a winner
	// is an explicit user judgement made against the chain list - message counts
	// and previews were visible - so it also carries the Force semantics: a
	// picked winner that does not cover the current main is still promoted, with
	// the previous main archived whole under the recoverable .trash.
	WinnerPath string
}

// ConsolidateSessionRecoveryBranches merges the recovery copies of mainPath
// with default (conservative) options.
func ConsolidateSessionRecoveryBranches(mainPath string) (ConsolidationReport, error) {
	return ConsolidateSessionRecoveryBranchesWithOptions(mainPath, ConsolidateOptions{})
}

// ConsolidateSessionRecoveryBranchesWithOptions merges the recovery copies of
// mainPath: the fullest loadable copy becomes the canonical main transcript,
// the previous main is archived whole under the recoverable .trash, and every
// copy fully covered by the winner is folded into the same trash. Copies with
// unique content are preserved and reported. Nothing is ever hard-deleted.
func ConsolidateSessionRecoveryBranchesWithOptions(mainPath string, opts ConsolidateOptions) (ConsolidationReport, error) {
	mainPath = filepath.Clean(strings.TrimSpace(mainPath))
	report := ConsolidationReport{MainPath: mainPath}
	if err := validateConsolidationTarget(mainPath); err != nil {
		return report, err
	}
	if SessionLeaseHeld(mainPath) {
		return report, ErrSessionLeaseHeldForConsolidation
	}
	copies, err := recoveryCopiesForMain(mainPath)
	if err != nil {
		return report, err
	}
	if len(copies) == 0 {
		return report, ErrNoRecoveryBranches
	}

	mainCand, mainOK := recoveryConsolidationCandidate(mainPath, true)
	if !mainOK {
		normalized, normErr := normalizeTranscriptInPlaceIfDirty(mainPath)
		if normErr != nil {
			return report, fmt.Errorf("could not normalize the main transcript before consolidating %s: %w", mainPath, normErr)
		}
		if normalized {
			report.NormalizedMain = true
			if mainCand, mainOK = recoveryConsolidationCandidate(mainPath, true); !mainOK {
				return report, fmt.Errorf("main transcript still failed a conservative load after normalization %s", mainPath)
			}
		} else {
			return report, fmt.Errorf("main transcript failed a conservative load; refusing to consolidate %s", mainPath)
		}
	}
	report.MainMessageCount = mainCand.MessageCount

	cands := make([]RecoveryBranchCandidate, 0, len(copies)+1)
	cands = append(cands, mainCand)
	for _, copy := range copies {
		cand, ok := recoveryConsolidationCandidate(copy, false)
		if !ok {
			// Recovery forks usually carry the same older-format payload as
			// the main they forked from; normalize them the same way so they
			// can participate instead of being silently skipped.
			if normalized, normErr := normalizeTranscriptInPlaceIfDirty(copy); normErr == nil && normalized {
				if cand, ok = recoveryConsolidationCandidate(copy, false); !ok {
					report.SkippedUnloadable = append(report.SkippedUnloadable, copy)
					continue
				}
			} else {
				report.SkippedUnloadable = append(report.SkippedUnloadable, copy)
				continue
			}
		}
		cands = append(cands, cand)
	}

	winner := mainCand
	if requested := strings.TrimSpace(opts.WinnerPath); requested != "" {
		// The user picked a branch. Only a loadable candidate of this session
		// may win - anything else means the picker and the backend disagreed,
		// which must fail loudly rather than silently merge into a chain the
		// user did not choose.
		requested = filepath.Clean(requested)
		found := false
		for _, cand := range cands {
			if cand.Path == requested {
				winner = cand
				found = true
				break
			}
		}
		if !found {
			// The user picked a branch the strict loader refuses (typically an
			// unnormalized copy that the preview showed through the tolerant
			// loader). The merge normalizes in place anyway, so do that here and
			// give the branch one more chance before refusing; refusing while the
			// preview had happily shown it reads as the panel being broken.
			if normalized, normErr := normalizeTranscriptInPlaceIfDirty(requested); normErr == nil && normalized {
				if cand, ok := recoveryConsolidationCandidate(requested, requested == mainPath); ok {
					winner = cand
					found = true
				}
			}
		}
		if !found {
			// A damaged event log is the remaining case: the strict snapshot
			// refuses it, normalization does not apply (the file is not merely
			// old-format), but the tolerant loader still reads the messages - the
			// preview proved that. Promotion is a file-level rename and needs no
			// strict load, and the user has seen this branch's content and named
			// it on purpose, so the tolerant count stands in for the strict one.
			if msgs, _, _, loadErr := loadSessionMessages(requested); loadErr == nil && len(msgs) > 0 {
				turns := 0
				for _, msg := range msgs {
					if IsUserAuthoredTurnMessage(msg) {
						turns++
					}
				}
				winner = RecoveryBranchCandidate{
					Path:         requested,
					MessageCount: len(msgs),
					Turns:        turns,
					Loadable:     true,
					IsMain:       requested == mainPath,
				}
				found = true
			}
		}
		if !found {
			return report, fmt.Errorf("the picked branch still cannot be loaded for merging (it failed normalization): %s", requested)
		}
	} else {
		for _, cand := range cands {
			if !cand.IsMain && consolidationCandidateBeats(cand, winner) {
				winner = cand
			}
		}
	}
	report.WinnerMessageCount = winner.MessageCount
	if winner.Path != mainPath {
		report.WinnerPath = winner.Path
	}

	dir := filepath.Dir(mainPath)
	if winner.Path != mainPath {
		if SessionLeaseHeld(winner.Path) {
			return report, ErrSessionLeaseHeldForConsolidation
		}
		// A main that went through compaction holds a summarized transcript
		// whose prefix no longer matches the pre-compaction recovery fork, so
		// neither side covers the other. Refuse with a structured report the
		// UI can turn into an explicit confirmation instead of failing. A
		// user-named winner skips this refusal: the choice was made against the
		// visible chain list, which is the same judgement the Force
		// confirmation exists to obtain.
		if !SessionContentCovers(winner.Path, mainPath) && !opts.Force && strings.TrimSpace(opts.WinnerPath) == "" {
			report.BlockedByDivergence = true
			report.WinnerPath = winner.Path
			report.WinnerMessageCount = winner.MessageCount
			return report, nil
		}
		grafted, forkIndex, err := promoteRecoveryCopyToMain(mainPath, winner.Path, dir, opts.Force)
		if err != nil {
			return report, err
		}
		report.Prefixed = grafted
		report.ForkIndex = forkIndex
		report.Promoted = true
		report.MainMessageCount = winner.MessageCount
	}

	// Fold covered losers into the recoverable trash. The winner itself has
	// already been renamed onto the main path; everything else that the
	// canonical transcript fully covers is now redundant by definition.
	for _, cand := range cands {
		if cand.IsMain || cand.Path == winner.Path {
			continue
		}
		trashErr := TrashRecoveryBranchCoveredBy(cand.Path, mainPath, dir)
		if trashErr != nil && opts.ArchiveLeftovers {
			// CoveredBy refuses a copy the winner does not contain. The user has
			// already chosen the winner, so the refusal is overridden here and the
			// copy is archived anyway - recoverably.
			trashErr = TrashRecoveryBranchForced(cand.Path, dir)
		}
		if err := trashErr; err != nil {
			report.SkippedNotCovered = append(report.SkippedNotCovered, cand.Path)
			detail := CopyOverlapDetail{Path: cand.Path, Reason: err.Error()}
			if overlap, ok := SessionContentOverlap(mainPath, cand.Path); ok {
				detail.Shared = overlap.Shared
				detail.Unique = overlap.Unique
			}
			report.NotCoveredDetail = append(report.NotCoveredDetail, detail)
			continue
		}
		report.Trashed = append(report.Trashed, cand.Path)
	}
	// Unloadable copies never made it into cands, so the sweep above never
	// touched them - which is how a merge could finish with damaged copies
	// still squatting in the session directory. With ArchiveLeftovers the user
	// has named a winner and asked for the sweep, so the damaged files join the
	// recoverable trash; a merge run without it keeps reporting them instead of
	// destroying anything it could not read.
	for _, unloadable := range report.SkippedUnloadable {
		if !opts.ArchiveLeftovers {
			break
		}
		if err := TrashRecoveryBranchForced(unloadable, dir); err == nil {
			report.Trashed = append(report.Trashed, unloadable)
		}
	}
	report.SkippedUnloadable = stillUnloadable(report.SkippedUnloadable, report.Trashed)
	sort.Strings(report.Trashed)
	sort.Strings(report.SkippedNotCovered)
	sort.Strings(report.SkippedUnloadable)
	return report, nil
}

// promoteRecoveryCopyToMain swaps the winner copy onto the main session
// identity. Both transcripts are held behind removal guards for the whole
// operation; the previous main is archived as one recoverable .trash entry
// (transcript plus every sidecar) before the winner takes over, so a crash
// between the two renames still leaves a complete rollback artifact.
// Returns the number of messages grafted onto the new main and the losing
// chain's fork index, so the caller can report what was recovered.
func promoteRecoveryCopyToMain(mainPath, winnerPath, dir string, force bool) (grafted int, forkIndex int, err error) {
	paths := []string{mainPath, winnerPath}
	sort.Strings(paths)
	guards := make([]*SessionRemovalGuard, 0, len(paths))
	for _, path := range paths {
		guard, err := TryAcquireSessionRemovalGuard(path)
		if err != nil {
			for _, held := range guards {
				held.Release()
			}
			return 0, 0, err
		}
		guards = append(guards, guard)
	}
	defer func() {
		for _, guard := range guards {
			guard.Release()
		}
	}()

	// The swap direction is only safe when the winner contains the whole
	// current main transcript; otherwise main-only turns would survive only
	// inside the trash archive. Force skips this refusal after an explicit
	// user confirmation — the previous main is still archived whole, so the
	// swapped-out content stays recoverable.
	if !SessionContentCovers(winnerPath, mainPath) {
		if !force {
			return 0, 0, ErrMainNotCoveredByWinner
		}
	}

	// The losing chain is the current main: it holds the turns the winner does
	// not. Computed here because both files must still be on disk — after the
	// rename the old main is already staged in the trash.
	gap, gapKnown := SessionContentPrefixGap(winnerPath, mainPath)
	if !gapKnown {
		slog.Warn("promote: prefix gap unavailable; proceeding without grafting",
			"main", mainPath, "winner", winnerPath)
		gap = PrefixGap{}
	}

	legacyMeta, legacyOK, err := LoadBranchMeta(mainPath)
	if err != nil {
		return 0, 0, err
	}
	winnerMeta, winnerOK, err := LoadBranchMeta(winnerPath)
	if err != nil {
		return 0, 0, err
	}
	if !winnerOK {
		return 0, 0, fmt.Errorf("recovery copy meta is missing for %s", winnerPath)
	}

	// 1) Archive the previous main (transcript + sidecars) as one recoverable
	// trash entry, staged atomically like every other recovery trash move.
	key := filepath.Base(mainPath)
	if !validRecoveryTrashKey(key) {
		return 0, 0, fmt.Errorf("invalid main session path for trash staging: %s", mainPath)
	}
	stageDir, err := reserveRecoveryTrashStage(dir)
	if err != nil {
		return 0, 0, err
	}
	if err := writeRecoveryTrashPending(stageDir, key); err != nil {
		return 0, 0, err
	}
	if err := moveRecoveryTrashPath(mainPath, filepath.Join(stageDir, key)); err != nil {
		return 0, 0, err
	}
	for _, src := range recoveryTrashSidecars(mainPath) {
		if err := moveRecoveryTrashPath(src, filepath.Join(stageDir, filepath.Base(src))); err != nil {
			return 0, 0, err
		}
	}
	itemDir, err := publishRecoveryTrashStage(dir, key, stageDir)
	if err != nil {
		return 0, 0, err
	}
	if err := writeRecoveryTrashMetaExisting(itemDir, key); err != nil {
		if !os.IsNotExist(err) {
			return 0, 0, err
		}
		// Published entries may already have been restored or purged.
	}
	if err := clearRecoveryTrashPending(itemDir); err != nil {
		return 0, 0, err
	}

	// 2) The winner copy takes over the main identity. Derived indexes are
	// dropped rather than renamed: the next load rebuilds them from content.
	if err := os.Rename(winnerPath, mainPath); err != nil {
		return 0, 0, err
	}
	winnerStem := strings.TrimSuffix(filepath.Base(winnerPath), ".jsonl")
	mainStem := strings.TrimSuffix(filepath.Base(mainPath), ".jsonl")
	dropSidecar := map[string]bool{
		filepath.Base(store.SessionEventIndex(winnerPath)):   true,
		filepath.Base(store.SessionDisplayIndex(winnerPath)): true,
		// Damaged-log markers describe the winner's *pre-promote* log. After the
		// rename the winner's transcript is the main, and a stale marker would
		// put the promoted main into fail-closed (eventLogDamaged) on its very
		// next load — which is exactly "merged, now the session cannot send".
		// The next load re-validates the log for real and re-raises the marker
		// if damage is genuinely still there. The turn-log marker is the same
		// story. (2026-09-13 incident: the fork-development session went
		// read-only after a merge because the winner's stale marker moved in.)
		filepath.Base(store.SessionEventLogDamaged(winnerPath)):     true,
		filepath.Base(store.SessionTurnEventLogDamaged(winnerPath)): true,
	}
	artifacts := append([]string{}, store.SessionSidecarFiles(winnerPath)...)
	artifacts = append(artifacts,
		winnerPath+".telemetry.json",
		store.SessionCheckpointDir(winnerPath),
		store.SessionJobsDir(winnerPath),
		store.SessionInboxDir(winnerPath),
	)
	for _, src := range artifacts {
		base := filepath.Base(src)
		if dropSidecar[base] {
			if err := os.RemoveAll(src); err != nil {
				return 0, 0, err
			}
			continue
		}
		dst := filepath.Join(dir, strings.Replace(base, winnerStem, mainStem, 1))
		if err := moveRecoveryTrashPath(src, dst); err != nil {
			return 0, 0, err
		}
		// The pinned-context sidecar embeds the session identity it was created
		// under. After the rename that identity is the winner copy's, which
		// fails the SessionID check on the promoted main and silently kills the
		// pinned-files feature (2026-09-13 incident). Rewrite it in place; if
		// it cannot be parsed, drop it — pinning rebuilds from an empty list.
		if base == filepath.Base(store.SessionPinnedContext(winnerPath)) {
			if err := rewritePinnedContextSessionID(dst, mainStem); err != nil {
				slog.Warn("promote: pinned-context rewrite failed; dropping", "file", dst, "err", err)
				os.Remove(dst)
			}
		}
	}

	// 3) Recover the losing chain's head onto the new main.
	//
	// This has to run AFTER the sidecar move above: the winner's event log is one
	// of the sidecars, so it arrives after the rename and would overwrite a
	// replace record written before it — leaving the file grafted but every load
	// (which reads through the event log) showing the pre-graft content.
	graftedMessages := 0
	graftedForkIndex := 0
	if len(gap.Messages) > 0 {
		current, err := os.ReadFile(mainPath)
		if err != nil {
			return 0, 0, err
		}
		mergedLines, err := graftPrefixOntoLines(splitTranscriptLines(current), gap.Messages)
		if err != nil {
			return 0, 0, err
		}
		if err := atomicWriteFileContext(context.Background(), mainPath, "promote-graft", "transcript.promote-graft", joinTranscriptLines(mergedLines), 0o600, true); err != nil {
			return 0, 0, err
		}
		mergedMsgs, err := decodeTranscriptMessages(mergedLines)
		if err != nil {
			return 0, 0, err
		}
		digest, _, err := digestAndSizeSessionMessages(mergedMsgs)
		if err != nil {
			return 0, 0, err
		}
		// The event log keys records by message index, so every index it holds is
		// now wrong; folding it to one replace record is both the correct fix and
		// the existing mechanism for it.
		if err := compactSessionEventLog(mainPath, mergedMsgs, digest, legacyMeta.Revision, "promote-prefix-graft"); err != nil {
			return 0, 0, err
		}
		graftedMessages = len(gap.Messages)
		graftedForkIndex = gap.ForkIndex
		slog.Info("promote: grafted the losing chain's prefix",
			"main", mainPath, "messages", graftedMessages, "forkIndex", gap.ForkIndex)
	}

	// 3) Rewrite the main meta: keep the session's display/ownership identity,
	// take the winner's content identity, and clear every recovery marker so
	// the lineage reads as resolved. The touched UpdatedAt is what open tabs
	// notice (the #9468 reload path) when they refresh.
	return graftedMessages, graftedForkIndex, UpdateBranchMeta(mainPath, true, func(meta *BranchMeta) error {
		meta.ID = BranchID(mainPath)
		meta.Recovered = false
		meta.RecoveryReason = ""
		meta.RecoveryDigest = ""
		meta.RecoveryDepth = 0
		meta.RecoveryPreferred = false
		meta.RecoveryPreferredDigest = ""
		meta.ParentID = ""
		meta.ForkTurn = 0
		meta.ForkMessageIndex = 0
		meta.InFlightTurn = nil
		meta.Revision = winnerMeta.Revision
		meta.ContentDigest = winnerMeta.ContentDigest
		meta.Turns = winnerMeta.Turns
		meta.Preview = winnerMeta.Preview
		meta.SchemaVersion = winnerMeta.SchemaVersion
		meta.WriterID = SessionWriterID()
		if legacyOK {
			meta.Name = legacyMeta.Name
			meta.CustomTitle = legacyMeta.CustomTitle
			meta.TopicID = legacyMeta.TopicID
			meta.TopicTitle = legacyMeta.TopicTitle
			meta.Scope = legacyMeta.Scope
			meta.WorkspaceRoot = legacyMeta.WorkspaceRoot
			meta.Model = legacyMeta.Model
			meta.QualityFloor = legacyMeta.QualityFloor
			meta.Mode = legacyMeta.Mode
			meta.ToolApprovalMode = legacyMeta.ToolApprovalMode
			meta.SubagentPolicy = legacyMeta.SubagentPolicy
			meta.Goal = legacyMeta.Goal
			meta.CreatedAt = legacyMeta.CreatedAt
			meta.DismissedTodoBatches = legacyMeta.DismissedTodoBatches
		}
		return nil
	})
}

// rewritePinnedContextSessionID re-points a migrated pinned-context sidecar at
// the promoted main. The desktop package owns the schema (schemaVersion /
// sessionId / files); agent re-encodes it generically so the files list and
// any future fields survive byte-for-byte apart from the identity.
func rewritePinnedContextSessionID(path, mainStem string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	id, err := json.Marshal(mainStem)
	if err != nil {
		return err
	}
	doc["sessionId"] = id
	out, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o600)
}
