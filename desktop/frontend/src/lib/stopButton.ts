// 任务461-P7 三级终止: the stop button's view-state machine (pure, testable).
// The backend owns the authoritative escalation level (RuntimeStatus mirror);
// this only decides what the button shows and what the next click does.
//
// tl1420 档位显形: the button itself carries the tier feedback (main signal,
// hover/tooltip stays auxiliary). Three engaged tiers read directly off the
// button —
//   L1 已受理 (normal/armed): 「停止中…」 amber; armed adds the pulse.
//   L2 强制宽限 (grace):      「强制停止 · Ns」 deep red, the deadline counts
//                             down on the button.
//   L3 已终止 (force/settled): 「已终止」 holds briefly (settle linger) after
//                             the turn ends, then the button returns to idle.
// All tier helpers are pure so the harness can pin label/class pairing.

export type StopButtonPhase =
  | "idle"
  | "normal"
  | "armed"
  | "grace"
  | "force"
  | /** tl1420: stop settled — 「已终止」 hold before returning to idle */ "settled";

export type StopButtonView = {
  phase: StopButtonPhase;
  /** Grace countdown seconds (ceil); null outside the grace. */
  countdownSeconds: number | null;
};

/** tl1420: how long 「已终止」 stays on the button after the turn ends. */
export const STOP_SETTLE_LINGER_MS = 1500;

export function stopButtonView(input: {
  stopLevel: number;
  stopDeadlineUnix: number;
  /** When THIS surface fired the L1 stop (the first click); null before. */
  stopInitiatedAt: number | null;
  running: boolean;
  now: number;
  /** How long after the L1 click the button arms into 强制停止 (default 1s). */
  armAfterMs?: number;
  /** tl1420: a stop had been engaged on this turn at some point. */
  stopEverEngaged?: boolean;
  /** tl1420: when the turn ended after an engaged stop; null before. */
  stopSettledAt?: number | null;
  /** How long 「已终止」 holds after settle (default STOP_SETTLE_LINGER_MS). */
  settleLingerMs?: number;
}): StopButtonView {
  const armAfterMs = input.armAfterMs ?? 1000;
  const settleLingerMs = input.settleLingerMs ?? STOP_SETTLE_LINGER_MS;
  if (!input.running) {
    // tl1420 L3 定格: after an engaged stop settles the turn, hold 「已终止」
    // for a beat so the outcome is legible, then fall back to idle (button
    // unmounts). A turn that ended without any stop engagement skips the hold.
    if (
      input.stopEverEngaged &&
      input.stopSettledAt != null &&
      input.now - input.stopSettledAt < settleLingerMs
    ) {
      return { phase: "settled", countdownSeconds: null };
    }
    return { phase: "idle", countdownSeconds: null };
  }
  const localLevel = input.stopInitiatedAt != null ? 1 : 0;
  const level = Math.max(input.stopLevel, localLevel);
  if (level <= 0) return { phase: "idle", countdownSeconds: null };
  if (level >= 3) return { phase: "force", countdownSeconds: null };
  if (level >= 2) {
    const remaining = input.stopDeadlineUnix > 0
      ? Math.max(0, Math.ceil(input.stopDeadlineUnix - input.now / 1000))
      : null;
    // tl1514 兜底分支: the countdown ran out but the backend's L3 push never
    // landed (the publish lane wedges with the very turn being stopped —
    // 2026-11-06 field shape: the view sat at 「强制停止 · 0s」 forever). The
    // grace is over, so the view reads the terminal tier locally; the next
    // click routes through onEscalateStop and the backend (still at Grace)
    // fires the force immediately — one press, no re-press ladder.
    if (remaining != null && remaining <= 0) {
      return { phase: "force", countdownSeconds: null };
    }
    return { phase: "grace", countdownSeconds: remaining };
  }
  const armed = input.stopInitiatedAt != null && input.now - input.stopInitiatedAt >= armAfterMs;
  return { phase: armed ? "armed" : "normal", countdownSeconds: null };
}

/** tl1420: visual tier → CSS modifier on .composer__btn--stop (three color
 * steps + the settled hold); null on idle keeps the resting look untouched. */
export type StopButtonTier = "stopping" | "grace" | "force" | "settled";

export function stopButtonTier(phase: StopButtonPhase): StopButtonTier | null {
  if (phase === "normal" || phase === "armed") return "stopping";
  if (phase === "grace") return "grace";
  if (phase === "force") return "force";
  if (phase === "settled") return "settled";
  return null;
}

/** tl1420: the on-button label (i18n key + optional countdown var). The label
 * IS the main tier feedback; the tooltip hint stays secondary. */
export type StopButtonLabelSpec = {
  key: "composer.stopStopping" | "composer.stopForceCountdown" | "composer.stopTerminated";
  n?: number;
};

export function stopButtonLabelSpec(
  phase: StopButtonPhase,
  countdownSeconds: number | null,
): StopButtonLabelSpec {
  if (phase === "grace") return { key: "composer.stopForceCountdown", n: countdownSeconds ?? 0 };
  if (phase === "force" || phase === "settled") return { key: "composer.stopTerminated" };
  return { key: "composer.stopStopping" };
}
