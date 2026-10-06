import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
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
import { estimateUserMessageLines } from "../lib/messageFold";

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

/** Task 446: the queue row's text clamps to 2 rendered lines
 * (`-webkit-line-clamp: 2` on .composer-guidance-item__text) — the row-local
 * counterpart of task 436's USER_MSG_FOLD_LINE_THRESHOLD, which budgets the
 * transcript bubble instead. Hover peek fires only past this budget, so a
 * message the row already shows in full never spawns a floating card. */
export const GUIDANCE_ROW_VISIBLE_LINES = 2;
const GUIDANCE_HOVER_CARD_MAX_W = 420;
const GUIDANCE_HOVER_GAP = 8;

/** Task 446: does this row hide content worth a hover peek? Exact in a real
 * layout — the clamped box reports scrollHeight beyond clientHeight only when
 * line-clamp actually cuts it (jsdom reports no layout, i.e. both zero, so
 * fall back to task 436's CJK-aware line estimate there). */
export function guidanceRowIsTruncated(target: HTMLElement | null, text: string): boolean {
  const laidOut = target !== null && (target.scrollHeight > 0 || target.clientHeight > 0);
  if (laidOut) return target.scrollHeight > target.clientHeight;
  return estimateUserMessageLines(text) > GUIDANCE_ROW_VISIBLE_LINES;
}

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
  onToggleSelectAll,
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
  /**
   * Task 466: select-all / clear (one toggle). The shelf hands back exactly the
   * rows the row-checkbox gate admits (hidden rows included — the batch bar
   * already counts them); the composer owns the all-or-none decision.
   */
  onToggleSelectAll?: (selectable: PendingGuidance[]) => void;
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
  // Task 221#6 / 466: ONE shared gate for the row checkbox AND the select-all
  // sweep — an in-flight/delivering/unknown/paused row or the row being edited
  // can be neither selected nor moved. (Reorder additionally requires a durable
  // row: a local (unsent) row has no queue position to persist — see `movable`.)
  const rowSelectable = (item: PendingGuidance): boolean => {
    const editing = editingId === item.id;
    const inFlight = guidanceIsInFlight(item.state);
    const delivering = guidanceIsDelivering(item.state);
    const unknownState = !guidanceHasKnownPendingState(item.state);
    return !editing && !inFlight && !delivering && !unknownState && !item.paused;
  };
  // Task 466: the select-all sweep covers EVERY selectable row, including the
  // ones a collapsed queue keeps out of sight — the batch bar counts them too
  // (batchSelected maps over all items, not the visible slice).
  const selectableItems = items.filter(rowSelectable);
  // Task 466: tri-state mirror of the row checkboxes — checked only when every
  // selectable row is selected, indeterminate when some but not all are.
  const allSelected = selectableItems.length > 0 && selectableItems.every((item) => selectedSet.has(item.id));
  const someSelected = !allSelected && selectableItems.some((item) => selectedSet.has(item.id));
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

  // Task 446: hovering a clamped row peeks its body in a floating card, so a
  // long queued guidance reads without the click-to-expand round trip. Own
  // state machine (hoverIdRef mirrors previewIdRef's stale-guard pattern):
  // the card closes on mouseleave, scroll/resize, queue collapse, row removal,
  // drag start, and whenever the inline preview for the same row opens — hover
  // never writes previewId, so the click preview's expand/collapse is intact.
  const [hoverCard, setHoverCard] = useState<{ id: string; text: string; left: number; top: number | null; bottom: number | null } | null>(null);
  const hoverIdRef = useRef<string | null>(null);
  const hoverTimeoutRef = useRef<number | null>(null);
  const hoverCardRef = useRef<HTMLDivElement | null>(null);

  const clearHoverTimeout = () => {
    if (hoverTimeoutRef.current !== null) {
      window.clearTimeout(hoverTimeoutRef.current);
      hoverTimeoutRef.current = null;
    }
  };

  const closeHoverCard = (id?: string) => {
    if (id !== undefined && hoverIdRef.current !== id) return;
    clearHoverTimeout();
    hoverIdRef.current = null;
    setHoverCard(null);
  };

  const openHoverCard = async (item: PendingGuidance, target: HTMLElement) => {
    // The inline preview already shows this row in full — no floating double.
    if (previewIdRef.current === item.id) return;
    // Acceptance: a row the layout says is NOT clamped never gets a card.
    // (jsdom reports no layout, so the estimator carries that path in tests.)
    if (!guidanceRowIsTruncated(target, item.text)) return;
    clearHoverTimeout();
    hoverIdRef.current = item.id;
    const rect = target.getBoundingClientRect();
    const viewW = window.innerWidth || 0;
    const viewH = window.innerHeight || 0;
    const left = Math.max(GUIDANCE_HOVER_GAP, Math.min(rect.left, viewW - GUIDANCE_HOVER_CARD_MAX_W - GUIDANCE_HOVER_GAP));
    // Prefer above the row (the composer sits at the bottom of the window);
    // flip below when the row is near the top.
    const above = rect.top > 260;
    const fallback = item.submitText.trim() || item.text.trim();
    setHoverCard({
      id: item.id,
      text: fallback,
      left,
      top: above ? null : rect.bottom + GUIDANCE_HOVER_GAP,
      bottom: above ? viewH - rect.top + GUIDANCE_HOVER_GAP : null,
    });
    if (!onPreviewText) return;
    try {
      const full = await Promise.race([
        onPreviewText(item),
        new Promise<never>((_, reject) => {
          hoverTimeoutRef.current = window.setTimeout(() => reject(new Error("guidance hover preview timed out")), PREVIEW_TIMEOUT_MS);
        }),
      ]);
      // The mouse left (or moved to another row) while the body was in flight
      // — same stale-id rule as the click preview (task 289).
      if (hoverIdRef.current !== item.id) return;
      const text = full.trim() || fallback;
      setHoverCard((prev) => (prev && prev.id === item.id ? { ...prev, text } : prev));
    } catch {
      // The row's own text already stands in the card (task 289's fallback rule).
    } finally {
      if (hoverIdRef.current === item.id) clearHoverTimeout();
    }
  };

  // Task 289 (previewId single driver): collapsing the queue — by the head
  // button, the more/collapse footer, or Composer's auto-collapse rules — also
  // closes any open preview, so no "open but unlisted" preview survives into
  // the next expansion (the tab-switch auto-recovery was this residue).
  // Task 446: the hover card follows the same collapse rule.
  useEffect(() => {
    if (!expanded && previewIdRef.current !== null) closePreview();
    if (!expanded && hoverIdRef.current !== null) closeHoverCard();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expanded]);

  // Task 446: the card is pinned to viewport coordinates captured at hover
  // time — scrolling (the tall list scrolls internally) or resizing would
  // strand it, so it closes; a re-hover re-anchors it.
  useEffect(() => {
    if (!hoverCard) return;
    const hide = () => closeHoverCard(hoverCard.id);
    window.addEventListener("scroll", hide, true);
    window.addEventListener("resize", hide);
    return () => {
      window.removeEventListener("scroll", hide, true);
      window.removeEventListener("resize", hide);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hoverCard]);

  // Task 446: the card must not outlive its row (dismissed/sent), a drag, or
  // the inline preview taking the same row over.
  useEffect(() => {
    if (!hoverCard) return;
    if (dragId || previewId === hoverCard.id || !items.some((item) => item.id === hoverCard.id)) {
      closeHoverCard(hoverCard.id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hoverCard, items, previewId, dragId]);

  // Task 446: a pending body fetch must not outlive the shelf itself.
  useEffect(() => () => clearHoverTimeout(), []);

  // Task 446: the 436-style bottom fade only belongs on a clamped card — a
  // short body must not fade its own last line.
  useLayoutEffect(() => {
    const card = hoverCardRef.current;
    if (!card) return;
    if (card.scrollHeight > card.clientHeight) card.setAttribute("data-clipped", "");
    else card.removeAttribute("data-clipped");
  }, [hoverCard]);

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
            {/* Task 466: select-all lives in the head so it stays reachable at a
                zero selection (the batch bar only mounts once something is
                selected). Tri-state checkbox mirroring the row checks: checked
                when every selectable row is selected, indeterminate when some
                are; one click sweeps all selectable rows in, a second click
                clears (indeterminate resolves to all, the standard checkbox
                group behavior). Hidden rows are included — same set the batch
                bar counts. */}
            {selectMode && onToggleSelectAll && selectableItems.length > 0 && (
              <label className="composer-guidance-head__selectall">
                <input
                  ref={(el) => {
                    if (el) el.indeterminate = someSelected;
                  }}
                  type="checkbox"
                  aria-label={t("composer.guidanceSelectAll")}
                  checked={allSelected}
                  onChange={() => onToggleSelectAll(selectableItems)}
                />
                <span>{t("composer.guidanceSelectAll")}</span>
              </label>
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
                  className="composer-guidance-batchbar__dismiss"
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
              // Task 221#6 / 181 / 466: the shared gate (rowSelectable above) —
              // reorder additionally requires a durable row: a local (unsent)
              // row has no queue position to persist.
              const selectable = rowSelectable(item);
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
                    /* Task 446: hover peeks a clamped row in a floating card —
                       display-only, never writes previewId, so the click path
                       below keeps owning expand/collapse. */
                    onMouseEnter={(event) => {
                      void openHoverCard(item, event.currentTarget);
                    }}
                    onMouseLeave={() => closeHoverCard(item.id)}
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
      {/* Task 446: the hover peek card — portal to <body> so neither the
          shelf's own overflow (the tall list scrolls at 40vh) nor a transformed
          composer ancestor can clip or offset it; position: fixed anchors it
          to the viewport coordinates captured at hover time. */}
      {hoverCard &&
        typeof document !== "undefined" &&
        createPortal(
          <div
            ref={hoverCardRef}
            className="guidance-hover-preview"
            role="tooltip"
            data-guidance-hover={hoverCard.id}
            style={
              hoverCard.top !== null
                ? { left: hoverCard.left, top: hoverCard.top }
                : { left: hoverCard.left, bottom: hoverCard.bottom ?? GUIDANCE_HOVER_GAP }
            }
          >
            {hoverCard.text}
          </div>,
          document.body,
        )}
    </>
  );
}
