import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useI18n, type DictKey } from "../lib/i18n";
import {
  PLAN_USAGE_WINDOW_KEYS,
  planFiveHourExhausted,
  planUsageNoteText,
  planUsageTone,
  planWindowLabelKey,
} from "../lib/planUsage";
import { resetCountdown } from "../lib/opencodeGoUsage";
import { ensurePlanUsagePolling, usePlanUsageStore } from "../store/planUsage";

// Task 287 — the plan-usage block of the right-dock overview (显示位 a).
//
// Data lives in the global plan-usage store (provider-scoped, polled every
// 60s); this card is a pure view over it. A provider without plan support
// renders nothing at all — hidden, never an error (task 287 fallback rule).
// The refresh button is the manual refresh path alongside the polling.

export function PlanUsageCard() {
  const { t } = useI18n();
  const view = usePlanUsageStore((s) => s.view);
  const loading = usePlanUsageStore((s) => s.loading);
  const refresh = usePlanUsageStore((s) => s.refresh);

  // Start the shared poll from the first mounted surface (idempotent).
  useEffect(() => {
    ensurePlanUsagePolling();
  }, []);

  // The countdown lines re-render every 30s off the same fetched resetsAt
  // values (same cadence as the OpenCode Go card).
  const [tick, setTick] = useState(0);
  const hasResets = view?.windows.some((w) => w.resetsAt) ?? false;
  useEffect(() => {
    if (!hasResets) return;
    const timer = window.setInterval(() => setTick((value) => value + 1), 30_000);
    return () => window.clearInterval(timer);
  }, [hasResets]);

  if (!view || !view.supported) return null;

  const windows = PLAN_USAGE_WINDOW_KEYS
    .map((key) => view.windows.find((w) => w.window === key))
    .filter((w): w is NonNullable<typeof w> => Boolean(w));
  const noteText = planUsageNoteText(view.note, t);
  const exhausted = planFiveHourExhausted(view);
  const providerLabel = [view.provider, view.region?.toUpperCase()].filter(Boolean).join(" · ");

  return (
    <section className="context-panel__section plan-usage" data-testid="plan-usage-card">
      <header className="context-panel__section-head">
        <h3>{t("planUsage.title")}</h3>
        {providerLabel && <span className="plan-usage__provider">{providerLabel}</span>}
        <button
          type="button"
          className="plan-usage__refresh"
          disabled={loading}
          aria-label={t("planUsage.refresh")}
          title={t("planUsage.refresh")}
          onClick={() => void refresh()}
        >
          <RefreshCw size={13} data-tick={tick} />
        </button>
      </header>
      {noteText && windows.length === 0 ? (
        <p className="plan-usage__note">{noteText}</p>
      ) : windows.length > 0 ? (
        <div className="plan-usage__rows">
          {windows.map((w) => {
            const tone = planUsageTone(w.percent);
            const labelKey = planWindowLabelKey(w.window) as DictKey;
            return (
              <div key={w.window} className="plan-usage__row" data-window={w.window}>
                <div className="plan-usage__row-head">
                  <span className="plan-usage__window">{t(labelKey)}</span>
                  <strong className={tone ? `plan-usage__percent--${tone}` : undefined}>{Math.round(w.percent ?? 0)}%</strong>
                </div>
                <div className="plan-usage__track" aria-hidden="true">
                  <span className={`plan-usage__fill${tone ? ` plan-usage__fill--${tone}` : ""}`} style={{ width: `${Math.max(0, Math.min(100, w.percent ?? 0))}%` }} />
                </div>
                <span className="plan-usage__reset">
                  {/* tick participates so the interval re-render refreshes this line */}
                  {w.resetsAt ? `${t("planUsage.resetsIn")} ${resetCountdown(w.resetsAt) || "—"}` : ""}
                </span>
              </div>
            );
          })}
          {exhausted && <p className="plan-usage__exhausted">{t("planUsage.exhausted")}</p>}
        </div>
      ) : (
        <p className="plan-usage__note">{t("planUsage.note.empty")}</p>
      )}
    </section>
  );
}
