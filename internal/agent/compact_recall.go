package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/retrieval"
	"reasonix/internal/tool"
)

// A fold moves earlier messages out of the model-visible view. Recall is what
// brings them back: the canonical transcript keeps them verbatim, so a read is
// a lookup, not a re-derivation (studio compact_recall.go / compact_search.go
// semantics, ported onto this fork's session state).
//
// The budget is per compaction generation, so the fold that freed the window
// cannot be undone by the turns after it: a recall loop runs out of budget
// instead of running forever.
const (
	recallBudgetRatio     = 0.10
	minRecallBudgetTokens = 2000
)

// Search constants. recallSearchNoise drops trailing hits far below the best
// one, so a query whose terms are common does not return twenty near-zero rows.
const (
	defaultRecallSearchLimit = 8
	maxRecallSearchLimit     = 20
	recallSnippetRunes       = 240
	recallSearchNoise        = 0.25
)

// Recall document kinds. tool_input is a call's arguments and tool_output its
// result; both are addressed by the assistant message that made the call.
const (
	recallKindUser      = "user_text"
	recallKindAssistant = "assistant_text"
	recallKindToolIn    = "tool_input"
	recallKindToolOut   = "tool_output"
)

// recallBudget is one generation's ceiling on pulling folded content back.
func (a *Agent) recallBudget() int {
	window := a.effectiveContextWindow()
	if window <= 0 {
		return minRecallBudgetTokens
	}
	return max(minRecallBudgetTokens, int(float64(window)*recallBudgetRatio))
}

// RecallContext is the read half of the recall mechanism: it answers with
// content the fold replaced, addressed by position or found by keyword.
func (a *Agent) RecallContext(ctx context.Context, req tool.RecallRequest) (tool.RecallResult, error) {
	return a.recallContext(ctx, req)
}

// recallContext reads folded content back. Both operations spend one
// generation's budget: two would let a search refill what a read was refused.
func (a *Agent) recallContext(_ context.Context, req tool.RecallRequest) (tool.RecallResult, error) {
	query := strings.TrimSpace(req.Query)
	switch {
	case query != "" && len(req.Positions) > 0:
		return tool.RecallResult{}, fmt.Errorf("recall: give positions to read or a query to search, not both")
	case query == "" && len(req.Positions) == 0:
		return tool.RecallResult{}, fmt.Errorf("recall: no positions given")
	}
	if a.sess.conversation == nil {
		return tool.RecallResult{}, fmt.Errorf("recall is unavailable outside an active agent session")
	}
	canonical, _ := a.sess.conversation.snapshotMessagesVersion()

	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	state := &a.sess.compactionState
	covered := min(state.Projection.CoveredCount, len(canonical))
	if covered <= 0 {
		return tool.RecallResult{}, fmt.Errorf("recall: nothing has been folded in this session yet, so every message is still in your context")
	}
	if state.Recall == nil || state.Recall.Generation != state.Generation {
		state.Recall = &RecallLedger{Generation: state.Generation}
	}
	budget := a.recallBudget()
	left := budget - state.Recall.SpentTokens
	if query != "" {
		return a.searchRecallLocked(canonical[:covered], query, req, budget, left)
	}

	var body strings.Builder
	var recalled, missing []int
	for _, pos := range req.Positions {
		if pos < 0 || pos >= len(canonical) {
			missing = append(missing, pos)
			continue
		}
		if pos >= covered {
			return tool.RecallResult{}, fmt.Errorf("recall: #%d is not folded — it is still in your context, so read it there", pos)
		}
		rendered := renderRecallSpan(recallSpan(canonical, pos))
		if strings.TrimSpace(rendered) == "" {
			missing = append(missing, pos)
			continue
		}
		fmt.Fprintf(&body, "#%d\n%s\n", pos, strings.TrimRight(rendered, "\n"))
		recalled = append(recalled, pos)
	}
	if len(recalled) == 0 {
		return tool.RecallResult{BudgetLeft: left, Missing: missing},
			fmt.Errorf("recall: %s named nothing the transcript still holds", positionsText(missing))
	}
	// Refused whole rather than truncated: a half-recalled span reads as the
	// whole of what was there, which is the failure recall exists to prevent.
	cost := recallCostTokens(body.String())
	if cost > left {
		return tool.RecallResult{BudgetLeft: left},
			fmt.Errorf("recall: %d tokens exceeds the %d left in this generation's recall budget — ask for fewer positions", cost, left)
	}
	state.Recall.SpentTokens += cost
	a.persistRecallLedger()
	return tool.RecallResult{
		Text:       strings.TrimRight(body.String(), "\n"),
		Recalled:   recalled,
		Missing:    missing,
		Tokens:     cost,
		BudgetLeft: budget - state.Recall.SpentTokens,
	}, nil
}

