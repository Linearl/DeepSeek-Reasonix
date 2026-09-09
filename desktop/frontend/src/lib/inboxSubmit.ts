import type { AppBindings } from "./bridge";
import type { StructuredInvocationSubmit } from "./invocationDisplay";
import { asArray } from "./array";
import type { QuestionAnswer } from "./types";

type InboxEnqueueBindings = Pick<AppBindings, "EnqueueInboxFollowup" | "EnqueueInboxFollowupWithInvocations" | "EnqueueInboxSteer" | "EnqueueInboxSteerForTurn">;
type ActiveTurnBindings = Pick<AppBindings, "ListTabs" | "SteerInboxItem" | "SteerInboxItemForTurn">;

export async function resolveActiveTurnId(binding: Pick<AppBindings, "ListTabs">, tabId: string, known?: string): Promise<string | undefined> {
  // The cached id can outlive the controller turn during startup, recovery,
  // or a runtime rebuild. Always refresh from the tab owner before crossing
  // the exact-turn answer/steer boundary. When the tab owner cannot answer
  // (controller rebuild / recovery fork / backend busy — the #9944 family),
  // fall back to the caller's known turn id instead of returning undefined:
  // dropping it here dead-locks every later ask/approval submit behind the
  // "active turn id is unavailable" guard while the server-side pending
  // prompt waits forever. A stale id surfaces a visible exact-turn rejection
  // instead of a silent self-lock, which is recoverable.
  const authoritative = asArray(await binding.ListTabs()).find((tab) => tab.id === tabId)?.turnId;
  return authoritative || known || undefined;
}

export async function resolvePromptForTab(
  binding: Pick<AppBindings, "ListTabs" | "ResolvePromptForTab">,
  tabId: string,
  promptId: string,
  kind: string,
  answer: Record<string, unknown>,
  knownTurnId?: string,
  knownRuntimeEpoch?: string,
): Promise<void> {
  const submit = await import("./exactPromptSubmit");
  return submit.resolvePromptForTab(binding, tabId, promptId, kind, answer, knownTurnId, knownRuntimeEpoch);
}


type AskAnswerBindings = Pick<AppBindings, "ListTabs" | "ResolvePromptForTab" | "AnswerQuestionForTab" | "AnswerPromptForTab">;

// Final frontend boundary before optimistic transcript state is created.
export function normalizeTurnSubmit(displayText: string, submitText: string) {
  const display = displayText.trim();
  const submit = submitText.trim();
  if (!submit) throw new Error("Message cannot be empty.");
  return { display, submit };
}

// Host-only commands do not create an agent turn or receive a turn id.
export function isLocalRuntimeCommand(input: string): boolean {
  const trimmed = input.trim();
  return trimmed === "/reload" || trimmed === "/effort" || trimmed.startsWith("/effort ");
}

export async function answerPromptForActiveTurn(
  binding: AskAnswerBindings,
  tabId: string,
  promptId: string,
  answers: QuestionAnswer[],
  knownTurnId?: string,
  _knownRuntimeEpoch?: string,
): Promise<void> {
  if (typeof binding.AnswerPromptForTab !== "function") {
    await binding.AnswerQuestionForTab(tabId, promptId, answers);
    return;
  }
  const turnId = await resolveActiveTurnId(binding, tabId, knownTurnId);
  // Fork: without a turn id (turn owned by another process / recovery fork),
  // degrade to the session-scoped answer instead of failing the user's reply.
  if (!turnId) {
    await binding.AnswerQuestionForTab(tabId, promptId, answers);
    return;
  }
  await binding.AnswerPromptForTab(tabId, turnId, promptId, answers);
}

export async function steerInboxItemForActiveTurn(binding: ActiveTurnBindings, tabId: string, itemId: string, knownTurnId?: string) {
  if (typeof binding.SteerInboxItemForTurn !== "function") return binding.SteerInboxItem(tabId, itemId);
  const turnId = await resolveActiveTurnId(binding, tabId, knownTurnId);
  // Fork: no turn id means the turn is owned elsewhere (second writer,
  // recovery fork, or a controller rebuild). Fall back to the session-level
  // steer instead of failing the user's action: the owner applies it to the
  // running turn, and the item stays durable either way.
  if (!turnId) return binding.SteerInboxItem(tabId, itemId);
  return binding.SteerInboxItemForTurn(tabId, turnId, itemId);
}

export async function enqueueInboxGuidanceForActiveTurn(
  binding: InboxEnqueueBindings & Pick<AppBindings, "ListTabs">,
  tabId: string,
  display: string,
  submit: string,
  structured?: StructuredInvocationSubmit,
  knownTurnId?: string,
) {
  const turnId = !structured && typeof binding.EnqueueInboxSteerForTurn === "function"
    ? await resolveActiveTurnId(binding, tabId, knownTurnId)
    : knownTurnId;
  return enqueueInboxGuidance(binding, tabId, display, submit, structured, { steer: true, turnId });
}

export function enqueueInboxGuidance(
  binding: InboxEnqueueBindings,
  tabId: string,
  display: string,
  submit: string,
  structured?: StructuredInvocationSubmit,
  opts?: { steer?: boolean; turnId?: string },
) {
  if (structured) {
    return binding.EnqueueInboxFollowupWithInvocations(
      tabId,
      structured.display.trim() || display,
      structured.input.trim(),
      structured.invocations,
      "",
    );
  }
  if (opts?.steer && typeof binding.EnqueueInboxSteer === "function") {
    if (typeof binding.EnqueueInboxSteerForTurn === "function") {
      // Fork: when the exact-turn id is unknown (turn owned by another
      // process / recovery fork / controller rebuild), degrade to the
      // session-level steer queue instead of rejecting the user's message.
      // The host durably records the item and the owner picks it up.
      if (!opts.turnId) return binding.EnqueueInboxSteer(tabId, display, submit || display, "");
      return binding.EnqueueInboxSteerForTurn(tabId, opts.turnId, display, submit || display, "");
    }
    return binding.EnqueueInboxSteer(tabId, display, submit || display, "");
  }
  return binding.EnqueueInboxFollowup(tabId, display, submit || display, "");
}
