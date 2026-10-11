package session

// Task 773: read-only importer for upstream Desktop v5 stores
// (desktop-sessions-v5/by-id + workspace-state-v1.json + header.json +
// v4 rev0-3 framed logs) into this fork's legacy v3 store.
//
// The v5 store is never written: every open is read-only, payloads resolve
// through the digest-verified content store, and the whole import targets a
// staged legacy directory that publishes only after verification (see
// v5import_migrate.go). The frame scanner and content store are the fork's own
// production pieces (v4_codec.go, internal/sessioncontent); what is new here is
// the manifest gate widening (upstream normalized rev1 stores to rev2/rev3 in
// the field — 1.38.10/1.38.11 — while this fork's write-side gate still pins
// rev1), the by-id enumeration, the header, and the message folding that
// includes upstream-only kinds (message/retract).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/sessioncontent"
)

const (
	// V5ImportHeaderName is the immutable Desktop ownership file inside a
	// by-id session directory (upstream internal/session/header.go).
	V5ImportHeaderName = "header.json"
	// v5ImportMaxManifestBytes bounds manifest/header reads like upstream's
	// own readers do, so a damaged store cannot balloon memory.
	v5ImportMaxManifestBytes = 1 << 20
)

// ErrV5ImportLayout marks source trees that are not a v5 by-id store.
var ErrV5ImportLayout = errors.New("v5 import: source is not a desktop-sessions-v5/by-id store")

// V5Manifest is the on-disk manifest of a v4-framed session under by-id.
// Upstream accepts StorageRevision 0..3 and normalizes stores to 3 on open;
// this reader accepts the same span read-only without normalizing anything.
type V5Manifest struct {
	SchemaVersion    int       `json:"schemaVersion"`
	Codec            string    `json:"codec"`
	StorageRevision  int       `json:"storageRevision,omitempty"`
	ContentRoot      string    `json:"contentRoot,omitempty"`
	SessionID        string    `json:"sessionId"`
	CreatedAt        time.Time `json:"createdAt"`
	WriterGeneration uint64    `json:"writerGeneration"`
}

// v5ImportSupportedManifest accepts every upstream-written v4 generation:
// rev 0 (draft-era), rev 1 (≤1.38.9), rev 2 (1.38.10, content-v1 payloads),
// rev 3 (1.38.11+, offset index + identity). The physical frame format is
// shared across the span; only auxiliary indexes differ, and those are
// reader-side accelerators this importer does not need.
func v5ImportSupportedManifest(m V5Manifest) bool {
	return m.SchemaVersion == V4SchemaVersion && m.Codec == V4Codec &&
		m.StorageRevision >= 0 && m.StorageRevision <= 3
}

// V5Header is the v5 identity head (upstream SessionHeader). Display and model
// projections deliberately do not live here (upstream keeps them in Session).
type V5Header struct {
	SchemaVersion   int       `json:"schemaVersion"`
	SessionID       string    `json:"sessionId"`
	CreatedAt       time.Time `json:"createdAt"`
	CWD             string    `json:"cwd"`
	ParentSessionID string    `json:"parentSessionId,omitempty"`
	Origin          string    `json:"origin"`
}

// V5Session is one by-id session directory opened read-only.
type V5Session struct {
	ID       string
	Dir      string
	Manifest V5Manifest
	Header   V5Header
	// HeaderMissing records that the directory carried no header.json. The
	// manifest still names the session; the header only adds cwd/origin.
	HeaderMissing bool
	// LogSize and LogModTime describe events.frames at open time.
	LogSize    int64
	LogModTime time.Time
	// AuxFiles are the rev3 auxiliary artifacts (events.offset-index.json,
	// storage.identity.json) recorded for the plan report. They are never read.
	AuxFiles []string
}

