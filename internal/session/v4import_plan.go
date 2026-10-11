package session

// Task 805: v4→v3 backfill planning. The v4 store (sessions-v4, by-id) is read
// read-only and diffed against the legacy store (sessions/, one .jsonl per
// session); the plan reports, per v4 session, what a backfill run would do
// BEFORE anything is written:
//
//	skip      nothing to do (v3 already covers the v4 projection, or the v4
//	          session has no recoverable v3 identity and the v3 store exists)
//	append    the v3 transcript is a strict prefix of the v4 projection — the
//	          missing tail is appended (idempotent: re-running plans skip)
//	rebuild   the v3 transcript is missing (deleted file or a full v3 store
//	          rebuild) — the whole projection is written back
//	conflict  the two copies diverged (neither is a prefix of the other, or the
//	          v3 file does not decode) — nothing is written, the entry reports
//	fail      the v4 session itself could not be read
//
// The mapping anchor follows the two identities the fork actually mints:
// manifest.Source.Path (MigrateLegacy products) and the deterministic
// "bridge-<sha256>" session id (session_v4_bridge). Sessions with neither
// anchor are only rebuilt when the v3 store itself is gone (mode full);
// otherwise they are skipped with a reason, because a missing v3 file with an
// unmapped v4 id is indistinguishable from a session the user deleted.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/provider"
)

// V4ImportStatus is one plan entry's route through the backfill.
type V4ImportStatus string

const (
	V4ImportSkip     V4ImportStatus = "skip"
	V4ImportAppend   V4ImportStatus = "append"
	V4ImportRebuild  V4ImportStatus = "rebuild"
	V4ImportConflict V4ImportStatus = "conflict"
	V4ImportFail     V4ImportStatus = "fail"
)

// V4ImportPlanMode names the store-level mode the v3 file existence chose.
const (
	V4ImportModeIncremental = "incremental"
	V4ImportModeFull        = "full"
)

// V4PlanEntry is one v4 session's route through the backfill.
type V4PlanEntry struct {
	SessionID  string         `json:"sessionId"`
	Status     V4ImportStatus `json:"status"`
	Reason     string         `json:"reason,omitempty"`
	TargetPath string         `json:"targetPath,omitempty"`
	Mapping    string         `json:"mapping,omitempty"` // source | bridge-id | ""
	V3Count    int            `json:"v3Count,omitempty"`
	V4Count    int            `json:"v4Count,omitempty"`
	TailCount  int            `json:"tailCount,omitempty"`
	Title      string         `json:"title,omitempty"`
	ModelRef   string         `json:"modelRef,omitempty"`
	CreatedAt  time.Time      `json:"createdAt,omitempty"`
	UpdatedAt  time.Time      `json:"updatedAt,omitempty"`
	LogBytes   int64          `json:"logBytes,omitempty"`
}

// V4ImportPlan is the dry-run result: what a run would do, before it does it.
type V4ImportPlan struct {
	Mode              string        `json:"mode"`
	V4Root            string        `json:"v4Root"`
	V3Dirs            []string      `json:"v3Dirs"`
	Entries           []V4PlanEntry `json:"entries"`
	SkipCount         int           `json:"skipCount"`
	AppendCount       int           `json:"appendCount"`
	RebuildCount      int           `json:"rebuildCount"`
	ConflictCount     int           `json:"conflictCount"`
	FailCount         int           `json:"failCount"`
	TotalTailMessages int           `json:"totalTailMessages"`
	TotalRebuildMsgs  int           `json:"totalRebuildMessages"`
	GeneratedAt       time.Time     `json:"generatedAt"`
}

// V4ImportOptions bounds one plan/run. The kernel stays free of config
// wiring: every path arrives from the caller (same contract as V5ImportOptions).
type V4ImportOptions struct {
	// V4Root is the by-id v4 store (config.SessionStoreDir() at the call site).
	V4Root string
	// V3Dirs are the candidate legacy stores scanned for mapping anchors
	// (config.SessionDir() and any project session dirs at the call site).
	V3Dirs []string
	// MintRebuildPath mints the legacy transcript path for a session rebuilt
	// under a fresh identity (unmapped, full-rebuild mode). Nil falls back to
	// agent.NewSessionPath(RebuildDir, label).
	MintRebuildPath func(info SessionInfo) (string, error)
	// RebuildDir is the fallback directory for MintRebuildPath.
	RebuildDir string
}

