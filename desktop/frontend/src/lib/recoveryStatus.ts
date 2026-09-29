import type { Translator } from "./i18n";

export interface RecoveryStatus {
  state?: "recovery_required" | string;
  call_id?: string;
  attempt_id?: string;
  requires_user_decision?: boolean;
  read_only?: boolean;
  phase?: string;
  reason?: string;
  next_attempt_at?: number;
  waited_ms?: number;
  wait_budget_ms?: number;
  waiting?: boolean;
  // Task 372 (visibility only): the task-243 A4 sliding-window counts, so the
  // strip can say "auto-retried N (N/limit) times" and name the give-up end
  // state. Admission semantics live in Go and are untouched here.
  budget_used?: number;
  budget_limit?: number;
  budget_exhausted?: boolean;
}

export interface RecoveryRetry {
  attempt: number;
  max: number;
  recovery?: RecoveryStatus;
}

export interface RecoveryEventFields {
  recovery?: RecoveryStatus;
  retryAttempt?: number;
  retryMax?: number;
}

export function recoveryNextAttemptSeconds(recovery: RecoveryStatus, now: number): number {
  return Math.max(0, Math.ceil(((recovery.next_attempt_at ?? now) - now) / 1000));
}

export function recoveryStatusText(t: Translator, retry: RecoveryRetry, now: number): string {
  const r = retry.recovery;
  // Task 372 end state first: the budget window or the attempt cap refused
  // another round — name the termination instead of flipping to idle.
  if (r?.budget_exhausted) {
    const limit = r.budget_limit || retry.max;
    const used = r.budget_used || retry.attempt;
    return t("status.retryExhausted", { used, limit });
  }
  // In-window frame carrying the budget counts: the "auto-retried N (N/limit)"
  // face the user asked for (372 scope 1).
  if (r?.budget_limit) {
    return t("status.retryingBudget", { used: r.budget_used ?? retry.attempt, limit: r.budget_limit });
  }
  if (!r?.waiting) return t("status.retrying", { attempt: retry.attempt, max: retry.max });
  const phase = t(r.phase === "connect" ? "status.recoveryNetwork" : "status.recoveryProvider");
  return t("status.recoveryWaiting", { seconds: recoveryNextAttemptSeconds(r, now), phase });
}
