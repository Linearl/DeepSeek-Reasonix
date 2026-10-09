import { createPortal } from "react-dom";
import { useEffect, useId, useRef, useState } from "react";
import { useI18n } from "../lib/i18n";

// ForkNoticeDialog (task 670): the first-launch notice after an update (or a
// fast version switch) lands a NEW version tree. It says "this is the fork
// build", offers a jump into Settings → 实验室, and a 「下次不提醒」 opt-out
// that persists in the user config (NOT an experimental switch — a stored
// user answer).
//
// Interaction contract (task 670 spec):
//   - a 10s countdown auto-closes the dialog; the close button carries the
//     remaining seconds (same `(Ns)` shape as the task-129 confirm dialog);
//   - any click INSIDE the dialog cancels the countdown — the dialog then
//     waits for the user instead of vanishing mid-read;
//   - any click OUTSIDE (the backdrop) closes it immediately;
//   - the same version never prompts twice: App acknowledges the version as
//     soon as the dialog is raised (write-before-show, task-277 shape), so
//     every close path here is terminal for this version.

/** How long the dialog stays up before auto-closing (task 670: 10s). */
export const FORK_NOTICE_COUNTDOWN_MS = 10_000;

type ForkNoticeDialogProps = {
  open: boolean;
  /** The running version-tree name, shown in the body text. */
  version: string;
  /** Closes the dialog by any route (countdown hit zero, outside click, close button). */
  onClose: () => void;
  /** Jump into Settings → 实验室 (and close the dialog). */
  onOpenLab: () => void;
  /** 「下次不提醒」: persist the mute, then close. */
  onDismissForever: () => void;
};

export function ForkNoticeDialog({ open, version, onClose, onOpenLab, onDismissForever }: ForkNoticeDialogProps) {
  const { t } = useI18n();
  const titleId = useId();
  // Countdown active until the first click inside the dialog cancels it.
  const [countdownActive, setCountdownActive] = useState(true);
  const [remainingMs, setRemainingMs] = useState(FORK_NOTICE_COUNTDOWN_MS);
  // Latest-callback ref: the interval keeps the initial closure, so a parent
  // re-render must never restart (or leak) the running timer.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) {
      setCountdownActive(true);
      setRemainingMs(FORK_NOTICE_COUNTDOWN_MS);
      return;
    }
    if (!countdownActive) return;
    const started = Date.now();
    const timer = window.setInterval(() => {
      const left = FORK_NOTICE_COUNTDOWN_MS - (Date.now() - started);
      if (left <= 0) {
        setRemainingMs(0);
        window.clearInterval(timer);
        onCloseRef.current();
        return;
      }
      setRemainingMs(left);
    }, 200);
    return () => window.clearInterval(timer);
  }, [open, countdownActive]);

  if (!open) return null;
  const secondsLeft = Math.ceil(remainingMs / 1000);
  const closeLabel = countdownActive && remainingMs > 0
    ? `${t("forkNotice.close")} (${secondsLeft}s)`
    : t("forkNotice.close");
  return createPortal(
    <div
      className="modal-backdrop reasonix-confirm-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        // Outside click closes immediately (task 670 ④) — the countdown's
        // early exit, not a cancel-then-wait.
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        className="modal reasonix-confirm-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onMouseDown={() => {
          // Any click inside cancels the auto-close (task 670 ⑤): the user
          // is interacting, so the dialog waits for an explicit action.
          setCountdownActive(false);
        }}
      >
        <div className="modal__title reasonix-confirm-dialog__title" id={titleId}>{t("forkNotice.title")}</div>
        <div className="reasonix-confirm-dialog__message">
          {t("forkNotice.body")}
          {version ? (
            <span style={{ fontFamily: "monospace" }}> {version}</span>
          ) : null}
        </div>
        <div style={{ opacity: 0.7, fontSize: "0.85em", margin: "4px 0 10px" }}>{t("forkNotice.hint")}</div>
        <div className="modal__actions reasonix-confirm-dialog__actions">
          <button className="btn btn--small" type="button" onClick={onDismissForever}>
            {t("forkNotice.dismiss")}
          </button>
          <button className="btn btn--small" type="button" onClick={onClose}>
            {closeLabel}
          </button>
          <button className="btn btn--primary btn--small" type="button" onClick={onOpenLab}>
            {t("forkNotice.openLab")}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
