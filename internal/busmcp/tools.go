package busmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reasonix/internal/sessioncollab"
)

// toolDescriptions are kept next to the tool table so the model-visible copy
// stays one source. Keep each to a single line: they ride in every connected
// session's context.
const (
	inboxReadDesc  = "Read messages delivered to this runtime's Reasonix bus inbox. By default the batch is consumed (at-least-once); pass mark_read=false to peek without consuming."
	sendDesc       = "Send a message to a bus contact (e.g. zcode-heartbeat, or any Reasonix session contact id). Messages wait durably until the target reads them."
	taskCreateDesc = "Create a bus task card. State machine: pending → running → done/failed/blocked; blocked returns via running. Title is required."
	taskUpdateDesc = "Update a bus task card's status. Illegal transitions (e.g. done → running) are rejected; reopening terminal cards goes through pending."
	spawnDesc      = "Create a task card AND dispatch its assignment mail to a target contact in one step (e.g. to zcode-worker for unattended headless execution). Gated to spawn-authorized roles with a daily quota."
)

// ── collab_inbox_read ────────────────────────────────────────────────────────

type inboxReadIn struct {
	// MarkRead is a pointer so "absent" can mean the useful default (drain).
	MarkRead *bool `json:"mark_read,omitempty" jsonschema:"false peeks without consuming; default true drains the batch"`
}

