package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// SessionDisplayIndexSchemaVersion is the on-disk schema of the display index
// sidecar. Loaders reject any other version so a future layout change fails
// closed into the rescan fallback instead of misreading offsets.
const SessionDisplayIndexSchemaVersion = 1

// sessionDisplayIndexMaxLineBytes keeps recovery scans bounded when a damaged
// transcript has no newline for an arbitrarily large payload. A normal
// provider message can contain attachments, so this stays comfortably above
// the usual attachment threshold while refusing a single corrupt line before
// it can exhaust the desktop process.
const sessionDisplayIndexMaxLineBytes = 16 << 20

// SessionDisplayIndex pages history by exact canonical JSONL byte ranges.
// Consumers validate TranscriptSize and content identity before using it.
type SessionDisplayIndex struct {
	SchemaVersion  int    `json:"schema_version"`
	Revision       int64  `json:"revision"`
	RevisionKnown  bool   `json:"revision_known"`
	ContentDigest  string `json:"content_digest"`
	TranscriptSize int64  `json:"transcript_size"`
	MessageCount   int    `json:"message_count"`
	AuthoredTurns  int    `json:"authored_turns"`
	// ListingPreview is derived from the same transcript generation as
	// AuthoredTurns. The explicit known bit keeps old v1 indexes compatible:
	// an empty preview may be authoritative, while an omitted preview must
	// still fall back to a repair scan.
	ListingPreview      string              `json:"listing_preview,omitempty"`
	ListingPreviewKnown bool                `json:"listing_preview_known,omitempty"`
	Entries             []DisplayIndexEntry `json:"entries"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

// DisplayIndexEntry is one message's metadata. AuthoredTurn is the absolute
// authored-turn number the message belongs to, counted with
// SessionPreviewFromMessages semantics (IsUserAuthoredTurn over
// UserMessageText); messages before the first authored turn carry 0.
type DisplayIndexEntry struct {
	Index        int           `json:"index"`
	Offset       int64         `json:"offset"`
	Length       int64         `json:"length"`
	Role         provider.Role `json:"role"`
	AuthoredTurn int           `json:"authored_turn"`
	StartsTurn   bool          `json:"starts_turn,omitempty"`
	HasImages    bool          `json:"has_images,omitempty"`
	HasToolCalls bool          `json:"has_tool_calls,omitempty"`
	LocalOnly    bool          `json:"local_only,omitempty"`
	ToolResult   bool          `json:"tool_result,omitempty"`
	// PinnedContextRevision distinguishes the hidden host revision from other
	// user-role records. History paging needs this bit to keep legacy persisted
	// user timestamps aligned without decoding the entire transcript prefix.
	PinnedContextRevision bool `json:"pinned_context_revision,omitempty"`
	// Synthetic and Steer use raw authored text; desktop may first resolve
	// @file references for display, but the control-message predicates match.
	Synthetic bool `json:"synthetic,omitempty"`
	Steer     bool `json:"steer,omitempty"`
}

// BuildSessionDisplayIndex derives the index from an in-memory message slice.
// It is a pure function: same messages, same entries. It returns nil when a
// message fails to marshal — impossible for a slice that already survived
// digestAndSizeSessionMessages, so save-path callers can treat nil as a
// warn-only failure.
func BuildSessionDisplayIndex(messages []provider.Message, revision int64, revisionKnown bool, digest [sha256.Size]byte) *SessionDisplayIndex {
	idx, _ := BuildSessionDisplayIndexContext(context.Background(), messages, revision, revisionKnown, digest)
	return idx
}

func BuildSessionDisplayIndexContext(ctx context.Context, messages []provider.Message, revision int64, revisionKnown bool, digest [sha256.Size]byte) (*SessionDisplayIndex, error) {
	preview, _ := SessionPreviewFromMessages(messages)
	idx := &SessionDisplayIndex{
		SchemaVersion:       SessionDisplayIndexSchemaVersion,
		Revision:            revision,
		RevisionKnown:       revisionKnown,
		ContentDigest:       digestString(digest),
		MessageCount:        len(messages),
		ListingPreview:      preview,
		ListingPreviewKnown: true,
		Entries:             make([]DisplayIndexEntry, 0, len(messages)),
		UpdatedAt:           time.Now().UTC(),
	}
	if _, _, err := encodeDisplayIndexEntriesContext(ctx, idx, messages, 0, 0, 0); err != nil {
		return nil, err
	}
	return idx, nil
}

// encodeDisplayIndexEntries appends the entries for msgs[startIndex:] to idx,
// beginning at byte offset and authored-turn count turn. The encoding must
// match writeSessionMessages byte-for-byte (json.Marshal + '\n'), NOT the
// digest encoding: the digest zeroes CreatedAt while the transcript file
// stores the real bytes, and the offsets describe the file.
func encodeDisplayIndexEntries(idx *SessionDisplayIndex, msgs []provider.Message, startIndex int, offset int64, turn int) (int64, int, error) {
	return encodeDisplayIndexEntriesContext(context.Background(), idx, msgs, startIndex, offset, turn)
}

func encodeDisplayIndexEntriesContext(ctx context.Context, idx *SessionDisplayIndex, msgs []provider.Message, startIndex int, offset int64, turn int) (int64, int, error) {
	for i := startIndex; i < len(msgs); i++ {
		b, err := marshalJSONContext(ctx, msgs[i])
		if err != nil {
			return 0, 0, fmt.Errorf("encode message %d: %w", i, err)
		}
		entry, nextTurn := classifyDisplayIndexMessage(msgs[i], i, offset, int64(len(b))+1, turn)
		turn = nextTurn
		idx.Entries = append(idx.Entries, entry)
		offset += int64(len(b)) + 1
	}
	idx.AuthoredTurns = turn
	idx.TranscriptSize = offset
	return offset, turn, nil
}

// classifyDisplayIndexMessage fills one entry's metadata from the message
// alone — no body retention, no cross-message state beyond the running turn
// counter.
func classifyDisplayIndexMessage(m provider.Message, index int, offset, length int64, turn int) (DisplayIndexEntry, int) {
	entry := DisplayIndexEntry{
		Index:                 index,
		Offset:                offset,
		Length:                length,
		Role:                  m.Role,
		AuthoredTurn:          turn,
		HasImages:             len(m.Images) > 0,
		HasToolCalls:          len(m.ToolCalls) > 0,
		LocalOnly:             m.LocalOnly,
		ToolResult:            m.Role == provider.RoleTool,
		PinnedContextRevision: IsPinnedContextRevision(m),
	}
	if m.Role == provider.RoleUser {
		switch {
		case IsUserAuthoredTurnMessage(m):
			turn++
			entry.AuthoredTurn = turn
			entry.StartsTurn = true
		case IsHostGeneratedUserMessage(m):
			entry.Synthetic = true
		default:
			if _, isSteer := SteerText(m.Content); isSteer {
				entry.Steer = true
			}
		}
	}
	return entry, turn
}

// extendSessionDisplayIndex incrementally extends the previous index when the
// save was append-only: the on-disk transcript was verified to be a prefix of
// msgs, so when the previous index describes exactly that prefix (its revision
// is the base revision this save built on and its entry count matches the
// append boundary), its entries stay valid — the canonical encoding is
// prefix-stable — and only the tail needs encoding. Any doubt returns nil and
// the caller rebuilds from the full slice.
func extendSessionDisplayIndex(indexPath string, msgs []provider.Message, digest [sha256.Size]byte, revision int64, appendFrom int) *SessionDisplayIndex {
	prev, err := LoadSessionDisplayIndex(indexPath)
	if err != nil || prev == nil {
		return nil
	}
	if prev.MessageCount != appendFrom || !prev.RevisionKnown || prev.Revision != revision-1 {
		return nil
	}
	preview, _ := SessionPreviewFromMessages(msgs)
	idx := &SessionDisplayIndex{
		SchemaVersion:       SessionDisplayIndexSchemaVersion,
		Revision:            revision,
		RevisionKnown:       true,
		ContentDigest:       digestString(digest),
		MessageCount:        len(msgs),
		ListingPreview:      preview,
		ListingPreviewKnown: true,
		Entries:             make([]DisplayIndexEntry, 0, len(msgs)),
		UpdatedAt:           time.Now().UTC(),
	}
	idx.Entries = append(idx.Entries, prev.Entries...)
	if _, _, err := encodeDisplayIndexEntries(idx, msgs, appendFrom, prev.TranscriptSize, prev.AuthoredTurns); err != nil {
		return nil
	}
	return idx
}

// refreshSessionDisplayIndex republishes the display index after a successful
// save. appendFrom >= 0 hints that the previous on-disk revision is a strict
// prefix of msgs (an append-only save), allowing an incremental extension; any
// other save shape rebuilds from the full slice. Rewind, compaction, and other
// rewrites change revision+digest, so they land here as rebuilds naturally.
func refreshSessionDisplayIndex(path string, msgs []provider.Message, digest [sha256.Size]byte, revision int64, appendFrom int) error {
	indexPath := store.SessionDisplayIndex(path)
	if indexPath == "" {
		return nil
	}
	var idx *SessionDisplayIndex
	if appendFrom > 0 {
		idx = extendSessionDisplayIndex(indexPath, msgs, digest, revision, appendFrom)
	}
	if idx == nil {
		idx = BuildSessionDisplayIndex(msgs, revision, true, digest)
	}
	if idx == nil {
		return fmt.Errorf("encode session display index")
	}
	return WriteSessionDisplayIndex(indexPath, idx)
}

// RefreshSessionDisplayIndexFromReadModel is the task 123-tail cold-path
// incremental repair (187 prefix-rescan family). Save appends the read model
// (jsonl) BEFORE refreshing the display index, and the index refresh is
// warn-only — so the common stale shape is "read model already has the tail,
// index identity lags". A cold open that fails Validate used to fall through
// to the authoritative full replay (measured 1290ms on a 15MiB transcript,
// the reconcile bottleneck). This function re-derives ONLY the transcript
// bytes past the index's TranscriptSize and extends the index in place —
// no full decode, no event-log replay.
//
// Preconditions (any failure returns false; callers keep the full path):
//  1. the sidecar index loads and its entries are self-consistent
//     (last entry end == TranscriptSize, size on a line boundary);
//  2. the transcript GREW past the recorded size (append-only tail);
//  3. the tail decodes cleanly to complete provider messages;
//  4. alignment with the authoritative counts: the session event index's
//     MessageCount == index count + tail count, and its revision/digest
//     match the ledger identity — the tail provably reaches exactly the
//     authoritative end, so stamping the ledger identity cannot lie.
func RefreshSessionDisplayIndexFromReadModel(path string) (bool, error) {
	// Every refusal logs its stage at Debug (observability only — no runtime
	// behavior change): the guards are deliberately conservative, and an
	// operator must be able to see WHICH guard declined instead of only the
	// fallback cost.
	refuse := func(stage string, kv ...any) (bool, error) {
		slog.Debug("agent: display index tail extension declined",
			append([]any{"stage", stage, "path", path}, kv...)...)
		return false, nil
	}
	if path == "" {
		return false, nil
	}
	indexPath := store.SessionDisplayIndex(path)
	prev, err := LoadSessionDisplayIndex(indexPath)
	if err != nil || prev == nil || prev.SchemaVersion != SessionDisplayIndexSchemaVersion {
		return refuse("load_index", "err", err, "have", prev != nil)
	}
	if len(prev.Entries) != prev.MessageCount {
		return refuse("entries_count_mismatch", "entries", len(prev.Entries), "count", prev.MessageCount)
	}
	if prev.TranscriptSize > 0 {
		last := prev.Entries[len(prev.Entries)-1]
		if last.Offset+last.Length != prev.TranscriptSize {
			return refuse("entries_tail_mismatch", "end", last.Offset+last.Length, "size", prev.TranscriptSize)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return refuse("stat", "err", err)
	}
	if info.Size() <= prev.TranscriptSize {
		return refuse("no_growth", "file", info.Size(), "index", prev.TranscriptSize)
	}
	// Line-boundary guard: a mid-line index size means the transcript was
	// rewritten underneath, not appended — never extend over a rewrite.
	if prev.TranscriptSize > 0 {
		f, err := os.Open(path)
		if err != nil {
			return refuse("probe_open", "err", err)
		}
		probe := make([]byte, 1)
		_, err = f.ReadAt(probe, prev.TranscriptSize-1)
		f.Close()
		if err != nil || (probe[0] != '\n' && probe[0] != '\r') {
			return refuse("not_line_boundary", "err", err, "byte", probe[0])
		}
	}

	identity, identityKnown, err := SessionContentIdentity(path)
	if err != nil || !identityKnown {
		return refuse("identity", "err", err, "known", identityKnown)
	}
	evtIdx, err := readSessionEventIndex(path)
	if err != nil || evtIdx == nil {
		return refuse("event_index", "err", err, "have", evtIdx != nil)
	}

	// Read only the tail the index has never seen; a trailing partial line
	// (a save mid-write) aborts — the next request retries after the save lands.
	tail, complete, err := readTranscriptTail(path, prev.TranscriptSize)
	if err != nil || !complete || len(tail) == 0 {
		return refuse("tail_read", "err", err, "complete", complete, "count", len(tail))
	}
	if evtIdx.Revision != identity.Revision || evtIdx.ContentDigest != identity.DigestHex {
		return refuse("event_index_vs_ledger",
			"evt_rev", evtIdx.Revision, "ledger_rev", identity.Revision,
			"evt_digest", evtIdx.ContentDigest[:min(12, len(evtIdx.ContentDigest))],
			"ledger_digest", identity.DigestHex[:min(12, len(identity.DigestHex))])
	}
	if evtIdx.MessageCount != prev.MessageCount+len(tail) {
		return refuse("alignment",
			"evt_count", evtIdx.MessageCount, "index_count", prev.MessageCount, "tail_count", len(tail))
	}

	idx := *prev // shallow copy; entries are replaced below
	idx.Entries = append(make([]DisplayIndexEntry, 0, prev.MessageCount+len(tail)), prev.Entries...)
	offset := prev.TranscriptSize
	turn := prev.AuthoredTurns
	for k, m := range tail {
		b, err := json.Marshal(m)
		if err != nil {
			return false, err
		}
		entry, nextTurn := classifyDisplayIndexMessage(m, prev.MessageCount+k, offset, int64(len(b))+1, turn)
		turn = nextTurn
		idx.Entries = append(idx.Entries, entry)
		offset += int64(len(b)) + 1
	}
	if offset != info.Size() {
		return refuse("encode_size_drift", "encoded", offset, "file", info.Size())
	}
	idx.MessageCount = prev.MessageCount + len(tail)
	idx.AuthoredTurns = turn
	idx.TranscriptSize = offset
	idx.Revision = identity.Revision
	idx.RevisionKnown = true
	idx.ContentDigest = identity.DigestHex
	idx.UpdatedAt = time.Now().UTC()
	// ListingPreview stays the previous one: computing it needs the full
	// message slice (the cost this function exists to avoid). It is a derived
	// preview; the background full repair refreshes it.
	if err := WriteSessionDisplayIndex(indexPath, &idx); err != nil {
		return false, err
	}
	slog.Info("agent: display index extended from read-model tail",
		"path", path, "appended", len(tail), "from_bytes", prev.TranscriptSize)
	return true, nil
}

// readTranscriptTail decodes complete JSON-message lines starting at offset.
// complete=false means the file ends mid-line (a save is still writing).
func readTranscriptTail(path string, offset int64) (msgs []provider.Message, complete bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, err
	}
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil {
			if readErr == io.EOF {
				if len(line) == 0 {
					return msgs, true, nil
				}
				return nil, false, nil // partial trailing line (save still writing)
			}
			return nil, false, readErr
		}
		trimmed := bytesTrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var m provider.Message
		if err := json.Unmarshal(trimmed, &m); err != nil {
			return nil, false, fmt.Errorf("decode transcript tail line: %w", err)
		}
		msgs = append(msgs, m)
	}
}

// bytesTrimSpace avoids importing bytes for one ASCII trim of JSON lines.
func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

// WriteSessionDisplayIndex publishes the index atomically (tmp + fsync +
// rename) with 0600 permissions, matching the other session sidecars.
func WriteSessionDisplayIndex(path string, idx *SessionDisplayIndex) error {
	return WriteSessionDisplayIndexContext(context.Background(), path, idx)
}

func WriteSessionDisplayIndexContext(ctx context.Context, path string, idx *SessionDisplayIndex) error {
	if path == "" {
		return fmt.Errorf("empty session display index path")
	}
	b, err := marshalJSONIndentContext(ctx, idx)
	if err != nil {
		return fmt.Errorf("encode session display index: %w", err)
	}
	b = append(b, '\n')
	if err := atomicWriteFileContext(ctx, path, ".session-display-index.*.tmp", "atomic-write", b, 0o600, true); err != nil {
		return fmt.Errorf("write session display index: %w", err)
	}
	return nil
}

// LoadSessionDisplayIndex reads the sidecar, rejecting truncated JSON,
// unsupported schema versions, and header/entry-count disagreements so a
// corrupt index fails closed into the rescan fallback.
func LoadSessionDisplayIndex(path string) (*SessionDisplayIndex, error) {
	if path == "" {
		return nil, fmt.Errorf("empty session display index path")
	}
	b, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return nil, err
	}
	var idx SessionDisplayIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("decode session display index: %w", err)
	}
	if idx.SchemaVersion != SessionDisplayIndexSchemaVersion {
		return nil, fmt.Errorf("unsupported session display index schema %d", idx.SchemaVersion)
	}
	if idx.MessageCount != len(idx.Entries) {
		return nil, fmt.Errorf("session display index message_count %d does not match %d entries", idx.MessageCount, len(idx.Entries))
	}
	expectedOffset := int64(0)
	for i, entry := range idx.Entries {
		if entry.Index != i {
			return nil, fmt.Errorf("session display index entry %d has index %d", i, entry.Index)
		}
		if entry.Offset != expectedOffset || entry.Length <= 0 || entry.Length > idx.TranscriptSize-entry.Offset {
			return nil, fmt.Errorf("session display index entry %d has invalid range %d+%d", i, entry.Offset, entry.Length)
		}
		expectedOffset += entry.Length
	}
	if expectedOffset != idx.TranscriptSize {
		return nil, fmt.Errorf("session display index covers %d bytes, transcript_size is %d", expectedOffset, idx.TranscriptSize)
	}
	return &idx, nil
}

// ValidateSessionDisplayIndex reports whether the index still describes the
// transcript identified by revision/digest/transcriptSize. The caller picks
// transcriptSize: the canonical encoding size when checking in-memory state,
// the anchor's actual file size when checking the on-disk .jsonl.
func ValidateSessionDisplayIndex(idx *SessionDisplayIndex, revision int64, revisionKnown bool, digest [sha256.Size]byte, transcriptSize int64) bool {
	if idx == nil || idx.SchemaVersion != SessionDisplayIndexSchemaVersion {
		return false
	}
	if idx.RevisionKnown != revisionKnown || (revisionKnown && idx.Revision != revision) {
		return false
	}
	if idx.ContentDigest != digestString(digest) {
		return false
	}
	if idx.TranscriptSize != transcriptSize {
		return false
	}
	return idx.MessageCount == len(idx.Entries)
}

// ScanSessionDisplayIndex is the recovery path for a missing, corrupt, or
// stale index: it streams the .jsonl transcript line by line (lines can be
// megabytes when a message carries Images, so the file is never loaded whole),
// decoding each line only as far as classification requires and recording the
// real byte offsets. The content digest is rebuilt alongside so the scanned
// index validates exactly like a built one; the revision is not recoverable
// from the transcript alone and stays unknown. A line that does not decode
// fails the scan — the caller falls back to a full parse.
func ScanSessionDisplayIndex(transcriptPath string) (*SessionDisplayIndex, error) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	idx := &SessionDisplayIndex{
		SchemaVersion: SessionDisplayIndexSchemaVersion,
		Entries:       []DisplayIndexEntry{},
		UpdatedAt:     time.Now().UTC(),
	}
	h := sha256.New()
	reader := bufio.NewReaderSize(f, 1<<20)
	offset := int64(0)
	turn := 0
	for {
		line, readErr := readSessionDisplayIndexLine(reader)
		if len(line) > 0 {
			var m provider.Message
			if err := json.Unmarshal(line, &m); err != nil {
				return nil, fmt.Errorf("decode session transcript line %d: %w", len(idx.Entries), err)
			}
			identity, err := json.Marshal(messageForSessionIdentity(m))
			if err != nil {
				return nil, fmt.Errorf("re-encode session transcript line %d: %w", len(idx.Entries), err)
			}
			h.Write(identity)
			h.Write([]byte{'\n'})
			var entry DisplayIndexEntry
			entry, turn = classifyDisplayIndexMessage(m, len(idx.Entries), offset, int64(len(line)), turn)
			if entry.StartsTurn && !idx.ListingPreviewKnown {
				preview := truncatePreview(previewProse(UserMessageText(m)))
				if preview != "" {
					idx.ListingPreview = preview
					idx.ListingPreviewKnown = true
				}
			}
			idx.Entries = append(idx.Entries, entry)
			offset += int64(len(line))
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read session transcript: %w", readErr)
		}
	}
	idx.MessageCount = len(idx.Entries)
	idx.AuthoredTurns = turn
	if !idx.ListingPreviewKnown {
		idx.ListingPreviewKnown = true
	}
	idx.TranscriptSize = offset
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	idx.ContentDigest = digestString(digest)
	return idx, nil
}

// readSessionDisplayIndexLine preserves the exact byte range of one JSONL
// record while enforcing a hard per-record allocation cap. bufio.Reader's
// ReadBytes grows until it finds a delimiter, which turns a malformed giant
// line into an unbounded allocation before ScanSessionDisplayIndex can reject
// it.
func readSessionDisplayIndexLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > sessionDisplayIndexMaxLineBytes-len(line) {
			return nil, fmt.Errorf("session transcript line exceeds %d bytes", sessionDisplayIndexMaxLineBytes)
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}