// persistRecallLedger writes the spent budget back to the sidecar, so a restart
// cannot hand the model a fresh allowance for the same generation.
func (a *Agent) persistRecallLedger() {
	if err := a.persistCompactionStateLocked(); err != nil {
		// The in-memory ledger still bounds this session; only the durable copy
		// is missing, and losing it costs one generation's budget after a
		// restart, not correctness.
		slog.Warn("agent: recall budget ledger not persisted", "error", err)
	}
}

// recallCostTokens prices a recall result on the same census the rest of the
// compaction path uses for plain text.
func recallCostTokens(text string) int {
	return estimateTextTokens(text)
}

// recallSpan returns the message at pos plus the tool results that answer it.
// An index line addresses the call, and a call without its result is the half
// that says least.
func recallSpan(canonical []provider.Message, pos int) []provider.Message {
	span := []provider.Message{canonical[pos]}
	wanted := map[string]bool{}
	for _, tc := range canonical[pos].ToolCalls {
		wanted[tc.ID] = true
	}
	for i := pos + 1; i < len(canonical) && len(wanted) > 0; i++ {
		m := canonical[i]
		if m.Role != provider.RoleTool {
			break
		}
		if wanted[m.ToolCallID] {
			span = append(span, m)
			delete(wanted, m.ToolCallID)
		}
	}
	return span
}

// renderRecallSpan renders a recalled span the way it happened: the tool
// arguments and the tool bodies stay whole, because a recall exists to return
// what was actually said and run. Only provider-visible content is returned —
// RawContent is display-only state that never reaches a provider, and a recall
// result does.
func renderRecallSpan(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "[user]\n%s\n\n", UserMessageText(m))
		case provider.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "[assistant]\n%s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[assistant calls %s] %s\n", tc.Name, tc.Arguments)
			}
			b.WriteString("\n")
		case provider.RoleTool:
			fmt.Fprintf(&b, "[tool %s result]\n%s\n\n", m.Name, recallToolBody(m))
		case provider.RoleSystem:
			fmt.Fprintf(&b, "[system]\n%s\n\n", m.Content)
		}
	}
	return b.String()
}

// recallToolBody is what the call actually returned, for the one caller whose
// answer is that and whose own budget bounds the size.
func recallToolBody(m provider.Message) string {
	if m.RawContent != "" {
		return m.RawContent
	}
	return m.Content
}

// positionsText names the addresses a request asked for that are gone.
func positionsText(positions []int) string {
	if len(positions) == 0 {
		return "the request"
	}
	parts := make([]string, len(positions))
	for i, p := range positions {
		parts[i] = fmt.Sprintf("#%d", p)
	}
	return strings.Join(parts, ", ")
}

// recallDoc is one searchable unit of the folded region.
type recallDoc struct {
	position int // canonical position the hit is addressed by
	kind     string
	tool     string
	text     string
	counts   map[string]int
	length   int
}

// buildRecallDocs turns the folded region into searchable units. A tool result
// is searched on its own text but addressed by the assistant call above it,
// because recallSpan reads a call together with its results: addressing the
// result would return output without the command behind it.
func buildRecallDocs(region []provider.Message) []recallDoc {
	callAt := map[string]int{}
	callName := map[string]string{}
	var docs []recallDoc
	add := func(pos int, kind, toolName, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		terms := retrieval.Tokens(text)
		if len(terms) == 0 {
			return
		}
		docs = append(docs, recallDoc{
			position: pos, kind: kind, tool: toolName, text: text,
			counts: retrieval.Counts(terms), length: len(terms),
		})
	}
	for i, m := range region {
		if m.LocalOnly {
			continue
		}
		for _, tc := range m.ToolCalls {
			callAt[tc.ID], callName[tc.ID] = i, tc.Name
			add(i, recallKindToolIn, tc.Name, tc.Name+" "+tc.Arguments)
		}
		switch m.Role {
		case provider.RoleUser:
			// A prior digest is itself folded content, but searching it would
			// rank the summary over the messages it replaced — the exact
			// secondhand text recall exists to bypass.
			if !isCompactionSummary(m) {
				add(i, recallKindUser, "", UserMessageText(m))
			}
		case provider.RoleAssistant:
			add(i, recallKindAssistant, "", m.Content)
		case provider.RoleTool:
			pos, ok := callAt[m.ToolCallID]
			if !ok {
				continue
			}
			add(pos, recallKindToolOut, callName[m.ToolCallID], recallToolBody(m))
		}
	}
	return docs
}

