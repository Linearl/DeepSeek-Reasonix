package agent

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"log/slog"
	"math"
	"os"
	"reflect"
	"strings"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 持久化状态与身份域（tl1633 自 save.go 纯移动拆出）：persisted state
// 基线/摘要（digest）/revision 账（ledger）/转写 hasher/前缀比对。

var (
	sessionWriterID = newSessionWriterID()
)

func (s *Session) ownsPersistedState(path string, existingDigest [sha256.Size]byte, existingRevision int64, existingLedgerDigest string, nextVersion uint64) bool {
	state := s.persistState(path)
	if !state.ok || state.version > nextVersion || !bytes.Equal(existingDigest[:], state.digest[:]) {
		return false
	}
	// An unknown-revision baseline still owns the transcript it loaded — the
	// digest+version match proves it. Requiring revision equality here would
	// make every rewrite from such a baseline a permanent conflict, because
	// the revision can only be re-learned by a successful save.
	// A disk ledger with no recorded revision is the mirror case: recorded
	// revisions start at 1, so revision 0 means the sidecar was deleted or
	// rebuilt by a listing-only writer after this session's save. An absent
	// claim cannot revoke the ownership the digest+version match proves.
	if !state.revisionKnown || existingRevision == 0 || state.revision == existingRevision {
		return true
	}
	// A foreign revision stamp whose recorded digest still describes these
	// exact bytes (a same-content heal or no-op record by another runtime)
	// vouches for no content of its own: the transcript is byte-for-byte what
	// this session last persisted, so rewriting it destroys nothing of
	// theirs — at worst the conflict moves to the stamper's next divergent
	// save, where its in-memory history forks a recovery branch as usual.
	// A stamp that disagrees with the on-disk transcript (or a legacy stamp
	// with no digest) keeps revoking ownership: that is the aftermath of a
	// save whose bytes and record split, the bytes cannot be attributed, and
	// only the conservative conflict path preserves both sides.
	return existingLedgerDigest == digestString(existingDigest)
}

