import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { useT } from "../lib/i18n";

function fallbackCopyText(value: string): boolean {
  const activeElement = document.activeElement;
  const selection = document.getSelection();
  const ranges: Range[] = [];
  if (selection) {
    for (let index = 0; index < selection.rangeCount; index += 1) {
      ranges.push(selection.getRangeAt(index));
    }
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.inset = "0 auto auto 0";
  textarea.style.width = "1px";
  textarea.style.height = "1px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } finally {
    textarea.remove();
    if (selection) {
      selection.removeAllRanges();
      for (const range of ranges) selection.addRange(range);
    }
    if (activeElement instanceof HTMLElement) activeElement.focus();
  }
  return ok;
}

async function writeClipboardText(value: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(value);
    return;
  } catch {
    /* try the desktop runtime below */
  }
  try {
    if (typeof window !== "undefined" && (await window.runtime?.ClipboardSetText?.(value))) return;
  } catch {
    /* runtime unavailable in browser dev */
  }
  if (fallbackCopyText(value)) return;
  throw new Error("clipboard unavailable");
}

// CopyButton copies text to the clipboard on click and briefly flips to a check.
// Clipboard writes are best-effort across browser dev, Wails, and webviews, so
// the visible acknowledgement stays tied to the user action.
export function CopyButton({
  text,
  getText,
  className,
  label,
  showInlineLabel = true,
  disabled = false,
  disabledLabel,
}: {
  text?: string;
  getText?: () => string | Promise<string>;
  className?: string;
  label?: string;
  showInlineLabel?: boolean;
  /** Task 630: keep the copy entry mounted on textless turns and explain the
   * empty payload instead, so the row's buttons don't appear and vanish. */
  disabled?: boolean;
  /** Tooltip shown while disabled (the reason nothing can be copied). */
  disabledLabel?: string;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<number | null>(null);
  const actionLabel = label ?? t("msg.copy");
  const stateLabel = disabled
    ? (disabledLabel ?? actionLabel)
    : copied
      ? t("msg.copied")
      : actionLabel;

  useEffect(() => {
    return () => {
      if (timerRef.current != null) window.clearTimeout(timerRef.current);
    };
  }, []);

  const copy = async () => {
    if (disabled) return;
    try {
      const value = getText ? await getText() : text ?? "";
      void writeClipboardText(value).catch(() => {});
      setCopied(true);
      if (timerRef.current != null) window.clearTimeout(timerRef.current);
      timerRef.current = window.setTimeout(() => {
        setCopied(false);
        timerRef.current = null;
      }, 1200);
    } catch {
      /* clipboard unavailable */
    }
  };
  return (
    <button
      className={[
        "copybtn",
        copied ? "copybtn--copied" : "",
        className ?? "",
      ].filter(Boolean).join(" ")}
      onClick={copy}
      disabled={disabled}
      aria-label={stateLabel}
      title={stateLabel}
      type="button"
    >
      {copied ? <Check size={13} /> : <Copy size={13} />}
      {showInlineLabel && (
        <span className="copybtn__label-inline">{stateLabel}</span>
      )}
    </button>
  );
}
