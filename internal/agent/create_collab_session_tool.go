package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// CreateCollabSessionFunc is the host capability that creates a session in a
// collaboration group. It is nil in hosts that cannot create sessions.
// Either group (a new group's title) or groupID (an existing group) may be set.
type CreateCollabSessionFunc func(workspaceRoot, title, purpose, group, groupID string) (topicID string, err error)

// NewCreateCollabSessionTool exposes host session creation to a self-organising
// secretary (task 19 / 144). Without a host callback the tool is not
// registered at all: a tool that always fails would be worse than absent.
func NewCreateCollabSessionTool(workspaceRoot string, create CreateCollabSessionFunc) tool.Tool {
	return createCollabSessionTool{workspaceRoot: workspaceRoot, create: create}
}

type createCollabSessionTool struct {
	workspaceRoot string
	create        CreateCollabSessionFunc
}

func (createCollabSessionTool) Name() string { return "create_collab_session" }

func (createCollabSessionTool) Description() string {
	return "Create a new collaborating session (a topic) with a one-line purpose and file it into a group, so a team is visible together in the sidebar. Use for standing domain experts the secretariat will route work to. Pass group to create/join a group by name, or group_id to join an existing group by its stable id (preferred when you already know the team). The new session answers to a contact_id once it has run once; discover it with list_addressable_sessions. Experimental."
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
	topicID, err := t.create(t.workspaceRoot, strings.TrimSpace(p.Title), strings.TrimSpace(p.Purpose), strings.TrimSpace(p.Group), strings.TrimSpace(p.GroupID))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]string{
		"topicId": topicID,
		"title":   strings.TrimSpace(p.Title),
		"purpose": strings.TrimSpace(p.Purpose),
		"group":   strings.TrimSpace(p.Group),
		"groupId": strings.TrimSpace(p.GroupID),
		"next":    "the session registers its contact_id after its first run; poll list_addressable_sessions",
	})
	return string(out), nil
}