// snapshotUpToDate reports whether a snapshot save to path is a provable
// no-op from in-memory bookkeeping alone: the last successful save went to
// this same path with a known ledger revision, the transcript version and
// rewrite version have not moved since, and no load-time repair or event-log
// damage is waiting to be persisted. Every one of these flags fails open —
// when any is unset or stale the caller falls through to the full save path,
// which re-derives the truth from disk.
//
// persisted.projectionPending is deliberately not part of this decision. It
// reports derived files (listing sidecar, display cache) that a tool
// checkpoint or an unlocked shutdown append left to a later save; the
// transcript bytes themselves are already committed, and this baseline
// describes exactly those bytes. Letting it veto the no-op would push a
// defensive switch/close snapshot on a large session through the serialize +
// digest + probe path this fast path exists to avoid (#6607) merely to
// republish a derived index. The caller republishes those derived files
// directly instead — see flushDeferredDerivedFiles.
// snapshotNoOpReason reports why a defensive snapshot may skip the write
// entirely: "" when it may not, "save-verified" when the baseline came from a
// completed save in this process, and "clean-load" when the baseline came from
// a load that proved the in-memory transcript identical to the ledger-backed
// disk transcript (任务 831②). A clean-load no-op must not fire on a repaired
// or damaged load — the first save still owns those fixes — nor on a
// first-paint tail window, whose transcript is a subset of the disk
// transcript, and never before the baseline's revision is a real ledger value.
//
// The memory-side guards above are not sufficient on their own: another
// runtime can extend the log after this baseline was recorded, and a save that
// is genuinely behind must surface the stale-prefix conflict rather than skip
// (TestDAGSaveBehindDiskReportsStalePrefix). So the fast path re-verifies the
// disk before it claims anything: the event log must still end exactly where
// the baseline saw it, the transcript checkpoint must still exist at its
// baseline size (tl1313 — a baseline-known checkpoint that vanished out of
// band must never arm a skip), and the ledger must still describe the baseline
// digest. All three are small fixed-size reads (two stats, one sidecar read) —
// the cost the no-op exists to avoid is the full-transcript serialize + digest,
// which stays gone.
func (s *Session) snapshotNoOpReason(path string) string {
	key := canonicalSessionSavePath(path)
	s.mu.RLock()
	if !s.persisted.ok || s.persisted.path != key ||
		s.persisted.version != s.version ||
		!s.persisted.revisionKnown ||
		s.rewriteVersion != s.persistedRewriteVersion ||
		s.normalizedDirty || s.eventLogDamaged || s.tailTruncated {
		s.mu.RUnlock()
		return ""
	}
	baselineTail := s.persisted.logTail
	baselineCheckpointBytes := s.persisted.checkpointBytes
	baselineRevision := s.persisted.revision
	baselineDigest := s.persisted.digest
	armed := "save-verified"
	if !s.persisted.saveVerified {
		if !s.persisted.cleanLoad {
			s.mu.RUnlock()
			return ""
		}
		armed = "clean-load"
	}
	s.mu.RUnlock()
	// Disk re-verification. A missing or resized log means someone wrote —
	// fall through to the full path, which owns conflict semantics.
	if info, err := os.Stat(store.SessionEventLog(path)); err != nil || info.Size() != baselineTail {
		return ""
	}
	// tl1313: the transcript checkpoint rides the same re-verification. The
	// two checks above can both pass while the .jsonl itself was removed out
	// of band (ghost-save incident 20261031: saves kept reporting
	// save-verified while canonical probes recorded the file missing), and a
	// skip on that state leaves a baseline-known transcript missing forever.
	// Absence is only legitimate when the baseline itself recorded no
	// checkpoint (schema-2 events-authoritative sessions never materialize
	// one) — mirrored off the logTail zero case in snapshotTailWindowClean.
	if info, err := os.Stat(path); err != nil {
		if baselineCheckpointBytes != 0 {
			return ""
		}
	} else if info.IsDir() || info.Size() != baselineCheckpointBytes {
		return ""
	}
	rev, ledgerDigest, err := sessionContentRevision(path)
	if err != nil || rev != baselineRevision || ledgerDigest != digestString(baselineDigest) {
		return ""
	}
	return armed
}

// snapshotUpToDate reports that the transcript this session holds is exactly
// what the ledger already describes, so a snapshot can skip the serialize +
// digest + probe entirely (#6607).
func (s *Session) snapshotUpToDate(path string) bool {
	return s.snapshotNoOpReason(path) != ""
}

// SnapshotUpToDateForPath reports whether the live session's in-memory
// transcript is byte-stable since the last successful save to path — nothing
// changed in memory and the file on disk still matches the persisted baseline
// (tl1352 A: deferred recovery promotion gates on this before folding the
// copy back onto the main file — promoting while the session holds unsaved
// content would discard that content in the post-promotion reload). A
// tail-window session (task 187 first-paint) counts as stable when its tail
// baseline matches the file: promotion's reload restores exactly that tail.
func (s *Session) SnapshotUpToDateForPath(path string) bool {
	return s.snapshotUpToDate(path) || s.snapshotTailWindowClean(path)
}

