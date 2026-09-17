package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// CreateCollabSessionResult is the host's receipt for a freshly created
// collaborating session. ContactID is present immediately — the transcript and
// its contact_id are written before the call returns, so the session is
// addressable without waiting for a first run (task 154).
type CreateCollabSessionResult struct {
	TopicID     string `json:"topicId"`
	ContactID   string `json:"contactId"`
	SessionPath string `json:"sessionPath"`
	Purpose     string `json:"purpose"`
	Group       string `json:"group,omitempty"`
	GroupID     string `json:"groupId,omitempty"`
}

// CreateCollabSessionFunc is the host capability that creates a session in a
// collaboration group. It is nil in hosts that cannot create sessions.
// Either group (a new group's title) or groupID (an existing group) may be set.
type CreateCollabSessionFunc func(workspaceRoot, title, purpose, group, groupID string) (CreateCollabSessionResult, error)

// NewCreateCollabSessionTool exposes host session creation to a self-organising
// secretary (task 19 / 144, hardened by task 154). Without a host callback the
// tool is not registered at all: a tool that always fails would be worse than
// absent.
func NewCreateCollabSessionTool(workspaceRoot string, create CreateCollabSessionFunc) tool.Tool {
	return createCollabSessionTool{workspaceRoot: workspaceRoot, create: create}
}

type createCollabSessionTool struct {
	workspaceRoot string
	create        CreateCollabSessionFunc
}

func (createCollabSessionTool) Name() string { return "create_collab_session" }

func (createCollabSessionTool) Description() string {
	return "Create a new collaborating session with a one-line purpose and file it into a group. The transcript and contact_id are written immediately, so the session is addressable in the contact directory (通讯录) BEFORE anyone opens it — no first run required. Returns contactId, topicId, and sessionPath; address it with talk_to_session using contact_id. Group is required so the team stays visible in the sidebar. Experimental."
}

func (createCollabSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"Session title."},"purpose":{"type":"string","description":"One-line duty, e.g. 'React frontend expert'."},"group":{"type":"string","description":"Group title. Created when absent; matched by id when group_id is also given."},"group_id":{"type":"string","description":"Existing group id to join. Takes precedence over the title match, so renaming a group cannot detach the team."}},"required":["title","purpose"]}`)
}

func (createCollabSessionTool) ReadOnly() bool { return false }

func (t createCollabSessionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Title   string `json:"title"`
		Purpose string `json:"purpose"`
		Group   string `json:"group"`
		GroupID string `json:"group_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Purpose) == "" {
		return "", fmt.Errorf("title and purpose are required")
	}
	if strings.TrimSpace(p.Group) == "" && strings.TrimSpace(p.GroupID) == "" {
		return "", fmt.Errorf("group or group_id is required: an ungrouped expert session is invisible to the team view")
	}
	if t.create == nil {
		return "", fmt.Errorf("create_collab_session: this host cannot create sessions")
	}
	result, err := t.create(t.workspaceRoot, strings.TrimSpace(p.Title), strings.TrimSpace(p.Purpose), strings.TrimSpace(p.Group), strings.TrimSpace(p.GroupID))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"topicId":     result.TopicID,
		"contactId":   result.ContactID,
		"sessionPath": result.SessionPath,
		"title":       strings.TrimSpace(p.Title),
		"purpose":     strings.TrimSpace(p.Purpose),
		"group":       strings.TrimSpace(p.Group),
		"groupId":     strings.TrimSpace(p.GroupID),
		"addressable": true,
		"note":        "contactId is live now — talk_to_session(to=contactId) works without opening the session",
	})
	return string(out), nil
}
