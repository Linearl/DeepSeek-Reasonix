package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/tool"
)

// MaxCreateCollabSessions bounds one batch call (task 166). Every item writes a
// session file plus a contact_id, so an unbounded array would let one call fill
// the sidebar; the limit is enforced with an actionable error, never silently.
const MaxCreateCollabSessions = 20

// CreateCollabSessionItem is one entry of the batch form (task 166). Group,
// GroupID and Model fall back to the top-level values, so a batch can share one
// group and one model while a single item still overrides them.
type CreateCollabSessionItem struct {
	Title   string
	Purpose string
	Group   string
	GroupID string
	Model   string
}

// CreateCollabSessionItemResult reports one created session. The single form
// returns exactly one entry, so both shapes can be read the same way.
type CreateCollabSessionItemResult struct {
	Title       string `json:"title"`
	Purpose     string `json:"purpose,omitempty"`
	TopicID     string `json:"topicId,omitempty"`
	ContactID   string `json:"contactId,omitempty"`
	SessionPath string `json:"sessionPath,omitempty"`
	Group       string `json:"group,omitempty"`
	GroupID     string `json:"groupId,omitempty"`
	Model       string `json:"model,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ProjectRoot string `json:"projectRoot,omitempty"`
	// MessageID is set when a first message was queued for THIS session
	// (task 167).
	MessageID string `json:"messageId,omitempty"`
}

// CreateCollabSessionFailure reports one item the host refused. Creation
// registers the session, so a failing item never rolls back the items that
// already landed — partial success is the contract, and the caller decides
// whether to retry (task 166).
type CreateCollabSessionFailure struct {
	Title  string `json:"title,omitempty"`
	Reason string `json:"reason"`
}

// CreateCollabSessionResult is the host's receipt for a create_collab_session
// call. ContactID is present immediately — the transcript and its contact_id
// are written before the call returns, so the session is addressable without
// waiting for a first run (task 154).
//
// The single-form fields stay populated for compatibility; Created carries the
// same data (plus the batch entries), and Failed is empty unless an item was
// refused.
type CreateCollabSessionResult struct {
	TopicID     string `json:"topicId,omitempty"`
	ContactID   string `json:"contactId,omitempty"`
	SessionPath string `json:"sessionPath,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	Group       string `json:"group,omitempty"`
	GroupID     string `json:"groupId,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ProjectRoot string `json:"projectRoot,omitempty"`
	// Model is the provider/model ref the session was created with (task 162);
	// empty means "resolve the default at first open", unchanged behaviour.
	Model string `json:"model,omitempty"`
	// Created and Failed report the batch outcome (task 166). Created has one
	// entry in the single form.
	Created []CreateCollabSessionItemResult `json:"created,omitempty"`
	Failed  []CreateCollabSessionFailure    `json:"failed,omitempty"`
	// MessageID is set when the first message was handed to the target's
	// mailbox (task 167); Delivery echoes the mode that was used.
	MessageID string `json:"messageId,omitempty"`
	Delivery  string `json:"delivery,omitempty"`
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
	// Model is an optional `provider/model` ref (task 162). The host resolves
	// it through the same config lookup the UI uses and refuses a bare model id:
	// two endpoints may expose the same model name, so a provider-less ref is
	// ambiguous, and picking one silently would be a guess.
	Model string
	// Sessions is the batch form (task 166). When empty the single form is used;
	// items inherit Group/GroupID/Model unless they set their own.
	Sessions []CreateCollabSessionItem
	// Message is delivered to each created session as its first inbox entry, so
	// "create + first instruction" is one tool call (task 167). Delivery picks
	// steer (start it now) or followup (queue until its current turn ends).
	Message  string
	Delivery string
}

// ItemList normalises the request into the items to create: the batch form when
// given, otherwise the single form as a one-item list. Items inherit the
// top-level group/group_id/model unless they set their own (task 166).
func (r CreateCollabSessionRequest) ItemList() []CreateCollabSessionItem {
	if len(r.Sessions) > 0 {
		out := make([]CreateCollabSessionItem, 0, len(r.Sessions))
		for _, item := range r.Sessions {
			if strings.TrimSpace(item.Group) == "" {
				item.Group = r.Group
			}
			if strings.TrimSpace(item.GroupID) == "" {
				item.GroupID = r.GroupID
			}
			if strings.TrimSpace(item.Model) == "" {
				item.Model = r.Model
			}
			out = append(out, item)
		}
		return out
	}
	return []CreateCollabSessionItem{{
		Title:   r.Title,
		Purpose: r.Purpose,
		Group:   r.Group,
		GroupID: r.GroupID,
		Model:   r.Model,
	}}
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
	return "Create one or more collaborating sessions with a one-line purpose and file them into a group. Without `project` the session lands in the CALLING session's own project — or in Global when the caller is Global — so a session cannot be adopted by another project by accident. Pass `project` (the root of a REGISTERED project) to create it for a different project; an unknown root is refused with the list of known projects, because a typo would otherwise leave a stray project nobody opened. " +
		"Batch form: pass `sessions: [{title, purpose, group?, group_id?, model?}, ...]` (max " + fmt.Sprint(MaxCreateCollabSessions) + ") instead of the top-level title/purpose — one call creates them all, and a single bad item does NOT roll back the others: the result reports `created` and `failed` separately, and every created session is registered immediately, so retrying only the failed ones is safe. " +
		"`model` pins the new session to a `provider/model` ref (a bare model id is refused: two endpoints can expose the same name). Pass `message` to hand the new session its first instruction in the same call, with `delivery: steer|followup` (default steer, so the session starts working immediately; followup queues the message until its current turn finishes — use it for sessions you do not want interrupted). " +
		"The transcript and contact_id are written immediately, so a session is addressable in the contact directory (通讯录) BEFORE anyone opens it — no first run required. Returns contactId, topicId, sessionPath (per item), the scope/projectRoot it landed in, and messageId when a first message was queued; address a session with talk_to_session using its contact_id. Group is required so the team stays visible in the sidebar. Experimental."
}

func (createCollabSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"title":{"type":"string","description":"Session title (single form; omit when using sessions)."},` +
		`"purpose":{"type":"string","description":"One-line duty, e.g. 'React frontend expert' (single form)."},` +
		`"group":{"type":"string","description":"Group title. Created when absent; matched by id when group_id is also given. Doubles as the batch default."},` +
		`"group_id":{"type":"string","description":"Existing group id to join. Takes precedence over the title match, so renaming a group cannot detach the team."},` +
		`"project":{"type":"string","description":"Optional project ROOT to create the session in (e.g. C:\\work\\my-project). Must be a registered project; an unknown root is rejected. Omit to use the calling session's own project, or Global when the caller is Global."},` +
		`"workspace_root":{"type":"string","description":"Alias of project; pass one of the two."},` +
		`"model":{"type":"string","description":"Optional model ref in provider/model form (e.g. deepseek/deepseek-v4-flash). A bare model id is refused: different endpoints may expose the same model name, and the provider prefix is the only reliable way to tell them apart. Doubles as the batch default."},` +
		`"sessions":{"type":"array","description":"Batch form: create several sessions in one call (max 20). Each item needs title+purpose and may override group/group_id/model. A failing item does not roll back the others; the result lists created and failed separately.","items":{"type":"object","properties":{` +
		`"title":{"type":"string"},"purpose":{"type":"string"},` +
		`"group":{"type":"string"},"group_id":{"type":"string"},` +
		`"model":{"type":"string","description":"Optional provider/model ref for this item."}},` +
		`"required":["title","purpose"]}},` +
		`"message":{"type":"string","description":"Optional first message delivered to each created session in the same call (max 20 sessions). Saves a second talk_to_session round-trip."},` +
		`"delivery":{"type":"string","enum":["steer","followup"],"description":"How the first message is delivered: steer (default) starts the new session immediately; followup queues it until the target's current turn ends. Degrades to followup when steer cannot inject."}` +
		`},"required":[]}`)
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
		Model         string `json:"model"`
		Message       string `json:"message"`
		Delivery      string `json:"delivery"`
		Sessions      []struct {
			Title   string `json:"title"`
			Purpose string `json:"purpose"`
			Group   string `json:"group"`
			GroupID string `json:"group_id"`
			Model   string `json:"model"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	title, purpose := strings.TrimSpace(p.Title), strings.TrimSpace(p.Purpose)
	batch := len(p.Sessions) > 0
	// Two shapes, never a guess: a caller that passes both cannot tell which one
	// the host honoured.
	if batch && (title != "" || purpose != "") {
		return "", fmt.Errorf("pass either the top-level title/purpose or sessions[], not both — the host will not guess which shape you meant")
	}
	if !batch && (title == "" || purpose == "") {
		return "", fmt.Errorf("title and purpose are required (or pass sessions[] to create several at once)")
	}
	if len(p.Sessions) > MaxCreateCollabSessions {
		return "", fmt.Errorf("sessions has %d items; the limit is %d per call — split the batch so an accidental hundred sessions cannot fill the sidebar", len(p.Sessions), MaxCreateCollabSessions)
	}
	group, groupID := strings.TrimSpace(p.Group), strings.TrimSpace(p.GroupID)
	if group == "" && groupID == "" {
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
	delivery := strings.TrimSpace(p.Delivery)
	switch delivery {
	case "", "steer", "followup":
	default:
		return "", fmt.Errorf("delivery %q is not supported — use steer (start it now) or followup (queue it)", delivery)
	}
	if strings.TrimSpace(p.Message) == "" && delivery != "" {
		return "", fmt.Errorf("delivery is only meaningful with message — pass message, or drop delivery")
	}
	if t.create == nil {
		return "", fmt.Errorf("create_collab_session: this host cannot create sessions")
	}

	req := CreateCollabSessionRequest{
		WorkspaceRoot: t.workspaceRoot,
		ProjectRoot:   project,
		Group:         group,
		GroupID:       groupID,
		Message:       strings.TrimSpace(p.Message),
		Delivery:      delivery,
	}
	if batch {
		req.Sessions = make([]CreateCollabSessionItem, 0, len(p.Sessions))
		for _, item := range p.Sessions {
			req.Sessions = append(req.Sessions, CreateCollabSessionItem{
				Title:   strings.TrimSpace(item.Title),
				Purpose: strings.TrimSpace(item.Purpose),
				Group:   strings.TrimSpace(item.Group),
				GroupID: strings.TrimSpace(item.GroupID),
				Model:   strings.TrimSpace(item.Model),
			})
		}
	} else {
		req.Title, req.Purpose, req.Model = title, purpose, strings.TrimSpace(p.Model)
	}

	result, err := t.create(req)
	if err != nil {
		return "", err
	}
	return marshalCreateCollabResult(result), nil
}

// marshalCreateCollabResult renders the tool output. The single form keeps its
// historical top-level fields (callers and tests read them), while `created`
// carries the same entry so both shapes can be consumed identically.
func marshalCreateCollabResult(result CreateCollabSessionResult) string {
	out := map[string]any{
		"addressable": true,
		"note":        "contactId is live now — talk_to_session(to=contactId) works without opening the session",
	}
	if len(result.Created) > 0 {
		out["created"] = result.Created
		out["createdCount"] = len(result.Created)
	}
	if len(result.Failed) > 0 {
		out["failed"] = result.Failed
		out["failedCount"] = len(result.Failed)
		out["partialFailureNote"] = "created sessions are already registered and stay created — retry only the failed items"
	}
	if result.MessageID != "" {
		out["messageId"] = result.MessageID
		out["delivery"] = result.Delivery
	}
	if len(result.Failed) == 0 && len(result.Created) <= 1 {
		// Single form (or a one-item batch): keep the flat shape.
		out["topicId"] = result.TopicID
		out["contactId"] = result.ContactID
		out["sessionPath"] = result.SessionPath
		if len(result.Created) == 1 {
			out["title"] = result.Created[0].Title
		}
		out["purpose"] = result.Purpose
		out["group"] = result.Group
		out["groupId"] = result.GroupID
		out["scope"] = result.Scope
		out["projectRoot"] = result.ProjectRoot
		if result.Model != "" {
			out["model"] = result.Model
		}
	}
	encoded, _ := json.Marshal(out)
	return string(encoded)
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