// warnVanishedPersistedFiles stamps an out-of-band removal of a baseline-known
// transcript checkpoint or event log (tl1313). Both sizes live on the persist
// baseline (checkpointBytes / logTail, recorded at the last save or load), so
// one stat per armed file decides. The WARN fires once per disappearance
// episode: the latch arms on the missing sighting and clears as soon as a
// later save sees the file back (heal/recreation/restore — regardless of who
// brought it back), so a continuing remover cannot storm the log (the
// 847-lines-per-day class task 710 retired). The WARN carries the baseline
// facts so desktop.log alone can answer "what was lost, and since when". An
// external remover cannot be audited from inside this process, but its effect
// on every writer must at least stop being silent.
func (s *Session) warnVanishedPersistedFiles(path string) {
	key := canonicalSessionSavePath(path)
	s.mu.RLock()
	armed := s.persisted.ok && s.persisted.path == key
	var baselineLogTail, baselineCheckpointBytes, baselineRevision int64
	if armed {
		baselineLogTail = s.persisted.logTail
		baselineCheckpointBytes = s.persisted.checkpointBytes
		baselineRevision = s.persisted.revision
	}
	s.mu.RUnlock()
	if !armed {
		return
	}
	if baselineCheckpointBytes > 0 {
		if _, err := os.Stat(path); err != nil {
			if s.vanishWarnedCheckpoint.CompareAndSwap(false, true) {
				slog.Warn("session: transcript vanished on disk since the last save (removed out of band?)",
					"path", path, "baseline_bytes", baselineCheckpointBytes,
					"baseline_revision", baselineRevision)
			}
		} else {
			s.vanishWarnedCheckpoint.Store(false)
		}
	}
	if baselineLogTail > 0 {
		logPath := store.SessionEventLog(path)
		if _, err := os.Stat(logPath); err != nil {
			if s.vanishWarnedLog.CompareAndSwap(false, true) {
				slog.Warn("session: event log vanished on disk since the last save (removed out of band?)",
					"path", logPath, "baseline_bytes", baselineLogTail,
					"baseline_revision", baselineRevision)
			}
		} else {
			s.vanishWarnedLog.Store(false)
		}
	}
}

