// QuestionHistoryPanel: a callable panel to browse the current conversation's
// prior user questions as a scrollable list. Each card jumps to that question.
// Unlike QuestionSearchPanel it has no filter input — it is a pure chronological
// history list. Scrolling to the top requests older history when available.

import { useCallback, useEffect, useRef, type UIEvent as ReactUIEvent } from "react";
import { History, X } from "lucide-react";

import { useT } from "../lib/i18n";
import type { QuestionAnchor } from "../lib/transcriptGrouping";

export function QuestionHistoryPanel({
  open,
  onClose,
  questions,
  totalQuestions,
  onJump,
  hasOlderHistory,
  loadingOlderHistory,
  onReachTop,
}: {
  open: boolean;
  onClose: () => void;
  questions: QuestionAnchor[];
  totalQuestions: number;
  onJump: (question: QuestionAnchor) => void;
  hasOlderHistory: boolean;
  loadingOlderHistory: boolean;
  /** Called when the list scrolls to the top so older history can be paged in. */
  onReachTop?: () => void;
}) {
  const t = useT();
  const panelRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const reachTopFiredRef = useRef(false);

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

  // Reset the reach-top latch whenever the panel (re)opens or new history lands.
  useEffect(() => {
    if (!open) return;
    reachTopFiredRef.current = false;
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight });
  }, [open, questions.length]);

  if (!open) return null;

  const jump = (question: QuestionAnchor) => {
    onJump(question);
    onClose();
  };

  return (
    <div className="question-search-panel question-history-panel" ref={panelRef} role="dialog" aria-label={t("questionHistory.label")}>
      <div className="question-search__header">
        <History size={15} aria-hidden="true" />
        <span className="question-history__title">{t("questionHistory.title")}</span>
        <button
          type="button"
          className="topicbar__action-btn topicbar__action-btn--icon"
          aria-label={t("common.close")}
          onClick={onClose}
        >
          <X size={15} aria-hidden="true" />
        </button>
      </div>
      <div
        className="question-search__results question-history__list"
        ref={listRef}
        onScroll={handleScroll}
      >
        {questions.length === 0 && (
          <div className="question-search__empty">{t("questionHistory.empty")}</div>
        )}
        {questions.map((question) => (
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
        {loadingOlderHistory ? t("questionHistory.loading") : t("questionHistory.count", { total: totalQuestions })}
        {hasOlderHistory && !loadingOlderHistory && " · "}
        {hasOlderHistory && !loadingOlderHistory && t("questionHistory.more")}
      </div>
    </div>
  );
}

export default QuestionHistoryPanel;
