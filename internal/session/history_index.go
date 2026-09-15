package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/projectiondb"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontent"
)

func (q *Query) prepareHistoryIndex(ctx context.Context, ref SessionRef) (*FilesystemPersistence, string, error) {
	if q == nil {
		return nil, "", errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return nil, "", err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return nil, "", errors.New("session: history index requires filesystem persistence")
	}
	path := historyIndexPath(filesystem.Root, ref.SessionID)
	lock := q.projectionLock("history", ref.SessionID)
	lock.Lock()
	err := ensureHistoryIndex(ctx, filesystem, ref.SessionID, path)
	lock.Unlock()
	if err != nil {
		return nil, "", err
	}
	return filesystem, path, nil
}

func (q *Query) ReadContent(ctx context.Context, ref SessionRef, contentRef sessioncontent.Ref, offset, length int64) ([]byte, error) {
	if q == nil {
		return nil, errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return nil, err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return nil, errors.New("session: content reads require filesystem persistence")
	}
	if offset < 0 || length < 0 || length > 1<<20 || offset > contentRef.Bytes || length > contentRef.Bytes-offset {
		return nil, errors.New("session: invalid or oversized content range")
	}
	if !q.contentAuthorized(ref.SessionID, contentRef.Digest, contentRef.Bytes, contentRef.IndexDigest) {
		return nil, errors.New("session: content reference is not authorized for this session")
	}
	return contentStoreForSessionDir(filepath.Join(filesystem.Root, ref.SessionID)).ReadRange(ctx, contentRef, offset, length)
}

func ensureHistoryIndex(ctx context.Context, persistence *FilesystemPersistence, sessionID, path string) error {
	dir := filepath.Join(persistence.Root, sessionID)
	revision, err := revisionOfLog(dir)
	if err != nil {
		return err
	}
	if historyIndexCurrent(ctx, dir, path, sessionID, revision) {
		return nil
	}
	if updated, err := incrementHistoryIndex(ctx, dir, path, sessionID, revision); updated || err != nil {
		return err
	}
	return rebuildHistoryIndex(ctx, dir, path, sessionID, revision)
}

type historyIndexMetadata struct {
	sessionID       string
	logSize         int64
	storageRevision int
	projection      int
	durableSequence uint64
	viewSequence    uint64
	generation      string
}