// PlanV4Import walks the v4 store and diffs every session against the legacy
// store. It never writes: v4 opens are read-only, v3 opens are reads.
func PlanV4Import(ctx context.Context, opts V4ImportOptions) (*V4ImportPlan, error) {
	root := strings.TrimSpace(opts.V4Root)
	if root == "" {
		return nil, fmt.Errorf("session v4 import: v4 root is required")
	}
	if len(opts.V3Dirs) == 0 {
		return nil, fmt.Errorf("session v4 import: at least one v3 directory is required")
	}
	persistence := NewFilesystemPersistence(root)

	v3Files := scanLegacyTranscripts(opts.V3Dirs)
	mode := V4ImportModeIncremental
	if len(v3Files) == 0 && !anyDirExists(opts.V3Dirs) {
		mode = V4ImportModeFull
	}

	ids, err := enumerateV4StoreIDs(root)
	if err != nil {
		return nil, err
	}
	plan := &V4ImportPlan{
		Mode:        mode,
		V4Root:      root,
		V3Dirs:      opts.V3Dirs,
		GeneratedAt: time.Now().UTC(),
		Entries:     make([]V4PlanEntry, 0, len(ids)),
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := planV4Session(ctx, persistence, id, v3Files, mode, opts)
		plan.Entries = append(plan.Entries, entry)
		switch entry.Status {
		case V4ImportSkip:
			plan.SkipCount++
		case V4ImportAppend:
			plan.AppendCount++
			plan.TotalTailMessages += entry.TailCount
		case V4ImportRebuild:
			plan.RebuildCount++
			plan.TotalRebuildMsgs += entry.V4Count
		case V4ImportConflict:
			plan.ConflictCount++
		case V4ImportFail:
			plan.FailCount++
		}
	}
	sortPlanEntries(plan.Entries)
	return plan, nil
}

func sortPlanEntries(entries []V4PlanEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].SessionID < entries[j].SessionID
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
}

// legacyFile is one scanned v3 transcript with both mapping anchors.
type legacyFile struct {
	path   string // absolute .jsonl path
	clean  string // filepath.Clean form (Source.Path comparisons)
	bridge string // deterministic bridge id derived from the path
}

// scanLegacyTranscripts maps every v3 identity the caller knows about:
// by cleaned path (for manifest.Source.Path matches) and by the deterministic
// bridge id the v4 bridge derives from the transcript path.
func scanLegacyTranscripts(dirs []string) map[string]legacyFile {
	out := map[string]legacyFile{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			clean := filepath.Clean(path)
			key := strings.ToLower(clean)
			if _, seen := out[key]; seen {
				continue
			}
			out[key] = legacyFile{path: path, clean: clean, bridge: bridgeSessionIDForPath(clean)}
		}
	}
	return out
}

func anyDirExists(dirs []string) bool {
	for _, dir := range dirs {
		if info, err := os.Stat(strings.TrimSpace(dir)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// bridgeSessionIDForPath recomputes the deterministic v4 session id the bridge
// mints for a transcript path (control.SessionV4Bridge.deterministicSessionID).
// The constant prefix must stay in step with that function.
func bridgeSessionIDForPath(agentPath string) string {
	sum := sha256.Sum256([]byte("v4bridge\x00" + agentPath))
	return "bridge-" + hex.EncodeToString(sum[:12])
}

// enumerateV4StoreIDs lists the by-id session directories that carry a
// manifest, newest activity first.
func enumerateV4StoreIDs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("session v4 import: v4 store %s does not exist", root)
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name(), "manifest.json")); err != nil {
			continue
		}
		ids = append(ids, entry.Name())
	}
	sort.Strings(ids)
	return ids, nil
}