// contentStoreForV5Session resolves the content pool for one by-id session the
// same two ways the fork's own writer layout allows: the shared pool sibling
// of the session directory (the by-id root's .content-v1) or a session-local
// pool when the manifest names it. Any other contentRoot spelling is refused
// so a manifest cannot point a read outside the store.
func contentStoreForV5Session(dir, named string) (*sessioncontent.Store, error) {
	root := filepath.Join(filepath.Dir(dir), ".content-v1")
	if trimmed := strings.TrimSpace(named); trimmed != "" && trimmed != sharedContentRoot {
		candidate := filepath.Clean(filepath.Join(dir, trimmed))
		relative, relErr := filepath.Rel(dir, candidate)
		if relErr != nil || (relative != ".content-v1" && relative != sharedContentRoot) {
			return nil, fmt.Errorf("%w: manifest contentRoot %q", ErrV5ImportLayout, named)
		}
		root = candidate
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			// A store whose payloads are all inline has no pool at all.
			return nil, nil
		}
		return nil, err
	}
	return sessioncontent.New(root), nil
}

// readV5JSONFile reads one bounded JSON file inside the session directory.
func readV5JSONFile(path string, into any) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(b) > v5ImportMaxManifestBytes {
		return false, fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), v5ImportMaxManifestBytes)
	}
	if err := json.Unmarshal(b, into); err != nil {
		return false, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return true, nil
}

// OpenV5Session opens one by-id session directory read-only. The manifest gate
// accepts upstream rev 0..3; anything else is reported as unsupported so the
// plan can list the reason instead of misreading the store.
func OpenV5Session(dir string) (*V5Session, error) {
	id := filepath.Base(dir)
	if err := ValidateV5ImportSessionID(id); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	if len(raw) > v5ImportMaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest exceeds %d bytes", ErrV5ImportLayout, v5ImportMaxManifestBytes)
	}
	var manifest V5Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if !v5ImportSupportedManifest(manifest) {
		return nil, fmt.Errorf("%w: manifest schema %d codec %q revision %d", ErrUnsupportedVersion, manifest.SchemaVersion, manifest.Codec, manifest.StorageRevision)
	}
	if manifest.SessionID != "" && manifest.SessionID != id {
		return nil, fmt.Errorf("%w: manifest names session %q, directory is %q", ErrDamagedStore, manifest.SessionID, id)
	}
	session := &V5Session{ID: id, Dir: dir, Manifest: manifest}
	headerFound, err := readV5JSONFile(filepath.Join(dir, V5ImportHeaderName), &session.Header)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", V5ImportHeaderName, err)
	}
	session.HeaderMissing = !headerFound
	for _, aux := range []string{"events.offset-index.json", "storage.identity.json"} {
		if _, err := os.Stat(filepath.Join(dir, aux)); err == nil {
			session.AuxFiles = append(session.AuxFiles, aux)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, currentLogName)); err == nil {
		session.LogSize, session.LogModTime = info.Size(), info.ModTime()
	}
	return session, nil
}

// ValidateV5ImportSessionID applies the upstream identity rules before the id
// becomes a file name in this fork's legacy store: local, single element,
// printable, no Windows reserved names.
func ValidateV5ImportSessionID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 255 || strings.HasPrefix(id, ".") || strings.HasSuffix(id, ".") ||
		!filepath.IsLocal(id) || id == "." || filepath.Base(id) != id || strings.ContainsAny(id, `/\<>:"|?*`) {
		return fmt.Errorf("%w: invalid session id %q", ErrV5ImportLayout, id)
	}
	for _, char := range id {
		if char < 0x20 {
			return fmt.Errorf("%w: invalid session id %q", ErrV5ImportLayout, id)
		}
	}
	base := strings.ToUpper(strings.SplitN(id, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
		(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return fmt.Errorf("%w: reserved session id %q", ErrV5ImportLayout, id)
	}
	return nil
}

// OpenV5Store validates that root is a desktop-sessions-v5/by-id tree and
// returns it. The tree is only ever read.
type V5Store struct {
	Root string
}

// OpenV5Store roots the importer at a by-id directory. The path must name a
// directory whose final two elements are desktop-sessions-v5/by-id (the
// upstream persistence root, config.DesktopSessionStoreDir).
func OpenV5Store(root string) (*V5Store, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrV5ImportLayout, root)
	}
	if base := filepath.Base(root); base != "by-id" || filepath.Base(filepath.Dir(root)) != "desktop-sessions-v5" {
		return nil, fmt.Errorf("%w: %s (want .../desktop-sessions-v5/by-id)", ErrV5ImportLayout, root)
	}
	return &V5Store{Root: root}, nil
}

