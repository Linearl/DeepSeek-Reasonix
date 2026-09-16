import { useMemo, useState } from "react";
import { ChevronDown, ChevronUp, Eye, RotateCcw } from "lucide-react";
import { useT } from "../lib/i18n";
import {
  buildTurnEditList,
  turnEditFileLabel,
  turnEditKindLabel,
  turnEditListHeader,
  TURN_EDIT_LIST_INITIAL,
} from "../lib/turnEditList";
import type { TurnChanges } from "../lib/turnResultTypes";

export interface TurnEditListProps {
  diff: TurnChanges | undefined;
  /** Zero-based checkpoint turn used by code rewind. */
  turn?: number;
  onReview?: () => void;
  /** Triggers code rewind for `turn`. Omit to hide the undo action. */
  onUndoCode?: (turn: number) => void;
  undoDisabled?: boolean;
}

/**
 * Task 113: compact per-turn edit list — aggregate header, collapsible file
 * rows, undo (code rewind) and review entry points.
 */
export function TurnEditList({ diff, turn, onReview, onUndoCode, undoDisabled }: TurnEditListProps) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const [confirmUndo, setConfirmUndo] = useState(false);
  const model = useMemo(
    () => buildTurnEditList(diff, expanded ? 0 : TURN_EDIT_LIST_INITIAL),
    [diff, expanded],
  );
  if (!model) return null;

  return (
    <section className="turn-edit-list" aria-label={t("turnEdit.header", { count: model.totalFiles })}>
      <header className="turn-edit-list__head">
        <span className="turn-edit-list__summary">{turnEditListHeader(model, t)}</span>
        <div className="turn-edit-list__actions">
          {onReview && (
            <button type="button" className="btn btn--small" onClick={onReview}>
              <Eye size={13} aria-hidden="true" />
              <span>{t("turnEdit.review")}</span>
            </button>
          )}
          {onUndoCode && turn !== undefined && (
            <button
              type="button"
              className={`btn btn--small${confirmUndo ? " btn--danger" : ""}`}
              disabled={undoDisabled}
              onClick={() => {
                if (confirmUndo) {
                  onUndoCode(turn);
                  setConfirmUndo(false);
                } else {
                  setConfirmUndo(true);
                }
              }}
              onBlur={() => setConfirmUndo(false)}
            >
              <RotateCcw size={13} aria-hidden="true" />
              <span>{confirmUndo ? t("turnEdit.undoConfirm") : t("turnEdit.undo")}</span>
            </button>
          )}
        </div>
      </header>
      <ul className="turn-edit-list__files">
        {(expanded ? model.files : model.visible).map((file) => (
          <li key={file.path} className="turn-edit-list__file">
            <span className="turn-edit-list__kind" title={turnEditKindLabel(file.kind, t)}>
              {turnEditKindLabel(file.kind, t)}
            </span>
            <span className="turn-edit-list__path" title={file.path}>
              {file.path}
            </span>
            <span className="turn-edit-list__stat">
              {file.binary || file.modeOnly || file.uncounted ? (
                turnEditFileLabel(file, t)
              ) : (
                <>
                  <span className="turn-result-added">+{file.added}</span>{" "}
                  <span className="turn-result-removed">−{file.removed}</span>
                </>
              )}
            </span>
          </li>
        ))}
      </ul>
      {model.totalFiles > TURN_EDIT_LIST_INITIAL && (
        <button
          type="button"
          className="turn-edit-list__more"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
        >
          {expanded ? (
            <>
              <ChevronUp size={13} aria-hidden="true" />
              <span>{t("turnEdit.showLess")}</span>
            </>
          ) : (
            <>
              <ChevronDown size={13} aria-hidden="true" />
              <span>{t("turnEdit.showMore", { count: model.hiddenCount })}</span>
            </>
          )}
        </button>
      )}
      {onUndoCode && (turn === undefined || undoDisabled) && (
        <p className="turn-edit-list__hint">{t("rewind.disabledNoCode")}</p>
      )}
    </section>
  );
}
