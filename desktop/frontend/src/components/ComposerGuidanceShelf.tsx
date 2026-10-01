import { useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronUp, Combine, CornerDownRight, GripVertical, Pencil, Send, Trash2 } from "lucide-react";
import {
  guidanceEditableInComposer,
  guidanceHasKnownPendingState,
  guidanceIsDelivering,
  guidanceIsInFlight,
  guidanceNeedsRetry,
} from "../lib/composerGuidance";
import { useI18n } from "../lib/i18n";
import type { StructuredInvocationSubmit } from "../lib/invocationDisplay";
import { InboxRecoveryBanner } from "./InboxRecoveryBanner";
import { Tooltip } from "./Tooltip";

export type PendingGuidance = {
  id: string;
  text: string;
  submitText: string;
  state?: string;
  intent?: string;
  source?: string;
  paused?: boolean;
  recoveredCount?: number;
  structured?: StructuredInvocationSubmit;
};

export type InboxRecoveryNotice = {
  draftKey: string;
  tabId: string;
  count: number;
  recovered: boolean;
};

/** Task 289: a hung preview fetch must not keep the row in its loading state
 * forever — after this budget the fetch loses the race and the row falls back
 * to its own text (already in place), fully interactive and collapsible. */
export const PREVIEW_TIMEOUT_MS = 8000;

