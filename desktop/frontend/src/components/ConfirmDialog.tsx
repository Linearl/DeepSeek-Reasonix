import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

export type ConfirmDialogRequest = {
  title: string;
  message: ReactNode;
  confirmLabel: string;
  cancelLabel: string;
  tone?: "default" | "danger";
  // wide lets a content-heavy dialog (chain previews, tables) use a much larger
  // max-width than the default modal. The dialog reads the class, the CSS owns
  // the actual size.
  wide?: boolean;
  /** Task 129: keep confirm disabled for this many ms to prevent mis-clicks. */
  confirmDelayMs?: number;
};

type PendingConfirmation = ConfirmDialogRequest & {
  resolve: (confirmed: boolean) => void;
};

function ConfirmDialog({ request, onResolve }: { request: ConfirmDialogRequest; onResolve: (confirmed: boolean) => void }) {
  const titleId = useId();
  const messageId = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const restoreFocusRef = useRef<HTMLElement | null>(null);
  const delayMs = Math.max(0, request.confirmDelayMs ?? 0);
  const [remainingMs, setRemainingMs] = useState(delayMs);

  useLayoutEffect(() => {
    restoreFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    cancelRef.current?.focus();
    return () => {
      if (restoreFocusRef.current?.isConnected) restoreFocusRef.current.focus();
    };
  }, []);

  useEffect(() => {
    if (delayMs <= 0) return;
    setRemainingMs(delayMs);
    const started = Date.now();
    const timer = window.setInterval(() => {
      const left = delayMs - (Date.now() - started);
      if (left <= 0) {
        setRemainingMs(0);
        window.clearInterval(timer);
        return;
      }
      setRemainingMs(left);
    }, 200);
    return () => window.clearInterval(timer);
  }, [delayMs]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        onResolve(false);
        return;
      }
      if (event.key !== "Tab") return;
      const first = cancelRef.current;
      const last = confirmRef.current;
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown, { capture: true });
    return () => document.removeEventListener("keydown", onKeyDown, { capture: true });
  }, [onResolve]);

  const confirmDisabled = remainingMs > 0;
  const secondsLeft = Math.ceil(remainingMs / 1000);
  const confirmText = confirmDisabled
    ? `${request.confirmLabel} (${secondsLeft}s)`
    : request.confirmLabel;

  return createPortal(
    <div
      className="modal-backdrop reasonix-confirm-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onResolve(false);
      }}
    >
      <div
        className={`modal reasonix-confirm-dialog${request.wide ? " reasonix-confirm-dialog--wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={messageId}
      >
        <div className="modal__title reasonix-confirm-dialog__title" id={titleId}>{request.title}</div>
        <div className="reasonix-confirm-dialog__message" id={messageId}>{request.message}</div>
        <div className="modal__actions reasonix-confirm-dialog__actions">
          <button ref={cancelRef} className="btn btn--small" type="button" onClick={() => onResolve(false)}>
            {request.cancelLabel}
          </button>
          <button
            ref={confirmRef}
            className={`btn btn--small ${request.tone === "danger" ? "btn--danger" : "btn--primary"}`}
            type="button"
            disabled={confirmDisabled}
            onClick={() => {
              if (confirmDisabled) return;
              onResolve(true);
            }}
          >
            {confirmText}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

export function useConfirmDialog(): {
  confirm: (request: ConfirmDialogRequest) => Promise<boolean>;
  dialog: ReactNode;
  dismiss: () => void;
} {
  const [pending, setPending] = useState<PendingConfirmation | null>(null);
  const pendingRef = useRef<PendingConfirmation | null>(null);

  const resolvePending = useCallback((confirmed: boolean) => {
    const current = pendingRef.current;
    if (!current) return;
    pendingRef.current = null;
    setPending(null);
    current.resolve(confirmed);
  }, []);

  const confirm = useCallback((request: ConfirmDialogRequest) => new Promise<boolean>((resolve) => {
    pendingRef.current?.resolve(false);
    const next = { ...request, resolve };
    pendingRef.current = next;
    setPending(next);
  }), []);

  useEffect(() => () => {
    pendingRef.current?.resolve(false);
    pendingRef.current = null;
  }, []);

  const dismiss = useCallback(() => resolvePending(false), [resolvePending]);
  return {
    confirm, dismiss,
    dialog: pending ? <ConfirmDialog request={pending} onResolve={resolvePending} /> : null,
  };
}
