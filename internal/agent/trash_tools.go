package agent

// 任务 tl1710：agent 会话回收站缺口三件中的两件——trash_list / trash_restore。
// 20261119 事故：delete_session 把 hasTurn=true 的『决策模式』移入回收站后，
// agent 侧没有任何工具能查看回收站内容或还原指定对话，只能请用户去 UI
// Trash 页手动恢复。这两个工具补上「列」与「还原」两面，是 delete_session
// 的逆操作伴生面。
//
// 契约与既有 collab 工具一致（manage_sessions 同款）：宿主拥有全部读写；
// 工具只透传与包装。回收站的目录/元数据/还原通道全部留在宿主（desktop 的
// .trash 布局与 restoreSession 链），agent 包不复制第二套真相源。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// TrashRestoreUntilManual is the restoreUntil value trash_list reports for
// every entry. The desktop trash has no automatic purge (the same contract
// delete_session's RestoreUntil documents), so every entry is restorable
// until a human purges it from the Trash page.
const TrashRestoreUntilManual = "manual"

// TrashEntry is one row of the trash_list answer: enough for the caller to
// tell "is this the conversation I meant" (title/hasTurn/deleted time) and to
// hand the exact identity back to trash_restore (sessionPath).
type TrashEntry struct {
	// Title is the display title the contact directory used before the delete
	// (custom title, else topic title, else file stem). Empty for an
	// empty-shell bucket (transcript gone mid-restore/purge).
	Title string `json:"title"`
	// SessionPath is the bucket transcript path — the value trash_restore
	// takes. It is NOT a live session path; the transcript lives inside the
	// trash bucket.
	SessionPath string `json:"sessionPath"`
	// HasTurn mirrors delete_session's impact field (SessionTranscriptHas-
	// Content): true when the trashed transcript still holds real turns, i.e.
	// the entry is not an empty shell. This is the field the 20261119
	// incident needed — "决策模式" was trashed while its hasTurn was only
	// visible in delete_session's dry run.
	HasTurn bool `json:"hasTurn"`
	// DeletedAt is the trash-meta timestamp (unix milliseconds); 0 when the
	// meta is missing/unparseable (the trashedSessionDeletedAt semantics).
	DeletedAt int64 `json:"deletedAt"`
	// RestoreUntil is always TrashRestoreUntilManual — no auto purge exists.
	RestoreUntil string `json:"restoreUntil"`
	// Scope/WorkspaceRoot name the session directory the bucket belongs to
	// (global / project + root), so the caller can tell two same-titled
	// trashed sessions apart across scopes.
	Scope         string `json:"scope,omitempty"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
}

// TrashListFunc is the host capability behind trash_list. Nil = not supported.
// The host enumerates every known session directory's trash buckets — the
// same listing the desktop Trash page renders.
type TrashListFunc func() ([]TrashEntry, error)

// TrashRestoreFunc is the host capability behind trash_restore: move one
// trashed bucket back into its session directory through the same channel the
// desktop Trash page uses, so the sidebar/grouping/transcript come back
// exactly as they were before the delete. Nil = not supported.
type TrashRestoreFunc func(path string) error

// NewTrashListTool exposes the host's trash contents to a collaborating agent
// (tl1710 件1).
func NewTrashListTool(cfg SessionCollabConfig, list TrashListFunc) tool.Tool {
	return trashListTool{cfg: cfg, list: list}
}

type trashListTool struct {
	cfg  SessionCollabConfig
	list TrashListFunc
}

func (trashListTool) Name() string { return "trash_list" }

func (trashListTool) Description() string {
	return "List the desktop trash (manual-restore trash, no automatic purge): every session moved there by delete_session or the desktop Delete/Archive actions. Each entry carries title, sessionPath (the value trash_restore takes), hasTurn (transcript still holds real turns — this is how you tell a real conversation from an empty shell), deletedAt (unix ms), restoreUntil (always \"manual\"), and the scope/workspace_root the bucket belongs to. Read-only. To bring one back, pass its sessionPath to trash_restore. Experimental."
}

func (trashListTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (trashListTool) ReadOnly() bool { return true }

func (t trashListTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	if t.list == nil {
		return "", fmt.Errorf("trash_list: this host cannot list the trash")
	}
	entries, err := t.list()
	if err != nil {
		return "", err
	}
	if entries == nil {
		entries = []TrashEntry{}
	}
	hasTurn := 0
	for _, e := range entries {
		if e.HasTurn {
			hasTurn++
		}
	}
	out := map[string]any{
		"count":        len(entries),
		"hasTurnCount": hasTurn,
		"items":        entries,
		"restoreUntil": TrashRestoreUntilManual,
		"note": "Entries are listed newest-deleted first. To restore one, re-run trash_restore with its sessionPath." +
			"｜条目按删除时间从新到旧排列；要还原某条，把它的 sessionPath 传给 trash_restore。" +
			"｜條目按刪除時間從新到舊排列；要還原某條，把它的 sessionPath 傳給 trash_restore。",
	}
	outJSON, _ := json.Marshal(out)
	return string(outJSON), nil
}

// NewTrashRestoreTool exposes the desktop Trash page's restore channel to a
// collaborating agent (tl1710 件2) — the inverse of delete_session.
func NewTrashRestoreTool(cfg SessionCollabConfig, restore TrashRestoreFunc) tool.Tool {
	return trashRestoreTool{cfg: cfg, restore: restore}
}

type trashRestoreTool struct {
	cfg     SessionCollabConfig
	restore TrashRestoreFunc
}

func (trashRestoreTool) Name() string { return "trash_restore" }

func (trashRestoreTool) Description() string {
	return "Restore one session from the desktop trash back into its session directory — the inverse of delete_session, and the same channel the Trash page's Restore button uses, so the sidebar, its group, and the transcript all come back as they were before the delete. Pass `path` exactly as trash_list reported it (a trash bucket transcript path, not a live session path). Refuses with a conflict error when a live session with the same name already exists (the trash bucket stays untouched, nothing is lost). Experimental."
}

func (trashRestoreTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "path":{"type":"string","description":"The sessionPath of the trash entry to restore, exactly as trash_list reported it."}
},"required":["path"],"additionalProperties":false}`)
}

func (trashRestoreTool) ReadOnly() bool { return false }

func (t trashRestoreTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	path := strings.TrimSpace(p.Path)
	if path == "" {
		return "", fmt.Errorf("path is required — take it from trash_list's sessionPath field")
	}
	if t.restore == nil {
		return "", fmt.Errorf("trash_restore: this host cannot restore from the trash")
	}
	if err := t.restore(path); err != nil {
		return "", err
	}
	out := map[string]any{
		"status": "restored",
		"path":   path,
		"note": "The session is back in its session directory; the sidebar/group refresh rides the host's tree-change broadcast. Verify with manage_sessions list or read_session_tail." +
			"｜会话已回到原会话目录；侧栏/分组随宿主的树变更广播刷新。可用 manage_sessions list 或 read_session_tail 核验。" +
			"｜會話已回到原會話目錄；側欄/分組隨宿主的樹變更廣播刷新。可用 manage_sessions list 或 read_session_tail 核驗。",
	}
	outJSON, _ := json.Marshal(out)
	return string(outJSON), nil
}