// snapshotTailWindowClean reports that a first-paint tail-window session is a
// pure read projection of an unchanged disk transcript (tl1281). The window
// decoded from the checkpoint is a strict subset of the disk transcript, and
// the disk state is authoritative and complete — a defensive snapshot of such
// a session has nothing to write, but the ordinary save path would still pay
// upgradeTruncatedTranscriptForWrite's FULL reload (a 173MB-class decode) to
// decide that. The skip's proof mirrors snapshotNoOpReason's re-verification
// discipline: in-memory unchanged since load (version/rewrite/repair flags),
// and both transcript source files byte-stable since the baseline was
// recorded. Any real change (a turn's Add bumps version; any writer grows a
// file) disarms the skip and the save proceeds through the ordinary upgrade,
// whose CAS/probe semantics own the conflict surface unchanged.
//
// The exposure window is a same-byte-size external rewrite between the checks
// and the skip — the same class the no-op path already carries — and the next
// real save still runs the full upgrade and probes for divergence.
func (s *Session) snapshotTailWindowClean(path string) bool {
	key := canonicalSessionSavePath(path)
	s.mu.RLock()
	if !s.tailTruncated || s.normalizedDirty || s.eventLogDamaged ||
		!s.persisted.ok || s.persisted.path != key ||
		s.persisted.version != s.version ||
		s.rewriteVersion != s.persistedRewriteVersion {
		s.mu.RUnlock()
		return false
	}
	baseline := s.persisted
	s.mu.RUnlock()
	if info, err := os.Stat(store.SessionEventLog(path)); err == nil {
		if info.Size() != baseline.logTail {
			return false
		}
	} else if baseline.logTail != 0 {
		// The log vanished or became unreadable since load — not clean.
		return false
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() || info.Size() != baseline.checkpointBytes {
		return false
	}
	return true
}

func (s *Session) persistState(path string) sessionPersistState {
	key := canonicalSessionSavePath(path)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.persisted.ok && s.persisted.path == key {
		return s.persisted
	}
	return sessionPersistState{}
}

// PersistedState is a read-only view of the baseline the session last
// persisted to (or loaded from) its transcript path. History paging uses it to
// validate a display-index sidecar against the live session without touching
// disk: an index built at the same revision with the same content digest
// describes exactly the persisted prefix of the in-memory log.
type PersistedState struct {
	// Digest is the content digest of the persisted transcript.
	Digest [sha256.Size]byte
	// DigestHex is Digest in the hex form sidecars store.
	DigestHex string
	// Revision is the CAS ledger revision of the persisted transcript. It is
	// meaningful only when RevisionKnown is true.
	Revision      int64
	RevisionKnown bool
	// RewriteEpoch is the highest rewriteVersion that has reached disk. It
	// changes only when a content rewrite (compaction, rewind, …) is saved, so
	// it doubles as a stable epoch token for history entry IDs: append-only
	// saves keep it, rewrites bump it.
	RewriteEpoch int
	// AppendOnlyTail reports that no rewrite landed after the baseline, so the
	// persisted transcript is still a prefix of the in-memory log (the tail,
	// if any, is unsaved appends).
	AppendOnlyTail bool
	// UnchangedSincePersisted reports that the in-memory log is exactly the
	// persisted transcript (no appends, no rewrites since the baseline).
	UnchangedSincePersisted bool
}

// PersistedState returns the session's persistence baseline for path, or
// false when the session has never persisted to (or loaded from) that path.
func (s *Session) PersistedState(path string) (PersistedState, bool) {
	key := canonicalSessionSavePath(path)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.persisted.ok || s.persisted.path != key {
		return PersistedState{}, false
	}
	return PersistedState{
		Digest:                  s.persisted.digest,
		DigestHex:               digestString(s.persisted.digest),
		Revision:                s.persisted.revision,
		RevisionKnown:           s.persisted.revisionKnown,
		RewriteEpoch:            s.persistedRewriteVersion,
		AppendOnlyTail:          s.rewriteVersion == s.persistedRewriteVersion,
		UnchangedSincePersisted: s.persisted.version == s.version,
	}, true
}

// SessionContentIdentity returns the ledger identity of the authoritative
// persisted transcript without loading its message bodies. It is intended for
// derived sidecars such as the desktop display index: a matching checkpoint
// size alone cannot prove that offsets still describe the event-log-backed
// transcript. The bool is false for legacy sessions that have no digest in
// their branch metadata; callers must then validate against the transcript
// bytes directly.
func SessionContentIdentity(path string) (PersistedState, bool, error) {
	revision, digestHex, err := sessionContentRevision(path)
	if err != nil {
		return PersistedState{}, false, err
	}
	if digestHex == "" {
		return PersistedState{}, false, nil
	}
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(digestHex)
	if err != nil || len(decoded) != len(digest) {
		return PersistedState{}, false, fmt.Errorf("invalid session content digest")
	}
	copy(digest[:], decoded)
	return PersistedState{
		Digest:        digest,
		DigestHex:     digestString(digest),
		Revision:      revision,
		RevisionKnown: true,
	}, true, nil
}

// sessionContentRevision reads the CAS ledger (revision + content digest) from
// the branch-meta sidecar. A missing sidecar is revision 0 — a session that
// has never recorded one. An unreadable sidecar is an error: reporting it as
// revision 0 would desync every runtime baseline from the ledger and turn the
// next honest save into a bogus conflict (and a recovery branch).
func sessionContentRevision(path string) (int64, string, error) {
	ledger, err := sessionContentLedger(path)
	if err != nil {
		return 0, "", err
	}
	return ledger.revision, ledger.digest, nil
}

func recordSessionContentRevision(path string, digest [sha256.Size]byte, baseRevision, reservedRevision int64) (int64, error) {
	// Revision allocation is a read-modify-write transaction. Holding only the
	// final SaveBranchMeta lock would still let another writer replace the
	// sidecar between our read and write, so keep the ledger lock across the
	// increment and read-back as well.
	unlock, err := LockSessionMetaPath(path)
	if err != nil {
		return 0, err
	}
	defer unlock()

	meta, ok, err := loadBranchMetaRetry(path)
	if err != nil {
		// Fail the save instead of rebuilding the ledger from a bad read: the
		// transcript bytes already landed, so a later save can record the
		// revision once the sidecar reads cleanly again — a content-bearing
		// save lands here again, and a same-content retry heals through the
		// up-to-date ledgerStale path in save().
		return 0, err
	}
	if !ok {
		meta = BranchMeta{ID: BranchID(path)}
	}
	if reservedRevision > 0 {
		if reservedRevision != baseRevision+1 || meta.Revision != reservedRevision || meta.SchemaVersion != 0 {
			return 0, fmt.Errorf("session revision reservation changed: base=%d reserved=%d current=%d schema=%d", baseRevision, reservedRevision, meta.Revision, meta.SchemaVersion)
		}
	} else {
		if meta.Revision < baseRevision {
			meta.Revision = baseRevision
		}
		if meta.Revision == math.MaxInt64 {
			return 0, fmt.Errorf("session revision exhausted")
		}
		meta.Revision++
	}
	meta.ContentDigest = digestString(digest)
	meta.WriterID = SessionWriterID()
	if err := saveBranchMeta(path, meta, false); err != nil {
		return 0, err
	}
	stored, ok, err := loadBranchMetaRetry(path)
	if err != nil {
		return 0, err
	}
	committed := meta.Revision
	if ok && stored.Revision > 0 {
		committed = stored.Revision
	}
	noteLedgerCommit(path, committed, digest)
	return committed, nil
}

func digestString(digest [sha256.Size]byte) string {
	return fmt.Sprintf("%x", digest[:])
}

func SessionWriterID() string {
	return sessionWriterID
}

func newSessionWriterID() string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown-host"
	}
	host = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, host)
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Sprintf("%s-%d-%d", host, os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%d-%x", host, os.Getpid(), nonce[:])
}

