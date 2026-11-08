import type { Translator } from "./i18n";
import type { GoalActivation, GoalPhase } from "./types";

// tl1472 目标对象化: sidebar labels for the goal object's phase and activation.
// The activation label is the load-bearing half — showing only the running
// phase would replicate the 1459 mirror incident ("显示 running 其实早已
// disarmed"), so phase and activation always render together.

export function goalActivationLabel(t: Translator, activation: GoalActivation): string {
  if (activation === "disarmed") return t("goal.activation.disarmed");
  return t("goal.activation.armed");
}

export function goalPhaseLabel(t: Translator, phase: GoalPhase): string {
  switch (phase) {
    case "active":
      return t("goal.phase.active");
    case "paused":
      return t("goal.phase.paused");
    case "blocked":
      return t("goal.phase.blocked");
    case "complete":
      return t("goal.phase.complete");
    case "budget_limited":
      return t("goal.phase.budgetLimited");
    default:
      return "";
  }
}
