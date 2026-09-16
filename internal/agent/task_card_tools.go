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
	Enabled            bool
	WorkspaceRoot      string
	CurrentSessionPath string
	CurrentContactID   string
}

// NewTaskCardTools returns the four card tools when the experiment is on.
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

type createTaskCardTool struct{ cfg TaskCardConfig }

func (createTaskCardTool) Name() string   { return "create_task_card" }
func (createTaskCardTool) ReadOnly() bool { return false }
func (createTaskCardTool) Description() string {
	return "Create a collaboration task card (task 145). Process-visible record of who is working on what. Use update_task_card as status changes and talk_to_session to notify the assignee. After creating, show the returned JSON to the user inside a ```taskcard fence so it renders as a card in the conversation."
}
func (createTaskCardTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"},"assignee":{"type":"string","description":"Optional assignee contact_id."}},"required":["title"]}`)
}
func (t createTaskCardTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	store := sessioncollab.NewCardStore(t.cfg.WorkspaceRoot)
	c, err := store.Create(sessioncollab.Card{
		Title:       strings.TrimSpace(p.Title),
		Body:        p.Body,
		Assignee:    strings.TrimSpace(p.Assignee),
		Initiator:   t.cfg.CurrentContactID,
		SessionFrom: t.cfg.CurrentSessionPath,
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
func (t updateTaskCardTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
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
	c, err := store.Update(p.ID, func(card *sessioncollab.Card) error {
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
				ContactID: firstNonEmpty(p.Assignee, t.cfg.CurrentContactID),
				Session:   t.cfg.CurrentSessionPath,
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