// Sessions enumerates the by-id directories in deterministic order. Entries
// that fail to open are reported per-entry through the visit error channel of
// the plan (the caller decides whether a broken store aborts the run); this
// function itself only fails on an unreadable root.
func (s *V5Store) Sessions() ([]*V5Session, []V5ImportFailure, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, nil, err
	}
	var sessions []*V5Session
	var failures []V5ImportFailure
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		session, openErr := OpenV5Session(filepath.Join(s.Root, entry.Name()))
		if openErr != nil {
			failures = append(failures, V5ImportFailure{SessionID: entry.Name(), Reason: openErr.Error()})
			continue
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	return sessions, failures, nil
}

// v5ImportKnownKinds extends the fork's projection vocabulary with the two
// kinds upstream added after the fork's snapshot (1.38.10+). Unknown required
// kinds still fail the scan, so a future upstream layout is reported instead
// of silently misread.
func v5ImportKnownKinds() map[string]bool {
	known := make(map[string]bool, len(ProjectionKinds)+2)
	for kind := range ProjectionKinds {
		known[kind] = true
	}
	known["message/retract"] = true
	known["session/permission-preset"] = true
	return known
}

// V5Transcript is the materialized conversation of one v5 session.
type V5Transcript struct {
	Messages      []V5ImportMessage
	Title         string
	ModelRef      string
	ModelIdentity string
	// KindCounts counts every event kind seen, so the report can show what
	// the import folded and what it dropped (goal/plan/todo states land in
	// the loss accounting, not silently).
	KindCounts map[string]int
}

// V5ImportMessage is one folded transcript entry with its source identity, so
// post-import verification can compare counts against the projection.
type V5ImportMessage struct {
	ID      string
	Message json.RawMessage
}

// v5ImportMessagesPayload mirrors the wire shape of message-bearing events:
// {"message": {...}} for single-message kinds, {"messages": [...]} and
// {"messageIds": [...]} for replace/retract.
type v5ImportMessagesPayload struct {
	Message       json.RawMessage   `json:"message"`
	Messages      []json.RawMessage `json:"messages"`
	IDs           []string          `json:"messageIds"`
	Title         string            `json:"title"`
	ModelRef      string            `json:"modelRef"`
	ModelIdentity string            `json:"modelIdentity"`
}

// Transcript folds the framed log into the conversation the way upstream and
// this fork both project it: complete appends, upsert replaces by id or
// appends, retract removes, history/replace and legacy/import restart. The
// scan goes through the fork's production frame reader, so checksums, batch
// boundaries and sequence discipline are enforced exactly as at runtime.
func (s *V5Session) Transcript(ctx context.Context) (*V5Transcript, error) {
	content, err := contentStoreForV5Session(s.Dir, s.Manifest.ContentRoot)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(s.Dir, currentLogName))
	if err != nil {
		if os.IsNotExist(err) {
			// A reserved-but-empty by-id entry (pending create) has no log.
			return &V5Transcript{KindCounts: map[string]int{}}, nil
		}
		return nil, err
	}
	defer file.Close()
	folded := &V5Transcript{KindCounts: map[string]int{}}
	byID := map[string]int{}
	var foldErr error
	scanErr := scanV4CommitFileMode(ctx, file, 0, 1, content, v5ImportKnownKinds(), true, func(_ int64, commit Commit) bool {
		for _, event := range commit.Events {
			folded.KindCounts[event.Kind]++
			if err := foldV5ImportEvent(folded, byID, event); err != nil {
				foldErr = err
				return false
			}
		}
		return true
	})
	if scanErr != nil {
		return nil, scanErr
	}
	if foldErr != nil {
		return nil, foldErr
	}
	return folded, nil
}

