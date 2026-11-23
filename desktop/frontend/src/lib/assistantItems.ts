import type { Item, LiveStream, State } from "./useController";

type AssistantItem = Extract<Item, { kind: "assistant" }>;

export function assistantHasContent(item: AssistantItem | undefined, live?: LiveStream): boolean {
  return Boolean(
    `${live?.text ?? ""}${live?.reasoning ?? ""}${item?.text ?? ""}${item?.reasoning ?? ""}`.trim()
    || item?.memoryCitations?.length
    || item?.searchSources?.length
  );
}

export function removeEmptyAssistantItems(items: Item[]): Item[] {
  return items.filter((item) => item.kind !== "assistant" || assistantHasContent(item));
}

/** Allocate one provider sampling segment without changing the backend turn identity. */
export function ensureAssistant(s: State): State {
  if (s.currentAssistant && s.items.some((item) => item.kind === "assistant" && item.id === s.currentAssistant)) return s;
  const ordinal = s.assistantSegmentOrdinal;
  const id = s.activeTurnId ? `a:${s.activeTurnId}:${ordinal}` : `a${s.seq}`;
  const item: AssistantItem = { kind: "assistant", id, text: "", reasoning: "", streaming: true, wasStreamed: true, searchSources: s.pendingSearchSources?.length ? s.pendingSearchSources : undefined };
  return {
    ...s,
    items: [...s.items, item],
    currentAssistant: id,
    pendingSearchSources: undefined,
    seq: s.seq + 1,
    assistantSegmentOrdinal: ordinal + 1,
  };
}

export function ensureActiveAssistant(s: State): State {
  const active = ensureAssistant(s);
  const id = active.currentAssistant!;
  return active.live?.id === id ? active : { ...active, live: { id, text: "", reasoning: "", reasoningComplete: false } };
}

// tl1739r2: the surface's rendered-row fingerprint for one backend turn. The
// live path names every assistant segment `a:<turnId>:<ordinal>` (ensureAssistant),
// so "this turn already rendered here" is decidable from items alone — no
// separate ledger needed.
export function hasRenderedTurnRows(items: Item[], turnId: string): boolean {
  if (!turnId) return false;
  const prefix = `a:${turnId}:`;
  return items.some((item) => item.id.startsWith(prefix));
}

// tl1739r2: render-entry turn-id dedupe for the reconcile replay channel
// (reconcile 重放不重复 append). A replayed event whose turn is NOT the
// surface's active turn but already has rendered rows is a display-layer
// double-write: the seq watermark usually adjudicates it, but a restarted
// ledger (backend_status ledgerRestarted → appliedEventSeq reset, the tl1739
// tightening) re-arms small seqs and the whole settled turn re-pours onto the
// intact surface — the 20261120/20261121/20261123 duplicate-render recurrence
// (数据层无重复、display 层双写). The active-turn back-fill (fence-swallowed
// deltas, tl1444/tl1739 main shape) is preserved: equal activeTurnId passes.
export function replayDuplicatesRenderedTurn(
  state: Pick<State, "items" | "activeTurnId"> | undefined,
  turnId: string | undefined,
): boolean {
  if (!state || !turnId) return false;
  if (state.activeTurnId === turnId) return false;
  return hasRenderedTurnRows(state.items, turnId);
}
