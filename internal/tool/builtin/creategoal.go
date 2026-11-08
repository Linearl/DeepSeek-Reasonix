package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(createGoal{}) }

// createGoal (tl1472 双通道创建·模型通道) lets the model install a session
// goal on its own initiative — with or without goal mode. Like update_goal it
// is a host workflow operation: read-only, no approvals, no permission
// changes. The host owns the goal object (占位锁): a live goal refuses
// replacement with a readable cause; only a completed goal's slot frees.
// The install lands active+armed (DSH 参照), so the host auto-continues the
// goal after the creating turn ends.
type createGoal struct{}

func (createGoal) Name() string { return "create_goal" }

func (createGoal) Description() string {
	return "Create a session goal on your own initiative (the host then pursues it automatically across turns). Use it when the work is a multi-turn objective with an acceptance standard the user should be able to watch in the sidebar — e.g. \"refactor X and keep the tests green\", \"investigate Y and report findings with evidence\", or any task that will outlive this single reply. Do NOT create a goal for one-shot Q&A, quick edits, or anything finished within this turn. Fields: `goal` (the objective as a task contract: what counts as done, constraints, output format), `summary` (REQUIRED: the tidied one-line digest shown in the sidebar — same language as the conversation, never the raw prompt text). If a live goal already exists in this session the call is REFUSED (only the user can replace it, or you may replace a completed goal); surface the refusal cause to the user instead of retrying."
}

func (createGoal) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "goal":{"type":"string","description":"The objective, written as a small task contract: what counts as done, the constraints, the expected output. This is the text the goal loop will keep re-reading."},
  "summary":{"type":"string","description":"REQUIRED. The tidied digest for the sidebar display — one line, the conversation's language, not the raw input text."}
},
"required":["goal","summary"]
}`)
}

// ReadOnly is true: the call only installs host bookkeeping (the goal object);
// it never touches the workspace and never expands permissions.
func (createGoal) ReadOnly() bool { return true }

// ProviderVisible reports whether the host bound a goal creator. Plain host
// turns always have one (create_goal works without goal mode); sub-agent and
// isolated child contexts shadow it away so a child cannot create goals for
// its parent session.
func (createGoal) ProviderVisible(ctx context.Context) bool {
	_, ok := tool.GoalCreatorFromContext(ctx)
	return ok
}

// PlanModeSafe reports true: read-only host bookkeeping; the tool fails closed
// without a bound creator anyway.
func (createGoal) PlanModeSafe() bool { return true }

func (createGoal) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Goal    string `json:"goal"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid create_goal args: %w", err)
	}
	p.Goal = strings.TrimSpace(p.Goal)
	p.Summary = strings.TrimSpace(p.Summary)
	if p.Goal == "" {
		return "", fmt.Errorf("create_goal: goal is required — state the objective as a task contract (what counts as done); nothing was created")
	}
	if p.Summary == "" {
		return "", fmt.Errorf("create_goal: summary is required — the sidebar shows the tidied digest, not the raw text; nothing was created")
	}
	creator, ok := tool.GoalCreatorFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("create_goal: no goal creator is bound in this context — goals belong to a host session; create it in the host turn, not a sub-agent")
	}
	return creator.CreateGoal(p.Goal, p.Summary)
}
