package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"reasonix/internal/provider"
)

func (s *Session) classifySnapshotWriteForCommit(path string, msgs []provider.Message, digest [sha256.Size]byte, version uint64, ownedRewrite bool, mode sessionSaveMode) (snapshotWriteDecision, error) {
	decision, err := s.classifySnapshotWrite(path, msgs, digest, version, ownedRewrite)
	if err != nil && s.rescueReplayLimitedEventLog(path, msgs, digest, err) {
		// Task 339: the on-disk event log sits past the replay caps, the disk
		// state was provably this runtime's own last write, and the rescue
		// folded it from the in-memory snapshot. Re-classify against the folded
		// log; its error (if any) is the honest answer now.
		decision, err = s.classifySnapshotWrite(path, msgs, digest, version, ownedRewrite)
	}
	if err != nil || decision.upToDate && mode != sessionSaveRewriteCompact && !decision.ledgerStale {
		return decision, err
	}
	// Invalidate before any transcript mutation or stale-ledger repair so an
	// interrupted commit cannot leave the previous listing self-certified.
	reservedRevision, err := invalidateSessionListingProjection(path)
	if err != nil {
		return decision, fmt.Errorf("invalidate session listing projection: %w", err)
	}
	decision.reservedRevision = reservedRevision
	return decision, nil
}

func (s *Session) markPersistedWithListing(path string, digest [sha256.Size]byte, version uint64, revision int64, rewriteVersion int, msgs []provider.Message) {
	// Pair the persisted-message view with the baseline: writer-bound saves
	// use it to classify append shapes without reloading the transcript.
	s.setPersistedBaseline(path, digest, version, revision, true, true, rewriteVersion, msgs)
	persistSessionListingProjection(path, msgs, revision, digestString(digest))
}

// invalidateSessionListingProjection makes cached counts untrusted before a
// transcript commit begins. If the transcript lands but its revision ledger
// does not, readers must decode/repair instead of certifying the previous
// generation's preview from an internally consistent but stale sidecar.
func invalidateSessionListingProjection(path string) (int64, error) {
	if !sessionArtifactsHaveContent(path) {
		// A brand-new session has no prior projection to reuse. It also needs to
		// retain the historical first committed revision of one.
		return 0, nil
	}
	var reservedRevision int64
	err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		// Reserve the next transcript generation so stale whole-sidecar writers
		// preserve this invalidation. The transcript commit finalizes the same
		// revision, keeping the WAL and content ledger in one generation.
		if meta.Revision == math.MaxInt64 {
			return fmt.Errorf("session revision exhausted")
		}
		meta.Revision++
		reservedRevision = meta.Revision
		meta.WriterID = SessionWriterID()
		meta.SchemaVersion = 0
		return nil
	})
	return reservedRevision, err
}

func persistSessionListingProjection(path string, msgs []provider.Message, revision int64, contentDigest string) {
	preview, turns := SessionPreviewFromMessages(msgs)
	excerpts := SessionRecentExcerptsFromMessages(msgs, recentExcerptCount)
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		// The transcript commit and this repairable projection are separate
		// critical sections. A newer writer may already have advanced the
		// sidecar, so only publish counts for the generation this save committed.
		contentDigest = strings.TrimSpace(contentDigest)
		if revision <= 0 || meta.Revision != revision || strings.TrimSpace(meta.ContentDigest) != contentDigest {
			return nil
		}
		meta.Preview = preview
		meta.Turns = turns
		meta.RecentExcerpts = excerpts
		meta.SchemaVersion = BranchMetaCountsVersion
		meta.ListingRevision = revision
		meta.ListingContentDigest = contentDigest
		// tl1485: the activity stamp must follow the transcript itself. The
		// sidecar's only activity-stamping writer (SnapshotActivity /
		// markActivity=true) lost its callers, so collab/detached/cross-session
		// appends left UpdatedAt frozen at an old meta-only stamp and the
		// sidebar showed "1个月" for days-old messages. Every commit holds the
		// committed messages right here — publish their newest timestamp.
		raiseSessionListingActivity(meta, msgs)
		return nil
	}); err != nil {
		// JSONL/event log already committed. Listing metadata is a repairable
		// projection and must never turn a successful transcript save into an
		// application error.
		slog.Warn("session: listing metadata update deferred", "path", path, "err", err)
	}
}

