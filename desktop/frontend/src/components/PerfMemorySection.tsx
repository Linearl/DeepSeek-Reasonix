import { useCallback, useEffect, useState } from "react";

import { app, type HeapBreakdownView, type PerfTimeSeriesView } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { SettingsOptions } from "./SettingsOptions";

if (import.meta.env?.MODE) void import("./PerfMemorySection.css");

type ApplyFn = (fn: () => Promise<unknown>) => Promise<boolean>;

/** bytes → a short human size (same shape as RecoveryCopiesSection). */
function formatBytes(n: number): string {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

const WINDOWS = [
  { minutes: 60, labelKey: "settings.perfMonitor.window.1h" },
  { minutes: 360, labelKey: "settings.perfMonitor.window.6h" },
  { minutes: 1440, labelKey: "settings.perfMonitor.window.24h" },
  { minutes: 2880, labelKey: "settings.perfMonitor.window.48h" },
] as const;

// HeapCatKey mirrors the backend's five fixed bucket keys so the locale lookup
// stays a concrete union (the maps are strongly typed; a plain string would
// widen the template literal). The server always returns exactly these keys.
type HeapCatKey = "transcript" | "snapshot" | "dag" | "frontendCache" | "other";

// Five fixed palette slots, all existing theme tokens (check:theme-token safe).
const CATEGORY_COLORS: Record<string, string> = {
  transcript: "var(--accent)",
  snapshot: "var(--ok)",
  dag: "var(--warn)",
  frontendCache: "var(--danger)",
  other: "var(--fg-dim)",
};

/** wsPolyline maps the series onto the 600x120 viewBox (min..max padded). */
function wsPolyline(points: number[]): string {
  if (points.length === 0) return "";
  const min = Math.min(...points);
  const max = Math.max(...points);
  const span = max - min || 1;
  const step = points.length > 1 ? 600 / (points.length - 1) : 0;
  return points
    .map((value, index) => {
      const x = (index * step).toFixed(1);
      const y = (114 - ((value - min) / span) * 108).toFixed(1);
      return `${x},${y}`;
    })
    .join(" ");
}

/** pieArc describes one slice of the 120x120 pie (r=50, clockwise from 12 o'clock). */
function pieArc(startPercent: number, endPercent: number): string {
  const toXY = (percent: number) => {
    const angle = (percent / 100) * 2 * Math.PI - Math.PI / 2;
    return [60 + 50 * Math.cos(angle), 60 + 50 * Math.sin(angle)];
  };
  const [x1, y1] = toXY(startPercent);
  const [x2, y2] = toXY(endPercent);
  const largeArc = endPercent - startPercent > 0.5 ? 1 : 0;
  return `M60,60 L${x1.toFixed(2)},${y1.toFixed(2)} A50,50 0 ${largeArc} 1 ${x2.toFixed(2)},${y2.toFixed(2)} Z`;
}

/**
 * PerfMemorySection is the task-338 memory observability block inside the
 * monitoring detail card: a WS time series over the existing perf samples
 * (read-only, no sampling cost) and a heap pie rendered right after the
 * sample button runs, with the pprof file kept for export.
 */
export function PerfMemorySection({ busy, apply }: { busy: boolean; apply: ApplyFn }) {
  const t = useT();
  const [windowMinutes, setWindowMinutes] = useState(180);
  const [series, setSeries] = useState<PerfTimeSeriesView | null>(null);
  const [breakdown, setBreakdown] = useState<HeapBreakdownView | null>(null);
  const [exportPath, setExportPath] = useState("");
  const [heapError, setHeapError] = useState("");

  const loadSeries = useCallback(async (minutes: number) => {
    try {
      setSeries(await app.PerfTimeSeries(minutes));
    } catch {
      setSeries(null);
    }
  }, []);

  useEffect(() => {
    void loadSeries(windowMinutes);
  }, [windowMinutes, loadSeries]);

  useEffect(() => {
    void (async () => {
      try {
        setExportPath(await app.HeapBreakdownPath());
      } catch {
        setExportPath("");
      }
    })();
  }, []);

  const sampleHeap = () =>
    void apply(async () => {
      setHeapError("");
      try {
        const view = await app.SampleHeapBreakdown();
        setBreakdown(view);
        setExportPath(view.path);
      } catch (err) {
        // A failed sample keeps the panel usable: show why instead of a blank pie.
        setBreakdown(null);
        setHeapError(String(err));
      }
    });

  const wsValues = (series?.points ?? []).map((point) => point.workingSetMb);
  const wsMin = wsValues.length ? Math.min(...wsValues) : 0;
  const wsMax = wsValues.length ? Math.max(...wsValues) : 0;
  const categories = (breakdown?.categories ?? []).filter((category) => category.bytes > 0);

  return (
    <div className="perf-memory-section">
      {/* --- WS time series (read-only over existing samples) --- */}
      <div className="perf-memory-section__block">
        <div className="perf-memory-section__head">
          <span className="settings-field__hint-line">{t("settings.perfMonitor.chartTitle")}</span>
          <span className="perf-memory-section__actions">
            <SettingsOptions layout="field" className="set-seg perf-memory-section__windows">
              {WINDOWS.map((entry) => (
                <button
                  key={entry.minutes}
                  type="button"
                  className={`set-seg__btn${windowMinutes === entry.minutes ? " set-seg__btn--on" : ""}`}
                  disabled={busy}
                  onClick={() => setWindowMinutes(entry.minutes)}
                >
                  {t(entry.labelKey)}
                </button>
              ))}
            </SettingsOptions>
            <button type="button" className="btn btn--small" disabled={busy} onClick={() => void loadSeries(windowMinutes)}>
              {t("settings.perfMonitor.chartRefresh")}
            </button>
          </span>
        </div>
        {series && !series.enabled ? (
          <p className="settings-field__hint-line">{t("settings.perfMonitor.chartSamplerOff")}</p>
        ) : null}
        {!series || !series.available ? (
          <p className="settings-field__hint-line">{t("settings.perfMonitor.chartNoSamples")}</p>
        ) : (
          <>
            <div className="perf-memory-section__chart-row">
              {/* Task 355: Y axis in MB (max / mid / min) beside the curve —
                  before this, the series had only the min/max footer note and
                  no way to read an arbitrary point. */}
              <div className="perf-memory-section__yaxis" aria-hidden="true">
                <span>{`${wsMax.toFixed(1)} MB`}</span>
                <span>{((wsMax + wsMin) / 2).toFixed(1)}</span>
                <span>{wsMin.toFixed(1)}</span>
              </div>
              <svg className="perf-memory-section__chart" viewBox="0 0 600 120" preserveAspectRatio="none" role="img" aria-label={t("settings.perfMonitor.chartTitle")}>
                <line className="perf-memory-section__grid" x1="0" y1="6" x2="600" y2="6" vectorEffect="non-scaling-stroke" />
                <line className="perf-memory-section__grid" x1="0" y1="60" x2="600" y2="60" vectorEffect="non-scaling-stroke" />
                <polyline
                  points={wsPolyline(wsValues)}
                  fill="none"
                  stroke="var(--accent)"
                  strokeWidth={2}
                  vectorEffect="non-scaling-stroke"
                />
              </svg>
            </div>
            <div className="perf-memory-section__axis">
              <span>{`max ${wsMax.toFixed(1)} MB`}</span>
              <span>{`${series.points.length} × ${series.intervalSeconds}s`}</span>
              <span>{`min ${wsMin.toFixed(1)} MB`}</span>
            </div>
          </>
        )}
      </div>

      {/* --- Heap pie (sample → render, pprof kept for export) --- */}
      <div className="perf-memory-section__block">
        <div className="perf-memory-section__head">
          <span className="settings-field__hint-line">{t("settings.perfMonitor.heapPieTitle")}</span>
          <button type="button" className="btn btn--sm" disabled={busy} onClick={sampleHeap}>
            {t("settings.perfHeap.sampleAndChart")}
          </button>
        </div>
        {heapError ? (
          <p className="settings-field__hint-line" role="alert">
            {t("settings.perfHeap.sampleFailed", { error: heapError })}
          </p>
        ) : null}
        {breakdown && categories.length > 0 ? (
          <div className="perf-memory-section__pie-row">
            <svg className="perf-memory-section__pie" viewBox="0 0 120 120" width="120" height="120" preserveAspectRatio="xMidYMid meet" role="img" aria-label={t("settings.perfMonitor.heapPieTitle")}>
              {(() => {
                let cursor = 0;
                return categories.map((category) => {
                  const start = cursor;
                  cursor += category.percent;
                  return (
                    <path key={category.key} d={pieArc(start, cursor)} fill={CATEGORY_COLORS[category.key] ?? "var(--fg-dim)"} stroke="var(--bg-elev)" strokeWidth={1}>
                      <title>{`${category.name} ${category.percent}%`}</title>
                    </path>
                  );
                });
              })()}
            </svg>
            <ul className="perf-memory-section__legend">
              {categories.map((category) => (
                <li key={category.key} className="perf-memory-section__legend-row">
                  <span className="perf-memory-section__swatch" style={{ background: CATEGORY_COLORS[category.key] ?? "var(--fg-dim)" }} />
                  <span className="perf-memory-section__legend-name">
                    {t(`settings.perfHeap.cat.${category.key as HeapCatKey}`)}
                  </span>
                  <span className="perf-memory-section__legend-value">{`${category.percent}% · ${formatBytes(category.bytes)}`}</span>
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <p className="settings-field__hint-line">{t("settings.perfHeap.noData")}</p>
        )}
        {exportPath ? (
          <p className="settings-field__hint-line perf-memory-section__export" title={exportPath}>
            {t("settings.perfHeap.exported", { path: exportPath })}
          </p>
        ) : null}
      </div>
    </div>
  );
}
