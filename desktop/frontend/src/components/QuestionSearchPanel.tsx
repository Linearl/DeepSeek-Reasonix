// QuestionSearchPanel: a callable panel to search and jump to prior user
// questions in a long session. Top: a filter input. Below: cards for each
// matching question, clickable to jump. The list scrolls when there are many
// matches. Scrolling to the top of the list requests older history when the
// session still has unloaded turns.

import { useCallback, useEffect, useMemo, useRef, useState, type UIEvent as ReactUIEvent, type WheelEvent as ReactWheelEvent } from "react";
import { Search, X } from "lucide-react";

import { useT } from "../lib/i18n";
import type { QuestionAnchor } from "../lib/transcriptGrouping";

// filterQuestions is a pure helper so the panel can be tested without a DOM
// event harness: it matches question text against a case-insensitive query.
export function filterQuestions(questions: QuestionAnchor[], query: string): QuestionAnchor[] {
  const q = query.trim().toLowerCase();
  if (!q) return questions;
  return questions.filter((question) => question.text.toLowerCase().includes(q));
}

export function QuestionSearchPanel({
  open,
  onClose,
  questions,
  totalQuestions,
  onJump,
  hasOlderHistory = false,
  loadingOlderHistory = false,
  onReachTop,
}: {
  open: boolean;
  onClose: () => void;
  questions: QuestionAnchor[];
  totalQuestions: number;
  onJump: (question: QuestionAnchor) => void;
  hasOlderHistory?: boolean;
  loadingOlderHistory?: boolean;
  /** Called when the list scrolls to the top so older history can be paged in. */
  onReachTop?: () => void;
}) {
  const t = useT();
  const [query, setQuery] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  // Reset query whenever the panel opens so each invocation starts fresh.
  useEffect(() => {
    if (open) {
      setQuery("");
      // Focus the input after mount; delay one frame so the panel is laid out.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  const listRef = useRef<HTMLDivElement>(null);
  const reachTopFiredRef = useRef(false);

  // Fire onReachTop once when the list sits at the very top; the latch rearms
  // as soon as the user scrolls away so a later return can page in again.
  const handleScroll = useCallback((event: ReactUIEvent<HTMLDivElement>) => {
    const el = event.currentTarget;
    if (el.scrollTop > 8) {
      reachTopFiredRef.current = false;
      return;
    }
    if (reachTopFiredRef.current) return;
    reachTopFiredRef.current = true;
    onReachTop?.();
  }, [onReachTop]);

  // Wheel-up at the very top must page in older questions directly. The list
  // scrollTop stays 0 while the user wheels up (it cannot scroll further), so
  // no `scroll` event fires and handleScroll never runs — the wheel instead
  // chains into the transcript behind it, which is why only dragging the
  // scrollbar thumb down then back up seemed to work. Capture deltaY here and
  // fire onReachTop on an upward wheel at the top, guarded by the latch so a
  // single page-in request per gesture is not spammed.
  const handleWheel = useCallback((event: ReactWheelEvent<HTMLDivElement>) => {
    if (event.deltaY >= 0) return; // downward wheel: normal list scroll
    const el = event.currentTarget;
    if (el.scrollTop > 8) return; // not at the top yet
    if (reachTopFiredRef.current) return;
    reachTopFiredRef.current = true;
    onReachTop?.();
  }, [onReachTop]);

  // Rearm the reach-top latch when the panel reopens or new history lands.
  useEffect(() => {
    if (!open) return;
    reachTopFiredRef.current = false;
  }, [open, questions.length]);

  // Close on Escape or when clicking outside the panel.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    const onPointerDown = (event: MouseEvent) => {
      if (panelRef.current && !panelRef.current.contains(event.target as Node)) {
        onClose();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("mousedown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("mousedown", onPointerDown);
    };
  }, [open, onClose]);

  const filtered = useMemo(() => filterQuestions(questions, query), [questions, query]);

  if (!open) return null;

  const jump = (question: QuestionAnchor) => {
    onJump(question);
    onClose();
  };

  return (
    <div className="question-search-panel" ref={panelRef} role="dialog" aria-label={t("questionSearch.label")}>
      <div className="question-search__header">
        <Search size={15} aria-hidden="true" />
        <input
          ref={inputRef}
          className="question-search__input"
          type="text"
          value={query}
          placeholder={t("questionSearch.placeholder")}
          aria-label={t("questionSearch.placeholder")}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button
          type="button"
          className="topicbar__action-btn topicbar__action-btn--icon"
          aria-label={t("common.close")}
          onClick={onClose}
        >
          <X size={15} aria-hidden="true" />
        </button>
      </div>
      <div className="question-search__results" ref={listRef} onScroll={handleScroll} onWheel={handleWheel}>
        {filtered.length === 0 && (
          <div className="question-search__empty">{t("questionSearch.empty")}</div>
        )}
        {filtered.map((question) => (
          <button
            type="button"
            className="question-search__card"
            key={question.id}
            title={question.text}
            onClick={() => jump(question)}
          >
            <span className="question-search__index">{question.turn + 1}</span>
            <span className="question-search__text">{question.text}</span>
          </button>
        ))}
      </div>
      <div className="question-search__foot">
        {loadingOlderHistory ? t("questionSearch.loadingOlder") : t("questionSearch.count", { shown: filtered.length, total: totalQuestions })}
        {hasOlderHistory && !loadingOlderHistory && " · "}
        {hasOlderHistory && !loadingOlderHistory && t("questionSearch.olderAvailable")}
      </div>
    </div>
  );
}

export default QuestionSearchPanel;
