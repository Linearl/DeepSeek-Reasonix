package historycatalog

import (
	"context"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// sourceProjectionUnchanged reports whether the stored projection still
// describes the file. Task 195: the transcript fingerprint is size:modTime,
// which changes on every append-only write — comparing it first forced a full
// reload for every change even when nothing about the indexed projection was
// stale. When both sides carry a content digest the digest is authoritative
// (append-only writes bump it; metadata-only churn leaves it alone).
// Fingerprints remain the fallback for sources without an identity
// (schema-1 logs, unreadable sidecars), where the strict comparison still
// applies. The meta fingerprint stays part of the check either way: it gates
// the cheap metadata refresh in indexPath before any transcript work.
func sourceProjectionUnchanged(queryErr error, oldContent, content, oldMeta, meta, oldDigest, digest string) bool {
	if queryErr != nil {
		return false
	}
	if oldDigest != "" && digest != "" {
		return oldDigest == digest && oldMeta == meta
	}
	return oldContent == content && oldMeta == meta
}

// refreshProjectionMeta rewrites only the metadata columns for a transcript
// whose content digest is unchanged (task 195), so a title/topic edit or an
// autosave sidecar bump never costs a full reload. The CAS revision is synced
// with the columns so later append checks compare against the ledger's current
// value. It returns false when the row is absent, the preview sidecar is not
// authoritative, or the row moved under us (the WHERE clause pins
// content_digest) — callers then continue down the append/full paths.
func (c *Catalog) refreshProjectionMeta(ctx context.Context, path string, generation int64, metaFingerprint string, revision int64) (bool, error) {
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		return false, nil
	}
	index, err := agent.LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	if err != nil || !index.RevisionKnown || !index.ListingPreviewKnown {
		return false, nil
	}
	lastActivity := max(int64(0), agent.SessionContentModTime(path).UnixMilli())
	result, err := c.db.ExecContext(ctx, `UPDATE history_sources SET meta_fingerprint=?, custom_title=?, topic_id=?, topic_title=?,
		preview=?, created_at=?, last_activity_at=?, content_revision=?, seen_generation=CASE WHEN ?>0 THEN ? ELSE seen_generation END
		WHERE path=? AND content_digest=?`,
		metaFingerprint, meta.CustomTitle, meta.TopicID, meta.TopicTitle, index.ListingPreview,
		meta.CreatedAt.UnixMilli(), lastActivity, revision, generation, generation, path, index.ContentDigest)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (c *Catalog) tryAppendPath(ctx context.Context, root Root, path string, generation int64, appendFrom,
	oldMessageCount int, oldRevision, revision int64, digest, contentFingerprint, metaFingerprint string) (bool, error) {
	if appendFrom != oldMessageCount || revision != oldRevision+1 {
		return false, nil
	}
	index, err := agent.LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	// Task 195: the tail range must be strictly non-empty. A same-count rewrite
	// (compaction that swaps message text without changing the count) satisfies
	// every other condition — revision+1, digest match — yet appends zero rows,
	// leaving the old FTS terms in place. `<=` sends that case, like every
	// other refused increment, to the full reload that rebuilds the terms.
	if err != nil || !index.RevisionKnown || index.Revision != revision || index.ContentDigest != digest || index.MessageCount <= appendFrom {
		return false, nil
	}
	tail, checkedIndex, err := agent.LoadSessionDisplayMessageRange(path, appendFrom, index.MessageCount)
	if err != nil || checkedIndex.ContentDigest != digest || checkedIndex.Revision != revision {
		return false, nil
	}
	meta, _, _ := agent.LoadBranchMeta(path)
	lastActivity := max(int64(0), agent.SessionContentModTime(path).UnixMilli())
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE history_sources SET root=?,source=?,scope=?,workspace_root=?,content_revision=?,
		content_digest=?,content_fingerprint=?,meta_fingerprint=?,message_count=?,indexed_message_count=?,custom_title=?,topic_id=?,
		topic_title=?,preview=?,created_at=?,last_activity_at=?,health='ok',missing_since=0,
		seen_generation=CASE WHEN ?>0 THEN ? ELSE seen_generation END,last_error=''
		WHERE path=? AND content_revision=? AND indexed_message_count=?`, root.Path, root.Source, root.Scope, root.WorkspaceRoot,
		revision, digest, contentFingerprint, metaFingerprint, index.MessageCount, index.MessageCount, meta.CustomTitle, meta.TopicID,
		meta.TopicTitle, meta.Preview, meta.CreatedAt.UnixMilli(), lastActivity, generation, generation, path, oldRevision, oldMessageCount)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		_ = tx.Rollback()
		return false, nil
	}
	for _, doc := range documents(tail) {
		result, err := tx.ExecContext(ctx, `INSERT INTO history_documents(source_path,message_index,part_index,role,kind,tool_name,token_count)
			VALUES(?,?,?,?,?,?,?)`, path, appendFrom+doc.message, doc.part, doc.role, doc.kind, doc.tool, doc.count)
		if err != nil {
			_ = tx.Rollback()
			return false, err
		}
		rowID, err := result.LastInsertId()
		if err != nil {
			_ = tx.Rollback()
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO history_fts(rowid,terms) VALUES(?,?)`, rowID, doc.terms); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	newRevision, err := bump(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	c.publish(newRevision, []string{root.Path}, "source-appended")
	return true, nil
}
