import type { AppBindings } from "./bridge";
import { asArray } from "./array";
import { removeEmptyAssistantItems } from "./assistantItems";
import type { Item, State } from "./useController";

export function reduceSubmitFailure(
  state: State,
  submissionId: string,
  error: string,
  conservative: boolean,
  observedAt: number,
): State {
  if (state.pendingSubmissionId !== submissionId) return state;
  const index = state.items.findIndex((item) => item.kind === "user" && item.submissionId === submissionId);
  const items = index < 0
    ? state.items
    : state.items.map((item, itemIndex) => itemIndex === index ? { ...item, submissionId: undefined, failed: true } : item);
  const next = {
    ...state,
    pendingUser: undefined,
    pendingSubmissionId: undefined,
    deliveryRecoveryActive: false,
    cancelRequested: false,
    seq: state.seq + 1,
    items: [...removeEmptyAssistantItems(items), { kind: "notice", id: `n${state.seq}`, level: "warn", text: error } as Item],
  };
  // Keep the turn id alive while a server-side prompt (ask/approval/mcp)
  // is still pending on that turn: clearing it dead-locks every later
  // answer/steer behind the "active turn id is unavailable" guard while the
  // server-side pending waits forever (#9923/#9944 family). A stale id
  // surfaces a visible exact-turn rejection instead of a silent self-lock,
  // which is recoverable.
  const keepTurnId = Boolean(state.approval || state.ask || state.mcpInteraction);
  return {
    ...next,
    running: conservative,
    turnActive: conservative,
    pendingPrompt: conservative && Boolean(state.approval || state.ask || state.mcpInteraction),
    cancellable: conservative,
    ...(conservative ? {} : {
      activeTurnId: keepTurnId ? state.activeTurnId : undefined,
      currentAssistant: undefined,
      assistantSegmentOrdinal: 0,
      live: undefined,
      streamAttemptJournal: undefined,
      turnLifecycleObservedAt: observedAt,
    }),
  };
}

// 任务461-P6: a foreground submit bounced with "turn already running" degrades
// into the durable guidance queue instead of failing. The optimistic bubble
// stays delivered (NEVER marked failed — that face belongs to genuine send
// failures and the manual resend), an info notice names the destination
// (steer-injected now vs queued until the turn finishes), and the turn face
// keeps the facts the rejection just proved authoritatively: the turn IS
// running, so the composer stays blocked and live/turn state is not torn down
// the way the failure path flattens it.
export function reduceSubmitDegraded(
  state: State,
  submissionId: string,
  text: string,
  inboxItemId: string | undefined,
  turnId: string | undefined,
): State {
  if (state.pendingSubmissionId !== submissionId) return state;
  const index = state.items.findIndex((item) => item.kind === "user" && item.submissionId === submissionId);
  const items = index < 0
    ? state.items
    : state.items.map((item, itemIndex) => itemIndex === index ? { ...item, submissionId: undefined } : item);
  const notice: Item = { kind: "notice", id: `s${state.seq}`, level: "info", text, ...(inboxItemId ? { inboxItemId } : {}) } as Item;
  return {
    ...state,
    items: [...items, notice],
    pendingUser: undefined,
    pendingSubmissionId: undefined,
    deliveryRecoveryActive: false,
    cancelRequested: false,
    seq: state.seq + 1,
    running: true,
    turnActive: true,
    cancellable: true,
    pendingPrompt: Boolean(state.approval || state.ask || state.mcpInteraction),
    // Adopt the freshly resolved authoritative turn id when the tab owner
    // answered (turn-state alignment, ruling ②); keep the current one else.
    ...(turnId ? { activeTurnId: turnId } : {}),
  };
}

export function reduceManagementConfirmation(state: State, submissionId: string, observedAt: number): State {
  if (state.pendingSubmissionId !== submissionId) return state;
  return {
    ...state,
    items: state.items.filter((item) => !(item.kind === "user" && item.submissionId === submissionId)),
    pendingUser: undefined,
    pendingSubmissionId: undefined,
    running: false,
    turnActive: false,
    pendingPrompt: false,
    cancelRequested: false,
    cancellable: false,
    activeTurnId: undefined,
    currentAssistant: undefined,
    assistantSegmentOrdinal: 0,
    live: undefined,
    streamAttemptJournal: undefined,
    deliveryRecoveryActive: false,
    turnLifecycleObservedAt: observedAt,
  };
}

export async function findTabAfterSubmitFailure(
  binding: Pick<AppBindings, "ListTabs">,
  tabId: string,
  delays: readonly number[],
  clock: () => number,
) {
  for (const delay of delays) {
    if (delay) await new Promise((resolve) => setTimeout(resolve, delay));
    try {
      // Fence at read start, so a delayed response cannot override a turn or
      // prompt observed while it was in flight. Preserve a sub-tick advance
      // for synchronous bridges called in the initiating event's clock tick.
      const snapshotAt = clock() + 0.001;
      const tab = asArray(await binding.ListTabs()).find((candidate) => candidate.id === tabId);
      return [tab, snapshotAt] as const;
    } catch {
      // The caller's stale-turn watchdog remains the long-tail backstop.
    }
  }
  return undefined;
}
