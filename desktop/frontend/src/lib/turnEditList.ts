// Turn-level edit list aggregation (task 113).
//
// Summarizes one turn's file changes into a compact header plus a collapsible
// list, distinct from the right-panel workspace change scope.

import type { Translator } from "./i18n";
import type { TurnChanges, TurnFileChange } from "./turnResultTypes";

export interface TurnEditListEntry {
  path: string;
  kind: TurnFileChange["kind"];
  added: number;
  removed: number;
  binary?: boolean;
  modeOnly?: boolean;
  uncounted?: boolean;
}

export interface TurnEditListModel {
  files: TurnEditListEntry[];
  totalFiles: number;
  added: number;
  removed: number;
  coverage: TurnChanges["coverage"];
  /** Files not shown in the initial window. */
  hiddenCount: number;
  visible: TurnEditListEntry[];
}

export const TURN_EDIT_LIST_INITIAL = 5;

export function buildTurnEditList(
  diff: TurnChanges | undefined,
  initial: number = TURN_EDIT_LIST_INITIAL,
): TurnEditListModel | undefined {
  if (!diff || diff.coverage === "unknown" || diff.files.length === 0) return undefined;
  const files: TurnEditListEntry[] = diff.files.map((f) => ({
    path: f.path,
    kind: f.kind,
    added: f.added,
    removed: f.removed,
    binary: f.binary,
    modeOnly: f.modeOnly,
    uncounted: f.uncounted,
  }));
  const window = initial <= 0 ? files.length : initial;
  const visible = files.slice(0, window);
  return {
    files,
    totalFiles: files.length,
    added: diff.added,
    removed: diff.removed,
    coverage: diff.coverage,
    hiddenCount: Math.max(0, files.length - visible.length),
    visible,
  };
}

export function turnEditListHeader(model: TurnEditListModel, t: Translator): string {
  const files = t(
    model.coverage === "complete" ? "completion.filesChanged" : "completion.filesCounted",
    { count: model.totalFiles },
  );
  const suffix = model.coverage === "partial" ? ` · ${t("completion.partialStats")}` : "";
  return `${files} · +${model.added} −${model.removed}${suffix}`;
}

export function turnEditFileLabel(entry: TurnEditListEntry, t: Translator): string {
  if (entry.binary) return t("completion.binary");
  if (entry.modeOnly) return t("completion.modeOnly");
  if (entry.uncounted) return t("completion.linesUnknown");
  return `+${entry.added} −${entry.removed}`;
}

export function turnEditKindLabel(kind: TurnFileChange["kind"], t: Translator): string {
  switch (kind) {
    case "create":
      return t("turnEdit.kindCreate");
    case "delete":
      return t("turnEdit.kindDelete");
    default:
      return t("turnEdit.kindModify");
  }
}