// SessionListingActivityTime returns the transcript's own last-message time:
// the newest non-zero CreatedAt across msgs (unix ms; legacy rows may carry
// none, in which case the caller preserves the sidecar's existing value).
func SessionListingActivityTime(msgs []provider.Message) time.Time {
	for i := len(msgs) - 1; i >= 0; i-- {
		if at := msgs[i].CreatedAt; at > 0 {
			return time.UnixMilli(at)
		}
	}
	return time.Time{}
}

// raiseSessionListingActivity stamps the sidecar's UpdatedAt with the
// transcript's own last-message time, raise-only: a commit replaying older
// messages (rewind, recovery graft) never drags the displayed activity
// backward, matching the catalog's keep-max upsert (exact_index) and the
// #8694 discipline of not regressing a known sidecar value.
func raiseSessionListingActivity(meta *BranchMeta, msgs []provider.Message) {
	if at := SessionListingActivityTime(msgs); at.After(meta.UpdatedAt) {
		meta.UpdatedAt = at
	}
}

// sessionListingActivityRepairSlack separates the two UpdatedAt-vs-transcript
// mismatches the repair path must tell apart. Legitimate meta-only activity
// writes (in-flight turn markers, collab receipts, projection saves) trail the
// newest message by write latency: seconds to a few minutes — the 2026-11-08
// live scan measured that population decaying smoothly (103 rows at >10m,
// 28 at >30m, 13 at >1h across 464 sessions) with no natural gap. The dead
// meta-only pump (touch=true stamps now) left clusters hours to weeks ahead
// of the transcript. One hour keeps the entire benign tail untouched while
// capturing every pump wave; the baseline is message-level CreatedAt only —
// file mtime is banned (the 2026-10-29 maintenance wave inflated 95/261
// mtimes, #8694 discipline).
const sessionListingActivityRepairSlack = time.Hour

// settleSessionListingActivity is the repair-channel final say on UpdatedAt.
// It keeps tl1485's raise-only behavior for fresh evidence and adds the
// tl1485b stock heal: a stamp clearly ahead of the transcript's own last
// message drops to that message time, so sessions the old meta-only pump
// inflated converge without waiting for a new message.
//  1. no message-level timestamps (empty shell / legacy all-zero): untouched —
//     no evidence, no write;
//  2. transcript newer than the stamp: raised (tl1485);
//  3. stamp less than slack ahead: untouched — legitimate activity-write lag;
//  4. stamp slack or more ahead: lowered to the transcript's newest message.
func settleSessionListingActivity(meta *BranchMeta, msgs []provider.Message) {
	at := SessionListingActivityTime(msgs)
	if at.IsZero() {
		return
	}
	if at.After(meta.UpdatedAt) {
		meta.UpdatedAt = at
		return
	}
	if meta.UpdatedAt.Sub(at) >= sessionListingActivityRepairSlack {
		meta.UpdatedAt = at
	}
}

// sessionListingActivityProbeWindow is the tail window the cheap too-new probe
// starts with. Complete JSONL lines inside the window carry real CreatedAt
// values; the window only has to cover enough recent lines to make a one-hour
// overshoot obvious. Normal sessions are decided by the first read.
const sessionListingActivityProbeWindow = 64 << 10

// sessionListingActivityProbeMaxWindow caps the probe's growth. Real stock
// rows showed the timestamps living far above the tail: a 2-34MB transcript
// whose last 64KB is one huge tool-result line without CreatedAt missed the
// trigger entirely (2026-11-08 live run, 4/18 survivors). When the first
// window holds no evidence the read backs off 4x up to this cap — big files
// with head-only timestamps pay a bounded extra read, and a read that still
// finds nothing reports false: the probe prefers missing a row over
// fabricating evidence, and a partial read can never manufacture a newer
// timestamp (the replay re-checks with the full decode before touching
// anything).
const sessionListingActivityProbeMaxWindow = 16 << 20

