package agent

// 任务 892A：manage_sessions 工具——单工具多 action 的会话/分组整理入口
// （而非一堆小工具）。覆盖 list / rename_session / move_session /
// reorder_session / group_create / group_rename / group_delete /
// rollback / op_log；会话动作做目标解析（contact_id / topic_id / 标题），
// 分组动作用 kind 参数在两套分组体系间切换（session=侧栏会话组，
// project=项目分组 catalog），其余原样转发宿主。
//
// 契约与既有 collab 工具一致（task 170 同款）：宿主拥有全部写入；
// 工具只解析目标、校验参数、转发。宿主 nil（CLI/测试）时给可操作的拒绝。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// ManageSessionsFunc is the host capability behind the tool: one dispatch
// face for every action. The host receives the same JSON object the tool
// got, with `target` already resolved to topic_id/session_path/scope for the
// session actions. Nil host = not supported.
type ManageSessionsFunc func(action string, args json.RawMessage) (json.RawMessage, error)

// manageSessionsActions enumerates the supported action names. Kept exported
// so host tests and the desktop can assert the same contract.
var manageSessionsActions = []string{
	"list",
	"rename_session",
	"move_session",
	"reorder_session",
	"group_create",
	"group_rename",
	"group_delete",
	"archive",
	"rollback",
	"op_log",
}

// NewManageSessionsTool exposes the host's session/group management to a
// collaborating agent (task 892).
func NewManageSessionsTool(cfg SessionCollabConfig, manage ManageSessionsFunc) tool.Tool {
	return manageSessionsTool{cfg: cfg, manage: manage}
}

type manageSessionsTool struct {
	cfg    SessionCollabConfig
	manage ManageSessionsFunc
}

func (manageSessionsTool) Name() string { return "manage_sessions" }

func (manageSessionsTool) Description() string {
	return "Organize sessions and their groups with one tool, many actions. Actions: list (groups with nesting/parentId/order + each session's group), rename_session, move_session (a session belongs to one group at a time), reorder_session (order inside a group), group_create/group_rename/group_delete for both group kinds (kind=session sidebar groups, kind=project project-group catalog), archive (tl1305: archive a topic the way the desktop Archive button does — its sessions move to the manual-restore trash, the topic leaves the sidebar; proposal-first like group_delete), op_log (recent operations), rollback (restore one logged operation). Nesting is arbitrary depth; a parent must exist and cycles are rejected. group_delete is proposal-first: call without confirm to get the plan (children are promoted by default, cascade=true deletes the subtree; sessions are unfiled, never deleted), then confirm=true to execute. archive is proposal-first too: without confirm it reports the sessions that would move and whether work is active; with confirm=true it runs the desktop archive path (active sessions refuse until their turn finishes). The other writes are metadata-only and safe while sessions run; the sidebar refreshes immediately. Experimental."
}

