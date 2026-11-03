package sessioncatalog

import (
	"context"
	"errors"
	"log/slog"
)

// failDirectoryScan records a failed directory scan on the row and flips the
// catalog-wide status to degraded — the state the desktop sidebar renders as
// "正在整理历史 — 失败" with a rebuild button. tl1315: this was the only writer
// of that banner at runtime and it left no trace (every caller logged at debug
// or not at all), so a user-visible failure could not be tied to its cause.
// The line names the directory and the error; cancellation (catalog close on
// shutdown/rebuild) stays at info so it does not read as a fault.
func (c *Catalog) failDirectoryScan(ctx context.Context, path string, scanErr error) {
	c.mutationMu.Lock()
	_, _ = c.db.ExecContext(ctx, `UPDATE catalog_directories SET state='degraded',error=? WHERE path_key=?`, scanErr.Error(), c.pathKey(path))
	c.mutationMu.Unlock()
	c.statusMu.Lock()
	c.status.State = StateDegraded
	c.status.LastError = scanErr.Error()
	c.statusMu.Unlock()
	if errors.Is(scanErr, context.Canceled) {
		slog.Info("sessioncatalog: directory scan canceled; catalog marked degraded",
			"feature", "catalog-reconcile", "path", path, "err", scanErr)
		return
	}
	slog.Warn("sessioncatalog: directory scan failed; catalog marked degraded",
		"feature", "catalog-reconcile", "path", path, "err", scanErr)
}
