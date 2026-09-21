package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

type SessionListingRepairStatus string

const (
	SessionListingRepairApplied        SessionListingRepairStatus = "applied"
	SessionListingRepairAlreadyCurrent SessionListingRepairStatus = "already_current"
	SessionListingRepairSourceChanged  SessionListingRepairStatus = "source_changed"
	SessionListingRepairDamaged        SessionListingRepairStatus = "damaged"
	SessionListingRepairUnsupported    SessionListingRepairStatus = "unsupported"
)

var ErrSessionListingRepairBusy = errors.New("session listing repair source is busy")

// SessionListingRepairResult describes one convergent repair attempt. Preview
// and Turns are safe to publish only for applied/already_current results.
type SessionListingRepairResult struct {
	Status             SessionListingRepairStatus
	Preview            string
	Turns              int
	LedgerRepaired     bool
	ContentFingerprint string
	MetaFingerprint    string
}

// SessionListingGeneration identifies the transcript/event-log generation and
// its metadata sidecar while all writer locks for the session remain held.
type SessionListingGeneration struct {
	ContentFingerprint string
	MetaFingerprint    string
}

// TryLockSessionListingGeneration fences catalog publication against a
// foreground writer. The caller must invoke the returned unlock function.
func TryLockSessionListingGeneration(path string) (SessionListingGeneration, func(), error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return SessionListingGeneration{}, nil, fmt.Errorf("empty session path")
	}
	unlock, err := lockSessionListingRepair(path)
	if err != nil {
		return SessionListingGeneration{}, nil, err
	}
	return SessionListingGeneration{
		ContentFingerprint: sessionListingCatalogContentFingerprint(path),
		MetaFingerprint:    sessionListingCatalogFileFingerprint(BranchMetaPath(path)),
	}, unlock, nil
}

// RepairSessionListingProjection repairs one session generation while holding
// only that session's save/file/meta locks. Foreground saves win immediately;
// callers persist a retry instead of waiting behind active work.
//
// Task 206 (T2): the full-transcript decode of the replay branch runs outside
// those locks - only the fingerprint re-check and the write-back are locked,
// and the before/after fingerprints act as the CAS fence (checked once after
// the decode, once right after the locks are taken).
func RepairSessionListingProjection(ctx context.Context, path string) (result SessionListingRepairResult, resultErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return SessionListingRepairResult{}, fmt.Errorf("empty session path")
	}
	if err := ctx.Err(); err != nil {
		return SessionListingRepairResult{}, err
	}
	// Task 206 (T4): trash entries are storage the user chose to keep. The
	// repair worker must never spend its slot decoding them; users repair them
	// lazily through preview/restore, which loads via the main path anyway.
	if SessionPathInTrash(path) {
		return SessionListingRepairResult{Status: SessionListingRepairUnsupported}, nil
	}
	meta, metaOK, err := LoadBranchMeta(path)
	if err != nil {
		if isDamagedSessionRepairError(err) {
			return SessionListingRepairResult{Status: SessionListingRepairDamaged}, nil
		}
		return SessionListingRepairResult{}, err
	}
	if !metaOK {
		meta = BranchMeta{ID: BranchID(path)}
	}
	unlock, err := lockSessionListingRepair(path)
	if err != nil {
		return SessionListingRepairResult{}, err
	}
	result, handled, err := repairSessionListingFromIndex(path, meta)
	// The index branch is cheap; release the locks before anything slow.
	unlock()
	if handled || err != nil {
		defer func() {
			result.ContentFingerprint = sessionListingCatalogContentFingerprint(path)
			result.MetaFingerprint = sessionListingCatalogFileFingerprint(BranchMetaPath(path))
		}()
		return result, err
	}
	return repairSessionListingFromReplay(ctx, path, meta, result)
}

func lockSessionListingRepair(path string) (func(), error) {
	unlockSave, ok := tryLockSessionSavePath(path)
	if !ok {
		return nil, ErrSessionListingRepairBusy
	}
	unlockFile, err := tryLockSessionFile(path)
	if err != nil {
		unlockSave()
		if errors.Is(err, ErrSessionFileLockHeld) {
			return nil, ErrSessionListingRepairBusy
		}
		return nil, err
	}
	unlockMeta, ok, err := tryLockSessionMetaPath(path)
	if err != nil {
		unlockFile()
		unlockSave()
		return nil, err
	}
	if !ok {
		unlockFile()
		unlockSave()
		return nil, ErrSessionListingRepairBusy
	}
	return func() { unlockMeta(); unlockFile(); unlockSave() }, nil
}

