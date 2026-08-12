package main

import (
	"context"
	"strings"

	"reasonix/internal/sessioncatalog"
)

// resolveCanonicalSessionPath returns a unique adopted/canonical leaf for the
// topic that owns path, when the catalog has one. Empty means keep path.
// Retarget happens before Controller create/rebind so the new controller leases
// and binds authority on the canonical path only.
func (a *App) resolveCanonicalSessionPath(path string) string {
	if a == nil || strings.TrimSpace(path) == "" {
		return ""
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return ""
	}
	ctx := context.Background()
	rec, ok, err := catalog.GetSession(ctx, path)
	if err != nil || !ok {
		return ""
	}
	if rec.TopicID == "" {
		// Group by recovery group when topic is unset.
		if rec.RecoveryCanonical && rec.RecoveryRole == sessioncatalog.RecoveryRoleAdopted {
			return ""
		}
		return ""
	}
	page, err := catalog.ListSessions(ctx, sessioncatalog.SessionPageRequest{
		Scope:         rec.Scope,
		WorkspaceRoot: rec.WorkspaceRoot,
		Limit:         sessioncatalog.MaxLimit,
	})
	if err != nil {
		return ""
	}
	var sameTopic []sessioncatalog.SessionRecord
	for _, s := range page.Items {
		if s.TopicID == rec.TopicID && s.Scope == rec.Scope && s.WorkspaceRoot == rec.WorkspaceRoot {
			sameTopic = append(sameTopic, s)
		}
	}
	return sessioncatalog.CanonicalSessionPathForTopic(sameTopic, path)
}
