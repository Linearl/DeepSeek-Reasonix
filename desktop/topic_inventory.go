package main

// Task 550 ②: three-source topic inventory reconcile, aligned with Codex
// doctor/thread_inventory.rs (missing_rollout_paths). Sources: the restored
// tabs (desktop-tabs.json sidecar + in-memory runtime), the per-scope
// topic-state index, and the session files as projected by the session
// catalog. A mismatch is a queryable state, not an error to auto-fix: every
// entry carries a deterministic handling path so "the UI shows a session none
// of the sources have" can never again be undebuggable. Nothing is deleted
// here — cleanup stays with the existing archive/transient-blank sweeps.

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"reasonix/internal/sessioncatalog"
	"reasonix/internal/topicstate"
)

// Topic-inventory mismatch kinds. Values are stable identifiers: they appear
// in slog fields and in the binding payload.
const (
	topicInventoryTabWithoutIndexEntry      = "tab_without_index_entry"
	topicInventoryTabWithoutSessionFile     = "tab_without_session_file"
	topicInventoryIndexTopicWithoutSessions = "index_topic_without_session_or_tab"
	topicInventorySessionProjectionDegraded = "session_projection_degraded"

	topicInventoryHandlingTabWithoutIndex = "首个用户回合会重试索引写入；写入失败计入 indexWriteFailures（550①），不再静默"
	topicInventoryHandlingTabWithoutFile  = "下一回合由运行时重新落盘；零字节空会话残留由关闭 tab 时的 transient-blank 清理回收"
	topicInventoryHandlingIndexNoSessions = "等待归档/清理扫描回收或由用户手动删除；本对账不自动删除（550② 只列失配）"
	topicInventoryHandlingDegradedSession = "会话目录扫描完成后重跑 ReconcileTopicInventory 复核"
)

// TopicInventoryMismatch is one cross-source disagreement.
type TopicInventoryMismatch struct {
	Kind          string `json:"kind"`
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	TopicID       string `json:"topicId,omitempty"`
	TabID         string `json:"tabId,omitempty"`
	SessionPath   string `json:"sessionPath,omitempty"`
	Detail        string `json:"detail"`
	Handling      string `json:"handling"`
}

// TopicInventoryReconcileResult is the queryable reconcile state.
type TopicInventoryReconcileResult struct {
	ReconciledAt       int64                    `json:"reconciledAt"`
	ScannedTabs        int                      `json:"scannedTabs"`
	ScannedTopics      int                      `json:"scannedTopics"`
	SessionCatalogOpen bool                     `json:"sessionCatalogOpen"`
	IndexWriteFailures uint64                   `json:"indexWriteFailures"`
	Mismatches         []TopicInventoryMismatch `json:"mismatches"`
}

// topicInventoryState caches the latest reconcile result so the binding is a
// cheap read between runs.
type topicInventoryState struct {
	mu     sync.Mutex
	latest *TopicInventoryReconcileResult
}

func (a *App) storeTopicInventory(result *TopicInventoryReconcileResult) {
	a.topicInventory.mu.Lock()
	a.topicInventory.latest = result
	a.topicInventory.mu.Unlock()
}

// GetTopicInventoryMismatches returns the most recent reconcile result
// without re-running it. The startup pass primes it; tests and the frontend
// can poll it without side effects.
func (a *App) GetTopicInventoryMismatches() TopicInventoryReconcileResult {
	a.topicInventory.mu.Lock()
	defer a.topicInventory.mu.Unlock()
	if a.topicInventory.latest == nil {
		return TopicInventoryReconcileResult{Mismatches: []TopicInventoryMismatch{}}
	}
	return *a.topicInventory.latest
}

// ReconcileTopicInventory re-runs the three-source reconcile on demand and
// returns the fresh result. Aligned with Codex doctor/thread_inventory.rs:
// missing counterparts are normal intermediate states; the reconcile makes
// them visible and queryable instead of failing the caller.
func (a *App) ReconcileTopicInventory() TopicInventoryReconcileResult {
	ctx, cancel := a.catalogReadContext()
	defer cancel()
	return *a.reconcileTopicInventory(ctx)
}