func repairSessionListingFromIndex(path string, meta BranchMeta) (SessionListingRepairResult, bool, error) {
	preview, turns, ok, unsupported, err := indexedSessionListing(path, meta)
	if err != nil {
		return SessionListingRepairResult{}, true, err
	}
	if unsupported {
		return SessionListingRepairResult{Status: SessionListingRepairUnsupported}, true, nil
	}
	if !ok {
		return SessionListingRepairResult{}, false, nil
	}
	if sessionListingProjectionFresh(meta.SchemaVersion, meta.Turns, meta.Revision,
		meta.ListingRevision, meta.ContentDigest, meta.ListingContentDigest) &&
		meta.Preview == preview && meta.Turns == turns {
		return SessionListingRepairResult{Status: SessionListingRepairAlreadyCurrent, Preview: preview, Turns: turns}, true, nil
	}
	meta.Preview, meta.Turns, meta.SchemaVersion = preview, turns, BranchMetaCountsVersion
	stampSessionListingProjection(&meta)
	if err := saveBranchMeta(path, meta, false); err != nil {
		return SessionListingRepairResult{}, true, err
	}
	return SessionListingRepairResult{Status: SessionListingRepairApplied, Preview: preview, Turns: turns}, true, nil
}

// repairSessionListingFromReplay decodes the transcript without holding the
// save/file/meta locks (Task 206 T2) and hands the result to the locked commit
// half. result carries the caller's named returns so the fingerprint fill-in
// keeps working on every early exit.
func repairSessionListingFromReplay(ctx context.Context, path string, meta BranchMeta, result SessionListingRepairResult) (SessionListingRepairResult, error) {
	before, err := sessionRepairContentFingerprint(path)
	if err != nil {
		return SessionListingRepairResult{}, err
	}
	msgs, state, repairable, err := loadSessionDisplayMessagesContextUnlocked(ctx, path)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SessionListingRepairResult{}, ctxErr
		}
		if isUnsupportedSessionRepairError(err) {
			return SessionListingRepairResult{Status: SessionListingRepairUnsupported}, nil
		}
		if isDamagedSessionRepairError(err) {
			return SessionListingRepairResult{Status: SessionListingRepairDamaged}, nil
		}
		return SessionListingRepairResult{}, err
	}
	if !repairable {
		return SessionListingRepairResult{Status: SessionListingRepairDamaged}, nil
	}
	if err := ctx.Err(); err != nil {
		return SessionListingRepairResult{}, err
	}
	after, err := sessionRepairContentFingerprint(path)
	if err != nil {
		return SessionListingRepairResult{}, err
	}
	if before != after {
		return SessionListingRepairResult{Status: SessionListingRepairSourceChanged}, nil
	}
	return commitSessionListingReplay(ctx, path, meta, before, msgs, state, result)
}

// commitSessionListingReplay is the locked half of the replay repair: it re-
// checks the CAS fence now that the locks are held, then publishes the decoded
// transcript. Extracted so the SourceChanged path stays unit-testable without
// racing a real decoder.
func commitSessionListingReplay(ctx context.Context, path string, meta BranchMeta, before string, msgs []provider.Message, state PersistedState, result SessionListingRepairResult) (SessionListingRepairResult, error) {
	unlock, err := lockSessionListingRepair(path)
	if err != nil {
		return SessionListingRepairResult{}, err
	}
	defer unlock()
	defer func() {
		result.ContentFingerprint = sessionListingCatalogContentFingerprint(path)
		result.MetaFingerprint = sessionListingCatalogFileFingerprint(BranchMetaPath(path))
	}()
	// Second CAS fence: the source may have changed between the unlocked decode
	// and this locked critical section.
	after, err := sessionRepairContentFingerprint(path)
	if err != nil {
		return SessionListingRepairResult{}, err
	}
	if before != after {
		return SessionListingRepairResult{Status: SessionListingRepairSourceChanged}, nil
	}

	preview, turns := SessionPreviewFromMessages(msgs)
	ledgerCurrent := meta.Revision > 0 && strings.TrimSpace(meta.ContentDigest) == state.DigestHex
	if meta.Recovered && strings.TrimSpace(meta.RecoveryDigest) != state.DigestHex {
		ledgerCurrent = false
	}
	ledgerRepaired := !ledgerCurrent
	if ledgerRepaired {
		meta.Revision = max(int64(1), meta.Revision+1)
		meta.ContentDigest = state.DigestHex
		if meta.Recovered || strings.TrimSpace(meta.RecoveryDigest) != "" {
			meta.RecoveryDigest = state.DigestHex
		}
		meta.WriterID = SessionWriterID()
	}
	meta.Preview = preview
	meta.Turns = turns
	meta.SchemaVersion = BranchMetaCountsVersion
	stampSessionListingProjection(&meta)

	// The display index ranges describe the compatibility JSONL exactly, so
	// refresh it from this single authoritative replay before publishing meta.
	if err := writeSessionMessagesContext(ctx, path, msgs); err != nil {
		return SessionListingRepairResult{}, fmt.Errorf("write session display read model: %w", err)
	}
	if err := refreshSessionEventIndexContext(ctx, path, msgs, state.Digest, meta.Revision); err != nil {
		return SessionListingRepairResult{}, err
	}
	idx, err := BuildSessionDisplayIndexContext(ctx, msgs, meta.Revision, true, state.Digest)
	if err != nil {
		return SessionListingRepairResult{}, fmt.Errorf("encode session display index: %w", err)
	}
	if err := WriteSessionDisplayIndexContext(ctx, store.SessionDisplayIndex(path), idx); err != nil {
		return SessionListingRepairResult{}, err
	}
	if err := saveBranchMetaContext(ctx, path, meta, false); err != nil {
		return SessionListingRepairResult{}, err
	}
	return SessionListingRepairResult{
		Status: SessionListingRepairApplied, Preview: preview, Turns: turns, LedgerRepaired: ledgerRepaired,
	}, nil
}

