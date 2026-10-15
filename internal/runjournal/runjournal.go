// Package runjournal is task 419: a durable run journal for in-session
// multi-agent pipelines (one fleet or parallel_tasks group execution = one
// run).
//
// Three jobs, deliberately separated:
//
//   - Store writes: run rows, member rows, an append-only event trail, and
//     published artifacts to a SQLite database beside the session transcripts.
//     The event trail is the ONLY source of time (task-419 design §5, borrowed
//     from zcode's journal discipline): member rows carry no time columns, so
//     every "when did this happen" answer comes from events scanned in seq
//     order.
//   - Projections (projection.go) are pure functions over those rows: roster
//     health, timeline entries, artifact merge. They take `now` as an argument
//     and never touch I/O, so the rules are testable with hand-built rows.
//   - The read side degrades visibly: a missing or unopenable database yields
//     an empty list and one slog line, never a silent in-memory substitute
//     (borrowed from zcode's capability-probing discipline).
//
// The journal is observational only. A write failure must never fail the run
// being recorded: the agent-side recorder swallows and logs, and this package
// never panics on malformed input rows (decode skips, query errors return).
//
// Not this package's job (task 419 design §9): declarative DAG orchestration
// (task 685), structural flow-graph preview (task 686), cross-session delivery
// (task 349), and single task calls (v1 records groups only — the runs.tool
// column is the extension point).
package runjournal

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (house pattern: collabchannel/projectiondb/topicstate)
)

// schemaVersion is the additive migration generation. Bump only with a
// migration entry below; old databases ALTER forward, never rewrite rows.
const schemaVersion = 1

// Store is the run journal over SQLite. Writes are serialized by mu (a run's
// worker goroutines settle concurrently); readers take the same mu so a
// projection never sees a half-written terminal state.
type Store struct {
	db   *sql.DB
	mu   sync.Mutex
	now  func() int64 // ms clock (injectable for tests)
	path string
}

// Open builds (or migrates) the journal database at dbPath. An empty path is
// a configuration error, not an absent store: callers decide whether to
// disable recording before calling.
func Open(dbPath string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("runjournal: database path is required")
	}
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", diskFileDSN(dbPath))
	if err != nil {
		return nil, err
	}
	// One writer, WAL readers: runs are small (≤64 members, a handful of
	// events each), so a single connection keeps locking simple on Windows.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA busy_timeout=2000`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("runjournal: %s: %w", pragma, err)
		}
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{
		db:    db,
		now:   func() int64 { return time.Now().UnixMilli() },
		path:  dbPath,
	}, nil
}

// diskFileDSN mirrors the topicstate/collabchannel file URI (Windows drive →
// /C:/... path form) plus the two pragmas every store here wants.
func diskFileDSN(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slash := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && len(slash) >= 2 && slash[1] == ':' {
		slash = "/" + slash
	}
	u := &url.URL{Scheme: "file", Path: slash}
	return u.String() + "?_pragma=busy_timeout%282000%29&_pragma=foreign_keys%281%29"
}

