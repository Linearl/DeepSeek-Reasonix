// Task 265/262 lab intake flags: render-surface gates whose values the boot
// snapshot resolves (nil-means-on server-side), applied via module setters the
// same way sessionMonitor/splitView do. Pure frontend gates: no store writes,
// no restart-time semantics beyond "boot snapshot".

import { reportFrontendLog } from "./frontendLog";

export type LabFeatureFlag = "questionSearch" | "subagentTps" | "completionSummary" | "quickCommands" | "subagentPolicy" | "subagentPanel" | "sessionWall" | "tabCompress" | "subagentDetail";

const defaults: Record<LabFeatureFlag, boolean> = {
  questionSearch: true,
  subagentTps: true,
  completionSummary: true,
  quickCommands: false,
  subagentPolicy: true,
  // Task 495: the subagent panel package (dock tab + ended-card collapse)
  // ships off — same boot-snapshot contract, plain default-false bool.
  subagentPanel: false,
  // Task 505: the session graph wall (palette 跳转会话 entry + grid wall)
  // ships off — with it off the palette item list is byte-for-byte unchanged.
  sessionWall: false,
  // Task 506: tab-strip adaptive compression (tiered tab width once >8 tabs,
  // floor 84px) ships off — with it off the strip keeps the exact fixed widths.
  tabCompress: false,
  // Task 507: subagent detail view ships off — dual state, off = plan C
  // (inline preview expansion + widen affordance), on = plan A (row click
  // opens the read-only in-dock detail view with a back button).
  subagentDetail: false,
};

// Task 651: the tab permission indicator is a three-mode setting, not a bool,
// so it lives beside the boolean lab flags: "badge" (default) keeps the
// plan/goal/auto/yolo text badges, "off" hides the per-tab permission
// indicator entirely, "background" paints the low-opacity (10%) per-mode tab
// background instead of the badges. The server resolves the legacy task-504
// experimental_tab_mode_tint bool into this setting, and the settings save
// re-applies it via the same boot snapshot — no restart.
export type TabPermissionIndicatorMode = "badge" | "off" | "background";

export function normalizeTabPermissionIndicator(value: unknown): TabPermissionIndicatorMode {
  return value === "off" || value === "background" ? value : "badge";
}

let tabPermissionIndicator: TabPermissionIndicatorMode = "badge";

const flags: Record<LabFeatureFlag, boolean> = { ...defaults };
const listeners = new Set<() => void>();

/** Boot-time (or test) entry: replace the whole flag set from the snapshot. */
export function applyLabFlags(next: Partial<Record<LabFeatureFlag, boolean>>): void {
  for (const key of Object.keys(defaults) as LabFeatureFlag[]) {
    flags[key] = next[key] ?? defaults[key];
  }
  reportFrontendLog(
    "desktop-prefs",
    "lab flags",
    `questionSearch=${flags.questionSearch} subagentTps=${flags.subagentTps} completionSummary=${flags.completionSummary} quickCommands=${flags.quickCommands} subagentPanel=${flags.subagentPanel} sessionWall=${flags.sessionWall} tabCompress=${flags.tabCompress} subagentDetail=${flags.subagentDetail}`,
  );
  for (const listener of listeners) listener();
}

/**
 * Boot-time (or settings-save) entry for the task-651 three-mode tab
 * permission indicator. Unknown/absent values normalize to "badge" — the
 * server already folds the legacy 504 bool in, so the frontend only ever sees
 * the resolved string.
 */
export function applyTabPermissionIndicator(value: unknown): void {
  const next = normalizeTabPermissionIndicator(value);
  if (next === tabPermissionIndicator) return;
  tabPermissionIndicator = next;
  for (const listener of listeners) listener();
}

export function tabPermissionIndicatorMode(): TabPermissionIndicatorMode {
  return tabPermissionIndicator;
}

export function labFlagEnabled(key: LabFeatureFlag): boolean {
  return flags[key];
}

export function onLabFlagsChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