// reconcileTopicInventory runs the scan and caches + returns the result.
// The startup caller logs a summary; the binding returns it.
func (a *App) reconcileTopicInventory(ctx context.Context) *TopicInventoryReconcileResult {
	result := &TopicInventoryReconcileResult{
		ReconciledAt:       time.Now().UnixMilli(),
		SessionCatalogOpen: a.sessionCatalog.Load() != nil,
		IndexWriteFailures: a.topicIndexWriteFailures.Load(),
		Mismatches:         []TopicInventoryMismatch{},
	}

	// Source 1: the restored tabs. A blank tab before its first turn owns no
	// topic yet and is the tab bar's business, not the inventory's.
	type tabRef struct {
		id          string
		scope       string
		root        string
		topicID     string
		sessionPath string
	}
	tabs := []tabRef{}
	a.mu.RLock()
	for id, tab := range a.tabs {
		if tab == nil || strings.TrimSpace(tab.TopicID) == "" {
			continue
		}
		scope, root := normalizeDesktopTopicScope(tab.Scope, tab.WorkspaceRoot)
		tabs = append(tabs, tabRef{id: id, scope: scope, root: root,
			topicID: strings.TrimSpace(tab.TopicID), sessionPath: strings.TrimSpace(tab.SessionPath)})
	}
	for key, tab := range a.detachedSessions {
		if tab == nil || strings.TrimSpace(tab.TopicID) == "" {
			continue
		}
		scope, root := normalizeDesktopTopicScope(tab.Scope, tab.WorkspaceRoot)
		tabs = append(tabs, tabRef{id: "detached:" + key, scope: scope, root: root,
			topicID: strings.TrimSpace(tab.TopicID), sessionPath: strings.TrimSpace(tab.SessionPath)})
	}
	a.mu.RUnlock()
	result.ScannedTabs = len(tabs)

	// Source 2: the per-scope topic-state index (global + every registered
	// project root).
	type scopeRef struct {
		scope string
		root  string
	}
	scopes := []scopeRef{{scope: "global", root: ""}}
	for _, project := range loadProjectsFile().Projects {
		scopes = append(scopes, scopeRef{scope: "project", root: normalizeProjectRoot(project.Root)})
	}
	type indexKey struct {
		scope string
		root  string
	}
	indexByScope := map[indexKey]map[string]topicstate.Record{}
	for _, ref := range scopes {
		key := indexKey{scope: ref.scope, root: ref.root}
		if _, seen := indexByScope[key]; seen {
			continue
		}
		records := map[string]topicstate.Record{}
		if snapshot, err := desktopTopicState.snapshot(ref.root); err == nil {
			records = snapshot.Records
		}
		indexByScope[key] = records
		result.ScannedTopics += len(records)
	}

	// Tab-side mismatches.
	for _, tab := range tabs {
		records := indexByScope[indexKey{scope: tab.scope, root: tab.root}]
		if _, indexed := records[tab.topicID]; !indexed {
			result.Mismatches = append(result.Mismatches, TopicInventoryMismatch{
				Kind: topicInventoryTabWithoutIndexEntry, Scope: tab.scope, WorkspaceRoot: tab.root,
				TopicID: tab.topicID, TabID: tab.id, SessionPath: tab.sessionPath,
				Detail:   "tab 引用的 topic 不在 topic-state 索引里（550① 的索引写入失败即此失配的成因之一）",
				Handling: topicInventoryHandlingTabWithoutIndex,
			})
		}
		if tab.sessionPath == "" {
			continue
		}
		if _, err := os.Stat(tab.sessionPath); err != nil {
			result.Mismatches = append(result.Mismatches, TopicInventoryMismatch{
				Kind: topicInventoryTabWithoutSessionFile, Scope: tab.scope, WorkspaceRoot: tab.root,
				TopicID: tab.topicID, TabID: tab.id, SessionPath: tab.sessionPath,
				Detail:   "tab 持有的会话文件在磁盘上不存在（被清理的零字节残留或被外部删除）",
				Handling: topicInventoryHandlingTabWithoutFile,
			})
		}
	}

	// Source 3: session files as projected by the catalog. The catalog is the
	// indexed projection of the transcripts; while it has not opened, the
	// topic-side check degrades into one explicit mismatch instead of a silent
	// skip, so "UI 可见但哪个源都没有" stays catchable.
	catalog := a.sessionCatalog.Load()
	for _, ref := range scopes {
		scopeIndex := indexKey{scope: ref.scope, root: ref.root}
		for topicID, record := range indexByScope[scopeIndex] {
			tabReferenced := false
			for _, tab := range tabs {
				if tab.scope == ref.scope && tab.root == ref.root && tab.topicID == topicID {
					tabReferenced = true
					break
				}
			}
			if tabReferenced {
				continue
			}
			if catalog == nil {
				result.Mismatches = append(result.Mismatches, TopicInventoryMismatch{
					Kind: topicInventorySessionProjectionDegraded, Scope: ref.scope, WorkspaceRoot: ref.root,
					TopicID:  topicID,
					Detail:   "会话目录投影未打开，无法核对文件侧",
					Handling: topicInventoryHandlingDegradedSession,
				})
				continue
			}
			// GetTopic applies a zero-session tombstone overlay (a topic whose
			// sessions are all gone reads as not-found), so the reconcile uses
			// the row-level read: found-ness and session count stay separable.
			_, sessionCount, ok, err := catalog.GetTopicWithSessionCount(ctx, sessioncatalog.TopicKey{
				Scope: ref.scope, WorkspaceRoot: ref.root, TopicID: topicID,
			})
			if err != nil || !ok {
				// An unreadable catalog row is the catalog's own degraded
				// state, not an inventory mismatch; skip rather than
				// double-report.
				continue
			}
			if sessionCount > 0 {
				continue
			}
			// The Global-orphan class: indexed, no session ever. Zero sessions
			// implies zero activity; userTurns>0 means the conversation had
			// interaction and only its files were lost — still reported, with
			// that distinction in Detail.
			meta := topicAutoTitleMeta{}
			_ = json.Unmarshal(record.AutoMeta, &meta)
			detail := "索引有、会话文件侧无（零会话）：550 定义的孤儿类（零会话 + 零活动 + 无 userTurns）"
			if meta.UserTurns > 0 {
				detail = "索引有、会话文件侧无，但 auto meta 记录 userTurns>0：会话曾有交互，文件可能丢失"
			}
			result.Mismatches = append(result.Mismatches, TopicInventoryMismatch{
				Kind: topicInventoryIndexTopicWithoutSessions, Scope: ref.scope, WorkspaceRoot: ref.root,
				TopicID:  topicID,
				Detail:   detail,
				Handling: topicInventoryHandlingIndexNoSessions,
			})
		}
	}

	a.storeTopicInventory(result)
	return result
}

// logTopicInventorySummary emits the reconcile summary. One line keeps the
// mismatch count visible without flooding the log per entry; the full list
// stays queryable via GetTopicInventoryMismatches.
func logTopicInventorySummary(result *TopicInventoryReconcileResult) {
	if result == nil {
		return
	}
	if len(result.Mismatches) == 0 && result.IndexWriteFailures == 0 {
		slog.Info("desktop: topic inventory reconcile clean",
			"tabs", result.ScannedTabs, "topics", result.ScannedTopics)
		return
	}
	counts := map[string]int{}
	for _, mismatch := range result.Mismatches {
		counts[mismatch.Kind]++
	}
	slog.Warn("desktop: topic inventory reconcile found mismatches",
		"tabs", result.ScannedTabs, "topics", result.ScannedTopics,
		"kinds", counts, "index_write_failures", result.IndexWriteFailures,
		"query", "GetTopicInventoryMismatches")
}
