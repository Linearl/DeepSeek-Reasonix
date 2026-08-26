// QuestionSearchPanel: a callable panel to search and jump to prior user
// questions in a long session. Top: a filter input. Below: cards for each
// matching question, clickable to jump. The list scrolls when there are many
// matches.

import { useEffect, useMemo, useRef, useState } from "react";
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
}: {
  open: boolean;
  onClose: () => void;
  questions: QuestionAnchor[];
  totalQuestions: number;
  onJump: (question: QuestionAnchor) => void;
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
      <div className="question-search__results">
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
        {t("questionSearch.count", { shown: filtered.length, total: totalQuestions })}
      </div>
    </div>
  );
}

export default QuestionSearchPanel;
