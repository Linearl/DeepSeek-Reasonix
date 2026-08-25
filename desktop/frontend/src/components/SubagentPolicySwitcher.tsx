import { useCallback, useEffect, useRef, useState } from "react";
import { Check, ChevronsUpDown, Network } from "lucide-react";
import type { SubagentPolicy } from "../lib/types";
import { normalizeSubagentPolicy } from "../lib/types";
import { ANCHORED_POPOVER_CLOSE_MS, AnchoredPopover } from "./AnchoredPopover";
import { useI18n } from "../lib/i18n";

// SubagentPolicySwitcher is a compact dropdown for the per-session sub-agent
// delegation tier (light|balanced|aggressive), styled like EffortSwitcher and
// the composer task-mode menu so it sits on the same composer toolbar row.
export function SubagentPolicySwitcher({
  policy,
  disabled,
  onPick,
}: {
  policy?: string;
  disabled: boolean;
  onPick: (policy: SubagentPolicy) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [closing, setClosing] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const closeTimerRef = useRef<number | null>(null);
  const current = normalizeSubagentPolicy(policy);

  const clearCloseTimer = useCallback(() => {
    if (closeTimerRef.current === null) return;
    window.clearTimeout(closeTimerRef.current);
    closeTimerRef.current = null;
  }, []);

  const openMenu = useCallback(() => {
    clearCloseTimer();
    setClosing(false);
    setOpen(true);
  }, [clearCloseTimer]);

  const closeMenu = useCallback((afterClose?: () => void) => {
    clearCloseTimer();
    setClosing(true);
    window.requestAnimationFrame(() => setOpen(false));
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    closeTimerRef.current = window.setTimeout(() => {
      closeTimerRef.current = null;
      setClosing(false);
      afterClose?.();
    }, reduceMotion ? 0 : ANCHORED_POPOVER_CLOSE_MS);
  }, [clearCloseTimer]);

  useEffect(() => () => clearCloseTimer(), [clearCloseTimer]);

  const pick = (level: SubagentPolicy) => {
    closeMenu(() => {
      if (level !== current) onPick(level);
    });
  };

  const policies: SubagentPolicy[] = ["light", "balanced", "aggressive"];

  return (
    <div className="modelsw subagentpolicysw">
      <button
        ref={triggerRef}
        type="button"
        className={`modelsw__trigger subagentpolicysw__trigger ${current !== "light" ? "subagentpolicysw__trigger--explicit" : ""}`}
        disabled={disabled}
        aria-expanded={open && !closing}
        aria-label={t("composer.subagentPolicyTrigger")}
        title={t("composer.subagentPolicyTrigger")}
        onClick={() => (open || closing ? closeMenu() : openMenu())}
      >
        <Network size={14} className="modelsw__kind" />
        <span className="modelsw__label">{t(`composer.subagentPolicy_${current}`)}</span>
        <ChevronsUpDown size={11} />
      </button>
      <AnchoredPopover
        open={open && !disabled}
        closing={closing}
        anchorRef={triggerRef}
        onClose={() => closeMenu()}
        className="modelsw__menu modelsw__menu--portal subagentpolicysw__menu"
        align="end"
      >
        <div role="listbox">
          {policies.map((level) => (
            <button
              key={level}
              type="button"
              role="option"
              aria-selected={level === current}
              className={`modelsw__item ${level === current ? "modelsw__item--current" : ""}`}
              onClick={() => pick(level)}
            >
              <span className="modelsw__model">{t(`composer.subagentPolicy_${level}`)}</span>
              {level === current && <Check size={13} className="modelsw__check" />}
            </button>
          ))}
        </div>
      </AnchoredPopover>
    </div>
  );
}
