// tl1349 ①: the recall pool's durable, incremental FTS5 index.
//
// Problem this solves: BuildRecallIndex (recall_index.go) re-reads and
// re-tokenizes the whole fact pool on every process start — the pool lives in
// a process-resident atomic.Pointer, so a restart throws it away. With ~700
// facts that is a few hundred ms of IO + tokenization on every boot, and it
// grows linearly with the pool.
//
// Shape (mirrors internal/session/search_index.go and mimo-code's memory_fts):
// markdown stays the single source of truth; this SQLite file is a disposable
// acceleration copy. facts holds one row per fact file with its stat
// fingerprint, and facts_fts is an external-content FTS5 table fed by the
// standard ai/ad/au trigger trio.
//
// Two deliberate design choices:
//
//  1. The indexed text is the *output of retrieval.Tokens* joined by spaces,
//     not the raw search text. FTS5's built-in tokenizers cannot reproduce our
//     overlapping-CJK-bigram scheme, so indexing raw text would silently change
//     recall semantics. Indexing the token stream keeps FTS5 matching
//     byte-identical to BM25Score's vocabulary.
//  2. Reconcile compares (size, mtimeMs) per file. Unchanged files are never
//     re-read: their Memory struct and term counts come straight out of the
//     row. Only new/changed files pay read+tokenize cost.
//
// Any failure here (sqlite unavailable, index corrupt, read-only dir) must
// degrade to the previous full-rebuild path — recall is never blocked by an
// accelerator problem.
package memory

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"reasonix/internal/retrieval"
)

const recallFTSSchemaVersion = "2"

// recallFTSPath returns the durable index location for this store. Each project
// store gets its own file: the same store can see GlobalDir + Dir, and two
// projects must not clobber each other's project-scoped rows.
func recallFTSPath(store Store) string {
	base := store.Dir
	if base == "" {
		base = store.GlobalDir
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, ".index", "recall-v"+recallFTSSchemaVersion+".sqlite")
}

// recallFTSStats records what one reconcile actually did. This is the
// observability surface acceptance criterion 1 asks for: a restart must report
// reused>0 and added==0, proving the incremental path ran.
type recallFTSStats struct {
	Added    int
	Updated  int
	Removed  int
	Reused   int
	Duration time.Duration
}

// recallFTSMigrations builds the schema. Kept as statements (not a version
// ladder) because a version mismatch drops and rebuilds — the file is pure
// derived data.
func recallFTSMigrations() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS facts (
			path TEXT PRIMARY KEY,
			id TEXT NOT NULL,
			scope TEXT NOT NULL,
			revision INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			name TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT NOT NULL,
			ftype TEXT NOT NULL,
			activation TEXT NOT NULL,
			volatility TEXT NOT NULL,
			subject TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			last_verified_at INTEGER NOT NULL,
			keywords TEXT NOT NULL,
			triggers TEXT NOT NULL,
			body TEXT NOT NULL,
			search_text TEXT NOT NULL,
			fts_terms TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			term_count INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS facts_id ON facts(id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(fts_terms, content='facts', content_rowid='rowid', tokenize='unicode61')`,
		`CREATE TRIGGER IF NOT EXISTS facts_ai AFTER INSERT ON facts BEGIN
			INSERT INTO facts_fts(rowid, fts_terms) VALUES (new.rowid, new.fts_terms);
		END`,
		`CREATE TRIGGER IF NOT EXISTS facts_ad AFTER DELETE ON facts BEGIN
			INSERT INTO facts_fts(facts_fts, rowid, fts_terms) VALUES('delete', old.rowid, old.fts_terms);
		END`,
		`CREATE TRIGGER IF NOT EXISTS facts_au AFTER UPDATE ON facts BEGIN
			INSERT INTO facts_fts(facts_fts, rowid, fts_terms) VALUES('delete', old.rowid, old.fts_terms);
			INSERT INTO facts_fts(rowid, fts_terms) VALUES (new.rowid, new.fts_terms);
		END`,
	}
}

func openRecallFTS(path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(2000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	for _, statement := range recallFTSMigrations() {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("recall fts migration: %w", err)
		}
	}
	return db, nil
}

// schemaVersion returns the stored schema version, "" when absent.
func schemaVersion(db *sql.DB) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM metadata WHERE key='schema_version'`).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// factFileState is one markdown file's stat fingerprint.
type factFileState struct {
	path string
	hash string // "{size}|{mtimeMs}"
}

// scanFactFiles stats every active fact file across the store's scopes. Only
// stat() — no content is read — so this is the cheap half of reconcile.
func scanFactFiles(store Store) []factFileState {
	var out []factFileState
	for _, dir := range store.dirs() {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || e.Name() == indexFile || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			out = append(out, factFileState{
				path: filepath.Join(dir, e.Name()),
				hash: fmt.Sprintf("%d|%d", info.Size(), info.ModTime().UnixMilli()),
			})
		}
	}
	return out
}

