// tl1240: the sidebar batch manage action bar — the floating strip at the
// bottom of the project tree while batch mode is on (multi-select checkboxes in
// the rows feed it). Paradigm: the collab inbox batch bar (task 856 ②) —
// selected count, the two actions (archive / move-to-group), exit — extended
// with the tl1240 result strip: N/M progress after the single bridge call plus
// the failure list where forceable-class topics (turn running / lease busy)
// show as deliberately skipped rather than failed.
import { useState } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import { Archive, Check, FolderInput, X } from "lucide-react";
import { ContextMenu, contextMenuPointFromEvent, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { useT } from "../lib/i18n";
import type { BatchReportState } from "../lib/projectTreeBatch";
import type { SessionGroup } from "../lib/types";

export function ProjectTreeBatchBar({
  selectedCount,
  busy,
  report,
  moveGroups,
  mixedScope,
  onArchive,
  onMoveToGroup,
  onClearSelection,
  onExit,
  onDismissReport,
}: {
  selectedCount: number;
  busy: boolean;
  report: BatchReportState | null;
  /** Groups of the selection's common scope+root; null = the selection spans
   * workspaces (or is empty) and batch move is unavailable. */
  moveGroups: SessionGroup[] | null;
  mixedScope: boolean;
  onArchive: () => void;
  onMoveToGroup: (group: SessionGroup) => void;
  onClearSelection: () => void;
  onExit: () => void;
  onDismissReport: () => void;
}) {
  const t = useT();
  const [moveMenuPoint, setMoveMenuPoint] = useState<ContextMenuPoint | null>(null);
  const openMoveMenu = (event: ReactMouseEvent<HTMLElement>) => {
    event.preventDefault();
    event.stopPropagation();
    setMoveMenuPoint(contextMenuPointFromEvent(event));
  };
  const moveItems: ContextMenuItem[] = (moveGroups ?? []).map((group) => ({
    key: `batch-move:${group.id}`,
    icon: <FolderInput size={13} />,
    label: group.title,
    onSelect: () => {
      setMoveMenuPoint(null);
      onMoveToGroup(group);
    },
  }));
  return (
    <div className="project-tree__batchbar" role="toolbar" aria-label={t("projectTree.batchManage")}>
      <div className="project-tree__batchbar-row">
        <span className="project-tree__batchbar-count" aria-live="polite">
          {t("projectTree.batchSelectedCount", { n: selectedCount })}
        </span>
        <button
          type="button"
          className="btn btn--primary btn--small"
          disabled={busy || selectedCount === 0}
          onClick={onArchive}
        >
          <Archive size={13} aria-hidden="true" />
          {busy ? t("projectTree.batchArchiveBusyProgress") : t("projectTree.batchArchive")}
        </button>
        <button
          type="button"
          className="btn btn--secondary btn--small"
          disabled={busy || selectedCount === 0 || moveGroups === null}
          title={mixedScope && selectedCount > 0 ? t("projectTree.batchMoveMixedScope") : undefined}
          aria-haspopup="menu"
          aria-expanded={moveMenuPoint !== null}
          onClick={openMoveMenu}
        >
          <FolderInput size={13} aria-hidden="true" />
          {t("projectTree.batchMove")}
        </button>
        <button
          type="button"
          className="btn btn--secondary btn--small"
          disabled={busy || selectedCount === 0}
          onClick={onClearSelection}
        >
          {t("projectTree.batchClearSelection")}
        </button>
        <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={onExit}>
          <X size={13} aria-hidden="true" />
          {t("projectTree.batchManageExit")}
        </button>
      </div>
      {report && (
        <div className="project-tree__batchbar-report" role="status">
          <span className="project-tree__batchbar-report-line">
            {report.kind === "archive"
              ? t("projectTree.batchArchiveDone", { done: report.done, total: report.total })
              : t("projectTree.batchMoveDone", { done: report.done, total: report.total })}
            <button
              type="button"
              className="project-tree__batchbar-report-dismiss"
              aria-label={t("projectTree.batchReportDismiss")}
              onClick={onDismissReport}
            >
              <X size={12} aria-hidden="true" />
            </button>
          </span>
          {report.skipped > 0 && (
            <span className="project-tree__batchbar-report-line project-tree__batchbar-report-line--skipped">
              <Check size={12} aria-hidden="true" />
              {t("projectTree.batchSkippedRunning", { n: report.skipped })}
            </span>
          )}
          {report.failures.length > 0 && (
            <span className="project-tree__batchbar-report-line project-tree__batchbar-report-line--failed">
              {t("projectTree.batchFailedItems", { n: report.failures.length })}
              {report.failures.map((failure) => (
                <span
                  key={failure.topicId}
                  className="project-tree__batchbar-report-item"
                  title={failure.reason}
                >
                  {t("projectTree.batchItemLine", { title: failure.title, reason: failure.reason })}
                </span>
              ))}
            </span>
          )}
        </div>
      )}
      <ContextMenu
        open={moveMenuPoint !== null}
        point={moveMenuPoint}
        items={moveItems}
        minWidth={178}
        ariaLabel={t("projectTree.batchMove")}
        onClose={() => setMoveMenuPoint(null)}
      />
    </div>
  );
}