func digestSessionMessages(msgs []provider.Message) ([sha256.Size]byte, error) {
	digest, _, err := digestAndSizeSessionMessages(msgs)
	return digest, err
}

func messageForSessionIdentity(m provider.Message) provider.Message {
	// CreatedAt and ID are local metadata. Keep them out of transcript identity
	// so older builds that ignore the optional fields can share the same event-
	// log revision and append without false conflicts.
	// tl1459F2: the identity fold is where every digest byte stream starts (six
	// surfaces). The digest input is normalizedIdentityMessage(m) — the fold
	// PLUS encoding normalization; see the ruling in
	// save_digest_normalize_tl1459f2.go and the pins in
	// digest_normalize_tl1459f2_test.go. Hot equality paths
	// (messagesEqualForStorage) and every storage writer keep the raw value.
	m.CreatedAt = 0
	m.ID = ""
	return m
}

// snapshotDigest returns the transcript digest a schema-2 save needs. When the
// persistence baseline already describes exactly this version — the same proof
// the snapshot no-op relies on — the stored digest is that value by
// construction, because it was written for this version and only a version or
// rewrite bump invalidates it. A schema-2 save only records this digest and
// publishes it into derived files; it never uses it to decide what to append
// (planDAGWrite diffs the transcript itself). Recomputing it instead costs a
// json.Marshal + sha256 pass over every message, which on a large session
// dominates a defensive switch/close snapshot.
func (s *Session) snapshotDigest(path string, msgs []provider.Message, version uint64, rewriteVersion int) ([sha256.Size]byte, error) {
	if s.snapshotUpToDate(path) {
		if state := s.persistState(path); state.ok && state.version == version {
			return state.digest, nil
		}
	}
	// 任务 923: the append funnels maintain an incremental transcript hash;
	// when it describes exactly this snapshot the save skips the full
	// marshal+sha256 pass inside the save-path lock.
	if digest, _, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion); ok {
		return digest, nil
	}
	digest, size, h, err := digestAndSizeSessionMessagesState(msgs)
	if err != nil {
		return digest, err
	}
	s.seedDigestState(msgs, version, rewriteVersion, h, size)
	return digest, nil
}