type mailView struct {
	ID           string `json:"id"`
	From         string `json:"from,omitempty"`
	FromSession  string `json:"from_session,omitempty"`
	Body         string `json:"body"`
	Delivery     string `json:"delivery,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	CardID       string `json:"card_id,omitempty"`
	RequireReply bool   `json:"require_reply,omitempty"`
	At           int64  `json:"at,omitempty"`
}

type inboxReadOut struct {
	Messages []mailView `json:"messages"`
	// Unread counts what is left after this call, so a drained empty box and
	// a peek that saw nothing are distinguishable from "mailbox missing".
	Unread  int `json:"unread"`
	Refused int `json:"refused"`
}

func (rt roleRuntime) inboxRead(_ context.Context, _ *mcp.CallToolRequest, in inboxReadIn) (*mcp.CallToolResult, inboxReadOut, error) {
	markRead := in.MarkRead == nil || *in.MarkRead
	var out inboxReadOut
	if markRead {
		err := rt.bus.mail.Drain(rt.contact, func(pending, refused []sessioncollab.MailMessage) []string {
			out.Messages = makeMailViews(pending)
			out.Refused = len(refused)
			ids := make([]string, 0, len(pending))
			for _, m := range pending {
				ids = append(ids, m.ID)
			}
			return ids
		})
		if err != nil {
			return nil, out, fmt.Errorf("drain inbox: %w", err)
		}
	} else {
		pending, err := rt.bus.mail.Peek(rt.contact)
		if err != nil {
			return nil, out, fmt.Errorf("peek inbox: %w", err)
		}
		out.Messages = makeMailViews(pending)
	}
	out.Unread, _ = rt.bus.mail.InboxStatus(rt.contact)
	return nil, out, nil
}

func makeMailViews(msgs []sessioncollab.MailMessage) []mailView {
	views := make([]mailView, 0, len(msgs))
	for _, m := range msgs {
		views = append(views, mailView{
			ID:           m.ID,
			From:         m.From,
			FromSession:  m.FromSession,
			Body:         m.Body,
			Delivery:     m.Delivery,
			ThreadID:     m.ThreadID,
			CardID:       m.CardID,
			RequireReply: m.RequireReply,
			At:           m.At,
		})
	}
	return views
}

// ── collab_send ──────────────────────────────────────────────────────────────

type sendIn struct {
	To           string `json:"to" jsonschema:"target contact id, e.g. zcode-heartbeat or a Reasonix session contact"`
	Body         string `json:"body" jsonschema:"message body; keep large content in files and reference the path"`
	Delivery     string `json:"delivery,omitempty" jsonschema:"steer (default) delivers as soon as possible, followup queues for the next turn"`
	ThreadID     string `json:"thread_id,omitempty" jsonschema:"reply threading; empty starts a new thread"`
	CardID       string `json:"card_id,omitempty" jsonschema:"task card this message concerns"`
	RequireReply bool   `json:"require_reply,omitempty" jsonschema:"sender expects an answer on the same thread"`
}

type sendOut struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	To       string `json:"to"`
}

func (rt roleRuntime) send(_ context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, sendOut, error) {
	if strings.TrimSpace(in.To) == "" {
		return nil, sendOut{}, fmt.Errorf("to is required")
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, sendOut{}, fmt.Errorf("body is required")
	}
	// Hop starts at 0: bus runtimes are chain heads. Reply chains that run
	// through Reasonix sessions pick up their own hop accounting there.
	msg, err := rt.bus.mail.Deliver(sessioncollab.MailMessage{
		From:         rt.contact,
		To:           in.To,
		Body:         in.Body,
		Delivery:     in.Delivery,
		ThreadID:     in.ThreadID,
		CardID:       in.CardID,
		RequireReply: in.RequireReply,
	})
	if err != nil {
		return nil, sendOut{}, fmt.Errorf("deliver to %s: %w", in.To, err)
	}
	rt.bus.audit(rt.role, "send", in.To+" ("+msg.ID+")", true)
	return nil, sendOut{ID: msg.ID, ThreadID: msg.ThreadID, To: msg.To}, nil
}

// ── collab_task_create ───────────────────────────────────────────────────────

type taskCreateIn struct {
	Title    string `json:"title" jsonschema:"short task title"`
	Body     string `json:"body,omitempty" jsonschema:"what the task needs; put acceptance criteria here"`
	Assignee string `json:"assignee,omitempty" jsonschema:"contact id of the intended worker, when known"`
}

type taskCreateOut struct {
	CardID string `json:"card_id"`
	Status string `json:"status"`
}

func (rt roleRuntime) taskCreate(_ context.Context, _ *mcp.CallToolRequest, in taskCreateIn) (*mcp.CallToolResult, taskCreateOut, error) {
	card, err := rt.bus.cards.Create(sessioncollab.Card{
		Title:     in.Title,
		Body:      in.Body,
		Initiator: rt.contact,
		Assignee:  in.Assignee,
	})
	if err != nil {
		return nil, taskCreateOut{}, fmt.Errorf("create card: %w", err)
	}
	rt.bus.audit(rt.role, "task_create", card.ID+" "+card.Title, true)
	return nil, taskCreateOut{CardID: card.ID, Status: string(card.Status)}, nil
}

// ── collab_task_update ───────────────────────────────────────────────────────

type taskUpdateIn struct {
	CardID string `json:"card_id" jsonschema:"card id from collab_task_create"`
	Status string `json:"status" jsonschema:"pending|running|blocked|done|failed"`
	Note   string `json:"note,omitempty" jsonschema:"process note recorded on the card chain"`
	Result string `json:"result,omitempty" jsonschema:"result summary for done cards; reference files instead of inlining large content"`
	Error  string `json:"error,omitempty" jsonschema:"failure reason for failed cards"`
}

type taskUpdateOut struct {
	CardID string `json:"card_id"`
	Status string `json:"status"`
}

func (rt roleRuntime) taskUpdate(_ context.Context, _ *mcp.CallToolRequest, in taskUpdateIn) (*mcp.CallToolResult, taskUpdateOut, error) {
	if strings.TrimSpace(in.CardID) == "" {
		return nil, taskUpdateOut{}, fmt.Errorf("card_id is required")
	}
	to := sessioncollab.CardStatus(in.Status)
	if !sessioncollab.StatusAllowed(to) {
		return nil, taskUpdateOut{}, fmt.Errorf("unknown status %q (want pending|running|blocked|done|failed)", in.Status)
	}
	card, err := rt.bus.cards.Update(in.CardID, func(c *sessioncollab.Card) error {
		if !sessioncollab.StatusTransitionAllowed(c.Status, to) {
			return fmt.Errorf("illegal transition %s→%s (reopen terminal cards via pending)", c.Status, to)
		}
		c.Status = to
		if in.Result != "" {
			c.Result = in.Result
		}
		if in.Error != "" {
			c.Error = in.Error
		}
		note := in.Note
		if note == "" {
			note = "status → " + string(to)
		}
		c.Nodes = append(c.Nodes, sessioncollab.CardNode{
			ContactID: rt.contact,
			Role:      "zcode",
			At:        time.Now().UnixMilli(),
			Note:      note,
		})
		return nil
	})
	if err != nil {
		return nil, taskUpdateOut{}, fmt.Errorf("update card %s: %w", in.CardID, err)
	}
	rt.bus.audit(rt.role, "task_update", in.CardID+" → "+in.Status, true)
	return nil, taskUpdateOut{CardID: card.ID, Status: string(card.Status)}, nil
}

// ── collab_spawn ─────────────────────────────────────────────────────────────

type spawnIn struct {
	To        string `json:"to" jsonschema:"target contact that receives the assignment, e.g. zcode-worker (the headless pool) or another session contact"`
	Title     string `json:"title" jsonschema:"short task title"`
	Body      string `json:"body" jsonschema:"task instructions; put acceptance criteria here — they ride both the card and the assignment mail"`
	Workspace string `json:"workspace,omitempty" jsonschema:"working directory hint for the assignee, when the task is workspace-bound"`
}

type spawnOut struct {
	CardID string `json:"card_id"`
	MailID string `json:"mail_id"`
	To     string `json:"to"`
}

// spawn creates a task card and dispatches its assignment mail in one step.
// It is the only tool that hands out work, so it is registered exclusively
// for roles in spawn_roles and additionally capped by a per-role daily quota
// (both fail-closed: an unlisted role never even sees the tool, and the
// counters below make the cap exact under concurrency).
func (rt roleRuntime) spawn(_ context.Context, _ *mcp.CallToolRequest, in spawnIn) (*mcp.CallToolResult, spawnOut, error) {
	if strings.TrimSpace(in.To) == "" {
		return nil, spawnOut{}, fmt.Errorf("to is required")
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, spawnOut{}, fmt.Errorf("title is required")
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, spawnOut{}, fmt.Errorf("body is required")
	}
	if !rt.bus.spawnAllowed[rt.role] {
		rt.bus.audit(rt.role, "spawn", "denied: role not in spawn_roles", false)
		return nil, spawnOut{}, fmt.Errorf("spawn: role %q is not allowed to spawn tasks", rt.role)
	}
	rt.bus.spawnMu.Lock()
	today := time.Now().Format("2006-01-02")
	if today != rt.bus.spawnDay {
		rt.bus.spawnDay = today
		rt.bus.spawnUsed = map[string]int{}
	}
	if rt.bus.spawnUsed[rt.role] >= rt.bus.spawnQuota {
		rt.bus.spawnMu.Unlock()
		rt.bus.audit(rt.role, "spawn", "denied: daily quota exhausted", false)
		return nil, spawnOut{}, fmt.Errorf("spawn: daily quota (%d) exhausted for role %q", rt.bus.spawnQuota, rt.role)
	}
	rt.bus.spawnUsed[rt.role]++
	rt.bus.spawnMu.Unlock()

	card, err := rt.bus.cards.Create(sessioncollab.Card{
		Title:     in.Title,
		Body:      in.Body,
		Initiator: rt.contact,
		Assignee:  in.To,
		Workspace: in.Workspace,
	})
	if err != nil {
		return nil, spawnOut{}, fmt.Errorf("create card: %w", err)
	}
	// The assignment mail is the machine contract busworker (or any assignee
	// runtime) parses; RequireReply pins the "report back on this card"
	// expectation onto the message itself.
	body, err := json.Marshal(map[string]string{
		"kind":      "bus-task",
		"card_id":   card.ID,
		"prompt":    in.Body,
		"title":     in.Title,
		"workspace": in.Workspace,
	})
	if err != nil {
		return nil, spawnOut{}, fmt.Errorf("encode assignment: %w", err)
	}
	msg, err := rt.bus.mail.Deliver(sessioncollab.MailMessage{
		From:         rt.contact,
		To:           in.To,
		Body:         string(body),
		CardID:       card.ID,
		RequireReply: true,
	})
	if err != nil {
		return nil, spawnOut{}, fmt.Errorf("deliver assignment to %s: %w", in.To, err)
	}
	rt.bus.audit(rt.role, "spawn", in.To+" card="+card.ID+" mail="+msg.ID, true)
	return nil, spawnOut{CardID: card.ID, MailID: msg.ID, To: msg.To}, nil
}

// ── per-role server assembly ─────────────────────────────────────────────────

// roleRuntime binds the tools to one caller's identity. It exists so the
// tool handlers never have to trust a per-request header for identity: the
// binding happens once, at server construction, from the token table.
type roleRuntime struct {
	bus     *Server
	role    string
	contact string
}

func (s *Server) newRoleServer(role, contact string, canSpawn bool) *mcp.Server {
	rt := roleRuntime{bus: s, role: role, contact: contact}
	srv := mcp.NewServer(&mcp.Implementation{Name: "reasonix-bus", Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: "Reasonix task bus: read the shared inbox, send cross-runtime mail, and keep task cards. " +
			"This runtime's contact id is " + contact + ".",
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "collab_inbox_read", Description: inboxReadDesc}, rt.inboxRead)
	mcp.AddTool(srv, &mcp.Tool{Name: "collab_send", Description: sendDesc}, rt.send)
	mcp.AddTool(srv, &mcp.Tool{Name: "collab_task_create", Description: taskCreateDesc}, rt.taskCreate)
	mcp.AddTool(srv, &mcp.Tool{Name: "collab_task_update", Description: taskUpdateDesc}, rt.taskUpdate)
	if canSpawn {
		mcp.AddTool(srv, &mcp.Tool{Name: "collab_spawn", Description: spawnDesc}, rt.spawn)
	}
	return srv
}
