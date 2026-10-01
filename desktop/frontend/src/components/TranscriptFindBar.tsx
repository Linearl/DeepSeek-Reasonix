// Task 399: transcript find bar — a single-row search strip pinned to the top
// center of the transcript shell (same anchor family as QuestionSearchPanel,
// above the right-edge jump bar). Owns only the query string; match indexing,
// highlighting and jumps live in Transcript so the bar stays testable alone.

import { useEffect, useRef, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { ChevronDown, ChevronUp, Search, X } from "lucide-react";

import { useT } from "../lib/i18n";

export interface TranscriptFindBarProps {
  open: boolean;
  query: string;
  onQueryChange: (query: string) => void;
  /** 1-based index of the active hit, 0 when there is none. */
  activeIndex: number;
  /** Row-level match count (already capped by the searcher). */
  matchCount: number;
  /** True when more rows matched than `matchCount` (show "N+"). */
  capped: boolean;
  /** Query trimmed non-empty but zero matches — the explicit empty state. */
  noMatches: boolean;
  onPrev: () => void;
  onNext: () => void;
  onClose: () => void;
  /** Bumped by every Ctrl+F while open: re-focus + select the query text
   *  (browser-find behavior on a repeated chord). */
  focusSignal?: number;
}

export function TranscriptFindBar({
  open,
  query,
  onQueryChange,
  activeIndex,
  matchCount,
  capped,
  noMatches,
  onPrev,
  onNext,
  onClose,
  focusSignal = 0,
}: TranscriptFindBarProps) {
  const t = useT();
  const inputRef = useRef<HTMLInputElement>(null);
  const barRef = useRef<HTMLDivElement>(null);

  // Focus the input when the bar opens so Ctrl+F → type works with no extra
  // click. One frame of delay lets the panel lay out first. Repeated Ctrl+F
  // while open (focusSignal bump) re-selects the query, like browser find.
  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    });
    return () => cancelAnimationFrame(frame);
  }, [open, focusSignal]);

  // Escape closes — bubble-phase document listener, the same shape
  // QuestionSearchPanel uses, so a code block's Escape (capture-phase
  // stopPropagation inside .code-block__wrap) still wins when it is the one
  // handling the key.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

  if (!open) return null;

  const handleInputKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      if (event.shiftKey) onPrev();
      else onNext();
    } else if (event.key === "ArrowDown" && !event.altKey) {
      event.preventDefault();
      onNext();
    } else if (event.key === "ArrowUp" && !event.altKey) {
      event.preventDefault();
      onPrev();
    }
  };

  const counter = noMatches
    ? t("transcriptFind.noMatches")
    : matchCount > 0
      ? `${activeIndex}/${matchCount}${capped ? "+" : ""}`
      : "";

  return (
    <div className="transcript-find" ref={barRef} role="search" aria-label={t("transcriptFind.label")}>
      <Search size={14} className="transcript-find__icon" aria-hidden="true" />
      <input
        ref={inputRef}
        type="text"
        className="transcript-find__input"
        value={query}
        placeholder={t("transcriptFind.placeholder")}
        aria-label={t("transcriptFind.placeholder")}
        onChange={(event) => onQueryChange(event.target.value)}
        onKeyDown={handleInputKeyDown}
      />
      <span className="transcript-find__count" aria-live="polite">{counter}</span>
      <button
        type="button"
        className="transcript-find__btn"
        aria-label={t("transcriptFind.prev")}
        title={t("transcriptFind.prev")}
        disabled={matchCount === 0}
        onClick={onPrev}
      >
        <ChevronUp size={14} aria-hidden="true" />
      </button>
      <button
        type="button"
        className="transcript-find__btn"
        aria-label={t("transcriptFind.next")}
        title={t("transcriptFind.next")}
        disabled={matchCount === 0}
        onClick={onNext}
      >
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      <button
        type="button"
        className="transcript-find__btn"
        aria-label={t("common.close")}
        title={t("common.close")}
        onClick={onClose}
      >
        <X size={14} aria-hidden="true" />
      </button>
    </div>
  );
}

export default TranscriptFindBar;
