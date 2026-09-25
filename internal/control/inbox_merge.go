package control

import (
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"

	"reasonix/internal/sessioninbox"
)

// Task 221: drain-time inbox merge. The tri-state lives in config
// (collab_inbox_merge: off | same_sender | all) and is armed once per process
// at host boot; the dispatcher reads it at admission time. "off" is the
// current behaviour to the letter — one item per dispatch, no extra reads.

const (
	// CollabInboxMergeOff keeps the FIFO one-item-per-dispatch behaviour.
	CollabInboxMergeOff = "off"
	// CollabInboxMergeSameSender merges queued items sharing an envelope
	// Source (one collaborating session, or one frontend).
	CollabInboxMergeSameSender = "same_sender"
	// CollabInboxMergeAll merges every queued item in the drain group.
	CollabInboxMergeAll = "all"
)

var collabInboxMergeMode atomic.Value // string

// SetCollabInboxMergeMode arms the drain-time merge for every controller in
// this process. Unknown values read as "off", so a bad config write can never
// arm a mode the dispatcher does not implement.
func SetCollabInboxMergeMode(mode string) {
	collabInboxMergeMode.Store(NormalizeCollabInboxMergeMode(mode))
}

// CollabInboxMergeMode reports the armed merge mode; "off" before anyone arms
// it (the zero value must stay the zero behaviour).
func CollabInboxMergeMode() string {
	if v, ok := collabInboxMergeMode.Load().(string); ok {
		return v
	}
	return CollabInboxMergeOff
}

// NormalizeCollabInboxMergeMode mirrors config.NormalizeCollabInboxMerge for
// the process-level setter (kept local so control does not import config).
func NormalizeCollabInboxMergeMode(mode string) string {
	switch mode {
	case CollabInboxMergeSameSender, CollabInboxMergeAll:
		return mode
	default:
		return CollabInboxMergeOff
	}
}

// mergeSegmentHeader prefixes one merged segment with its provenance so every
// original stays addressable from the merged body (task 221: 原文留痕).
func mergeSegmentHeader(meta sessioninbox.InboxItemMeta) string {
	var b strings.Builder
	b.WriteString("── 合并自 inbox 条目 ")
	b.WriteString(meta.ID)
	if meta.Source != "" {
		b.WriteString("（来源 ")
		b.WriteString(meta.Source)
		b.WriteString("）")
	}
	b.WriteString(" ──")
	return b.String()
}

// mergeInboxEnvelope concatenates the members' submit bodies in dispatch order.
// The merged body is mechanical — no summarisation, no rephrasing — so nothing
// is lost and the model reads every original verbatim under its own header.
func mergeInboxEnvelope(metas []sessioninbox.InboxItemMeta, envelopes []sessioninbox.PromptEnvelope, mode string) sessioninbox.PromptEnvelope {
	var b strings.Builder
	b.WriteString("[合并消息 ×")
	b.WriteString(strconv.Itoa(len(metas)))
	b.WriteString("]\n")
	for i, env := range envelopes {
		b.WriteString("\n")
		b.WriteString(mergeSegmentHeader(metas[i]))
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(firstNonEmptyStr(env.SubmitText, env.RawText, env.DisplayText)))
		b.WriteString("\n")
	}
	merged := strings.TrimSpace(b.String())
	first := envelopes[0]
	return sessioninbox.PromptEnvelope{
		DisplayText:    merged,
		RawText:        merged,
		SubmitText:     merged,
		Format:         first.Format,
		Source:         first.Source,
		Invocations:    first.Invocations,
		Refs:           first.Refs,
		FrozenRefBlock: first.FrozenRefBlock,
		FrozenImages:   first.FrozenImages,
		ExplicitRefs:   first.ExplicitRefs,
		Extra:          mergeExtra(metas, mode),
	}
}

// envelopeBodiesAllEmpty reports that every member renders to nothing but the
// merge scaffolding — all three text slots empty on every envelope (invocation
// metadata does not count as content for the merged body).
func envelopeBodiesAllEmpty(envelopes []sessioninbox.PromptEnvelope) bool {
	for _, env := range envelopes {
		if strings.TrimSpace(firstNonEmptyStr(env.SubmitText, env.RawText, env.DisplayText)) != "" {
			return false
		}
	}
	return true
}

