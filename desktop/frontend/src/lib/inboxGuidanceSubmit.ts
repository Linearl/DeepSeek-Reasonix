import type { AppBindings } from "./bridge";
import type { StructuredInvocationSubmit } from "./invocationDisplay";
import { resolveActiveTurnId } from "./inboxSubmit";
import { attachInboxDedupItemId, noteInboxEnqueue } from "./inboxDedup";

type InboxEnqueueBindings = Pick<AppBindings, "EnqueueInboxFollowup" | "EnqueueInboxFollowupWithInvocations" | "EnqueueInboxSteer" | "EnqueueInboxSteerForTurn">;

export type InboxGuidanceReceipt = {
  itemId?: string;
  paused?: boolean;
  /** Task 51 UI-2: true when the short-window dedup collapsed this submit. */
  duplicate?: boolean;
  error?: string;
};

function structuredFingerprint(structured?: StructuredInvocationSubmit): string {
  if (!structured) return "";
  return JSON.stringify({ d: structured.display, i: structured.input, n: structured.invocations });
}

export async function enqueueInboxGuidanceForActiveTurn(
  binding: InboxEnqueueBindings & Pick<AppBindings, "ListTabs">,
  tabId: string,
  display: string,
  submit: string,
  structured?: StructuredInvocationSubmit,
  knownTurnId?: string,
): Promise<InboxGuidanceReceipt> {
  const turnId = !structured && typeof binding.EnqueueInboxSteerForTurn === "function"
    ? await resolveActiveTurnId(binding, tabId, knownTurnId)
    : knownTurnId;
  return enqueueInboxGuidance(binding, tabId, display, submit, structured, { steer: true, turnId });
}

export async function enqueueInboxGuidance(
  binding: InboxEnqueueBindings,
  tabId: string,
  display: string,
  submit: string,
  structured?: StructuredInvocationSubmit,
  opts?: { steer?: boolean; turnId?: string; idempotency?: string },
): Promise<InboxGuidanceReceipt> {
  // Task 51 UI-2: collapse a same-text same-structure double submit inside a
  // short window. The first attempt owns the durable enqueue; the second is a
  // no-op that reports the already-queued item id.
  const fp = structuredFingerprint(structured);
  const prior = noteInboxEnqueue(tabId, display, fp);
  if (prior) {
    return { itemId: prior.itemId, duplicate: true };
  }
  const receipt = await performEnqueue(binding, tabId, display, submit, structured, opts);
  if (receipt?.itemId) attachInboxDedupItemId(tabId, display, fp, receipt.itemId);
  return receipt ?? {};
}

async function performEnqueue(
  binding: InboxEnqueueBindings,
  tabId: string,
  display: string,
  submit: string,
  structured?: StructuredInvocationSubmit,
  opts?: { steer?: boolean; turnId?: string; idempotency?: string },
): Promise<InboxGuidanceReceipt | undefined> {
  if (structured) {
    return binding.EnqueueInboxFollowupWithInvocations(
      tabId,
      structured.display.trim() || display,
      structured.input.trim(),
      structured.invocations,
      opts?.idempotency ?? "",
    ) as Promise<InboxGuidanceReceipt>;
  }
  if (opts?.steer && typeof binding.EnqueueInboxSteer === "function") {
    if (typeof binding.EnqueueInboxSteerForTurn === "function") {
      // Fork: degrade to the session-level steer queue instead of rejecting the
      // user's message when the exact-turn id is unknown (owner may be another
      // process / recovery fork / controller rebuild).
      if (!opts.turnId) return binding.EnqueueInboxSteer(tabId, display, submit || display, opts?.idempotency ?? "") as Promise<InboxGuidanceReceipt>;
      return binding.EnqueueInboxSteerForTurn(tabId, opts.turnId, display, submit || display, "") as Promise<InboxGuidanceReceipt>;
    }
    return binding.EnqueueInboxSteer(tabId, display, submit || display, "") as Promise<InboxGuidanceReceipt>;
  }
  return binding.EnqueueInboxFollowup(tabId, display, submit || display, opts?.idempotency ?? "") as Promise<InboxGuidanceReceipt>;
}

