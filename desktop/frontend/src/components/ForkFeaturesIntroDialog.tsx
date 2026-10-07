// Task 379 — the fork-features intro as a LARGE modal (the 0929 direction:
// the narrow side-column form is gone; the entry button opens this dialog and
// the copy shows as a card wall). Structural only — the pure-display panel
// (ForkFeaturesIntro, task 282 contract) is rendered unchanged inside; every
// control (backdrop click, close button, Escape) lives HERE in the dialog
// layer, so the 282 "no form controls in the component" check stays green.

import { useEffect, useId, useRef } from "react";
import { createPortal } from "react-dom";
import ForkFeaturesIntro from "./ForkFeaturesIntro";
import LabPicksWall from "./LabPicksWall";
import type { Translator } from "../lib/i18n";

interface ForkFeaturesIntroDialogProps {
  t: Translator;
  onClose: () => void;
}

export default function ForkFeaturesIntroDialog({ t, onClose }: ForkFeaturesIntroDialogProps) {
  const titleId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    restoreRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      if (restoreRef.current?.isConnected) restoreRef.current.focus();
    };
  }, [onClose]);

  return createPortal(
    <div className="modal-backdrop fork-features-dialog__backdrop" role="presentation" onClick={onClose}>
      <div
        className="fork-features-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="fork-features-dialog__bar">
          <span id={titleId} className="fork-features-dialog__title">{t("settings.forkFeaturesIntro.title")}</span>
          <button ref={closeRef} type="button" className="btn btn--sm" onClick={onClose}>
            {t("settings.forkFeaturesIntro.close")}
          </button>
        </div>
        <div className="fork-features-dialog__body">
          <ForkFeaturesIntro t={t} />
          {/* 任务 562: the lab picks wall — 16 curated cards with tier badges,
              sourced from lib/experimentTiers exactly like the settings lab
              tab (two views, one source). 任务 563 grows the cards. */}
          <LabPicksWall t={t} />
        </div>
      </div>
    </div>,
    document.body,
  );
}
