import { useState } from "react";
import { ChevronDown, ChevronUp, CornerDownRight, Pencil, Trash2 } from "lucide-react";
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
  onReview,
  onRecoveryResumed,
  onRecoveryError,
  onToggleExpanded,
  onSend,
  onDismiss,
  onEdit,
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
  onReview: () => void;
  onRecoveryResumed: () => void;
  onRecoveryError: (error: unknown) => void;
  onToggleExpanded: () => void;
  onSend: (item: PendingGuidance) => void;
  onDismiss: (item: PendingGuidance) => void;
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

  const closePreview = () => {
    setPreviewId(null);
    setPreviewText("");
    setPreviewLoading(false);
  };

  const togglePreview = async (item: PendingGuidance) => {
    if (previewId === item.id) {
      closePreview();
      return;
    }
    setPreviewId(item.id);
    setPreviewLoading(true);
    const fallback = item.submitText.trim() || item.text.trim();
    setPreviewText(fallback);
    if (!onPreviewText) {
      setPreviewLoading(false);
      return;
    }
    try {
      const full = await onPreviewText(item);
      setPreviewText(full.trim() || fallback);
    } catch {
      // The preview is a convenience; the row's own text already stands in.
    } finally {
      setPreviewLoading(false);
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
          </div>
          <div className="composer-guidance-list">
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
                <div className={`composer-guidance-item${editing ? " composer-guidance-item--editing" : ""}`} key={item.id}>
                  <CornerDownRight size={14} className="composer-guidance-item__icon" />
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
