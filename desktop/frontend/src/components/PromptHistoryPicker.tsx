import { useEffect, useRef } from "react";
import { Clock, Loader2 } from "lucide-react";
import type { Translator } from "../lib/i18n";

// Task 261 (upstream #10425): the clock-icon prompt history picker. An explicit
// alternative to the plain-ArrowUp history navigation - picking an entry NEVER
// replaces the unsent draft: the entry text is inserted at the caret (only an
// active selection is consumed), so composing work is untouched.

export interface PromptHistoryPickerEntry {
  text: string;
  at: number;
}

export interface PromptHistoryPickerProps {
  entries: readonly PromptHistoryPickerEntry[];
  /** More pages exist on the backend tape (unknown while loading). */
  hasMore: boolean;
  loading: boolean;
  onPick: (text: string) => void;
  onLoadMore: () => void;
  onClose: () => void;
  t: Translator;
}

function summaryOf(text: string): string {
  const firstLine = text.split("\n", 1)[0] ?? "";
  const trimmed = firstLine.trim();
  if (trimmed.length > 96) return `${trimmed.slice(0, 96)}…`;
  return trimmed.length > 0 ? trimmed : "…";
}

function timeOf(at: number): string {
  try {
    return new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  } catch {
    return "";
  }
}

export function PromptHistoryPicker({ entries, hasMore, loading, onPick, onLoadMore, onClose, t }: PromptHistoryPickerProps) {
  const listRef = useRef<HTMLDivElement>(null);

  // Focus the list so Escape lands here (not on the composer) and closes only
  // the picker; blur-closing is handled by the backdrop in the host.
  useEffect(() => {
    listRef.current?.focus();
  }, []);

  return (
    <div
      ref={listRef}
      className="composer-history-menu composer-menu-surface"
      role="dialog"
      aria-label={t("composer.historyPicker")}
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <div className="composer-history-menu__head">
        <Clock size={13} aria-hidden="true" />
        <span>{t("composer.historyPicker")}</span>
        <span className="composer-history-menu__hint">{t("composer.historyPickerHint")}</span>
      </div>
      {entries.length === 0 && !loading ? (
        <div className="composer-history-menu__empty">{t("composer.historyPickerEmpty")}</div>
      ) : (
        <div className="composer-history-menu__list" role="listbox" aria-label={t("composer.historyPicker")}>
          {entries.map((entry, index) => (
            <button
              key={`${entry.at}-${index}`}
              type="button"
              role="option"
              aria-selected={false}
              className="composer-history-menu__item"
              title={entry.text}
              onClick={() => onPick(entry.text)}
            >
              <span className="composer-history-menu__time">{timeOf(entry.at)}</span>
              <span className="composer-history-menu__summary">{summaryOf(entry.text)}</span>
            </button>
          ))}
        </div>
      )}
      {hasMore && (
        <button type="button" className="composer-history-menu__more" onClick={onLoadMore} disabled={loading}>
          {loading ? <Loader2 size={12} className="spin" aria-hidden="true" /> : null}
          <span>{t("composer.historyPickerMore")}</span>
        </button>
      )}
    </div>
  );
}
