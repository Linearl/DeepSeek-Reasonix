package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// Session management tools (task 170): the "改" half of the collaboration CRUD.
// Renaming and re-grouping are metadata operations - they touch the topic title
// map and the branch-meta sidecar, never the transcript - so they stay safe for
// a session that is mid-turn. The host owns both writes; the tools only resolve
// a target and report the outcome.

// RenameSessionResult reports one title change.
type RenameSessionResult struct {
	TopicID     string `json:"topicId,omitempty"`
	ContactID   string `json:"contactId,omitempty"`
	SessionPath string `json:"sessionPath,omitempty"`
	Previous    string `json:"previousTitle,omitempty"`
	Title       string `json:"title"`
	// ListingSynced says the host updated every listing surface it owns (topic
	// title map, open tab label, branch-meta sidecar) and re-emitted the tree, so
	// the sidebar, search and the contact directory all show the new title.
	ListingSynced bool `json:"listingSynced"`
}

// RenameSessionFunc is the host capability that renames a session. topicID may
// be empty for a session that was never filed as a topic; the host then renames
// by transcript path. Nil = not supported.
type RenameSessionFunc func(topicID, sessionPath, title string) (RenameSessionResult, error)

// MoveTopicToGroupResult reports one grouping change.
type MoveTopicToGroupResult struct {
	TopicID     string `json:"topicId,omitempty"`
	ContactID   string `json:"contactId,omitempty"`
	SessionPath string `json:"sessionPath,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ProjectRoot string `json:"projectRoot,omitempty"`
	GroupID     string `json:"groupId"`
	Group       string `json:"group"`
	// RemovedFrom lists the group titles this session left, so a caller can
	// report a real move rather than "filed".
	RemovedFrom []string `json:"removedFrom,omitempty"`
	// AlreadyFiled is true when the session was already in the target group.
	AlreadyFiled bool `json:"alreadyFiled,omitempty"`
}

// MoveTopicToGroupFunc is the host capability that files a session into a group,
// removing it from whatever group it was in. A topic belongs to exactly one
// group: the group store de-duplicates members, so appending without removing
// would silently leave the session in its old group. Nil = not supported.
type MoveTopicToGroupFunc func(topicID, sessionPath, scope, workspaceRoot, groupID, groupTitle string) (MoveTopicToGroupResult, error)

// NewRenameSessionTool exposes the desktop's rename for a collaborating agent.
func NewRenameSessionTool(cfg SessionCollabConfig, rename RenameSessionFunc) tool.Tool {
	return renameSessionTool{cfg: cfg, rename: rename}
}

type renameSessionTool struct {
	cfg    SessionCollabConfig
	rename RenameSessionFunc
}

func (renameSessionTool) Name() string { return "rename_session" }

func (renameSessionTool) Description() string {
	return "Rename a session (its sidebar/contact-directory title) by contact_id, topic_id or exact title. The new title propagates to every listing surface: the sidebar, search and the contact directory. Metadata only: safe while the session is running (its transcript is untouched). Experimental."
}

func (renameSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"contact_id, topic_id, or exact title of the session to rename."},"title":{"type":"string","description":"New display title."}},"required":["target","title"]}`)
}

func (renameSessionTool) ReadOnly() bool { return false }

func (t renameSessionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target string `json:"target"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Target) == "" {
		return "", fmt.Errorf("target is required")
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	id, err := ResolveTarget(ids, p.Target)
	if err != nil {
		return "", err
	}
	if t.rename == nil {
		return "", fmt.Errorf("rename_session: this host cannot rename sessions")
	}
	result, err := t.rename(id.TopicID, id.SessionPath, title)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.TopicID) == "" {
		result.TopicID = id.TopicID
	}
	if strings.TrimSpace(result.ContactID) == "" {
		result.ContactID = id.ContactID
	}
	if strings.TrimSpace(result.SessionPath) == "" {
		result.SessionPath = id.SessionPath
	}
	if strings.TrimSpace(result.Previous) == "" {
		result.Previous = id.Title
	}
	if strings.TrimSpace(result.Title) == "" {
		result.Title = title
	}
	out, _ := json.Marshal(map[string]any{
		"status": "renamed",
		"result": result,
		"note":   "the sidebar, search and the contact directory now show the new title",
	})
	return string(out), nil
}

// NewMoveTopicToGroupTool exposes the desktop's grouping for a collaborating
// agent (task 170). The target session keeps its contact_id and transcript; only
// its group membership changes.
func NewMoveTopicToGroupTool(cfg SessionCollabConfig, move MoveTopicToGroupFunc) tool.Tool {
	return moveTopicToGroupTool{cfg: cfg, move: move}
}

type moveTopicToGroupTool struct {
	cfg  SessionCollabConfig
	move MoveTopicToGroupFunc
}

func (moveTopicToGroupTool) Name() string { return "move_topic_to_group" }

func (moveTopicToGroupTool) Description() string {
	return "Move a session into a sidebar group by contact_id, topic_id or exact title. The group is created when it does not exist yet, and the session is removed from the group it was in - a session belongs to one group at a time. Addressability is unchanged (same contact_id, same transcript), and the sidebar updates immediately. Experimental."
}

func (moveTopicToGroupTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"contact_id, topic_id, or exact title of the session to move."},"group":{"type":"string","description":"Target group name; created when missing."},"group_id":{"type":"string","description":"Optional group id. Authoritative when given: an existing group is matched by id, never by name."}},"required":["target","group"]}`)
}

func (moveTopicToGroupTool) ReadOnly() bool { return false }

func (t moveTopicToGroupTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target  string `json:"target"`
		Group   string `json:"group"`
		GroupID string `json:"group_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Target) == "" {
		return "", fmt.Errorf("target is required")
	}
	group, groupID := strings.TrimSpace(p.Group), strings.TrimSpace(p.GroupID)
	if group == "" && groupID == "" {
		return "", fmt.Errorf("group is required")
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	id, err := ResolveTarget(ids, p.Target)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id.TopicID) == "" {
		return "", fmt.Errorf("session %q is not filed as a topic, so it cannot be grouped", p.Target)
	}
	if t.move == nil {
		return "", fmt.Errorf("move_topic_to_group: this host cannot regroup sessions")
	}
	result, err := t.move(id.TopicID, id.SessionPath, id.Scope, id.Workspace, groupID, group)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.TopicID) == "" {
		result.TopicID = id.TopicID
	}
	if strings.TrimSpace(result.ContactID) == "" {
		result.ContactID = id.ContactID
	}
	if strings.TrimSpace(result.SessionPath) == "" {
		result.SessionPath = id.SessionPath
	}
	status := "moved"
	if result.AlreadyFiled {
		status = "already_filed"
	}
	out, _ := json.Marshal(map[string]any{
		"status": status,
		"result": result,
		"note":   "the sidebar reflects the move immediately; the session keeps its contact_id and transcript",
	})
	return string(out), nil
}