// foldV5ImportEvent applies one event to the folded transcript. A payload
// decode failure aborts the fold with a damaged-store error: importing a
// half-decoded conversation would silently drop content.
func foldV5ImportEvent(t *V5Transcript, byID map[string]int, event Event) error {
	switch event.Kind {
	case "message/complete", "message/upsert":
		var body v5ImportMessagesPayload
		if err := json.Unmarshal(event.Payload, &body); err != nil {
			return fmt.Errorf("%w: event %s payload: %w", ErrDamagedStore, event.ID, err)
		}
		if len(body.Message) == 0 {
			return fmt.Errorf("%w: event %s carries no message", ErrDamagedStore, event.ID)
		}
		var identity struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body.Message, &identity)
		if i, ok := byID[identity.ID]; ok && identity.ID != "" {
			if event.Kind == "message/upsert" {
				t.Messages[i] = V5ImportMessage{ID: identity.ID, Message: append(json.RawMessage(nil), body.Message...)}
			}
			return nil
		}
		t.Messages = append(t.Messages, V5ImportMessage{ID: identity.ID, Message: append(json.RawMessage(nil), body.Message...)})
		if identity.ID != "" {
			byID[identity.ID] = len(t.Messages) - 1
		}
		return nil
	case "message/retract":
		var body v5ImportMessagesPayload
		if err := json.Unmarshal(event.Payload, &body); err != nil {
			return fmt.Errorf("%w: event %s payload: %w", ErrDamagedStore, event.ID, err)
		}
		drop := map[string]bool{}
		for _, id := range body.IDs {
			drop[id] = true
		}
		kept := t.Messages[:0:0]
		for _, m := range t.Messages {
			if drop[m.ID] {
				delete(byID, m.ID)
				continue
			}
			kept = append(kept, m)
		}
		t.Messages = kept
		return nil
	case "history/replace", "legacy/import":
		var body v5ImportMessagesPayload
		if err := json.Unmarshal(event.Payload, &body); err != nil {
			return fmt.Errorf("%w: event %s payload: %w", ErrDamagedStore, event.ID, err)
		}
		replacement := make([]V5ImportMessage, 0, len(body.Messages))
		seen := map[string]bool{}
		for _, raw := range body.Messages {
			var identity struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &identity)
			if identity.ID == "" || !seen[identity.ID] {
				if identity.ID != "" {
					seen[identity.ID] = true
				}
				replacement = append(replacement, V5ImportMessage{ID: identity.ID, Message: append(json.RawMessage(nil), raw...)})
			}
		}
		t.Messages = replacement
		for id := range byID {
			delete(byID, id)
		}
		for i, m := range replacement {
			if m.ID != "" {
				byID[m.ID] = i
			}
		}
		return nil
	case "session/title":
		var body v5ImportMessagesPayload
		if err := json.Unmarshal(event.Payload, &body); err != nil {
			return fmt.Errorf("%w: event %s payload: %w", ErrDamagedStore, event.ID, err)
		}
		if title := strings.TrimSpace(body.Title); title != "" {
			t.Title = title
		}
		return nil
	case "session/config":
		var body v5ImportMessagesPayload
		if err := json.Unmarshal(event.Payload, &body); err != nil {
			return fmt.Errorf("%w: event %s payload: %w", ErrDamagedStore, event.ID, err)
		}
		if ref := strings.TrimSpace(body.ModelRef); ref != "" {
			t.ModelRef = ref
		}
		if identity := strings.TrimSpace(body.ModelIdentity); identity != "" {
			t.ModelIdentity = identity
		}
		return nil
	}
	return nil
}

// UpdatedAt is when the conversation last changed: its log, or its creation.
func (s *V5Session) UpdatedAt() time.Time {
	if s.LogModTime.After(s.Manifest.CreatedAt) {
		return s.LogModTime
	}
	return s.Manifest.CreatedAt
}

// SourceTreeFingerprint hashes every regular file under root (bounded by the
// caller's context) so the post-import check can prove the source store was
// not touched. Names and contents both feed the digest; WalkDir order is
// lexical, so the result is stable across runs on an unchanged tree.
// The .query-cache directory is excluded: it is a rebuildable read cache the
// fork's own read path refreshes (a read-only open may grow it), so counting
// it would fail honest read-only runs.
func SourceTreeFingerprint(ctx context.Context, root string) (string, int, int64, error) {
	var files int
	var total int64
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".query-cache" {
			return fs.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files++
		total += info.Size()
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), files, total, nil
}