// planV4Session diffs one v4 session against the legacy store.
func planV4Session(ctx context.Context, persistence *FilesystemPersistence, id string, v3Files map[string]legacyFile, mode string, opts V4ImportOptions) V4PlanEntry {
	entry := V4PlanEntry{SessionID: id, Status: V4ImportFail}
	info, err := persistence.Stat(ctx, id)
	if err != nil {
		entry.Reason = "v4 session metadata: " + err.Error()
		return entry
	}
	entry.Title, entry.ModelRef = info.Title, info.ModelRef
	entry.CreatedAt, entry.UpdatedAt = info.CreatedAt, info.UpdatedAt

	messages, err := readV4Projection(ctx, persistence, id)
	if err != nil {
		entry.Reason = "v4 projection: " + err.Error()
		return entry
	}
	entry.V4Count = len(messages)
	entry.LogBytes = logSizeFor(persistence.Root, id)
	if len(messages) == 0 {
		entry.Status = V4ImportSkip
		entry.Reason = "v4 projection is empty"
		return entry
	}

	mapping, target := resolveV3Target(id, info, v3Files)
	entry.Mapping = mapping
	if target == "" {
		// No v3 identity: a deleted transcript with an unmapped v4 id is
		// indistinguishable from a session the user deleted on purpose, so the
		// incremental mode leaves it alone; a full rebuild restores everything.
		entry.Status = V4ImportSkip
		entry.Reason = "no corresponding v3 transcript (the v4 copy has no recoverable v3 identity)"
		if mode == V4ImportModeFull {
			minted, mintErr := mintRebuildPath(opts, info)
			if mintErr != nil {
				entry.Reason = "mint rebuild path: " + mintErr.Error()
				return entry
			}
			target = minted
			entry.Mapping = "new"
		}
		if target == "" {
			return entry
		}
	}
	entry.TargetPath = target

	// A missing v3 transcript is a rebuild; an unreadable one is a conflict.
	messages3, loadErr := loadLegacyMessages(target)
	if os.IsNotExist(loadErr) {
		entry.Status = V4ImportRebuild
		entry.Reason = "v3 transcript is missing; the v4 projection restores it"
		return entry
	}
	if loadErr != nil {
		entry.Status = V4ImportConflict
		entry.Reason = "v3 transcript does not decode: " + loadErr.Error()
		return entry
	}
	entry.V3Count = len(messages3)
	tail, verdict := diffLegacyAgainstV4(messages3, messages)
	switch verdict {
	case diffEqual:
		entry.Status = V4ImportSkip
		entry.Reason = "v3 already covers the v4 projection"
	case diffLegacyAhead:
		entry.Status = V4ImportSkip
		entry.Reason = "v4 lags behind v3; nothing to backfill"
	case diffLegacyPrefix:
		entry.Status = V4ImportAppend
		entry.TailCount = len(tail)
		entry.Reason = fmt.Sprintf("append %d message(s) the v3 transcript is missing", len(tail))
	default:
		entry.Status = V4ImportConflict
		entry.Reason = "the two copies diverged; nothing is written (rewinding v3 is a user decision, not a backfill)"
	}
	return entry
}

// resolveV3Target finds the v3 transcript a v4 session belongs to.
func resolveV3Target(id string, info SessionInfo, v3Files map[string]legacyFile) (mapping, target string) {
	// Anchor 1: the migration source recorded by MigrateLegacy (manifest.source.path).
	if source := readV4ManifestSource(info.Path); source != "" {
		// The recorded source is authoritative even when the file is gone:
		// a rebuild restores the exact transcript identity it had.
		return "source", filepath.Clean(source)
	}
	// Anchor 2: the deterministic bridge id derived from the transcript path.
	for _, file := range v3Files {
		if file.bridge == id {
			return "bridge-id", file.clean
		}
	}
	return "", ""
}

// readV4ManifestSource returns manifest.source.path for a by-id directory.
func readV4ManifestSource(v4Dir string) string {
	if strings.TrimSpace(v4Dir) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(v4Dir, "manifest.json"))
	if err != nil {
		return ""
	}
	var manifest Manifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Source == nil {
		return ""
	}
	return strings.TrimSpace(manifest.Source.Path)
}

