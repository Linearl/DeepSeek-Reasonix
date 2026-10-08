// 任务 563 — lab pick detail dialog (xlsx 表B col 7 copy). Opens from a wall
// card click and stacks ON TOP of the intro dialog: both portal to body (the
// 529 lesson — the wall lives inside a scrolling host, so a portal mount is
// what keeps this layer unclipped), and this layer's later DOM order paints
// it above. Escape is handled in the CAPTURE phase with stopPropagation so
// it closes only the top layer, never the intro dialog beneath.

import { useEffect, useId, useRef } from "react";
import { createPortal } from "react-dom";
import type { Translator } from "../lib/i18n";
import type { LabTier } from "../lib/experimentTiers";
import type { LabPickCopy } from "../lib/forkFeaturesYaml";
import { TierBadge } from "./TierBadge";

interface LabPickDetailDialogProps {
  t: Translator;
  title: string;
  tier: LabTier;
  on: boolean;
  suggest: boolean;
  /** 表B copy (effect col 6 / detail col 7); null ⇒ lines degrade out, the
   * dialog still opens with title + tier + live state. */
  copy: LabPickCopy | null;
  /** 任务 604 — 设置位置提示（已翻译段落，如 设置 → 实验室 → 界面 → 会话图墙）。
   * null / 空 ⇒ 整行不渲染：纯展示特性（无实验室开关）没有可指的路径。 */
  settingsPath?: readonly string[] | null;
  onClose: () => void;
}

export default function LabPickDetailDialog({ t, title, tier, on, suggest, copy, settingsPath, onClose }: LabPickDetailDialogProps) {
  const titleId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    restoreRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      // Capture phase: run before the intro dialog's bubble-phase document
      // listener, and keep it from seeing this event — one Escape, one layer.
      e.stopPropagation();
      onClose();
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      if (restoreRef.current?.isConnected) restoreRef.current.focus();
    };
  }, [onClose]);

  return createPortal(
    <div className="modal-backdrop lab-pick-dialog__backdrop" role="presentation" onClick={onClose}>
      <div
        className="lab-pick-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="lab-pick-dialog__bar">
          <span id={titleId} className="lab-pick-dialog__title">{title}</span>
          <TierBadge tier={tier} translator={t} />
          <button ref={closeRef} type="button" className="btn btn--sm" onClick={onClose}>
            {t("settings.forkFeaturesIntro.close")}
          </button>
        </div>
        <div className="lab-pick-dialog__body">
          <div className="lab-pick-dialog__state" data-on={on ? "true" : "false"}>
            {t(on ? "settings.labPicks.statusOn" : "settings.labPicks.statusOff")}
          </div>
          {suggest ? <div className="lab-pick-dialog__suggest">{t("settings.labPicks.suggest")}</div> : null}
          {copy?.effect ? <p className="lab-pick-dialog__effect">{copy.effect}</p> : null}
          {copy?.detail ? <p className="lab-pick-dialog__detail">{copy.detail}</p> : null}
          {settingsPath && settingsPath.length > 0 ? (
            <div className="lab-pick-dialog__path">
              <span className="lab-pick-dialog__path-label">{t("settings.labPicks.settingsPath")}</span>
              {settingsPath.map((seg, i) => (
                <span key={seg} className="lab-pick-dialog__path-seg">
                  {i > 0 ? <span className="lab-pick-dialog__path-arrow" aria-hidden="true">→</span> : null}
                  {seg}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      </div>
    </div>,
    document.body,
  );
}