// sessionTrashDirName matches the desktop trash layout (desktop/sessions.go
// sessionTrashDir); this package cannot import package main.
const sessionTrashDirName = ".trash"

// SessionPathInTrash reports whether any segment of the cleaned path is the
// session trash directory. Exported for the sessioncatalog, whose repair queue
// must skip trash entries for the same reason (Task 206 T4).
func SessionPathInTrash(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if seg == sessionTrashDirName {
			return true
		}
	}
	return false
}

func indexedSessionListing(path string, meta BranchMeta) (preview string, turns int, ok, unsupported bool, err error) {
	digest := strings.TrimSpace(meta.ContentDigest)
	if meta.Revision <= 0 || digest == "" || meta.Recovered && strings.TrimSpace(meta.RecoveryDigest) != digest {
		return "", 0, false, false, nil
	}
	idx, err := LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	if err != nil || idx == nil || !idx.ListingPreviewKnown || !idx.RevisionKnown ||
		idx.Revision != meta.Revision || idx.ContentDigest != digest {
		return "", 0, false, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, false, false, err
	}
	if info.IsDir() || info.Size() != idx.TranscriptSize {
		return "", 0, false, false, nil
	}
	indexInfo, err := os.Stat(store.SessionDisplayIndex(path))
	if err != nil || !indexInfo.ModTime().After(SessionContentModTime(path)) {
		return "", 0, false, false, nil
	}
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return "", 0, false, false, err
	}
	if probe.futureSchema {
		return "", 0, false, true, nil
	}
	if probe.native && probe.size > 0 {
		eventIdx, err := readSessionEventIndex(path)
		if err != nil || eventIdx == nil || eventIdx.LogSize != probe.size ||
			eventIdx.Revision != meta.Revision || eventIdx.ContentDigest != digest ||
			eventIdx.MessageCount != idx.MessageCount {
			return "", 0, false, false, nil
		}
		eventInfo, eventErr := os.Stat(store.SessionEventIndex(path))
		logInfo, logErr := os.Stat(store.SessionEventLog(path))
		if eventErr != nil || logErr != nil || !eventInfo.ModTime().After(logInfo.ModTime()) {
			return "", 0, false, false, nil
		}
	}
	return idx.ListingPreview, idx.AuthoredTurns, true, false, nil
}

func sessionRepairContentFingerprint(path string) (string, error) {
	h := sha256.New()
	for _, artifact := range []string{path, store.SessionEventLog(path)} {
		info, err := os.Stat(artifact)
		if err != nil {
			if os.IsNotExist(err) {
				_, _ = fmt.Fprintf(h, "missing\x00")
				continue
			}
			return "", err
		}
		_, _ = fmt.Fprintf(h, "%d\x00%d\x00%d\x00", info.Size(), info.ModTime().UnixNano(), info.Mode())
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func sessionListingCatalogFileFingerprint(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func sessionListingCatalogContentFingerprint(path string) string {
	return sessionListingCatalogFileFingerprint(path) + "|" + sessionListingCatalogFileFingerprint(store.SessionEventLog(path))
}

func isUnsupportedSessionRepairError(err error) bool {
	return errors.Is(err, ErrSessionReplayLimitExceeded) ||
		strings.Contains(err.Error(), "unsupported schema") ||
		strings.Contains(err.Error(), "supports up to") ||
		strings.Contains(err.Error(), "unsupported event type")
}

func isDamagedSessionRepairError(err error) bool {
	text := err.Error()
	return strings.Contains(text, "decode session transcript") ||
		strings.Contains(text, "decode session event log") ||
		strings.Contains(text, "decode ")
}