// searchRecallLocked ranks the folded region and charges the snippets to this
// generation's recall budget. Callers hold compactionMu.
func (a *Agent) searchRecallLocked(region []provider.Message, query string, req tool.RecallRequest, budget, left int) (tool.RecallResult, error) {
	hits, err := searchFoldedRegion(region, query, normalizeRecallLimit(req.Limit))
	if err != nil {
		return tool.RecallResult{BudgetLeft: left}, err
	}
	if len(hits) == 0 {
		// Not an error: "it is not in the folded region" is an answer, and
		// failing the call would read as the search itself being broken.
		return tool.RecallResult{
			Text:     fmt.Sprintf("No folded message matches %q. The %d folded messages were all searched.", query, len(region)),
			Searched: len(region), BudgetLeft: left,
		}, nil
	}
	text := renderRecallHits(query, hits)
	cost := recallCostTokens(text)
	if cost > left {
		return tool.RecallResult{BudgetLeft: left},
			fmt.Errorf("recall: %d tokens exceeds the %d left in this generation's recall budget — ask for fewer results", cost, left)
	}
	a.sess.compactionState.Recall.SpentTokens += cost
	a.persistRecallLedger()
	return tool.RecallResult{
		Text: text, Hits: hits, Searched: len(region),
		Tokens: cost, BudgetLeft: budget - a.sess.compactionState.Recall.SpentTokens,
	}, nil
}

// searchFoldedRegion ranks the folded region against a query.
func searchFoldedRegion(region []provider.Message, query string, limit int) ([]tool.RecallHit, error) {
	terms, err := retrieval.QueryTerms(query)
	if err != nil {
		return nil, fmt.Errorf("recall: %w", err)
	}
	docs := buildRecallDocs(region)
	counts := make([]map[string]int, 0, len(docs))
	for _, d := range docs {
		counts = append(counts, d.counts)
	}
	df := retrieval.DocumentFrequency(counts)
	total, avgLen := len(docs), 0.0
	for _, d := range docs {
		avgLen += float64(d.length)
	}
	if total > 0 {
		avgLen /= float64(total)
	}

	type scored struct {
		doc   recallDoc
		score float64
	}
	var ranked []scored
	for _, d := range docs {
		if score := retrieval.BM25Score(d.counts, d.length, terms, df, total, avgLen); score > 0 {
			ranked = append(ranked, scored{doc: d, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].doc.position < ranked[j].doc.position
	})
	ranked = retrieval.KeepTopRelativeScore(ranked, recallSearchNoise, func(s scored) float64 { return s.score })

	hits := make([]tool.RecallHit, 0, limit)
	seen := map[string]bool{}
	for _, s := range ranked {
		if len(hits) >= limit {
			break
		}
		// One line per (position, kind): a call and its result already share an
		// address, and reading it once returns both.
		key := fmt.Sprintf("%d\x00%s", s.doc.position, s.doc.kind)
		if seen[key] {
			continue
		}
		seen[key] = true
		hits = append(hits, tool.RecallHit{
			Position: s.doc.position, Kind: s.doc.kind, Tool: s.doc.tool,
			Snippet: retrieval.MakeSnippet(s.doc.text, query, terms, recallSnippetRunes),
		})
	}
	return hits, nil
}

// renderRecallHits is what the model reads back. Each line is an address it
// can pass straight to a read.
func renderRecallHits(query string, hits []tool.RecallHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Folded context matching %q:\n", query)
	for _, h := range hits {
		fmt.Fprintf(&b, "\n#%d %s", h.Position, h.Kind)
		if h.Tool != "" {
			b.WriteString(" " + h.Tool)
		}
		b.WriteString("\n" + h.Snippet + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func normalizeRecallLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultRecallSearchLimit
	case limit > maxRecallSearchLimit:
		return maxRecallSearchLimit
	default:
		return limit
	}
}

var _ tool.ContextRecaller = (*Agent)(nil)
