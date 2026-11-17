// tl1664: the "在主区打开" read-only replay surface. The dock detail is a
// narrow column; long transcripts read better in the main area — this surface
// replaces the primary transcript the same way the trajectory surface does
// (任务 704 precedent: a main-area body swap, no backend tab resource). The
// composer is disabled with a dedicated notice while the surface is open
// (App passes readOnly through the existing channel); the human→subagent
// send pipeline does not exist, so read-only is the honest state.
//
// Data plane: the same ReadSubagentSession bridge the dock detail already
// polls (tl1537 rationale unchanged — the read returns the full projection,
// so a re-pull IS the increment; no new event contract). A running child
// polls at the dock's live cadence; an ended child reads once (the
// transcript is final).

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, Eye } from "lucide-react";
import { useT } from "../lib/i18n";
import { historyMessagesToItems } from "../lib/useController";
import type { HistoryMessage } from "../lib/types";

const LazyTranscript = lazy(async () => ({ default: (await import("./Transcript")).Transcript }));

// Same cadence the dock detail polls (SUBAGENT_DOCK_LIVE_POLL_MS mirrors the
// backend autosave interval with headroom). Kept local so the surface stays
// independent of the dock panel's test seam.
const REPLAY_LIVE_POLL_MS = 2000;

// Backend sentinel phrase for "transcript not written yet" — same string the
// dock detail checks (the desktop bridge's file-missing error copy).
const TRANSCRIPT_PENDING_MARKER = "has not been saved yet";

export interface SubagentReplayTarget {
  sessionPath: string;
  ref: string;
  title: string;
  /** Snapshot at open time: running children follow the autosave, ended
   *  children read once. */
  running: boolean;
}

interface ReplayState {
  loading: boolean;
  failed: boolean;
  pending: boolean;
  messages: HistoryMessage[];
}

const EMPTY: ReplayState = { loading: false, failed: false, pending: false, messages: [] };

export function SubagentReplaySurface({
  target,
  onReadSubagent,
  onClose,
}: {
  target: SubagentReplayTarget;
  onReadSubagent?: (sessionPath: string, ref: string) => Promise<HistoryMessage[]>;
  onClose: () => void;
}) {
  const t = useT();
  const [state, setState] = useState<ReplayState>(() =>
    onReadSubagent ? { loading: true, failed: false, pending: false, messages: [] } : EMPTY,
  );
  const [attempt, setAttempt] = useState(0);
  const seq = useRef(0);
  useEffect(() => {
    if (!onReadSubagent) return;
    const seqAtEffect = ++seq.current;
    let cancelled = false;
    let busy = false;
    const pull = () => {
      if (busy) return;
      busy = true;
      // Keep already-loaded content on screen during a refresh re-pull.
      setState((prev) => (prev.messages.length > 0 && !prev.failed ? prev : { loading: true, failed: false, pending: false, messages: [] }));
      onReadSubagent(target.sessionPath, target.ref)
        .then((messages) => {
          if (cancelled || seqAtEffect !== seq.current) return;
          setState({ loading: false, failed: false, pending: false, messages });
        })
        .catch((err) => {
          if (cancelled || seqAtEffect !== seq.current) return;
          if (target.running && String(err).includes(TRANSCRIPT_PENDING_MARKER)) {
            // Pre-first-snapshot window of a running child: honest pending
            // note, the next poll picks the snapshot up.
            setState((prev) => (prev.messages.length > 0 ? prev : { loading: false, failed: false, pending: true, messages: [] }));
            return;
          }
          setState({ loading: false, failed: true, pending: false, messages: [] });
        })
        .finally(() => {
          busy = false;
        });
    };
    pull();
    if (!target.running) {
      return () => {
        cancelled = true;
      };
    }
    const iv = setInterval(pull, REPLAY_LIVE_POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(iv);
    };
  }, [onReadSubagent, target.sessionPath, target.ref, target.running, attempt]);
  const items = useMemo(() => historyMessagesToItems(state.messages, "subagent-replay").items, [state.messages]);
  return (
    <div className="subagent-replay" role="region" aria-label={t("subagentPanel.replayTitle")}>
      <div className="subagent-replay__head">
        <button type="button" className="subagents-panel__back" onClick={onClose}>
          <ArrowLeft size={13} aria-hidden="true" />
          <span>{t("subagentPanel.replayClose")}</span>
        </button>
        <span className="subagent-replay__badge" data-subagent-replay-badge="true">
          <Eye size={12} aria-hidden="true" />
          {t("subagentPanel.replayBadge")}
        </span>
        <span className="subagent-replay__title" data-subagent-replay-title="true">{target.title}</span>
      </div>
      <div className="subagent-replay__body" data-subagent-replay-state={state.loading ? "loading" : state.failed ? "failed" : state.pending ? "pending" : items.length === 0 ? "empty" : "ready"}>
        {state.loading ? (
          <div className="subagent-replay__note">{t("composer.capsuleHistoryLoading")}</div>
        ) : state.failed ? (
          <>
            <div className="subagent-replay__note">{t("composer.capsuleHistoryFailed")}</div>
            <button type="button" className="subagents-panel__retry" onClick={() => setAttempt((n) => n + 1)}>
              {t("composer.capsuleHistoryRetry")}
            </button>
          </>
        ) : state.pending ? (
          <div className="subagent-replay__note">{t("composer.capsuleHistoryPending")}</div>
        ) : items.length === 0 ? (
          <div className="subagent-replay__note">{t("composer.capsuleHistoryEmpty")}</div>
        ) : (
          <Suspense fallback={<div className="subagent-replay__note">{t("composer.capsuleHistoryLoading")}</div>}>
            <LazyTranscript items={items} onPrompt={() => {}} questionNavigator={false} rewindDisabled />
          </Suspense>
        )}
      </div>
      <p className="subagent-replay__readonly">{t("subagentPanel.replayReadOnly")}</p>
    </div>
  );
}