func (manageSessionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
	    "action": {
	      "type": "string",
	      "enum": ["list", "rename_session", "move_session", "reorder_session", "group_create", "group_rename", "group_delete", "archive", "rollback", "op_log"],
	      "description": "Which management operation to run."
	    },
	    "target": {"type": "string", "description": "Session actions (incl. archive): contact_id, topic_id, or exact title of the session."},
    "title": {"type": "string", "description": "rename_session/group_create/group_rename: the new display title."},
    "group": {"type": "string", "description": "move_session: target group title; created when missing."},
    "group_id": {"type": "string", "description": "group ops / move_session: group id (authoritative over title when given)."},
    "parent_id": {"type": "string", "description": "group_create: id of the parent group in the same roster; empty = top level. Cycles are rejected."},
    "kind": {"type": "string", "enum": ["session", "project"], "description": "group_* actions: which grouping system. Default session (sidebar topic groups); project = the project-group catalog."},
    "index": {"type": "integer", "description": "reorder_session: target index inside the group's member order (clamped)."},
    "cascade": {"type": "boolean", "description": "group_delete: delete the whole subtree instead of promoting children. Default false."},
    "confirm": {"type": "boolean", "description": "group_delete: execute the proposal. Without it the call only returns the plan."},
    "scope": {"type": "string", "description": "Session groups live per roster: global or project. Omit to let the host resolve from the session."},
    "workspace_root": {"type": "string", "description": "Project-scope roster root; omit for global."},
    "op_id": {"type": "string", "description": "rollback: the operation id from a write result or op_log."}
  },
  "required": ["action"]
}`)
}

func (manageSessionsTool) ReadOnly() bool { return false }

func (t manageSessionsTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	action := strings.TrimSpace(p.Action)
	if action == "" {
		return "", fmt.Errorf("action is required (%s)", strings.Join(manageSessionsActions, ", "))
	}
	if t.manage == nil {
		return "", fmt.Errorf("manage_sessions: this host cannot manage sessions")
	}
	forward, err := t.forwardArgs(action, args)
	if err != nil {
		return "", err
	}
	out, err := t.manage(action, forward)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// forwardArgs validates the action's required parameters and rewrites the
// session actions' `target` into the ids the host works with.
func (t manageSessionsTool) forwardArgs(action string, args json.RawMessage) (json.RawMessage, error) {
	var p struct {
		Target    string `json:"target"`
		Title     string `json:"title"`
		Group     string `json:"group"`
		GroupID   string `json:"group_id"`
		OpID      string `json:"op_id"`
		Index     int    `json:"index"`
		Scope     string `json:"scope"`
		Workspace string `json:"workspace_root"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	// resolve maps the caller's target reference onto the directory identity,
	// exactly like rename_session/move_topic_to_group already do.
	resolve := func() (sessioncollab.Identity, error) {
		if strings.TrimSpace(p.Target) == "" {
			return sessioncollab.Identity{}, fmt.Errorf("target is required (%s)", action)
		}
		ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
		return ResolveTarget(ids, p.Target)
	}
	switch action {
	case "list", "op_log":
		// No parameters are required; forward as-is.
		return args, nil
	case "rename_session":
		if strings.TrimSpace(p.Title) == "" {
			return nil, fmt.Errorf("title is required (rename_session)")
		}
		id, err := resolve()
		if err != nil {
			return nil, err
		}
		return marshalManageArgs(map[string]any{
			"topic_id":     id.TopicID,
			"session_path": id.SessionPath,
			"title":        strings.TrimSpace(p.Title),
		})
	case "move_session":
		if strings.TrimSpace(p.Group) == "" && strings.TrimSpace(p.GroupID) == "" {
			return nil, fmt.Errorf("group or group_id is required (move_session)")
		}
		id, err := resolve()
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(id.TopicID) == "" {
			return nil, fmt.Errorf("session %q is not filed as a topic, so it cannot be grouped", p.Target)
		}
		return marshalManageArgs(map[string]any{
			"topic_id":       id.TopicID,
			"session_path":   id.SessionPath,
			"scope":          firstNonEmpty(p.Scope, id.Scope),
			"workspace_root": firstNonEmpty(p.Workspace, id.Workspace),
			"group_id":       strings.TrimSpace(p.GroupID),
			"group":          strings.TrimSpace(p.Group),
		})
	case "reorder_session":
		if strings.TrimSpace(p.GroupID) == "" {
			return nil, fmt.Errorf("group_id is required (reorder_session)")
		}
		id, err := resolve()
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(id.TopicID) == "" {
			return nil, fmt.Errorf("session %q is not filed as a topic, so it cannot be reordered", p.Target)
		}
		return marshalManageArgs(map[string]any{
			"topic_id":       id.TopicID,
			"group_id":       strings.TrimSpace(p.GroupID),
			"index":          p.Index,
			"scope":          firstNonEmpty(p.Scope, id.Scope),
			"workspace_root": firstNonEmpty(p.Workspace, id.Workspace),
		})
	case "group_create":
		if strings.TrimSpace(p.Title) == "" {
			return nil, fmt.Errorf("title is required (group_create)")
		}
		return args, nil
	case "group_rename":
		if strings.TrimSpace(p.GroupID) == "" || strings.TrimSpace(p.Title) == "" {
			return nil, fmt.Errorf("group_id and title are required (group_rename)")
		}
		return args, nil
	case "group_delete":
		// Proposal-first: the host returns the plan unless confirm=true. Both
		// faces are the same args, so nothing to check here beyond the id.
		if strings.TrimSpace(p.GroupID) == "" {
			return nil, fmt.Errorf("group_id is required (group_delete)")
		}
		return args, nil
	case "archive":
		// tl1305: same target resolution as the other session actions, but the
		// desktop archive path works on the TOPIC, so an unfiled session (no
		// topic id) is refused with the alternative named — delete_session owns
		// the single-transcript face.
		id, err := resolve()
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(id.TopicID) == "" {
			return nil, fmt.Errorf("session %q is not filed as a topic, so it cannot be archived — archive follows the desktop topic; use delete_session to trash a standalone transcript, or move_session to file it first", p.Target)
		}
		var confirm struct {
			Confirm bool `json:"confirm"`
		}
		if err := json.Unmarshal(args, &confirm); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		return marshalManageArgs(map[string]any{
			"topic_id":       id.TopicID,
			"session_path":   id.SessionPath,
			"scope":          firstNonEmpty(p.Scope, id.Scope),
			"workspace_root": firstNonEmpty(p.Workspace, id.Workspace),
			"confirm":        confirm.Confirm,
		})
	case "rollback":
		if strings.TrimSpace(p.OpID) == "" {
			return nil, fmt.Errorf("op_id is required (rollback)")
		}
		return args, nil
	default:
		return nil, fmt.Errorf("unknown action %q (%s)", action, strings.Join(manageSessionsActions, ", "))
	}
}

func marshalManageArgs(fields map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
