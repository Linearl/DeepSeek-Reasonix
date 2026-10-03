package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// TaskCardConfig carries host knobs for task 145 card tools.
type TaskCardConfig struct {
	Enabled       bool
	WorkspaceRoot string
	// CurrentSessionPath / CurrentContactID are the boot-time snapshots;
	// ResolveSessionPath (optional) answers at call time and wins. The
	// transcript path is bound by the control layer after boot, so a snapshot
	// is empty for desktop sessions (task 158.B).
	CurrentSessionPath string
	CurrentContactID   string
	ResolveSessionPath func() string
}

// currentSessionPath resolves the caller's transcript path at call time.
func (c TaskCardConfig) currentSessionPath() string {
	if c.ResolveSessionPath != nil {
		if p := strings.TrimSpace(c.ResolveSessionPath()); p != "" {
			return p
		}
	}
	return strings.TrimSpace(c.CurrentSessionPath)
}

// currentContactID resolves the caller's own address, minting it on first use.
func (c TaskCardConfig) currentContactID() string {
	if id := strings.TrimSpace(c.CurrentContactID); id != "" {
		return id
	}
	path := c.currentSessionPath()
	if path == "" {
		return ""
	}
	if id := SessionContactID(path); id != "" {
		return id
	}
	if minted, err := EnsureContactID(path); err == nil {
		return minted
	}
	return ""
}

// NewTaskCardTools returns the four card tools when the experiment is on.
//
// Task 174 keeps this constructor for direct callers and tests, but boot now
// registers the merged taskCardTool instead: one schema, four actions, and a
// smaller selection space for the model.
func NewTaskCardTools(cfg TaskCardConfig) []tool.Tool {
	if !cfg.Enabled {
		return nil
	}
	return []tool.Tool{
		createTaskCardTool{cfg},
		updateTaskCardTool{cfg},
		getTaskCardTool{cfg},
		listTaskCardsTool{cfg},
	}
}

// NewTaskCardTool returns the single merged card tool (task 174): the four
// old tools' fields overlapped heavily, so one action parameter replaces four
// names in the model's selection space while every old capability stays
// reachable.
func NewTaskCardTool(cfg TaskCardConfig) tool.Tool {
	if !cfg.Enabled {
		return nil
	}
	return taskCardTool{cfg: cfg}
}

type taskCardTool struct{ cfg TaskCardConfig }

func (taskCardTool) Name() string   { return "task_card" }
func (taskCardTool) ReadOnly() bool { return false }
func (taskCardTool) Description() string {
	return "Collaboration task cards (task 145): a process-visible record of who is working on what, routed by `action`. create files a new card; update changes status/result on an existing card_id; get fetches one card; list shows recent cards (optionally filtered by status). After create or update, show the returned JSON to the user inside a ```taskcard fence so it renders as a card in the conversation, and use talk_to_session to notify the assignee."
}
func (taskCardTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["create","update","get","list"],"description":"Which card operation to run."},"title":{"type":"string","description":"create: the card title."},"body":{"type":"string","description":"create: the card body."},"assignee":{"type":"string","description":"create: optional assignee contact_id."},"id":{"type":"string","description":"update/get: which card."},"status":{"type":"string","description":"update: the new status; list: only cards with this status."},"result":{"type":"string","description":"update: the outcome text."},"limit":{"type":"integer","description":"list: how many cards to return (default 20)."}},"required":["action"]}`)
}

func (t taskCardTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
	}
	if len(args) == 0 {
		return "", fmt.Errorf("action is required (create|update|get|list)")
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	// The old four tools stay as the implementations: one code path per
	// operation, so the merged schema can never drift from the behavior the
	// tests pinned.
	switch p.Action {
	case "create":
		return createTaskCardTool{cfg: t.cfg}.Execute(ctx, args)
	case "update":
		return updateTaskCardTool{cfg: t.cfg}.Execute(ctx, args)
	case "get":
		return getTaskCardTool{cfg: t.cfg}.Execute(ctx, args)
	case "list":
		return listTaskCardsTool{cfg: t.cfg}.Execute(ctx, args)
	default:
		return "", fmt.Errorf("unknown action %q (create|update|get|list)", p.Action)
	}
}

type createTaskCardTool struct{ cfg TaskCardConfig }

func (createTaskCardTool) Name() string   { return "create_task_card" }
func (createTaskCardTool) ReadOnly() bool { return false }
func (createTaskCardTool) Description() string {
	return "Create a collaboration task card (task 145). Process-visible record of who is working on what. Use update_task_card as status changes and talk_to_session to notify the assignee. After creating, show the returned JSON to the user inside a ```taskcard fence so it renders as a card in the conversation."
}
func (createTaskCardTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"},"assignee":{"type":"string","description":"Optional assignee contact_id."}},"required":["title"]}`)
}
func (t createTaskCardTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	store := sessioncollab.NewCardStore(t.cfg.WorkspaceRoot)
	c, err := store.Create(ctx, sessioncollab.Card{
		Title:       strings.TrimSpace(p.Title),
		Body:        p.Body,
		Assignee:    strings.TrimSpace(p.Assignee),
		Initiator:   t.cfg.currentContactID(),
		SessionFrom: t.cfg.currentSessionPath(),
		Workspace:   t.cfg.WorkspaceRoot,
	})
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(c)
	return string(out), nil
}

