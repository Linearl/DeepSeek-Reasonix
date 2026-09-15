// Feedback inbox (task 121): the experimental panel that lists notes the agent
// wrote through submit_feedback. Read-only view over the local JSONL inbox;
// the Settings switch that enables the panel is the same one that documents
// the tool's existence.

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";

export type FeedbackEntry = {
  at: string;
  kind: string;
  text: string;
  tags?: string[];
  session?: string;
  model?: string;
};

let feedbackOpen = false;
let feedbackEnabled = false;
const openListeners = new Set<(open: boolean) => void>();
const enabledListeners = new Set<(enabled: boolean) => void>();

export function isFeedbackEnabled(): boolean {
  return feedbackEnabled;
}

export function setFeedbackEnabled(next: boolean): void {
  if (feedbackEnabled === next) return;
  feedbackEnabled = next;
  if (!next) setFeedbackOpen(false);
  for (const listener of enabledListeners) listener(next);
}

export function onFeedbackEnabledChange(cb: (enabled: boolean) => void): () => void {
  enabledListeners.add(cb);
  return () => enabledListeners.delete(cb);
}

export function isFeedbackOpen(): boolean {
  return feedbackOpen;
}

export function setFeedbackOpen(next: boolean): void {
  if (feedbackOpen === next) return;
  feedbackOpen = next;
  for (const listener of openListeners) listener(next);
}

export function onFeedbackOpenChange(cb: (open: boolean) => void): () => void {
  openListeners.add(cb);
  return () => openListeners.delete(cb);
}

function formatTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString();
}

export function FeedbackPanel() {
  const t = useT();
  const [open, setOpen] = useState(feedbackOpen);
  const [entries, setEntries] = useState<FeedbackEntry[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => onFeedbackOpenChange((next) => setOpen(next)), []);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    const refresh = () => {
      void app
        .ListFeedbackEntries(200)
        .then((rows) => {
          if (!cancelled) setEntries(Array.isArray(rows) ? rows : []);
        })
        .catch(() => {});
    };
    refresh();
    const timer = setInterval(refresh, 4000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [open]);

  if (!open) return null;

  const clear = async () => {
    setBusy(true);
    try {
      await app.ClearFeedbackEntries();
      setEntries([]);
    } finally {
      setBusy(false);
    }
  };

  return createPortal(
    <div className="feedback-panel" role="dialog" aria-label={t("feedbackInbox.title")}>
      <div className="feedback-panel__head">
        <span className="feedback-panel__title">{t("feedbackInbox.title")}</span>
        <div className="feedback-panel__actions">
          <button type="button" className="btn btn--small" disabled={busy || entries.length === 0} onClick={() => void clear()}>
            {t("feedbackInbox.clear")}
          </button>
          <button type="button" className="btn btn--small" onClick={() => setFeedbackOpen(false)}>
            {t("feedbackInbox.close")}
          </button>
        </div>
      </div>
      {entries.length === 0 ? (
        <div className="feedback-panel__empty">{t("feedbackInbox.empty")}</div>
      ) : (
        <div className="feedback-panel__rows">
          {entries.map((entry, index) => (
            <div key={`${entry.at}-${index}`} className="feedback-panel__row">
              <div className="feedback-panel__meta">
                <span className={`feedback-panel__kind feedback-panel__kind--${entry.kind}`}>{t(`feedbackInbox.kind.${entry.kind}` as "feedbackInbox.kind.bug")}</span>
                <span className="feedback-panel__time">{formatTime(entry.at)}</span>
                {entry.tags?.map((tag) => (
                  <span key={tag} className="feedback-panel__tag">{tag}</span>
                ))}
              </div>
              <div className="feedback-panel__text">{entry.text}</div>
            </div>
          ))}
        </div>
      )}
    </div>,
    document.body,
  );
}