func readHistoryIndexMetadata(ctx context.Context, db *sql.DB) (historyIndexMetadata, error) {
	values := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT key,value FROM metadata WHERE key IN ('session_id','log_size','storage_revision','projection_version','durable_sequence','history_view_sequence','generation')`)
	if err != nil {
		return historyIndexMetadata{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return historyIndexMetadata{}, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return historyIndexMetadata{}, err
	}
	metadata := historyIndexMetadata{sessionID: values["session_id"], generation: values["generation"]}
	if _, err := fmt.Sscan(values["log_size"], &metadata.logSize); err != nil {
		return historyIndexMetadata{}, err
	}
	if _, err := fmt.Sscan(values["storage_revision"], &metadata.storageRevision); err != nil {
		return historyIndexMetadata{}, err
	}
	if _, err := fmt.Sscan(values["projection_version"], &metadata.projection); err != nil {
		return historyIndexMetadata{}, err
	}
	if _, err := fmt.Sscan(values["durable_sequence"], &metadata.durableSequence); err != nil {
		return historyIndexMetadata{}, err
	}
	if values["history_view_sequence"] != "" {
		if _, err := fmt.Sscan(values["history_view_sequence"], &metadata.viewSequence); err != nil {
			return historyIndexMetadata{}, err
		}
	}
	return metadata, nil
}

// incrementHistoryIndex advances only the complete transactions appended after
// the published coverage watermark. It returns updated=false when the existing
// file cannot be trusted as a base and must be rebuilt atomically.
func incrementHistoryIndex(ctx context.Context, dir, path, sessionID string, revision logRevision) (updated bool, result error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return false, nil
	}
	defer handle.DB.Close()
	metadata, err := readHistoryIndexMetadata(ctx, handle.DB)
	if err != nil || metadata.sessionID != sessionID || metadata.storageRevision != StorageRevision || metadata.projection != historyIndexVersion || metadata.logSize < 0 || metadata.logSize > revision.Size {
		return false, nil
	}
	if metadata.logSize == revision.Size {
		return true, nil
	}

	manifest, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil || manifest.Codec != Codec {
		return false, nil
	}
	log, err := os.Open(logPathForManifest(dir, manifest))
	if err != nil {
		return false, err
	}
	defer log.Close()

	state, err := loadCurrentHistoryState(ctx, handle.DB)
	if err != nil {
		return false, nil
	}

	tx, err := handle.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	state.tx = tx
	state.statements, err = prepareHistoryBuildStatements(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	defer func() {
		state.statements.close()
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	content := contentStoreForSessionDir(dir)
	durable := metadata.durableSequence
	durableEnd := metadata.logSize
	viewSequence := metadata.viewSequence
	var buildErr error
	err = scanV4CommitFileRefs(ctx, log, metadata.logSize, metadata.durableSequence+1, content, nil, func(_ int64, commit Commit) bool {
		state.transactions = append(state.transactions, []any{commit.ID, commit.FirstSequence, commit.LastSequence(), commit.OperationID, commit.OperationHash, commit.TurnID, commit.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")})
		for _, event := range commit.Events {
			if event.Kind == "history/replace" {
				viewSequence = event.Sequence
			}
			digest := ""
			var contentBytes int64
			if event.PayloadRef != nil {
				digest, contentBytes = event.PayloadRef.Digest, event.PayloadRef.Bytes
				if err := insertContentRef(ctx, &state, *event.PayloadRef); err != nil {
					buildErr = err
					return false
				}
			}
			state.events = append(state.events, []any{event.Sequence, commit.ID, event.ID, event.Kind, digest, contentBytes})
			if err := indexMessageEvent(ctx, content, &state, event); err != nil {
				buildErr = err
				return false
			}
			durable = event.Sequence
		}
		durableEnd, _ = log.Seek(0, 1)
		return true
	})
	if err != nil || buildErr != nil {
		return false, errors.Join(err, buildErr)
	}
	if durableEnd == metadata.logSize {
		// An incomplete physical tail remains unpublished. A writer will preserve
		// and repair it before the next append.
		return true, nil
	}
	if err := flushHistoryBuildRows(ctx, tx, &state); err != nil {
		return false, err
	}
	generation, err := historyProjectionGeneration(dir, viewSequence)
	if err != nil {
		return false, err
	}
	values := map[string]string{
		"log_size": fmt.Sprint(durableEnd), "log_mtime_ns": fmt.Sprint(revision.ModTimeNS),
		"durable_sequence": fmt.Sprint(durable), "history_view_sequence": fmt.Sprint(viewSequence), "generation": generation,
	}
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			return false, err
		}
	}
	state.statements.close()
	state.statements = nil
	if err := tx.Commit(); err != nil {
		return false, err
	}
	tx = nil
	_, _ = handle.DB.ExecContext(ctx, `PRAGMA shrink_memory`)
	return true, nil
}

func loadCurrentHistoryState(ctx context.Context, db *sql.DB) (historyBuildState, error) {
	state := historyBuildState{positions: map[string]int64{}, turns: map[string]int{}, versions: map[string]int{}}
	rows, err := db.QueryContext(ctx, `SELECT message_id,position,visible_turn,version FROM messages WHERE current=1`)
	if err != nil {
		return historyBuildState{}, err
	}
	for rows.Next() {
		var id string
		var position int64
		var visibleTurn, version int
		if err := rows.Scan(&id, &position, &visibleTurn, &version); err != nil {
			_ = rows.Close()
			return historyBuildState{}, err
		}
		state.positions[id], state.turns[id], state.versions[id] = position, visibleTurn, version
		state.nextPosition = max(state.nextPosition, position)
		state.visibleTurn = max(state.visibleTurn, visibleTurn)
	}
	return state, errors.Join(rows.Err(), rows.Close())
}

func historyIndexCurrent(ctx context.Context, dir, path, sessionID string, revision logRevision) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return false
	}
	defer handle.DB.Close()
	values := map[string]string{}
	rows, err := handle.DB.QueryContext(ctx, `SELECT key,value FROM metadata WHERE key IN ('session_id','log_size','log_mtime_ns','storage_revision','projection_version','generation')`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			return false
		}
		values[key] = value
	}
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return false
	}
	identity, err := readStorageIdentity(dir, manifest)
	if err != nil || !strings.HasPrefix(values["generation"], identity.Generation+":") {
		return false
	}
	return values["session_id"] == sessionID && values["log_size"] == fmt.Sprint(revision.Size) && values["log_mtime_ns"] == fmt.Sprint(revision.ModTimeNS) && values["storage_revision"] == fmt.Sprint(StorageRevision) && values["projection_version"] == fmt.Sprint(historyIndexVersion)
}

func historyProjectionGeneration(dir string, viewSequence uint64) (string, error) {
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	identity, err := readStorageIdentity(dir, manifest)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", identity.Generation, viewSequence), nil
}

func rebuildHistoryIndex(ctx context.Context, dir, path, sessionID string, revision logRevision) error {
	manifest, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	log, err := os.Open(logPathForManifest(dir, manifest))
	if err != nil {
		return err
	}
	defer log.Close()
	content := contentStoreForSessionDir(dir)
	return projectiondb.Rebuild(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1, QuickCheck: true}, func(ctx context.Context, db *sql.DB) error {
		return populateHistoryIndex(ctx, db, log, content, dir, sessionID, revision)
	})
}

func populateHistoryIndex(ctx context.Context, db *sql.DB, log *os.File, content *sessioncontent.Store, dir, sessionID string, revision logRevision) error {
	// Keep SQLite's derived-data working set explicit. The history database
	// may be many GiB, but neither its page cache nor temporary sort state
	// belongs in the runtime's cumulative memory footprint.
	// Rebuild writes an unpublished, disposable replacement beside the live
	// index. Avoid WAL and durability work for that private file; Rebuild
	// validates it before one atomic publish, and the event log remains the
	// durable source if a crash leaves or corrupts the temporary database.
	for _, pragma := range []string{
		`PRAGMA journal_mode=OFF`,
		`PRAGMA synchronous=OFF`,
		`PRAGMA locking_mode=EXCLUSIVE`,
		`PRAGMA cache_size=-8192`,
		`PRAGMA temp_store=FILE`,
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	var tx *sql.Tx
	state := historyBuildState{positions: map[string]int64{}, turns: map[string]int{}, versions: map[string]int{}}
	defer func() {
		state.statements.close()
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	beginChunk := func() error {
		var err error
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		state.tx = tx
		state.statements, err = prepareHistoryBuildStatements(ctx, tx)
		return err
	}
	if err := beginChunk(); err != nil {
		return err
	}
	var durable uint64
	var viewSequence uint64
	var buildErr error
	chunkEvents := 0
	var chunkBytes int64
	commitChunk := func() error {
		if err := flushHistoryBuildRows(ctx, tx, &state); err != nil {
			return err
		}
		state.statements.close()
		state.statements = nil
		if err := tx.Commit(); err != nil {
			return err
		}
		// modernc SQLite allocates its page cache on the Go heap. Release dirty
		// pages after each bounded transaction so a multi-GiB derived index does
		// not retain every completed chunk until the database closes.
		if _, err := db.ExecContext(ctx, `PRAGMA shrink_memory`); err != nil {
			return err
		}
		chunkEvents, chunkBytes = 0, 0
		return beginChunk()
	}
	err := scanV4CommitFileRefs(ctx, log, 0, 1, content, nil, func(_ int64, commit Commit) bool {
		state.transactions = append(state.transactions, []any{commit.ID, commit.FirstSequence, commit.LastSequence(), commit.OperationID, commit.OperationHash, commit.TurnID, commit.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")})
		for _, event := range commit.Events {
			if event.Kind == "history/replace" {
				viewSequence = event.Sequence
			}
			digest := ""
			var bytes int64
			if event.PayloadRef != nil {
				digest, bytes = event.PayloadRef.Digest, event.PayloadRef.Bytes
				if err := insertContentRef(ctx, &state, *event.PayloadRef); err != nil {
					buildErr = err
					return false
				}
			}
			state.events = append(state.events, []any{event.Sequence, commit.ID, event.ID, event.Kind, digest, bytes})
			if err := indexMessageEvent(ctx, content, &state, event); err != nil {
				buildErr = err
				return false
			}
			durable = event.Sequence
			chunkEvents++
			chunkBytes += int64(len(event.Payload))
			if event.PayloadRef != nil {
				chunkBytes += min(event.PayloadRef.Bytes, int64(historyIndexTxnBytes))
			}
			if chunkEvents >= historyIndexTxnEvents || chunkBytes >= historyIndexTxnBytes {
				if err := commitChunk(); err != nil {
					buildErr = err
					return false
				}
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	if buildErr != nil {
		return buildErr
	}
	if err := flushHistoryBuildRows(ctx, tx, &state); err != nil {
		return err
	}
	generation, err := historyProjectionGeneration(dir, viewSequence)
	if err != nil {
		return err
	}
	metadata := map[string]string{"session_id": sessionID, "log_size": fmt.Sprint(revision.Size), "log_mtime_ns": fmt.Sprint(revision.ModTimeNS), "storage_revision": fmt.Sprint(StorageRevision), "projection_version": fmt.Sprint(historyIndexVersion), "durable_sequence": fmt.Sprint(durable), "history_view_sequence": fmt.Sprint(viewSequence), "generation": generation}
	for key, value := range metadata {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?)`, key, value); err != nil {
			return err
		}
	}
	state.statements.close()
	state.statements = nil
	err = tx.Commit()
	tx = nil
	return err
}