func mergeExtra(metas []sessioninbox.InboxItemMeta, mode string) map[string]string {
	ids := make([]string, 0, len(metas))
	for _, m := range metas {
		ids = append(ids, m.ID)
	}
	return map[string]string{
		"mergedFrom":  strings.Join(ids, ","),
		"mergeMode":   mode,
		"mergeSource": "task221",
	}
}

// maybeMergeInboxDispatchGroup applies the armed merge mode to the item the
// dispatcher just picked. It returns the id to admit — usually first itself,
// the carrier of the merged body when a merge happened. Merging never waits
// for more messages: whatever is already queued at dispatch time is the whole
// group, so a single arriving message is injected immediately either way.
func (c *Controller) maybeMergeInboxDispatchGroup(first sessioninbox.InboxItemMeta) sessioninbox.InboxItemMeta {
	mode := CollabInboxMergeMode()
	if mode == CollabInboxMergeOff {
		return first
	}
	st, err := c.ensureInbox()
	if err != nil {
		slog.Warn("inbox merge: open store", "err", err)
		return first
	}
	queued := st.QueuedDispatchItems()
	group := mergeGroupFor(mode, first, queued)
	if len(group) < 2 {
		return first
	}
	metas := make([]sessioninbox.InboxItemMeta, 0, len(group))
	envelopes := make([]sessioninbox.PromptEnvelope, 0, len(group))
	for _, meta := range group {
		_, env, err := st.ReadItem(meta.ID)
		if err != nil {
			slog.Warn("inbox merge: read member", "id", meta.ID, "err", err)
			return first // a half-readable group degrades to no merge, never to a lossy one
		}
		metas = append(metas, meta)
		envelopes = append(envelopes, env)
	}
	// Task 243 A5 (sub-report 01-④5): "宁可留队列不写空消息". A group whose
	// bodies are all empty would merge into a header-only shell and consume
	// the queue doing it. Leave everything queued for a human to fill in —
	// the single-item twin of this defence is Enqueue's ErrEmpty rejection
	// (sessioninbox store.go), so both drain paths refuse empty renders.
	if envelopeBodiesAllEmpty(envelopes) {
		slog.Warn("inbox merge: group bodies all empty, leaving queue untouched",
			"carrier", first.ID, "members", len(metas), "mode", mode)
		return first
	}
	merged := mergeInboxEnvelope(metas, envelopes, mode)
	if _, err := st.UpdateItem(first.ID, merged); err != nil {
		slog.Warn("inbox merge: write carrier", "id", first.ID, "err", err)
		return first
	}
	// The members' bodies are now verbatim inside the carrier blob. Remove them
	// from the queue so the merged turn is the only thing admitted; if a removal
	// fails, park the member as blocked with the carrier id so it can never be
	// dispatched twice — a human can still read or restore it.
	for _, meta := range metas[1:] {
		if err := st.DeleteItem(meta.ID); err != nil {
			slog.Warn("inbox merge: retire member", "id", meta.ID, "err", err)
			if stateErr := st.SetState(meta.ID, sessioninbox.StateBlocked, "merged into "+first.ID); stateErr != nil {
				slog.Error("inbox merge: park member after failed delete", "id", meta.ID, "err", stateErr)
			}
		}
	}
	slog.Info("inbox merge: drained group as one item", "carrier", first.ID, "members", len(metas), "mode", mode)
	return first
}

// mergeGroupFor picks the members that merge into first (which is always
// members[0]): same Source for same_sender, everything queued for all.
func mergeGroupFor(mode string, first sessioninbox.InboxItemMeta, queued []sessioninbox.InboxItemMeta) []sessioninbox.InboxItemMeta {
	group := []sessioninbox.InboxItemMeta{first}
	for _, meta := range queued {
		if meta.ID == first.ID {
			continue
		}
		if mode == CollabInboxMergeAll || meta.Source == first.Source {
			group = append(group, meta)
		}
	}
	return group
}
