package agent

// tl1768: batch_dispose — the mailbox's cleanup half. Task 689 merged the
// mailbox's read/consume verbs (peek/drain/query); none of them can clear
// already-handled mail. This action disposes BY FILTER through the SAME
// collabinbox Store primitives the desktop panel's delete/dismiss buttons use
// (需求③ 两路径同源), so an agent can clean up a pile of processed mail on the
// human's behalf.
//
// Two-phase safety (验收①③): dry_run DEFAULTS TO TRUE — a call that forgets
// the flag lists instead of mutating, and the execute call is always an
// explicit second step after the agent has seen (and the human has seen) the
// plan. mode=archive writes dismiss tombstones (recoverable via
// action=batch_dispose mode=archive + undismiss… — practically, via the
// panel's 已消除 view restore, collabinbox Store.Undismiss); mode=delete
// physically removes the archive copies (tl1722 双语义先例; NOT recoverable).
// The transport/consume semantics are untouched: drain's settle remains the
// consuming act, and dispose never advances any cursor (non-goals).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/collabinbox"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// batchDisposeTool implements mailbox(action=batch_dispose). Boot registers
// it only through the mailbox router (no standalone registration — same
// stance as the pre-merge delegates); the constructor stays exported for
// tests and direct hosts, mirroring the 689 pattern.
type batchDisposeTool struct{ cfg SessionCollabConfig }

// NewBatchDisposeTool builds the filter-dispose action behind mailbox.
func NewBatchDisposeTool(cfg SessionCollabConfig) tool.Tool {
	return batchDisposeTool{cfg: cfg}
}

func (batchDisposeTool) Name() string { return "batch_dispose" }

func (batchDisposeTool) ReadOnly() bool { return false }

func (batchDisposeTool) Description() string {
	return "Batch-dispose cross-session mail BY FILTER (tl1768) — the mailbox's cleanup half, routed as mailbox(action=batch_dispose). Two-phase: phase 1 (dry_run=true, the DEFAULT) lists exactly which entries the filters match without changing anything; phase 2 re-run with dry_run=false to execute. mode=archive hides entries via dismiss tombstones (recoverable: the panel's dismissed view restores them); mode=delete physically removes the archive copies (irreversible — the mail was already injected into conversations at delivery time, so conversation content is never touched). Filters: from/to contact, bucket (approval|mention|automation|system), thread_id, since/until (ms epoch), unread_only, state (all|pendingMe|mine|decided), include_dismissed. Execute is hard-capped at 500 entries per call — a wider match is refused with instructions to slice (until/since) or use the panel's clear-all. Consuming semantics untouched: this never advances the drain cursor."
}

func (batchDisposeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["archive","delete"],"description":"archive: hide via dismiss tombstones (recoverable). delete: physically remove the archive copies (irreversible)."},"dry_run":{"type":"boolean","description":"DEFAULT TRUE — phase 1 lists the matching entries and mutates nothing. Pass false explicitly to execute (phase 2, only after reviewing the list)."},"from":{"type":"string","description":"Only messages sent BY this contact_id."},"to":{"type":"string","description":"Only messages sent TO this contact_id."},"bucket":{"type":"string","enum":["all","approval","mention","automation","system"],"description":"Five-bucket view filter (default all)."},"thread_id":{"type":"string","description":"Only messages on this conversation thread."},"since":{"type":"integer","description":"Inclusive lower bound on at (ms epoch)."},"until":{"type":"integer","description":"Exclusive upper bound on at (ms epoch)."},"unread_only":{"type":"boolean","description":"Only messages the recipient has not consumed yet."},"state":{"type":"string","enum":["all","pendingMe","mine","decided"],"description":"Approval sub-state filter."},"include_dismissed":{"type":"boolean","description":"Also match already-archived (dismissed) entries — default false."}},"required":["mode"]}`)
}

func (t batchDisposeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Mode             string `json:"mode"`
		DryRun           *bool  `json:"dry_run"`
		From             string `json:"from"`
		To               string `json:"to"`
		Bucket           string `json:"bucket"`
		ThreadID         string `json:"thread_id"`
		Since            int64  `json:"since"`
		Until            int64  `json:"until"`
		UnreadOnly       bool   `json:"unread_only"`
		State            string `json:"state"`
		IncludeDismissed bool   `json:"include_dismissed"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if !collabinbox.ValidDisposeMode(p.Mode) {
		return "", fmt.Errorf("batch_dispose: unknown mode %q (want archive|delete) — archive hides entries recoverably, delete removes the archive copies permanently", strings.TrimSpace(p.Mode))
	}
	if !collabinbox.ValidBucket(p.Bucket) {
		return "", fmt.Errorf("batch_dispose: unknown bucket %q (want all|approval|mention|automation|system)", p.Bucket)
	}
	switch p.State {
	case "", collabinbox.StateAll, collabinbox.StatePendingMe, collabinbox.StateMine, collabinbox.StateDecided:
	default:
		return "", fmt.Errorf("batch_dispose: unknown state %q (want all|pendingMe|mine|decided)", p.State)
	}
	if p.Since != 0 && p.Until != 0 && p.Since >= p.Until {
		return "", fmt.Errorf("batch_dispose: since must precede until")
	}
	dryRun := true // two-phase: forgetting the flag lists, never mutates
	if p.DryRun != nil {
		dryRun = *p.DryRun
	}

	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	if strings.TrimSpace(mailDir) == "" {
		return "", fmt.Errorf("batch_dispose: no mail directory configured")
	}
	// Same store construction as the query tool (task 320/348): one directory
	// scan feeds the O(1) identity resolver, so bucket classification matches
	// what the panel and query show.
	identityByContact := map[string]string{}
	for _, id := range scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot) {
		if id.ContactID != "" && id.IdentityType != "" {
			identityByContact[id.ContactID] = id.IdentityType
		}
	}
	resolver := func(contact string) string { return identityByContact[contact] }
	store := collabinbox.New(mailDir, resolver)
	query := collabinbox.Query{
		Bucket:           p.Bucket,
		From:             p.From,
		To:               p.To,
		ThreadID:         p.ThreadID,
		Since:            p.Since,
		Until:            p.Until,
		Unread:           p.UnreadOnly,
		State:            p.State,
		Viewer:           t.cfg.currentContactID(),
		IncludeDismissed: p.IncludeDismissed,
	}

	if dryRun {
		plan, err := store.PlanDispose(ctx, query, p.Mode)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"dryRun":    true,
			"mode":      plan.Mode,
			"matched":   plan.Total,
			"truncated": plan.Truncated,
			"entries":   plan.Entries,
			"note":      "phase 1 of 2 — nothing disposed yet; review the list, then re-run with the SAME filters plus dry_run=false",
		})
		return string(out), nil
	}

	res, err := store.DisposeByFilter(ctx, query, p.Mode)
	if err != nil {
		return "", err
	}
	note := "archived — hidden from the default view; recoverable via the panel's dismissed view (已消除, restore)"
	if !res.Recoverable {
		note = "deleted — the archive copies are physically removed and NOT recoverable; conversation content was already injected and is untouched"
	}
	out, _ := json.Marshal(map[string]any{
		"dryRun":      false,
		"mode":        res.Mode,
		"disposed":    res.Disposed,
		"removed":     res.Removed,
		"ids":         res.IDs,
		"recoverable": res.Recoverable,
		"snapshot":    res.Snapshot,
		"note":        note,
	})
	return string(out), nil
}
