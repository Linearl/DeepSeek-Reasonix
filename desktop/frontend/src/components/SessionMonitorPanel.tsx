// SessionMonitorPanel (task 123) is the experimental diagnostics board behind the
// left-rail "session monitor" entry. It answers the question desktop.log could
// not: for each open tab, does the transcript cache still hold it, what did the
// last switch cost, why was history skipped, and did anything get evicted.
//
// Read-only: it composes the in-memory observations recorded by sessionMonitor.ts,
// transcriptStore and useController. The tab list comes from the same binding the
// write-directory picker uses, so the board never invents a session that is not open.

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import {
  evictionsFor,
  hydrateDecisionFor,
  isSessionMonitorOpen,
  onSessionMonitorOpenChange,
  recentEvictions,
  setSessionMonitorOpen,
  slowestStageFor,
  stageTimingsFor,
} from "../lib/sessionMonitor";
import { getTranscriptStore } from "../lib/transcriptStore";

type MonitorTab = { tabId: string; title: string; session: string[]; active: boolean };

function formatKiB(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${bytes} B`;
}

function shortPath(path: string): string {
  const parts = path.replace(/\\/g, "/").split("/");
  const tail = parts.slice(-2).join("/");
  return tail || path;
}

export function SessionMonitorPanel() {
  const t = useT();
  const [tabs, setTabs] = useState<MonitorTab[]>([]);
  const [tick, bumpTick] = useState(0);
  const open = isSessionMonitorOpen();
  void tick;

  useEffect(() => onSessionMonitorOpenChange(() => bumpTick((n) => n + 1)), []);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    const refresh = () => {
      void app
        .ListSessionWriteDirs()
        .then((rows) => {
          if (!cancelled) setTabs(Array.isArray(rows) ? (rows as MonitorTab[]) : []);
        })
        .catch(() => {});
    };
    refresh();
    // The board is a live view: refresh while it is open so a closed tab disappears
    // and a newly opened one shows up without reopening the panel.
    const timer = setInterval(refresh, 3000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [open, tick]);

  if (!open) return null;

  const store = getTranscriptStore();
  const stats = store.stats();
  const allEvictions = recentEvictions(6);

  // Rendered through a portal: inside the project tree the panel inherited its
  // ancestors' stacking and clipping contexts, which is why it came out half-styled.
  // On body it is a plain fixed overlay anchored above the trash row.
  return createPortal(
    <div className="session-monitor" role="dialog" aria-label={t("sessionMonitor.title")}>
      <div className="session-monitor__head">
        <span className="session-monitor__title">{t("sessionMonitor.title")}</span>
        <button type="button" className="btn btn--small" onClick={() => setSessionMonitorOpen(false)}>
          {t("sessionMonitor.close")}
        </button>
      </div>

      <div className="session-monitor__global">
        <span>{t("sessionMonitor.resident", { n: stats.residentSessions, max: stats.maxResidentSessions })}</span>
        <span>{t("sessionMonitor.bodyBytes", { used: formatKiB(stats.bodyBytes), budget: formatKiB(stats.bodyBudgetBytes) })}</span>
        <span>{t("sessionMonitor.markdownBytes", { used: formatKiB(stats.markdownBytes), budget: formatKiB(stats.markdownBudgetBytes) })}</span>
        <span>{t("sessionMonitor.evictions", { n: stats.historyEvictions + stats.markdownEvictions })}</span>
      </div>

      {tabs.length === 0 && <div className="session-monitor__empty">{t("sessionMonitor.noTabs")}</div>}

      <div className="session-monitor__rows">
        {tabs.map((tab) => {
          const sessions = store.sessionsForTab(tab.tabId);
          const decision = hydrateDecisionFor(tab.tabId);
          const worst = slowestStageFor(tab.tabId);
          const stages = stageTimingsFor(tab.tabId, 3);
          const gone = evictionsFor(tab.tabId, 3);
          const last = gone[gone.length - 1];
          return (
            <div key={tab.tabId} className={`session-monitor__row${tab.active ? " session-monitor__row--active" : ""}`}>
              <div className="session-monitor__row-head">
                <span className="session-monitor__tab" title={tab.tabId}>{tab.title || tab.tabId}</span>
                <span className="session-monitor__path">{shortPath(sessions[0]?.sessionPath ?? tab.session[0] ?? "")}</span>
              </div>
              <div className="session-monitor__metrics">
                {sessions.length > 0 ? (
                  sessions.map((session) => (
                    <span key={session.sessionPath}>
                      {t("sessionMonitor.residentRow", {
                        items: session.items,
                        records: session.records,
                        turns: session.totalTurns,
                        bytes: formatKiB(session.bodyBytes),
                      })}
                      {session.pinned ? ` · ${t("sessionMonitor.pinned")}` : ""}
                    </span>
                  ))
                ) : (
                  <span className="session-monitor__miss">{t("sessionMonitor.notResident")}</span>
                )}
              </div>
              <div className="session-monitor__metrics">
                {decision ? (
                  <span>
                    {t("sessionMonitor.skip", {
                      mode: decision.skipHistory ? t("sessionMonitor.skipYes") : t("sessionMonitor.skipNo"),
                      reason: decision.reason,
                    })}
                  </span>
                ) : (
                  <span>{t("sessionMonitor.skipUnknown")}</span>
                )}
                {worst && (
                  <span title={stages.map((entry) => `${entry.stage}=${Math.round(entry.ms)}ms`).join("  ")}>
                    {t("sessionMonitor.lastSwitch", { stage: worst.stage, ms: Math.round(worst.ms) })}
                  </span>
                )}
              </div>
              <div className="session-monitor__metrics">
                {last ? (
                  <span className="session-monitor__evicted">
                    {t("sessionMonitor.evictedRow", { n: gone.length, reason: last.reason, bytes: formatKiB(last.bodyBytes) })}
                  </span>
                ) : (
                  <span>{t("sessionMonitor.neverEvicted")}</span>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {allEvictions.length > 0 && (
        <div className="session-monitor__log">
          <span className="session-monitor__log-title">{t("sessionMonitor.recentEvictions")}</span>
          {allEvictions.map((entry) => (
            <span key={`${entry.at}-${entry.tabId}-${entry.reason}`} className="session-monitor__log-row">
              {t("sessionMonitor.evictedRow", { n: entry.records, reason: entry.reason, bytes: formatKiB(entry.bodyBytes) })}
            </span>
          ))}
        </div>
      )}
    </div>,
    document.body,
  );
}