// SessionListingActivityAheadOfTranscript is the cheap trigger predicate for
// the too-new stock form (kept as a thin wrapper over the two-way probe for
// existing callers): it compares the sidecar's UpdatedAt against the
// message-level CreatedAt values visible in the transcript tail, without a
// full decode. Read failures, empty transcripts, and timestamp-less lines all
// report false — the trigger prefers missing a row over fabricating evidence.
// Exported for the sessioncatalog scan, which must flag inflated rows so the
// existing repair queue picks them up; the exact correction itself always
// happens inside RepairSessionListingProjection against the full decode.
func SessionListingActivityAheadOfTranscript(path string, updatedAt time.Time) bool {
	_, ahead := SessionListingActivityMismatch(path, updatedAt)
	return ahead
}

// SessionListingActivityMismatch probes BOTH directions of the
// UpdatedAt-vs-transcript divergence in one tail read:
//
//   - ahead = the stamp sits slack or more ABOVE the newest tail message (the
//     dead meta-only pump's inflation, tl1485b);
//   - behind = the newest tail message sits slack or more ABOVE the stamp (the
//     frozen-stamp stock tl1485 originally fixed only through the commit
//     funnel: collab/detached/serve appends whose UpdatedAt never moved. A
//     session the user has not saved again after installing the fix still
//     displays "1 个月" for days-old messages — the 2026-11-08 二轮打回现场).
//
// One shared tail probe serves both triggers so a catalog scan pays a single
// bounded read per row, not two. Read failures, empty transcripts, and
// timestamp-less lines report false in both directions — the trigger prefers
// missing a row over fabricating evidence, and a partial read can never
// manufacture a verdict (the replay re-checks with the full decode before
// touching anything). Exported for the sessioncatalog scan and the repair
// index branch.
func SessionListingActivityMismatch(path string, updatedAt time.Time) (behind, ahead bool) {
	if updatedAt.IsZero() {
		return false, false
	}
	newest, ok := probeTranscriptTailNewestCreatedAt(path)
	if !ok {
		return false, false
	}
	behind = time.UnixMilli(newest).Sub(updatedAt) >= sessionListingActivityRepairSlack
	ahead = updatedAt.Sub(time.UnixMilli(newest)) >= sessionListingActivityRepairSlack
	return behind, ahead
}

// probeTranscriptTailNewestCreatedAt returns the newest message-level CreatedAt
// visible in the transcript tail, growing the read window with backoff (see
// sessionListingActivityProbeWindow / sessionListingActivityProbeMaxWindow).
// ok is false when no tail line carries a usable CreatedAt.
func probeTranscriptTailNewestCreatedAt(path string) (int64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() || info.Size() <= 0 {
		return 0, false
	}
	size := info.Size()
	window := int64(sessionListingActivityProbeWindow)
	for {
		if window > size {
			window = size
		}
		if newest, ok := probeTailCreatedAt(file, size, window); ok {
			return newest, true
		}
		if window >= size || window >= sessionListingActivityProbeMaxWindow {
			return 0, false
		}
		window *= 4
	}
}

// probeTailCreatedAt reads the trailing window of the transcript and returns
// the newest message-level CreatedAt among complete JSONL lines. The first
// chunk is dropped when the window starts mid-line: a truncated line is not
// evidence.
func probeTailCreatedAt(file *os.File, size, window int64) (int64, bool) {
	offset := int64(0)
	dropFirstLine := false
	if size > window {
		offset = size - window
		dropFirstLine = true
	}
	buf := make([]byte, window)
	n, err := file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return 0, false
	}
	var newest int64
	for _, line := range bytes.Split(buf[:n], []byte{'\n'}) {
		if dropFirstLine {
			dropFirstLine = false
			continue
		}
		var stamp struct {
			CreatedAt int64 `json:"createdAt"`
		}
		if json.Unmarshal(line, &stamp) != nil || stamp.CreatedAt <= 0 {
			continue
		}
		if stamp.CreatedAt > newest {
			newest = stamp.CreatedAt
		}
	}
	if newest <= 0 {
		return 0, false
	}
	return newest, true
}