type updateTaskCardTool struct{ cfg TaskCardConfig }

func (updateTaskCardTool) Name() string   { return "update_task_card" }
func (updateTaskCardTool) ReadOnly() bool { return false }
func (updateTaskCardTool) Description() string {
	return "Update a collaboration task card: status pending|running|blocked|done|failed, result, error, note, assignee, role. Terminal statuses are final — reopen with status=pending if the work resumes. Failures must be explicit. Show the returned JSON in a ```taskcard fence so the user sees the updated card."
}
func (updateTaskCardTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"status":{"type":"string","enum":["pending","running","blocked","done","failed"]},"result":{"type":"string"},"error":{"type":"string"},"note":{"type":"string"},"assignee":{"type":"string"},"role":{"type":"string","description":"Chain role for the appended node, e.g. secretariat|expert."}},"required":["id"]}`)
}
func (t updateTaskCardTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Result   string `json:"result"`
		Error    string `json:"error"`
		Note     string `json:"note"`
		Assignee string `json:"assignee"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	if strings.TrimSpace(p.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	store := sessioncollab.NewCardStore(t.cfg.WorkspaceRoot)
	c, err := store.Update(ctx, p.ID, func(card *sessioncollab.Card) error {
		if p.Status != "" {
			next := sessioncollab.CardStatus(p.Status)
			if !sessioncollab.StatusAllowed(next) {
				return fmt.Errorf("invalid status %q", p.Status)
			}
			// A terminal card must not silently restart: reopening hides that the
			// earlier run was abandoned.
			if !sessioncollab.StatusTransitionAllowed(card.Status, next) {
				return fmt.Errorf("cannot move a %s card to %s; reopen it explicitly with status=pending", card.Status, next)
			}
			card.Status = next
		}
		if p.Result != "" {
			card.Result = p.Result
		}
		if p.Error != "" {
			card.Error = p.Error
			if card.Status == "" || card.Status == sessioncollab.StatusRunning || card.Status == sessioncollab.StatusPending {
				card.Status = sessioncollab.StatusFailed
			}
		}
		if p.Assignee != "" {
			card.Assignee = p.Assignee
		}
		if p.Note != "" || p.Assignee != "" {
			card.Nodes = append(card.Nodes, sessioncollab.CardNode{
				ContactID: firstNonEmpty(p.Assignee, t.cfg.currentContactID()),
				Session:   t.cfg.currentSessionPath(),
				Role:      firstNonEmpty(p.Role, "expert"),
				Note:      p.Note,
				At:        time.Now().UnixMilli(),
			})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(c)
	return string(out), nil
}

type getTaskCardTool struct{ cfg TaskCardConfig }

func (getTaskCardTool) Name() string   { return "get_task_card" }
func (getTaskCardTool) ReadOnly() bool { return true }
func (getTaskCardTool) Description() string {
	return "Read one collaboration task card by id."
}
func (getTaskCardTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
}
func (t getTaskCardTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	store := sessioncollab.NewCardStore(t.cfg.WorkspaceRoot)
	c, err := store.Get(p.ID)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(c)
	return string(out), nil
}

type listTaskCardsTool struct{ cfg TaskCardConfig }

func (listTaskCardsTool) Name() string   { return "list_task_cards" }
func (listTaskCardsTool) ReadOnly() bool { return true }
func (listTaskCardsTool) Description() string {
	return "List collaboration task cards for the current workspace, newest first."
}
func (listTaskCardsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}
func (t listTaskCardsTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	store := sessioncollab.NewCardStore(t.cfg.WorkspaceRoot)
	cards, err := store.List()
	if err != nil {
		return "", err
	}
	if len(cards) == 0 {
		return "No task cards.\n", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Task cards (%d)\n\n", len(cards))
	b.WriteString("| id | status | title | assignee | updated |\n|---|---|---|---|---|\n")
	for _, c := range cards {
		fmt.Fprintf(&b, "| `%s` | %s | %s | `%s` | %d |\n", c.ID, c.Status, c.Title, c.Assignee, c.UpdatedAt)
	}
	return b.String(), nil
}