// ensureRecallFTS reconciles the durable index against markdown and returns the
// full recall pool rebuilt from rows. Callers fall back to BuildRecallIndex on
// any error.
func ensureRecallFTS(store Store) ([]recallFTSDoc, recallFTSStats, error) {
	started := time.Now()
	var stats recallFTSStats
	path := recallFTSPath(store)
	if path == "" {
		return nil, stats, fmt.Errorf("recall fts: store has no directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, stats, fmt.Errorf("recall fts: %w", err)
	}
	db, err := openRecallFTS(path)
	if err != nil {
		return nil, stats, err
	}
	defer db.Close()

	version, err := schemaVersion(db)
	if err != nil {
		return nil, stats, err
	}
	if version != recallFTSSchemaVersion {
		// Derived data: on mismatch drop every row and rebuild from scratch.
		// facts_ad keeps the FTS index in step with the content table; the
		// trailing 'rebuild' is the supported way to re-sync an
		// external-content table (§5.1 of the FTS5 docs) and costs nothing when
		// the trigger already did the work.
		if _, err := db.Exec(`DELETE FROM facts`); err != nil {
			return nil, stats, err
		}
		if _, err := db.Exec(`INSERT INTO facts_fts(facts_fts) VALUES('rebuild')`); err != nil {
			return nil, stats, err
		}
		if _, err := db.Exec(`INSERT INTO metadata(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, recallFTSSchemaVersion); err != nil {
			return nil, stats, err
		}
	}

	files := scanFactFiles(store)
	known := map[string]string{} // path -> content_hash
	rows, err := db.Query(`SELECT path, content_hash FROM facts`)
	if err != nil {
		return nil, stats, err
	}
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			rows.Close()
			return nil, stats, err
		}
		known[p] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, stats, err
	}

	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.path] = true
		previous, existed := known[f.path]
		if existed && previous == f.hash {
			stats.Reused++
			continue
		}
		memory, ok := loadMemory(f.path)
		if !ok {
			// A file that vanished between stat and read simply drops out on the
			// next pass; anything else is surfaced but does not abort the sweep.
			slog.Warn("memory: recall fts skipped unreadable fact", "path", f.path)
			delete(seen, f.path)
			continue
		}
		if err := upsertFactRow(db, memory, f); err != nil {
			return nil, stats, err
		}
		if existed {
			stats.Updated++
		} else {
			stats.Added++
		}
	}
	for p := range known {
		if seen[p] {
			continue
		}
		if _, err := db.Exec(`DELETE FROM facts WHERE path=?`, p); err != nil {
			return nil, stats, err
		}
		stats.Removed++
	}

	docs, err := loadRecallFTSDocs(db)
	if err != nil {
		return nil, stats, err
	}
	stats.Duration = time.Since(started)
	return docs, stats, nil
}

// upsertFactRow writes one fact's full state, including its pre-tokenized
// stream, so later reconciles can rebuild without touching the file.
func upsertFactRow(db *sql.DB, memory Memory, file factFileState) error {
	searchText := autoRecallSearchText(memory)
	terms := retrieval.Tokens(searchText)
	_, err := db.Exec(`INSERT INTO facts(
			path,id,scope,revision,created_at,updated_at,name,title,description,ftype,
			activation,volatility,subject,expires_at,last_verified_at,keywords,triggers,body,
			search_text,fts_terms,content_hash,term_count)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
			id=excluded.id, scope=excluded.scope, revision=excluded.revision,
			created_at=excluded.created_at, updated_at=excluded.updated_at,
			name=excluded.name, title=excluded.title, description=excluded.description,
			ftype=excluded.ftype, activation=excluded.activation, volatility=excluded.volatility,
			subject=excluded.subject, expires_at=excluded.expires_at,
			last_verified_at=excluded.last_verified_at, keywords=excluded.keywords,
			triggers=excluded.triggers,
			body=excluded.body, search_text=excluded.search_text, fts_terms=excluded.fts_terms,
			content_hash=excluded.content_hash, term_count=excluded.term_count`,
		file.path, memory.ID, string(memory.Scope), memory.Revision,
		memory.CreatedAt.Unix(), memory.UpdatedAt.Unix(),
		memory.Name, memory.Title, memory.Description, string(memory.Type),
		string(memory.Activation), string(memory.Volatility), memory.SubjectKey,
		memory.ExpiresAt.Unix(), memory.LastVerifiedAt.Unix(),
		memory.Keywords, memory.Triggers, memory.Body,
		searchText, strings.Join(terms, " "), file.hash, len(terms))
	return err
}

// loadRecallFTSDocs rebuilds the recall pool purely from rows: no markdown IO,
// no tokenization (the stored term stream is re-split).
func loadRecallFTSDocs(db *sql.DB) ([]recallFTSDoc, error) {
	rows, err := db.Query(`SELECT id,scope,revision,created_at,updated_at,name,title,description,
			ftype,activation,volatility,subject,expires_at,last_verified_at,keywords,triggers,body,
			search_text,fts_terms FROM facts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recallFTSDoc
	for rows.Next() {
		var (
			id, scope, name, title, description, ftype, activation, volatility string
			subject, keywords, triggers, body, searchText, ftsTerms            string
			revision, createdAt, updatedAt, expiresAt, lastVerifiedAt         int64
		)
		if err := rows.Scan(&id, &scope, &revision, &createdAt, &updatedAt, &name, &title,
			&description, &ftype, &activation, &volatility, &subject, &expiresAt,
			&lastVerifiedAt, &keywords, &triggers, &body, &searchText, &ftsTerms); err != nil {
			return nil, err
		}
		terms := strings.Fields(ftsTerms)
		if len(terms) == 0 {
			continue
		}
		out = append(out, recallFTSDoc{
			memory: Memory{
				ID:             id,
				Revision:       int(revision),
				CreatedAt:      time.Unix(createdAt, 0),
				UpdatedAt:      time.Unix(updatedAt, 0),
				Name:           name,
				Title:          title,
				Description:    description,
				Type:           Type(ftype),
				Scope:          FactScope(scope),
				Activation:     Activation(activation),
				Volatility:     Volatility(volatility),
				SubjectKey:     subject,
				ExpiresAt:      time.Unix(expiresAt, 0),
				LastVerifiedAt: time.Unix(lastVerifiedAt, 0),
				Keywords:       keywords,
				Triggers:       triggers,
				Body:           body,
			},
			text:   searchText,
			counts: retrieval.Counts(terms),
			length: len(terms),
		})
	}
	return out, rows.Err()
}

// recallFTSDoc is one fully-hydrated pool entry, structurally equal to
// autoRecallDoc but declared separately so the durable path never depends on
// the in-memory index's internals.
type recallFTSDoc struct {
	memory Memory
	text   string
	counts map[string]int
	length int
}

// filterRecallFTSPool applies the recall pool's scope-shadow gate to the rows
// the durable index loaded. BuildRecallIndex runs the markdown pool through
// recallMemories, which drops a global fact whose identity (id/name/title/
// subject) is claimed by a project fact — and (plan E off) skips pinned facts
// whose bodies already ride session-context. The FTS rows are one row per
// fact file, so without this gate the durable path silently resurrects
// shadowed global twins and pinned facts on exactly the deployments where the
// accelerator is healthy. recallMemories itself is the single semantic source;
// the kept set is matched back by ID, which is unique per fact in production
// (random mem-… or scope-qualified legacy-…).
func filterRecallFTSPool(docs []recallFTSDoc) []recallFTSDoc {
	if len(docs) == 0 {
		return docs
	}
	memories := make([]Memory, 0, len(docs))
	for _, doc := range docs {
		memories = append(memories, doc.memory)
	}
	kept := make(map[string]bool, len(memories))
	for _, memory := range recallMemories(memories) {
		kept[memory.ID] = true
	}
	out := docs[:0]
	for _, doc := range docs {
		if kept[doc.memory.ID] {
			out = append(out, doc)
		}
	}
	return out
}

// buildRecallIndexFromFTS tries the durable index first and falls back to the
// full rebuild. The fallback is unconditional on error: an accelerator that
// cannot work must never cost us recall.
func buildRecallIndexFromFTS(store Store) *RecallIndex {
	started := time.Now()
	docs, stats, err := ensureRecallFTS(store)
	if err != nil {
		slog.Warn("memory: recall fts unavailable, rebuilding from disk", "err", err)
		return BuildRecallIndex(store)
	}
	docs = filterRecallFTSPool(docs)
	// Keep BuildRecallIndex's observability contract: LastRecallIndexBuildStats
	// is how the cache tests and diagnostics tell "rebuilt" from "reused", so
	// the durable path must still publish one record per rebuild.
	terms := 0
	for _, doc := range docs {
		terms += doc.length
	}
	lastRecallIndexBuild.Store(&RecallIndexBuildStats{
		Docs: len(docs), Terms: terms, Duration: stats.Duration, At: started,
	})
	if len(docs) == 0 {
		// Empty pool: BuildRecallIndex returns nil and recall reports an empty
		// store. Handing back an empty struct instead would break callers that
		// treat nil as "nothing to search".
		return nil
	}
	slog.Info("memory: recall index served from fts5",
		"docs", len(docs),
		"added", stats.Added, "updated", stats.Updated,
		"removed", stats.Removed, "reused", stats.Reused,
		"reconcile_ms", stats.Duration.Milliseconds())
	index := &RecallIndex{}
	for _, doc := range docs {
		index.fielded = append(index.fielded, retrieval.FieldedDoc{ID: doc.memory.ID, Fields: map[string]string{
			"name": doc.memory.Name, "title": doc.memory.Title, "keywords": doc.memory.Keywords,
			"subject": doc.memory.SubjectKey, "description": doc.memory.Description, "body": doc.memory.Body,
		}})
		index.docs = append(index.docs, autoRecallDoc{
			memory: doc.memory,
			text:   doc.text,
			counts: doc.counts,
			length: doc.length,
		})
	}
	return index
}