// transcriptDigestAndSizeForSave is the schema-1 twin of snapshotDigest: the
// incremental stream first, then the full pass with a reseed (任务 923).
func (s *Session) transcriptDigestAndSizeForSave(msgs []provider.Message, version uint64, rewriteVersion int) ([sha256.Size]byte, int64, error) {
	if digest, size, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion); ok {
		return digest, size, nil
	}
	digest, size, h, err := digestAndSizeSessionMessagesState(msgs)
	if err != nil {
		return digest, 0, err
	}
	s.seedDigestState(msgs, version, rewriteVersion, h, size)
	return digest, size, nil
}

// digestAndSizeSessionMessages also reports the encoded transcript size, which
// the save path uses to bound the event log relative to the live content.
func digestAndSizeSessionMessages(msgs []provider.Message) ([sha256.Size]byte, int64, error) {
	digest, size, _, err := digestAndSizeSessionMessagesState(msgs)
	return digest, size, err
}

// sessionMessagesEncodedSize sums the encoded transcript bytes exactly the way
// digestAndSizeSessionMessages measures them (identity fold + one newline per
// message), without the digest pass — the schema-2 size gate needs only the
// number and runs inside the save critical section (任务 884: that gate used to
// pay a full digest of every live head's chain on every save).
func sessionMessagesEncodedSize(msgs []provider.Message) (int64, error) {
	size := int64(0)
	for _, m := range msgs {
		b, err := json.Marshal(messageForSessionIdentity(m))
		if err != nil {
			return 0, err
		}
		size += int64(len(b)) + 1
	}
	return size, nil
}

// sessionTranscriptHasher accumulates the transcript digest one message at a
// time, exactly like digestAndSizeSessionMessages, so load paths hash during
// decode instead of re-serializing. A nil receiver disables hashing.
type sessionTranscriptHasher struct {
	h   hash.Hash
	err error
}

func newSessionTranscriptHasher() *sessionTranscriptHasher {
	return &sessionTranscriptHasher{h: sha256.New()}
}

// rehash restarts accumulation over msgs (event-log replace records).
func (s *sessionTranscriptHasher) rehash(msgs []provider.Message) {
	if s == nil {
		return
	}
	s.h.Reset()
	s.err = nil
	s.addAll(msgs)
}

func (s *sessionTranscriptHasher) addAll(msgs []provider.Message) {
	for _, m := range msgs {
		s.add(m)
	}
}

// add hashes one message and returns it for append chaining.
func (s *sessionTranscriptHasher) add(m provider.Message) provider.Message {
	if s == nil || s.err != nil {
		return m
	}
	// tl1459F2: digest 输入经 digestInputLine（身份 fold + 编码规范化），与
	// 全量/增量口径同一字节流（裁决见 save_digest_normalize_tl1459f2.go）。
	b, err := digestInputLine(m)
	if err != nil {
		s.err = err
		return m
	}
	_, _ = s.h.Write(b)
	_, _ = s.h.Write([]byte{'\n'})
	return m
}

// sum reports the accumulated digest; ok is false when hashing was disabled
// or a message failed to encode, and callers fall back to digestSessionMessages.
func (s *sessionTranscriptHasher) sum() (digest [sha256.Size]byte, ok bool) {
	if s == nil || s.err != nil {
		return digest, false
	}
	copy(digest[:], s.h.Sum(nil))
	return digest, true
}

func messagesHavePrefix(full, prefix []provider.Message) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		if !messagesEqualForStorage(full[i], prefix[i]) {
			return false
		}
	}
	return true
}