// migrate creates the v1 tables. Tables are created IF NOT EXISTS so an
// existing database from a future additive revision keeps its rows; the
// schema_version meta row records the generation for forward checks.
func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			parent_session TEXT NOT NULL,
			tool_call_id TEXT NOT NULL,
			tool TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			total INTEGER NOT NULL DEFAULT 0,
			result_preview TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			ended_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_parent_created ON runs (parent_session, created_at)`,
		`CREATE TABLE IF NOT EXISTS members (
			run_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			member_id TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			profile TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			ref TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			preview TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (run_id, idx)
		)`,
		// events is append-only: seq is assigned under Store.mu, `at` is the
		// single source of time for every projection (design §5).
		`CREATE TABLE IF NOT EXISTS events (
			run_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			at INTEGER NOT NULL,
			kind TEXT NOT NULL,
			member_idx INTEGER NOT NULL DEFAULT 0,
			detail TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (run_id, seq)
		)`,
		// artifacts rows are written only with status='completed' (design §5
		// borrow 4): a failed publish never claims a version number.
		`CREATE TABLE IF NOT EXISTS artifacts (
			run_id TEXT NOT NULL,
			id TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'result',
			version INTEGER NOT NULL DEFAULT 1,
			title TEXT NOT NULL DEFAULT '',
			member_idx INTEGER NOT NULL DEFAULT 0,
			ref TEXT NOT NULL DEFAULT '',
			primary_flag INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'completed',
			published_at INTEGER NOT NULL,
			PRIMARY KEY (run_id, id, version)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("runjournal: migrate: %w", err)
		}
	}
	_, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(schemaVersion))
	return err
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SetClock overrides the ms clock (tests).
func (s *Store) SetClock(now func() int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Path reports the database file path (diagnostics/tests).
func (s *Store) Path() string { return s.path }

func (s *Store) clockLocked() int64 {
	if s.now == nil {
		return time.Now().UnixMilli()
	}
	return s.now()
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is not recoverable for identity; fall back to a
		// time-derived id so the run still lands (uniqueness degrades, the
		// journal still records).
		return fmt.Sprintf("%s%x", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}

// ---- write side ----

// MemberSpec declares one roster member at run creation.
type MemberSpec struct {
	Idx       int    // 1-based position in the group
	MemberID  string // stable event key (e.g. "group/fleet-1")
	Label     string
	Profile   string
}

// RunSpec declares a run at its start (borrow 5: the launch anchor rides the
// spec — tool_call_id is the caller's group tool-call id, recorded once).
type RunSpec struct {
	ParentSession string
	ToolCallID    string
	Tool          string // fleet | parallel_tasks
	Label         string
	Members       []MemberSpec
}

// BeginRun inserts the run, its roster, and the run_created event. It returns
// the new run id, or an error the caller may surface (recording began but
// storage failed — the agent-side recorder logs and continues without a run).
func (s *Store) BeginRun(spec RunSpec) (string, error) {
	if s == nil {
		return "", errors.New("runjournal: store is nil")
	}
	if strings.TrimSpace(spec.Tool) == "" {
		return "", errors.New("runjournal: run tool is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runID := newID("run_")
	now := s.clockLocked()
	if _, err := s.db.Exec(`
		INSERT INTO runs (id, parent_session, tool_call_id, tool, label, status, total,
			result_preview, created_at, updated_at, ended_at)
		VALUES (?, ?, ?, ?, ?, 'running', ?, '', ?, ?, 0)`,
		runID, spec.ParentSession, spec.ToolCallID, spec.Tool, spec.Label,
		len(spec.Members), now, now); err != nil {
		return "", fmt.Errorf("runjournal: begin run: %w", err)
	}
	for _, m := range spec.Members {
		if _, err := s.db.Exec(`
			INSERT INTO members (run_id, idx, member_id, label, profile, status)
			VALUES (?, ?, ?, ?, ?, 'pending')`,
			runID, m.Idx, m.MemberID, m.Label, m.Profile); err != nil {
			return "", fmt.Errorf("runjournal: begin member %d: %w", m.Idx, err)
		}
	}
	if err := s.appendEventLocked(runID, now, EventRunCreated, 0,
		fmt.Sprintf("tool=%s members=%d", spec.Tool, len(spec.Members))); err != nil {
		return "", err
	}
	return runID, nil
}

// Event kinds appended to the event trail. These strings are the journal's
// external contract (§9: stable kind = exportable offline).
const (
	EventRunCreated       = "run_created"
	EventMemberDispatched = "member_dispatched"
	EventMemberSettled    = "member_settled"
	EventArtifactPublish  = "artifact_published"
	EventRunEnded         = "run_ended"
)

// appendEventLocked appends the next seq event. Callers hold s.mu.
func (s *Store) appendEventLocked(runID string, at int64, kind string, memberIdx int, detail string) error {
	var next int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE run_id = ?`, runID).Scan(&next); err != nil {
		return fmt.Errorf("runjournal: next seq: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO events (run_id, seq, at, kind, member_idx, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		runID, next, at, kind, memberIdx, detail); err != nil {
		return fmt.Errorf("runjournal: append event: %w", err)
	}
	return nil
}

// Dispatched records a member starting (status running + dispatched event).
func (s *Store) Dispatched(runID string, idx int) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	if _, err := s.db.Exec(
		`UPDATE members SET status = 'running' WHERE run_id = ? AND idx = ?`, runID, idx); err != nil {
		return fmt.Errorf("runjournal: dispatch member %d: %w", idx, err)
	}
	return s.appendEventLocked(runID, now, EventMemberDispatched, idx, "")
}

// MemberSettlement is one member's terminal outcome.
type MemberSettlement struct {
	Idx     int
	Status  string // completed | failed | cancelled | skipped
	Label   string // roster label, lifted onto the published artifact title
	Ref     string
	Error   string
	Preview string // bounded answer preview (UTF-8 safe, caller truncates)
}

// Settled records a member terminal outcome plus its settled event. Only the
// first settle for an index takes effect (a cancelled drain must not
// overwrite a real result — mirrors the driveFleet drain discipline).
func (s *Store) Settled(runID string, m MemberSettlement) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	res, err := s.db.Exec(`
		UPDATE members SET status = ?, ref = ?, error = ?, preview = ?
		WHERE run_id = ? AND idx = ? AND status NOT IN ('completed','failed','cancelled','skipped')`,
		m.Status, bounded(m.Ref, 256), bounded(m.Error, 1024),
		bounded(m.Preview, maxMemberPreviewBytes), runID, m.Idx)
	if err != nil {
		return fmt.Errorf("runjournal: settle member %d: %w", m.Idx, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already terminal (double settle) or unknown index — record nothing:
		// the event trail stays consistent with the row state.
		return nil
	}
	detail := m.Status
	if m.Ref != "" {
		detail += " ref=" + m.Ref
	}
	if m.Error != "" {
		detail += " err=" + m.Error
	}
	return s.appendEventLocked(runID, now, EventMemberSettled, m.Idx, detail)
}

// PublishArtifact records a completed artifact row (borrow 4: completed rows
// only, version numbers never rewound) plus its event. Rows are upserted by
// (run_id, id, version): replay-safe.
func (s *Store) PublishArtifact(runID string, a ArtifactSpec) error {
	if s == nil {
		return nil
	}
	if a.ID == "" {
		return errors.New("runjournal: artifact id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	primary := 0
	if a.Primary {
		primary = 1
	}
	if _, err := s.db.Exec(`
		INSERT INTO artifacts (run_id, id, kind, version, title, member_idx, ref,
			primary_flag, status, published_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'completed', ?)
		ON CONFLICT(run_id, id, version) DO UPDATE SET
			title=excluded.title, ref=excluded.ref,
			primary_flag=MAX(artifacts.primary_flag, excluded.primary_flag),
			published_at=excluded.published_at`,
		runID, a.ID, a.KindOrDefault(), a.VersionOrDefault(), a.Title,
		a.MemberIdx, a.Ref, primary, now); err != nil {
		return fmt.Errorf("runjournal: publish artifact: %w", err)
	}
	return s.appendEventLocked(runID, now, EventArtifactPublish, a.MemberIdx, a.ID)
}

// End closes the run: terminal status on the row (idempotent — a second End
// keeps the first terminal), a result preview, and the run_ended event.
// Members still non-terminal at End time are left as-is: the projection
// counts them as leftoverRunning for a terminal run, which is exactly the
// zcode health semantic this journal borrows (a crashed writer shows up as
// stale rows, not as a lying status).
func (s *Store) End(runID, status, resultPreview string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clockLocked()
	if _, err := s.db.Exec(`
		UPDATE runs SET status = ?, result_preview = ?, updated_at = ?, ended_at = ?
		WHERE id = ? AND status = 'running'`,
		status, bounded(resultPreview, maxResultPreviewBytes), now, now, runID); err != nil {
		return fmt.Errorf("runjournal: end run: %w", err)
	}
	return s.appendEventLocked(runID, now, EventRunEnded, 0, status)
}

const (
	// maxResultPreviewBytes bounds the group aggregate preview stored on the
	// run row — the transcript keeps the full text; the journal keeps a head.
	maxResultPreviewBytes = 4 << 10
	// maxMemberPreviewBytes bounds a member answer preview likewise.
	maxMemberPreviewBytes = 2 << 10
)

// bounded returns s truncated to n bytes on a UTF-8 boundary with an
// ellipsis marker when cut.
func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// BoundMemberPreview exposes the member preview bound for the recorder.
func BoundMemberPreview(s string) string { return bounded(s, maxMemberPreviewBytes) }

// ---- read side rows ----

// RunRow is the runs-table row (list projection keeps it small: no member
// bodies, no events).
type RunRow struct {
	ID            string `json:"id"`
	ParentSession string `json:"parentSession"`
	ToolCallID    string `json:"toolCallId"`
	Tool          string `json:"tool"`
	Label         string `json:"label"`
	Status        string `json:"status"`
	Total         int    `json:"total"`
	ResultPreview string `json:"resultPreview"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	EndedAt       int64  `json:"endedAt"`
}

// MemberRow mirrors the members table — no time columns by design (§5).
type MemberRow struct {
	Idx       int    `json:"idx"`
	MemberID  string `json:"memberId"`
	Label     string `json:"label"`
	Profile   string `json:"profile"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	Error     string `json:"error"`
	Preview   string `json:"preview"`
}

// EventRow is one append-only trail entry.
type EventRow struct {
	Seq       int64  `json:"seq"`
	At        int64  `json:"at"`
	Kind      string `json:"kind"`
	MemberIdx int    `json:"memberIdx"`
	Detail    string `json:"detail"`
}

// ArtifactRow mirrors the artifacts table.
type ArtifactRow struct {
	RunID       string `json:"runId"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	MemberIdx   int    `json:"memberIdx"`
	Ref         string `json:"ref"`
	Primary     bool   `json:"primary"`
	Status      string `json:"status"`
	PublishedAt int64  `json:"publishedAt"`
}

// ListRuns enumerates the most recent runs, newest first. parentSession may
// be empty to enumerate across sessions (desktop overview). The narrow
// projection reads runs only — never member bodies or events — so a list
// page stays cheap.
func (s *Store) ListRuns(parentSession string, limit int) ([]RunRow, error) {
	if s == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	query := `SELECT id, parent_session, tool_call_id, tool, label, status, total,
		result_preview, created_at, updated_at, ended_at
		FROM runs`
	args := []any{}
	if strings.TrimSpace(parentSession) != "" {
		query += ` WHERE parent_session = ?`
		args = append(args, parentSession)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("runjournal: list runs: %w", err)
	}
	defer rows.Close()
	out := []RunRow{}
	for rows.Next() {
		var r RunRow
		if err := rows.Scan(&r.ID, &r.ParentSession, &r.ToolCallID, &r.Tool, &r.Label,
			&r.Status, &r.Total, &r.ResultPreview, &r.CreatedAt, &r.UpdatedAt, &r.EndedAt); err != nil {
			return nil, fmt.Errorf("runjournal: scan run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun loads one run row.
func (s *Store) GetRun(runID string) (RunRow, error) {
	if s == nil {
		return RunRow{}, errors.New("runjournal: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var r RunRow
	err := s.db.QueryRow(`SELECT id, parent_session, tool_call_id, tool, label, status,
		total, result_preview, created_at, updated_at, ended_at FROM runs WHERE id = ?`, runID).
		Scan(&r.ID, &r.ParentSession, &r.ToolCallID, &r.Tool, &r.Label, &r.Status,
			&r.Total, &r.ResultPreview, &r.CreatedAt, &r.UpdatedAt, &r.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RunRow{}, fmt.Errorf("runjournal: run %q not found", runID)
	}
	if err != nil {
		return RunRow{}, fmt.Errorf("runjournal: get run: %w", err)
	}
	return r, nil
}

// ListMembers loads a run's roster in idx order.
func (s *Store) ListMembers(runID string) ([]MemberRow, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT idx, member_id, label, profile, status, ref, error, preview
		FROM members WHERE run_id = ? ORDER BY idx`, runID)
	if err != nil {
		return nil, fmt.Errorf("runjournal: list members: %w", err)
	}
	defer rows.Close()
	out := []MemberRow{}
	for rows.Next() {
		var m MemberRow
		if err := rows.Scan(&m.Idx, &m.MemberID, &m.Label, &m.Profile, &m.Status,
			&m.Ref, &m.Error, &m.Preview); err != nil {
			return nil, fmt.Errorf("runjournal: scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListEvents loads a run's event trail in seq order — the time axis.
func (s *Store) ListEvents(runID string) ([]EventRow, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT seq, at, kind, member_idx, detail
		FROM events WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, fmt.Errorf("runjournal: list events: %w", err)
	}
	defer rows.Close()
	out := []EventRow{}
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.Seq, &e.At, &e.Kind, &e.MemberIdx, &e.Detail); err != nil {
			return nil, fmt.Errorf("runjournal: scan event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListArtifacts loads a run's artifact rows (already completed-only at
// write time; the read side just orders).
func (s *Store) ListArtifacts(runID string) ([]ArtifactRow, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT run_id, id, kind, version, title, member_idx, ref,
		primary_flag, status, published_at FROM artifacts WHERE run_id = ?
		ORDER BY primary_flag DESC, published_at ASC, version ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("runjournal: list artifacts: %w", err)
	}
	defer rows.Close()
	out := []ArtifactRow{}
	for rows.Next() {
		var a ArtifactRow
		var primary int
		if err := rows.Scan(&a.RunID, &a.ID, &a.Kind, &a.Version, &a.Title, &a.MemberIdx,
			&a.Ref, &primary, &a.Status, &a.PublishedAt); err != nil {
			return nil, fmt.Errorf("runjournal: scan artifact: %w", err)
		}
		a.Primary = primary != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// ArtifactSpec is the write-side artifact declaration.
type ArtifactSpec struct {
	ID        string
	Kind      string // default "result"
	Version   int    // default 1
	Title     string
	MemberIdx int
	Ref       string
	Primary   bool
}

// KindOrDefault returns the declared kind or "result".
func (a ArtifactSpec) KindOrDefault() string {
	if strings.TrimSpace(a.Kind) == "" {
		return "result"
	}
	return a.Kind
}

// VersionOrDefault returns the declared version or 1 (never 0: a versioned
// row without a number cannot be located in the UI — zcode borrow 4).
func (a ArtifactSpec) VersionOrDefault() int {
	if a.Version < 1 {
		return 1
	}
	return a.Version
}