export function ComposerGuidanceShelf({
  recovery,
  recoveryDisabled,
  items,
  expanded,
  running,
  disabled,
  readOnly,
  sendingId,
  editingId,
  unreadMailCount,
  selectMode,
  selectedIds,
  onToggleSelectMode,
  onToggleSelect,
  onBatchSend,
  onBatchDismiss,
  onMove,
  onReview,
  onRecoveryResumed,
  onRecoveryError,
  onToggleExpanded,
  onSend,
  onDismiss,
  onEdit,
  onMergeNext,
  mergeMode,
  onPreviewText,
}: {
  recovery: InboxRecoveryNotice | null;
  recoveryDisabled: boolean;
  items: PendingGuidance[];
  expanded: boolean;
  running: boolean;
  disabled: boolean;
  readOnly: boolean;
  sendingId: string | null;
  /** Task 181: the entry currently loaded into the main composer, if any. */
  editingId?: string | null;
  /** Task 221#6: cross-session mailbox unread count (badge semantics per the 批五
   * survey decision — the mailbox layer, not the guidance queue). */
  unreadMailCount?: number;
  /** Task 221#6: multi-select batch mode (controlled by the composer). */
  selectMode?: boolean;
  selectedIds?: string[];
  onToggleSelectMode?: () => void;
  onToggleSelect?: (item: PendingGuidance) => void;
  onBatchSend?: (items: PendingGuidance[]) => void;
  onBatchDismiss?: (items: PendingGuidance[]) => void;
  /** Task 181 reorder: move this durable entry to an absolute 0-based index. */
  onMove?: (item: PendingGuidance, toIndex: number) => void;
  onReview: () => void;
  onRecoveryResumed: () => void;
  onRecoveryError: (error: unknown) => void;
  onToggleExpanded: () => void;
  onSend: (item: PendingGuidance) => void;
  onDismiss: (item: PendingGuidance) => void;
  /**
   * Task 153: merge this entry with the one right after it (manual, one pair
   * per click). Undefined when the experimental switch is off — the button
   * then never renders and the shelf behaves exactly as before.
   */
  onMergeNext?: (item: PendingGuidance) => void;
  /** Task 366: the drain-merge tier (task 221, off | same_sender | all). Under
   * "all" the dispatcher merges queued rows at admit time, so the manual
   * merge-next button can never fire — the row then shows a disabled control
   * with a cross-hint instead of silently hiding it. */
  mergeMode?: string;
  /**
   * Task 181: the pencil no longer edits in place — a queued guidance body is
   * multi-line (cross-session replies carry a header plus prose), and a one-line
   * input can only ever show a truncated preview of it. The callback hands the
   * entry to the composer, which owns a textarea and the stash for the draft that
   * was already typed there.
   */
  onEdit?: (item: PendingGuidance) => void;
  /** Full body for the read-only preview; falls back to the row's own text. */
  onPreviewText?: (item: PendingGuidance) => Promise<string>;
}) {
  const { t } = useI18n();
  const visible = expanded ? items : items.slice(0, 2);
  const hiddenCount = Math.max(0, items.length - 2);
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [previewText, setPreviewText] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  // Task 289: previewId is the single source of truth for everything preview —
  // a ref mirrors it so async callbacks (which close over a stale state) can
  // check whether their preview is still the open one before touching state.
  // Task 366 S2: last logged hidden-reason signature, so the debug line fires
  // on change only instead of on every render.
  const mergeNextLogRef = useRef("");
  const previewIdRef = useRef<string | null>(null);
  const previewTimeoutRef = useRef<number | null>(null);
  const setPreviewingId = (id: string | null) => {
    previewIdRef.current = id;
    setPreviewId(id);
  };
  // Task 221#6 / 181: batch selection (ids managed by the composer) and the
  // drag source for reorder. A row is batch-selectable when at least one batch
  // action can still land on it — never an in-flight/delivering/unknown row,
  // never the row being edited into the composer.
  const selectedSet = new Set(selectedIds ?? []);
  // Task 441: dragId is set ONLY by a handle dragstart (task 181 had the whole
  // card draggable plus up/down arrows; both are gone — the six-dot handle is
  // the single reorder affordance now).
  const [dragId, setDragId] = useState<string | null>(null);
  const batchSelected = (selectedIds ?? [])
    .map((id) => items.find((item) => item.id === id))
    .filter((item): item is PendingGuidance => Boolean(item));
  // Same gate as the single-send button: a structured entry cannot ride an
  // active turn, so the batch must skip it instead of counting a silent no-op.
  const batchSendable = batchSelected.filter(
    (item) => !(running && !guidanceNeedsRetry(item.state) && Boolean(item.structured)),
  );

  const clearPreviewTimeout = () => {
    if (previewTimeoutRef.current !== null) {
      window.clearTimeout(previewTimeoutRef.current);
      previewTimeoutRef.current = null;
    }
  };

  const closePreview = () => {
    clearPreviewTimeout();
    setPreviewingId(null);
    setPreviewText("");
    setPreviewLoading(false);
  };

  // Task 289 (previewId single driver): collapsing the queue — by the head
  // button, the more/collapse footer, or Composer's auto-collapse rules — also
  // closes any open preview, so no "open but unlisted" preview survives into
  // the next expansion (the tab-switch auto-recovery was this residue).
  useEffect(() => {
    if (!expanded && previewIdRef.current !== null) closePreview();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expanded]);

  const togglePreview = async (item: PendingGuidance) => {
    if (previewIdRef.current === item.id) {
      closePreview();
      return;
    }
    clearPreviewTimeout();
    setPreviewingId(item.id);
    setPreviewLoading(true);
    const fallback = item.submitText.trim() || item.text.trim();
    setPreviewText(fallback);
    if (!onPreviewText) {
      setPreviewLoading(false);
      return;
    }
    try {
      const full = await Promise.race([
        onPreviewText(item),
        new Promise<never>((_, reject) => {
          previewTimeoutRef.current = window.setTimeout(() => reject(new Error("guidance preview timed out")), PREVIEW_TIMEOUT_MS);
        }),
      ]);
      // The fetch lost to a close/switch: previewId has moved on, so this
      // result must not repaint the newer preview (task 289, driver #2).
      if (previewIdRef.current !== item.id) return;
      setPreviewText(full.trim() || fallback);
    } catch {
      // The preview is a convenience; the row's own text already stands in.
    } finally {
      clearPreviewTimeout();
      if (previewIdRef.current === item.id) setPreviewLoading(false);
    }
  };

  return (
    <>
      {recovery && (
        <InboxRecoveryBanner
          key={`${recovery.draftKey}:${recovery.tabId}`}
          count={recovery.count}
          recovered={recovery.recovered}
          disabled={recoveryDisabled}
          tabId={recovery.tabId}
          onReview={onReview}
          onResumed={onRecoveryResumed}
          onError={onRecoveryError}
        />
      )}
      {items.length > 0 && (
        <div className="composer-guidance-shelf" aria-label={t("composer.guidanceQueue")}>
          <div className="composer-guidance-head">
            <span className="composer-guidance-head__label">
              <CornerDownRight size={14} />
              <span>{t("composer.guidanceCount", { n: items.length })}</span>
            </span>
            {(unreadMailCount ?? 0) > 0 && (
              <Tooltip label={t("composer.mailUnreadHint")}>
                <span className="composer-guidance-head__unread" aria-label={t("composer.mailUnread", { n: unreadMailCount ?? 0 })}>
                  {t("composer.mailUnread", { n: unreadMailCount ?? 0 })}
                </span>
              </Tooltip>
            )}
            {/* Task 289: an explicit collapse entry in the head while the queue
                is expanded — the footer chevron alone hid too far down a long
                list, and the user asked for a visible "collapse" control. */}
            {expanded && (
              <button
                className="composer-guidance-head__collapse"
                type="button"
                aria-expanded={expanded}
                aria-label={t("composer.guidanceCollapse")}
                onClick={onToggleExpanded}
              >
                <ChevronUp size={13} />
              </button>
            )}
            {!readOnly && !disabled && items.length > 1 && onToggleSelectMode && (
              <button
                className="composer-guidance-head__select-toggle"
                type="button"
                aria-pressed={Boolean(selectMode)}
                onClick={onToggleSelectMode}
              >
                {selectMode ? t("composer.guidanceSelectCancel") : t("composer.guidanceSelect")}
              </button>
            )}
          </div>
          {selectMode && batchSelected.length > 0 && (
            <div className="composer-guidance-batchbar" role="toolbar" aria-label={t("composer.guidanceBatchBar")}>
              <span className="composer-guidance-batchbar__count">{t("composer.guidanceBatchSelected", { n: batchSelected.length })}</span>
              {onBatchSend && (
                <button
                  className="composer-guidance-item__guide"
                  type="button"
                  aria-label={t("composer.guidanceBatchSend", { n: batchSendable.length })}
                  disabled={batchSendable.length === 0 || sendingId !== null}
                  onClick={() => onBatchSend(batchSendable)}
                >
                  <Send size={13} />
                  <span>{t("composer.guidanceBatchSend", { n: batchSendable.length })}</span>
                </button>
              )}
              {onBatchDismiss && (
                <button
                  className="composer-guidance-item__action"
                  type="button"
                  aria-label={t("composer.guidanceBatchDismiss", { n: batchSelected.length })}
                  disabled={sendingId !== null}
                  onClick={() => onBatchDismiss(batchSelected)}
                >
                  <Trash2 size={13} />
                  <span>{t("composer.guidanceBatchDismiss", { n: batchSelected.length })}</span>
                </button>
              )}
            </div>
          )}
          {/* Task 289: a long expanded queue stops stretching the composer —
              beyond five entries the list caps at 40vh and scrolls inside
              itself (the 21-entry screenshot state), while the head collapse
              button remains one click away. */}
          <div className={`composer-guidance-list${expanded && items.length > 5 ? " composer-guidance-list--tall" : ""}`}>
            {visible.map((item, index) => {
              const inFlight = guidanceIsInFlight(item.state);
              // Task 159: `running` / `steer_consumed` have left the cancellable queue, so
              // the row keeps its actions disabled — through this explicit branch with a
              // stated reason, not the "unknown state" fallback that never explains itself.
              const delivering = guidanceIsDelivering(item.state);
              const unknownState = !guidanceHasKnownPendingState(item.state);
              const needsRetry = guidanceNeedsRetry(item.state);
              const waitingForEarlier = !running && !inFlight && index > 0;
              const editing = editingId === item.id;
              const canEdit = Boolean(onEdit) && !readOnly && !disabled && guidanceEditableInComposer(item) && !waitingForEarlier && sendingId === null && !editing;
              const previewing = previewId === item.id;
              // Task 221#6 / 181: one gate for both batch selection and reorder —
              // an in-flight/delivering/unknown/paused row or the row being edited
              // can be neither selected nor moved. Reorder additionally requires a
              // durable row: a local (unsent) row has no queue position to persist.
              const selectable = !editing && !inFlight && !delivering && !unknownState && !item.paused;
              const movable = Boolean(onMove) && !item.id.startsWith("local-") && selectable;
              const actionLabel = inFlight
                ? t("composer.guidanceInFlight")
                : delivering
                  ? t("composer.guidanceDelivering")
                  : waitingForEarlier
                    ? t("composer.guidanceWaiting")
                    : needsRetry
                      ? t("composer.guidanceRetry")
                      : t("composer.guidanceSend");
              return (
                <div
                  className={`composer-guidance-item${editing ? " composer-guidance-item--editing" : ""}${dragId === item.id ? " composer-guidance-item--dragging" : ""}`}
                  key={item.id}
                  // Task 289 (root cause: the card body had no click handler, so
                  // anything outside the tiny text button felt stuck open). The
                  // whole card toggles the preview; row controls are protected
                  // by the closest() guard instead of stopPropagation on every
                  // button, so their own handlers fire exactly once and the
                  // 266-A source pins keep their literal onClick shapes.
                  onClick={(event) => {
                    if ((event.target as HTMLElement).closest("button, input")) return;
                    void togglePreview(item);
                  }}
                  onDragOver={movable && dragId !== null && dragId !== item.id ? (event) => event.preventDefault() : undefined}
                  onDrop={movable && dragId !== null && dragId !== item.id
                    ? (event) => {
                        event.preventDefault();
                        const from = items.findIndex((queued) => queued.id === dragId);
                        setDragId(null);
                        if (from >= 0 && onMove) onMove(items[from], index);
                      }
                    : undefined}
                >
                  {/* Task 441: the six-dot handle (⠿) is the drag source — the card
                      itself is no longer draggable, so text selection and the
                      card-body preview toggle stay clean. Dropping the drag on
                      another row still lands through the card's onDrop above. */}
                  {movable && !selectMode ? (
                    <Tooltip label={t("composer.guidanceDragHint")}>
                      <button
                        type="button"
                        className={`composer-guidance-item__handle${dragId === item.id ? " composer-guidance-item__handle--dragging" : ""}`}
                        aria-label={t("composer.guidanceDragHint")}
                        draggable
                        onDragStart={(event) => {
                          event.dataTransfer?.setData?.("text/plain", item.id);
                          setDragId(item.id);
                        }}
                        onDragEnd={() => setDragId(null)}
                      >
                        <GripVertical size={13} />
                      </button>
                    </Tooltip>
                  ) : (
                    <CornerDownRight size={14} className="composer-guidance-item__icon" />
                  )}
                  {selectMode && selectable && onToggleSelect && (
                    <input
                      className="composer-guidance-item__check"
                      type="checkbox"
                      aria-label={t("composer.guidanceSelectOne")}
                      checked={selectedSet.has(item.id)}
                      onChange={() => onToggleSelect(item)}
                    />
                  )}
                  <button
                    className="composer-guidance-item__text composer-guidance-item__text--button"
                    type="button"
                    aria-expanded={previewing}
                    aria-label={previewing ? t("composer.guidancePreviewClose") : t("composer.guidancePreviewOpen")}
                    onClick={() => void togglePreview(item)}
                  >
                    {item.text.trim() || t("composer.guidanceEmptyPreview")}
                  </button>
                  {canEdit && (
                    <Tooltip label={t("composer.guidanceEdit")}>
                      <button
                        className="composer-guidance-item__action"
                        type="button"
                        aria-label={t("composer.guidanceEdit")}
                        onClick={() => onEdit?.(item)}
                      >
                        <Pencil size={14} />
                      </button>
                    </Tooltip>
                  )}
                  {editing && (
                    <span className="composer-guidance-item__badge">{t("composer.guidanceEditingBadge")}</span>
                  )}
                  {/*
                   * Task 153: manual "merge next". Only when the experimental switch
                   * is on (onMergeNext provided), a following entry exists, and this
                   * row itself is still an actionable queue member; an in-flight or
                   * delivering row can never merge, and the editing row hides the
                   * button entirely (edit/merge are exclusive).
                   */}
                  {(() => {
                    // Task 153 original gate, kept byte-for-byte (task 366: zero
                    // relaxation — the conditions below decide the ACTIVE button).
                    const mergeNextActive = onMergeNext && index < items.length - 1 && !editing && !inFlight && !delivering && !unknownState && !needsRetry && !item.paused;
                    if (onMergeNext && !mergeNextActive) {
                      // Task 366 S2: reason-tagged debug line so the next "why is
                      // the button missing" investigation is one console glance
                      // instead of a full survey. Deduplicated per row+reasons.
                      const reasons: string[] = [];
                      if (!(index < items.length - 1)) reasons.push("length");
                      if (editing) reasons.push("editing");
                      if (inFlight) reasons.push("inFlight");
                      if (delivering) reasons.push("delivering");
                      if (unknownState) reasons.push("unknownState");
                      if (needsRetry) reasons.push("needsRetry");
                      if (item.paused) reasons.push("paused");
                      const dedupeKey = `${item.id}:${reasons.join(",")}`;
                      if (mergeNextLogRef.current !== dedupeKey) {
                        mergeNextLogRef.current = dedupeKey;
                        console.debug(`[guidance-merge-next] hidden id=${item.id} reasons=${reasons.join(",") || "none"} (task 366 S2)`);
                      }
                    }
                    // Task 366 S1: under the merge-all tier (task 221) the dispatcher
                    // merges queued rows at admit time, so this button can never fire
                    // — show a disabled control with a cross-hint instead of hiding
                    // the capability entirely. The original gate above is untouched.
                    if (onMergeNext && mergeMode === "all" && !mergeNextActive) {
                      return (
                        <Tooltip label={t("composer.guidanceMergeNextUnavailableAll")}>
                          <button
                            className="composer-guidance-item__action"
                            type="button"
                            aria-label={t("composer.guidanceMergeNextUnavailableAll")}
                            disabled
                          >
                            <Combine size={14} />
                          </button>
                        </Tooltip>
                      );
                    }
                    if (mergeNextActive) {
                      return (
                        <Tooltip label={t("composer.guidanceMergeNext")}>
                          <button
                            className="composer-guidance-item__action"
                            type="button"
                            aria-label={t("composer.guidanceMergeNext")}
                            disabled={disabled || readOnly || sendingId !== null || waitingForEarlier}
                            onClick={() => onMergeNext(item)}
                          >
                            <Combine size={14} />
                          </button>
                        </Tooltip>
                      );
                    }
                    return null;
                  })()}
                  <Tooltip label={actionLabel}>
                    <button
                      className="composer-guidance-item__guide"
                      type="button"
                      aria-label={actionLabel}
                      disabled={inFlight || delivering || unknownState || waitingForEarlier || disabled || readOnly || sendingId !== null || (running && !needsRetry && Boolean(item.structured)) || Boolean(item.paused)}
                      onClick={() => onSend(item)}
                    >
                      <CornerDownRight size={13} />
                      <span>{t(needsRetry ? "composer.guidanceRetryMode" : running ? "composer.guidanceMode" : "composer.guidanceSendMode")}</span>
                    </button>
                  </Tooltip>
                  <Tooltip label={inFlight || delivering ? actionLabel : t("composer.guidanceDismiss")}>
                    <button
                      className="composer-guidance-item__action"
                      type="button"
                      aria-label={inFlight || delivering ? actionLabel : t("composer.guidanceDismiss")}
                      disabled={disabled || readOnly || inFlight || delivering || unknownState || sendingId === item.id || editing}
                      onClick={() => onDismiss(item)}
                    >
                      <Trash2 size={14} />
                    </button>
                  </Tooltip>
                  {/* Task 441: the task 266-A up/down reorder arrows are gone —
                      the six-dot handle drag replaces them (dispatch 20261002-0010:
                      remove the 「↑ 立即」button plan; edit/delete stay). The
                      preview block below still follows the control row, so the
                      one-grid-line layout 266-A pinned is unchanged. */}
                  {previewing && (
                    <div className="composer-guidance-item__preview" role="note" aria-busy={previewLoading}>
                      {previewText}
                    </div>
                  )}
                </div>
              );
            })}
            {items.length > 2 && (
              <button
                className="composer-guidance-more"
                type="button"
                aria-expanded={expanded}
                onClick={onToggleExpanded}
              >
                {expanded ? <ChevronUp size={13} /> : <ChevronDown size={13} />}
                <span>{expanded ? t("composer.guidanceCollapse") : t("composer.guidanceRemaining", { n: hiddenCount })}</span>
              </button>
            )}
          </div>
        </div>
      )}
    </>
  );
}