func flushHistoryBuildRows(ctx context.Context, tx *sql.Tx, state *historyBuildState) error {
	if err := insertHistoryRows(ctx, tx, `INSERT INTO transactions(commit_id,first_sequence,last_sequence,operation_id,operation_hash,turn_id,created_at) VALUES `, 7, state.transactions); err != nil {
		return err
	}
	if err := insertHistoryRows(ctx, tx, `INSERT INTO events(sequence,commit_id,event_id,kind,payload_digest,payload_bytes) VALUES `, 6, state.events); err != nil {
		return err
	}
	if err := insertHistoryRows(ctx, tx, `INSERT OR IGNORE INTO content_refs(digest,bytes,index_digest) VALUES `, 3, state.contentRefs); err != nil {
		return err
	}
	if err := insertHistoryRows(ctx, tx, `INSERT INTO messages(message_id,version,position,event_sequence,valid_to,role,preview,inline,content_digest,content_bytes,content_index_digest,current,search_text,visible_turn) VALUES `, 14, state.messages); err != nil {
		return err
	}
	state.transactions = state.transactions[:0]
	state.events = state.events[:0]
	state.contentRefs = state.contentRefs[:0]
	state.messages = state.messages[:0]
	return nil
}

func insertHistoryRows(ctx context.Context, tx *sql.Tx, prefix string, columns int, rows [][]any) error {
	const rowsPerStatement = 128
	for start := 0; start < len(rows); start += rowsPerStatement {
		end := min(start+rowsPerStatement, len(rows))
		var query strings.Builder
		query.WriteString(prefix)
		args := make([]any, 0, (end-start)*columns)
		for rowIndex, row := range rows[start:end] {
			if len(row) != columns {
				return errors.New("session: invalid history index row width")
			}
			if rowIndex > 0 {
				query.WriteByte(',')
			}
			query.WriteByte('(')
			for column := range columns {
				if column > 0 {
					query.WriteByte(',')
				}
				query.WriteByte('?')
			}
			query.WriteByte(')')
			args = append(args, row...)
		}
		if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func indexMessageEvent(ctx context.Context, content *sessioncontent.Store, state *historyBuildState, event Event) error {
	if event.Kind != "message/complete" && event.Kind != "message/upsert" && event.Kind != "history/replace" && event.Kind != "legacy/import" {
		return nil
	}
	payload := event.Payload
	if event.PayloadRef != nil {
		var err error
		payload, err = resolveContentPayload(ctx, content, *event.PayloadRef)
		if err != nil {
			return err
		}
	}
	switch event.Kind {
	case "message/complete", "message/upsert":
		var body struct {
			Message *provider.Message `json:"message"`
		}
		if err := strictPayload(payload, &body); err != nil || body.Message == nil {
			return damagedPayload(event, err)
		}
		return indexOneMessage(ctx, content, state, *body.Message, event.Sequence, event.Kind == "message/upsert")
	case "history/replace":
		var body struct {
			Messages []provider.Message `json:"messages"`
		}
		if err := strictPayload(payload, &body); err != nil || body.Messages == nil {
			return damagedPayload(event, err)
		}
		return replaceIndexedMessages(ctx, content, state, body.Messages, event.Sequence)
	case "legacy/import":
		var body struct {
			Messages []provider.Message `json:"messages"`
		}
		if err := strictPayload(payload, &body); err != nil || body.Messages == nil {
			return damagedPayload(event, err)
		}
		return replaceIndexedMessages(ctx, content, state, body.Messages, event.Sequence)
	}
	return nil
}

func replaceIndexedMessages(ctx context.Context, content *sessioncontent.Store, state *historyBuildState, messages []provider.Message, sequence uint64) error {
	if err := flushHistoryBuildRows(ctx, state.tx, state); err != nil {
		return err
	}
	if _, err := state.statements.clear.ExecContext(ctx, sequence); err != nil {
		return err
	}
	state.nextPosition = 0
	state.visibleTurn = 0
	state.positions = map[string]int64{}
	state.turns = map[string]int{}
	state.versions = map[string]int{}
	for _, message := range messages {
		if err := indexOneMessage(ctx, content, state, message, sequence, false); err != nil {
			return err
		}
	}
	return nil
}

func indexOneMessage(ctx context.Context, content *sessioncontent.Store, state *historyBuildState, message provider.Message, sequence uint64, upsert bool) error {
	id := strings.TrimSpace(message.ID)
	if id == "" {
		return errors.New("session: indexed message has no stable id")
	}
	position, exists := state.positions[id]
	visibleTurn := state.turns[id]
	if !exists {
		state.nextPosition++
		position = state.nextPosition
		state.positions[id] = position
		if agent.IsUserAuthoredTurnMessage(message) {
			state.visibleTurn++
		}
		visibleTurn = state.visibleTurn
		state.turns[id] = visibleTurn
	} else if !upsert {
		return fmt.Errorf("session: duplicate indexed message id %q", id)
	}
	version := state.versions[id] + 1
	state.versions[id] = version
	if exists {
		if err := flushHistoryBuildRows(ctx, state.tx, state); err != nil {
			return err
		}
		if _, err := state.statements.expire.ExecContext(ctx, sequence, id); err != nil {
			return err
		}
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	ref, err := content.Put(ctx, bytes.NewReader(body), sessioncontent.Metadata{MediaType: "application/json"})
	if err != nil {
		return err
	}
	if err := insertContentRef(ctx, state, ref); err != nil {
		return err
	}
	state.messages = append(state.messages, []any{id, version, position, sequence, 0, string(message.Role), messagePreview(message), nil, ref.Digest, ref.Bytes, ref.IndexDigest, 1, "", visibleTurn})
	return nil
}

func messageSearchText(message provider.Message) string {
	parts := []string{message.Content, message.RawContent, message.ReasoningContent}
	return strings.Join(parts, "\n")
}

func insertContentRef(ctx context.Context, state *historyBuildState, ref sessioncontent.Ref) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state.contentRefs = append(state.contentRefs, []any{ref.Digest, ref.Bytes, ref.IndexDigest})
	return nil
}

func messagePreview(message provider.Message) string {
	preview := strings.TrimSpace(message.Content)
	if preview == "" {
		preview = strings.TrimSpace(message.RawContent)
	}
	runes := []rune(preview)
	if len(runes) > 240 {
		preview = string(runes[:240])
	}
	return preview
}

func encodeHistoryCursor(cursor historyCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeHistoryCursor(value string) (historyCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return historyCursor{}, errors.New("session: invalid history cursor")
	}
	var cursor historyCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return historyCursor{}, errors.New("session: invalid history cursor")
	}
	return cursor, nil
}

func encodeSearchHistoryCursor(cursor searchHistoryCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeSearchHistoryCursor(value string) (searchHistoryCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return searchHistoryCursor{}, errors.New("session: invalid history search cursor")
	}
	var cursor searchHistoryCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return searchHistoryCursor{}, errors.New("session: invalid history search cursor")
	}
	return cursor, nil
}

func scanMetadataUint(row *sql.Row, target *uint64) error {
	var value string
	if err := row.Scan(&value); err != nil {
		return err
	}
	_, err := fmt.Sscan(value, target)
	return err
}
