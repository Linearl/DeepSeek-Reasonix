import type { Translator } from "./i18n";

export function completionReviewLabel(review: string, t: Translator): string {
  switch (review.trim().toLowerCase()) {
    case "none": return t("common.none");
    case "passed": return t("task.state.succeeded");
    case "warned": return t("diag.warnings");
    case "failed": return t("task.state.failed");
    case "unavailable": return t("caps.toolUnavailable");
    default: return t("projectTree.status.waitingConfirmation");
  }
}

export function completionGapLabel(gap: string, t: Translator): string {
  switch (gap.trim().toLowerCase()) {
    case "suppressed":
    case "suppressed_requirement": return t("tool.shell.verificationNotRun");
    case "stale_check": return t("completion.gapStaleCheck");
    default: return t("context.other");
  }
}