func mintRebuildPath(opts V4ImportOptions, info SessionInfo) (string, error) {
	if opts.MintRebuildPath != nil {
		return opts.MintRebuildPath(info)
	}
	dir := strings.TrimSpace(opts.RebuildDir)
	if dir == "" {
		return "", fmt.Errorf("no rebuild directory configured")
	}
	// The name derives from the v4 session id, not the clock: plan and run
	// must agree on the target, and a re-run of a completed rebuild must plan
	// as skip instead of minting a second copy.
	name := sanitizeSessionFileName(info.SessionID)
	path := filepath.Join(dir, name+".jsonl")
	for suffix := 2; ; suffix++ {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		// An existing empty remnant (a crash leftover) is replaceable.
		if info.Size() == 0 {
			return path, nil
		}
		path = filepath.Join(dir, name+"-"+strconv.Itoa(suffix)+".jsonl")
	}
}

// sanitizeSessionFileName maps a v4 session id onto a safe file stem.
func sanitizeSessionFileName(id string) string {
	safe := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "<", "-", ">", "-", "\"", "-", "|", "-", "?", "-", "*", "-").Replace(id)
	if safe == "" {
		return "session"
	}
	return safe
}

// readV4Projection folds the durable v4 commits into the message projection —
// the same loop Query.Snapshot runs, without a live service.
func readV4Projection(ctx context.Context, persistence *FilesystemPersistence, id string) ([]provider.Message, error) {
	handle, err := persistence.Open(id, ReadOnly)
	if err != nil {
		return nil, err
	}
	defer handle.Close(context.WithoutCancel(ctx))
	projection := Projection{}
	var cursor uint64
	for {
		page, readErr := handle.Read(ctx, cursor, 1000)
		if readErr != nil {
			return nil, readErr
		}
		for _, commit := range page.Commits {
			if err := applyProjectionCommit(&projection, commit); err != nil {
				return nil, err
			}
		}
		if !page.Truncated {
			break
		}
		if page.Next <= cursor {
			return nil, fmt.Errorf("%w: cold history cursor did not advance", ErrDamagedStore)
		}
		cursor = page.Next
	}
	return append([]provider.Message(nil), projection.Messages...), nil
}

// logSizeFor stats the framed log of one by-id session (a display byte count;
// a missing log is not an error).
func logSizeFor(root, id string) int64 {
	if info, err := os.Stat(filepath.Join(root, id, currentLogName)); err == nil {
		return info.Size()
	}
	return 0
}

// loadLegacyMessages decodes a v3 transcript the way the fork's own readers
// do (one provider.Message per line). LoadSession repairs and locks; for the
// diff only the plain rows are needed, so the decoder runs on the raw file.
func loadLegacyMessages(path string) ([]provider.Message, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var messages []provider.Message
	decoder := json.NewDecoder(file)
	for {
		var message provider.Message
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				return messages, nil
			}
			return messages, fmt.Errorf("line %d: %w", len(messages)+1, err)
		}
		messages = append(messages, message)
	}
}

// messageDigest is the idempotency anchor: one transcript row, hashed over its
// canonical encoding. Both stores produce provider.Message rows through the
// same Go encoder, so equal messages hash equal on both sides.
func messageDigest(message provider.Message) string {
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Sprintf("err:%v", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type diffVerdict int

const (
	diffDiverged diffVerdict = iota
	diffEqual
	diffLegacyPrefix // v3 is a strict prefix of v4: a tail can be appended
	diffLegacyAhead  // v4 is a (strict) prefix of v3: v4 lags, nothing to do
)

// diffLegacyAgainstV4 compares the two transcripts row by row.
func diffLegacyAgainstV4(v3, v4 []provider.Message) (tail []provider.Message, verdict diffVerdict) {
	n := len(v3)
	if n > len(v4) {
		n = len(v4)
	}
	for i := 0; i < n; i++ {
		if messageDigest(v3[i]) != messageDigest(v4[i]) {
			return nil, diffDiverged
		}
	}
	switch {
	case len(v3) == len(v4):
		return nil, diffEqual
	case len(v3) < len(v4):
		return v4[len(v3):], diffLegacyPrefix
	default:
		return nil, diffLegacyAhead
	}
}
