import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { CSSProperties, KeyboardEvent as ReactKeyboardEvent, ReactNode } from "react";
import { createPortal } from "react-dom";
import { Check, Copy } from "lucide-react";
import { t } from "../lib/i18n";
import { writeClipboardText } from "../lib/clipboard";

type TooltipSide = "top" | "bottom" | "left" | "right";

const GAP = 8;
const EDGE_PAD = 8;
const ARROW_SIZE = 7;
const ARROW_PAD = 12;
// 任务1033: an interactive (wrap-mode) tooltip must survive the pointer hop
// from the trigger across the GAP to its own copy button, so mouse-leave on
// the trigger only *schedules* the hide for this long.
const INTERACTIVE_HIDE_MS = 200;
const COPY_FEEDBACK_MS = 1500;

// wrapCommandLines is the 任务1033 wrap model: greedy word wrap at maxWidthCh
// characters (default 72 — the 60-80 band the panel hover cards asked for),
// hard-cutting only when a single token exceeds the budget. Done in JS rather
// than by CSS soft-wrap so the line count is deterministic: the 30-line cap
// and the "共 N 行" notice must agree with what is actually shown, and the
// harness must be able to verify both without a layout engine.
export function wrapCommandLines(text: string, maxWidthCh: number): string[] {
  const out: string[] = [];
  for (const paragraph of text.split("\n")) {
    if (paragraph.length <= maxWidthCh) {
      out.push(paragraph);
      continue;
    }
    let rest = paragraph;
    while (rest.length > maxWidthCh) {
      let cut = rest.lastIndexOf(" ", maxWidthCh);
      if (cut <= 0) cut = maxWidthCh;
      out.push(rest.slice(0, cut));
      rest = rest.slice(cut).replace(/^ /, "");
    }
    if (rest) out.push(rest);
  }
  return out;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function oppositeSide(side: TooltipSide): TooltipSide {
  if (side === "top") return "bottom";
  if (side === "bottom") return "top";
  if (side === "left") return "right";
  return "left";
}

function samePosition(
  current: { left: number; top: number; side: TooltipSide; arrowX: number; arrowY: number },
  next: { left: number; top: number; side: TooltipSide; arrowX: number; arrowY: number },
): boolean {
  return (
    current.side === next.side &&
    Math.abs(current.left - next.left) < 0.5 &&
    Math.abs(current.top - next.top) < 0.5 &&
    Math.abs(current.arrowX - next.arrowX) < 0.5 &&
    Math.abs(current.arrowY - next.arrowY) < 0.5
  );
}

export function Tooltip({
  label,
  children,
  side = "top",
  fill = false,
  block = false,
  disabled = false,
  className,
  delay = 180,
  wrap = false,
  maxLines = 30,
  maxWidthCh = 72,
}: {
  label?: ReactNode;
  children: ReactNode;
  side?: TooltipSide;
  fill?: boolean;
  block?: boolean;
  disabled?: boolean;
  className?: string;
  delay?: number;
  /** 任务1033: wrap-mode hover card for long text (command lines etc.) —
   *  JS word-wrap at maxWidthCh characters, capped at maxLines lines with a
   *  truncation notice + full-text copy button. The default (false) keeps
   *  every pre-existing tooltip byte-for-byte (CSS pre-line, no footer). */
  wrap?: boolean;
  /** 任务1033: wrap-mode line cap; the tail becomes "…（已截断，共 N 行）". */
  maxLines?: number;
  /** 任务1033: wrap-mode width budget in `ch` (rendered as
   *  width: min(maxWidthCh ch, 46vw) so narrow docks clamp, never overflow). */
  maxWidthCh?: number;
}) {
  const id = useId();
  const triggerRef = useRef<HTMLElement | null>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const showTimerRef = useRef<number | null>(null);
  const hideTimerRef = useRef<number | null>(null);
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [position, setPosition] = useState({ left: 0, top: 0, side, arrowX: 0, arrowY: 0 });
  const active = !disabled && label !== undefined && label !== null && label !== "";
  // 任务1033: only a string label in wrap mode gets the line model (a ReactNode
  // label keeps the plain wrapped rendering, no cap, no footer).
  const wrapText = wrap && typeof label === "string" ? label : null;
  const interactive = wrapText !== null;
  const wrappedLines = wrapText !== null ? wrapCommandLines(wrapText, maxWidthCh) : null;
  const truncated = wrappedLines !== null && wrappedLines.length > maxLines;
  const visibleText = truncated ? wrappedLines!.slice(0, maxLines).join("\n") : wrapText;

  const clearTimer = () => {
    if (showTimerRef.current === null) return;
    window.clearTimeout(showTimerRef.current);
    showTimerRef.current = null;
  };

  const clearHideTimer = () => {
    if (hideTimerRef.current === null) return;
    window.clearTimeout(hideTimerRef.current);
    hideTimerRef.current = null;
  };

  const show = (delay = 180) => {
    if (!active) return;
    clearTimer();
    clearHideTimer();
    showTimerRef.current = window.setTimeout(() => setOpen(true), delay);
  };

  const hide = () => {
    clearTimer();
    clearHideTimer();
    setCopied(false);
    setOpen(false);
  };

  // 任务1033: an interactive card survives the pointer hop from trigger to the
  // card itself (copy button) — leave on the trigger only schedules, entering
  // the card cancels, leaving the card hides.
  const scheduleHide = () => {
    if (!interactive) {
      hide();
      return;
    }
    clearHideTimer();
    hideTimerRef.current = window.setTimeout(() => {
      hideTimerRef.current = null;
      setCopied(false);
      setOpen(false);
    }, INTERACTIVE_HIDE_MS);
  };

  useEffect(() => () => clearHideTimer(), []);

  const updatePosition = () => {
    const trigger = triggerRef.current;
    const tip = tooltipRef.current;
    if (!trigger || !tip) return;
    const rect = trigger.getBoundingClientRect();
    const tipRect = tip.getBoundingClientRect();
    const space = {
      top: rect.top - EDGE_PAD,
      bottom: window.innerHeight - rect.bottom - EDGE_PAD,
      left: rect.left - EDGE_PAD,
      right: window.innerWidth - rect.right - EDGE_PAD,
    };
    let actualSide = side;
    if ((side === "top" || side === "bottom") && space[side] < tipRect.height + GAP + ARROW_SIZE) {
      const opposite = oppositeSide(side);
      if (space[opposite] > space[side]) actualSide = opposite;
    } else if ((side === "left" || side === "right") && space[side] < tipRect.width + GAP + ARROW_SIZE) {
      const opposite = oppositeSide(side);
      if (space[opposite] > space[side]) actualSide = opposite;
    }

    let left =
      actualSide === "left"
        ? rect.left - tipRect.width - GAP - ARROW_SIZE
        : actualSide === "right"
          ? rect.right + GAP + ARROW_SIZE
          : rect.left + rect.width / 2 - tipRect.width / 2;
    let top =
      actualSide === "top"
        ? rect.top - tipRect.height - GAP - ARROW_SIZE
        : actualSide === "bottom"
          ? rect.bottom + GAP + ARROW_SIZE
          : rect.top + rect.height / 2 - tipRect.height / 2;

    left = clamp(left, EDGE_PAD, window.innerWidth - tipRect.width - EDGE_PAD);
    top = clamp(top, EDGE_PAD, window.innerHeight - tipRect.height - EDGE_PAD);
    const arrowX = clamp(rect.left + rect.width / 2 - left, ARROW_PAD, tipRect.width - ARROW_PAD);
    const arrowY = clamp(rect.top + rect.height / 2 - top, ARROW_PAD, tipRect.height - ARROW_PAD);

    const next = {
      left,
      top,
      side: actualSide,
      arrowX,
      arrowY,
    };
    setPosition((current) => (samePosition(current, next) ? current : next));
  };

  useLayoutEffect(() => {
    if (!open) return;
    updatePosition();
  }, [open, label, side]);

  useEffect(() => {
    if (!open) return;
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    return () => {
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [open]);

  useEffect(() => () => { clearTimer(); clearHideTimer(); }, []);

  const copyFull = () => {
    if (wrapText === null) return;
    void writeClipboardText(wrapText).then((ok) => {
      if (!ok) return;
      setCopied(true);
      window.setTimeout(() => setCopied(false), COPY_FEEDBACK_MS);
    });
  };

  const triggerClass = `tooltip-trigger${fill ? " tooltip-trigger--fill" : ""}${block ? " tooltip-trigger--block" : ""}${className ? ` ${className}` : ""}`;
  const setTriggerRef = (node: HTMLElement | null) => {
    triggerRef.current = node;
  };
  const triggerProps = {
    className: triggerClass,
    "aria-describedby": open ? id : undefined,
    onMouseEnter: () => show(delay),
    onMouseLeave: scheduleHide,
    onPointerDownCapture: hide,
    onFocus: () => show(0),
    onBlur: hide,
    onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => {
      if (event.key === "Escape" || event.key === "Enter" || event.key === " ") hide();
    },
  };

  return (
    <>
      {block ? <div ref={setTriggerRef} {...triggerProps}>{children}</div> : <span ref={setTriggerRef} {...triggerProps}>{children}</span>}
      {open &&
        active &&
        createPortal(
          <div
            id={id}
            ref={tooltipRef}
            className={`tooltip tooltip--${position.side}${interactive ? " tooltip--interactive" : ""}`}
            role="tooltip"
            onMouseEnter={interactive ? clearHideTimer : undefined}
            onMouseLeave={interactive ? scheduleHide : undefined}
            onClick={interactive ? (event) => event.stopPropagation() : undefined}
            style={{
              left: position.left,
              top: position.top,
              ...(interactive ? { width: `min(${maxWidthCh}ch, 46vw)` } : null),
              "--tooltip-arrow-x": `${position.arrowX}px`,
              "--tooltip-arrow-y": `${position.arrowY}px`,
            } as CSSProperties}
          >
            {interactive ? (
              <>
                <span className="tooltip__text">{visibleText}</span>
                {truncated && (
                  <span className="tooltip__foot">
                    <span className="tooltip__note">{t("tooltip.truncated", { n: wrappedLines!.length })}</span>
                    <button type="button" className="tooltip__copy" onClick={copyFull}>
                      {copied ? <Check size={11} aria-hidden="true" /> : <Copy size={11} aria-hidden="true" />}
                      {copied ? t("tooltip.copied") : t("tooltip.copyFull")}
                    </button>
                  </span>
                )}
                {/* 任务1033 验收⑧: the visible text is capped at 30 lines, so the
                    full command rides along for screen readers only. */}
                {truncated && <span className="tooltip__sr">{wrapText}</span>}
              </>
            ) : (
              label
            )}
          </div>,
          document.body,
        )}
    </>
  );
}
