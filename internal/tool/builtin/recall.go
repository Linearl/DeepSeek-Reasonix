package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

const maxRecallPositions = 8

// RecallTool is the tl1691 readback half of the fold mechanism: bring folded
// conversation back by address or by search. Registered only while
// experimental_recall_tool is on (curate_context precedent: with the switch
// off the tool does not exist in the registry at all, so the model-visible
// surface stays byte-identical to the pre-tl1691 baseline).
type RecallTool struct{}

func (RecallTool) Name() string { return "recall" }

func (RecallTool) Description() string {
	return "Bring folded conversation back: read the #n positions listed under \"Folded work index\" in a compaction pointer, or search the whole folded region by query when no index line names what you need. Prefer re-reading a file over recalling it — a current copy beats a folded one. One budget per compaction covers both operations, and a request past it is refused whole, not truncated."
}

func (RecallTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"properties":{
  "positions":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"integer","minimum":0},"description":"Read these #n addresses, from an index line or a search hit, without the '#'."},
  "query":{"type":"string","description":"Search for these words instead of reading an address. A tool result matches on its output and is addressed by the call that produced it."},
  "limit":{"type":"integer","minimum":1,"maximum":20,"description":"Search hits to return (default 8)."}
}
}`)
}

// ReadOnly is true in the permission/workspace sense: recall only reads this
// session's own canonical transcript.
func (RecallTool) ReadOnly() bool { return true }

func (RecallTool) PlanModeSafe() bool { return true }

func (RecallTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var request struct {
		Positions []int  `json:"positions"`
		Query     string `json:"query"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return "", fmt.Errorf("invalid recall args: %w", err)
	}
	// The arguments say which operation this is: positions read, a query
	// searches. Both together is refused rather than resolved, because either
	// way the tool would do something the caller did not ask for.
	query := strings.TrimSpace(request.Query)
	if query == "" && len(request.Positions) == 0 {
		return "", fmt.Errorf("recall: give positions to read, or a query to search for them")
	}
	if len(request.Positions) > maxRecallPositions {
		return "", fmt.Errorf("recall: at most %d positions per call", maxRecallPositions)
	}
	recaller, ok := tool.ContextRecallerFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("recall is unavailable outside an active agent session")
	}
	result, err := recaller.RecallContext(ctx, tool.RecallRequest{
		Positions: request.Positions, Query: query, Limit: request.Limit,
	})
	if err != nil {
		return "", err
	}
	if query != "" {
		return renderRecallSearch(result), nil
	}
	return renderRecall(result), nil
}

// renderRecallSearch keeps the same footer shape as a read: the model reads
// one accounting line whichever operation it ran.
func renderRecallSearch(res tool.RecallResult) string {
	var b strings.Builder
	b.WriteString(res.Text)
	fmt.Fprintf(&b, "\n\n[searched %d folded messages; %d tokens; %d left before the next compaction",
		res.Searched, res.Tokens, res.BudgetLeft)
	if len(res.Hits) > 0 {
		b.WriteString("; read them with positions")
	}
	b.WriteString("]")
	return b.String()
}

// renderRecall puts the accounting last: the content is what the model is here
// to read, and a footer keeps the budget visible without framing every line.
func renderRecall(res tool.RecallResult) string {
	var b strings.Builder
	b.WriteString(res.Text)
	if !strings.HasSuffix(res.Text, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n[recalled %s; %d tokens; %d left before the next compaction",
		positionList(res.Recalled), res.Tokens, res.BudgetLeft)
	if len(res.Missing) > 0 {
		fmt.Fprintf(&b, "; no longer addressable: %s", positionList(res.Missing))
	}
	b.WriteString("]")
	return b.String()
}

func positionList(positions []int) string {
	if len(positions) == 0 {
		return "nothing"
	}
	parts := make([]string, len(positions))
	for i, p := range positions {
		parts[i] = fmt.Sprintf("#%d", p)
	}
	return strings.Join(parts, ", ")
}