// messagesPrefixDigestDepth returns the number of leading messages of msgs
// whose storage digest equals target, or -1 when no prefix matches. The
// digest accumulates exactly like digestAndSizeSessionMessages, so a match at
// depth k means msgs[:k] has the same transcript identity as target.
func messagesPrefixDigestDepth(msgs []provider.Message, target [sha256.Size]byte) int {
	h := sha256.New()
	sum := make([]byte, 0, sha256.Size)
	for i, m := range msgs {
		// tl1459F2: digest 输入经 digestInputLine（身份 fold + 编码规范化），
		// 与全量/增量/解码侧口径同一字节流（裁决见
		// save_digest_normalize_tl1459f2.go）。
		b, err := digestInputLine(m)
		if err != nil {
			return -1
		}
		h.Write(b)
		h.Write([]byte{'\n'})
		sum = h.Sum(sum[:0])
		if bytes.Equal(sum, target[:]) {
			return i + 1
		}
	}
	return -1
}

// appendCoversPersistedBaseline reports whether an append-shaped write (disk
// transcript a prefix of next, modulo a compatible leading-system swap) still
// covers everything this session ever persisted: the baseline digest must be
// reachable as a prefix of the pending snapshot, and the disk transcript must
// still extend at least to that depth. A shorter disk transcript means some
// other runtime deliberately rewound below the baseline — appending over it
// would resurrect the removed suffix, so the caller must conflict instead.
func appendCoversPersistedBaseline(next, existing []provider.Message, baseDigest [sha256.Size]byte) bool {
	depth := messagesPrefixDigestDepth(next, baseDigest)
	if depth < 0 && len(next) > 0 && len(existing) > 0 &&
		next[0].Role == provider.RoleSystem && existing[0].Role == provider.RoleSystem &&
		!messagesEqualForStorage(next[0], existing[0]) {
		// A resume that swapped the system prompt persisted its baseline with
		// the previous system message — the one still on disk. Re-anchor the
		// search on that message so the swap alone doesn't hide the baseline.
		variant := append([]provider.Message{existing[0]}, next[1:]...)
		depth = messagesPrefixDigestDepth(variant, baseDigest)
	}
	return depth >= 0 && len(existing) >= depth
}

func messagesHavePrefixWithCompatibleSystem(full, prefix []provider.Message) bool {
	full = messagesWithoutLeadingSystem(full)
	prefix = messagesWithoutLeadingSystem(prefix)
	return messagesHavePrefix(full, prefix)
}

func messagesWithoutLeadingSystem(msgs []provider.Message) []provider.Message {
	// Resume refreshes the dynamic system header (fresh prompt, memory/tool
	// sections) into one or more leading system messages that never landed on
	// disk. Mirror the projection layer's semantics — leading system messages
	// are outside every fold and outside every prefix comparison — by
	// stripping the whole contiguous system header, not just the first entry.
	out := msgs
	for len(out) > 0 && out[0].Role == provider.RoleSystem {
		out = out[1:]
	}
	return out
}

func messagesEqualForStorage(a, b provider.Message) bool {
	a = messageForSessionIdentity(a)
	b = messageForSessionIdentity(b)
	// 任务 923: the DAG plan diff and prefix classifiers run this check over
	// the whole common prefix on every save, so the double json.Marshal was
	// O(transcript) CPU inside the save-path lock (measured 68.5ms per save on
	// a 4000-message synthetic session vs 5.3ms for this fast path — 12.8x).
	// A DeepEqual match proves the marshals would match (marshal is a pure
	// function of the value), so structurally identical messages skip
	// serialization entirely; any difference — including nil-vs-empty-slice,
	// which DeepEqual keeps distinct exactly like the json encoders do — still
	// falls through to the exact marshal comparison.
	if reflect.DeepEqual(a, b) {
		return true
	}
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

func messagesEqualForStorageList(a, b []provider.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !messagesEqualForStorage(a[i], b[i]) {
			return false
		}
	}
	return true
}

func messagesCompatibleForStorageBaseline(a, b []provider.Message) bool {
	if messagesEqualForStorageList(a, b) {
		return true
	}
	return messagesEqualForStorageList(messagesWithoutLeadingSystem(a), messagesWithoutLeadingSystem(b))
}
