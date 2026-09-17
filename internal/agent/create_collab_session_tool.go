package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	// Scope and ProjectRoot report WHERE the session landed ("global" or
	// "project"). A requested project must be verifiable from the result rather
	// than inferred from which parameters were passed.
	Scope       string `json:"scope,omitempty"`
	ProjectRoot string `json:"projectRoot,omitempty"`
}

// CreateCollabSessionRequest is the host request behind create_collab_session.
//
// WorkspaceRoot is the CALLER's own project root (empty or the global root when
// the caller is Global) and decides the default scope. ProjectRoot, when set,
// overrides that default so a secretary can file a session into ANOTHER project
// (task 158.C); the host must refuse a root that is not a registered project
// instead of creating a project nobody opened.
type CreateCollabSessionRequest struct {
	WorkspaceRoot string
	ProjectRoot   string
	Title         string
	Purpose       string
	Group         string
	GroupID       string
}

// CreateCollabSessionFunc is the host capability that creates a session in a
// collaboration group. It is nil in hosts that cannot create sessions.
// Either group (a new group's title) or groupID (an existing group) may be set.
type CreateCollabSessionFunc func(CreateCollabSessionRequest) (CreateCollabSessionResult, error)

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
	return "Create a new collaborating session with a one-line purpose and file it into a group. Without `project` the session lands in the CALLING session's own project — or in Global when the caller is Global — so a session cannot be adopted by another project by accident. Pass `project` (the root of a REGISTERED project) to create it for a different project; an unknown root is refused with the list of known projects, because a typo would otherwise leave a stray project nobody opened. " +
		"The transcript and contact_id are written immediately, so the session is addressable in the contact directory (通讯录) BEFORE anyone opens it — no first run required. Returns contactId, topicId, sessionPath and the scope/projectRoot it landed in; address it with talk_to_session using contact_id. Group is required so the team stays visible in the sidebar. Experimental."
}

func (createCollabSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"Session title."},"purpose":{"type":"string","description":"One-line duty, e.g. 'React frontend expert'."},"group":{"type":"string","description":"Group title. Created when absent; matched by id when group_id is also given."},"group_id":{"type":"string","description":"Existing group id to join. Takes precedence over the title match, so renaming a group cannot detach the team."},"project":{"type":"string","description":"Optional project ROOT to create the session in (e.g. C:\\work\\my-project). Must be a registered project; an unknown root is rejected. Omit to use the calling session's own project, or Global when the caller is Global."},"workspace_root":{"type":"string","description":"Alias of project; pass one of the two."}},"required":["title","purpose","group"]}`)
}

func (createCollabSessionTool) ReadOnly() bool { return false }

func (t createCollabSessionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Title         string `json:"title"`
		Purpose       string `json:"purpose"`
		Group         string `json:"group"`
		GroupID       string `json:"group_id"`
		Project       string `json:"project"`
		WorkspaceRoot string `json:"workspace_root"`
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
	// `project` and its alias `workspace_root` are one axis: accepting both with
	// different values would let one silently win, and the caller could not tell
	// which project it got.
	project, alias := strings.TrimSpace(p.Project), strings.TrimSpace(p.WorkspaceRoot)
	if project != "" && alias != "" && !sameRootRef(project, alias) {
		return "", fmt.Errorf("project and workspace_root name different roots (%q vs %q) — pass exactly one", project, alias)
	}
	if project == "" {
		project = alias
	}
	if t.create == nil {
		return "", fmt.Errorf("create_collab_session: this host cannot create sessions")
	}
	result, err := t.create(CreateCollabSessionRequest{
		WorkspaceRoot: t.workspaceRoot,
		ProjectRoot:   project,
		Title:         strings.TrimSpace(p.Title),
		Purpose:       strings.TrimSpace(p.Purpose),
		Group:         strings.TrimSpace(p.Group),
		GroupID:       strings.TrimSpace(p.GroupID),
	})
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
		"scope":       result.Scope,
		"projectRoot": result.ProjectRoot,
		"addressable": true,
		"note":        "contactId is live now — talk_to_session(to=contactId) works without opening the session",
	})
	return string(out), nil
}

// sameRootRef compares two project-root references for equality. It only
// compares: existence and registration are the host's business, so an unknown
// root still fails with the host's project list.
func sameRootRef(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	// Windows roots differ only by case in practice; the host canonicalises
	// again before it creates anything.
	return strings.EqualFold(ca, cb)
}
