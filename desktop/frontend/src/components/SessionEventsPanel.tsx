import { useCallback, useEffect, useState } from "react";

import { app, type SessionEventsInventoryView, type SessionEventsCompactResult } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { SettingsOptions } from "./SettingsOptions";

if (import.meta.env?.MODE) void import("./SessionEventsPanel.css");

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

const ROTATION_MODES = ["off", "manual", "auto"] as const;
// Literal union so the t() calls below resolve concrete i18n keys (the locale
// maps are strongly typed; a plain string would widen the template literal).
export type RotationMode = (typeof ROTATION_MODES)[number];

/**
 * SessionEventsPanel is the task-333 storage detail card: the three-way
 * rotation gate, the auto-mode thresholds, and the over-limit statistic card
 * with per-session and all-session repair actions. Every write goes through
 * the page's apply() so busy state and the settings reload stay consistent
 * with the rest of the lab panel.
 */
export function SessionEventsPanel({ mode, busy, apply }: { mode: RotationMode; busy: boolean; apply: ApplyFn }) {
  const t = useT();
  const [inventory, setInventory] = useState<SessionEventsInventoryView | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [results, setResults] = useState<SessionEventsCompactResult[]>([]);
  const [factor, setFactor] = useState("4");
  const [capMB, setCapMB] = useState("0");

  const refresh = useCallback(async () => {
    try {
      setInventory(await app.SessionEventsInventory());
      setLoadFailed(false);
    } catch {
      setInventory(null);
      setLoadFailed(true);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!inventory) return;
    setFactor(String(inventory.factor));
    setCapMB(String(inventory.capMB));
  }, [inventory]);

  const overEntries = (inventory?.entries ?? []).filter((entry) => entry.overLimit);
  const topThree = overEntries.slice(0, 3);

  const compactOne = (path: string) =>
    void apply(async () => {
      try {
        const res = await app.CompactSessionEvents(path);
        setResults((prev) => [res, ...prev].slice(0, 20));
      } catch (err) {
        // The bridge surfaces lease-busy and I/O failures as rejections with the
        // backend message; show it in the result list instead of swallowing.
        setResults((prev) =>
          [{ path, name: "", round: 1, before: 0, after: 0, freed: 0, elapsedMs: 0, skipped: false, error: String(err) }, ...prev].slice(0, 20),
        );
      }
      await refresh();
    });

  const compactAll = () =>
    void apply(async () => {
      const batch = await app.CompactAllSessionEvents(3);
      setResults((prev) => [...batch.reverse(), ...prev].slice(0, 40));
      await refresh();
    });

  return (
    <div className="events-rotation-panel">
      {/* Three-way gate: off skips the automatic rotation entirely, manual is
          today's default (built-in factor gate, explicit slimming only), auto
          lets the thresholds below drive the save-path gate. */}
      <SettingsOptionsLike mode={mode} busy={busy} apply={apply} />

      <p className="settings-field__hint-line">{t(`settings.eventsRotation.desc.${mode}`)}</p>

      {mode === "auto" ? (
        <div className="events-rotation-panel__thresholds">
          <label className="settings-field__hint-line">
            {t("settings.eventsRotation.factor")}
            <input
              className="mem-input events-rotation-panel__num"
              type="number"
              min={2}
              max={16}
              step={1}
              value={factor}
              disabled={busy}
              onChange={(e) => setFactor(e.target.value)}
            />
          </label>
          <label className="settings-field__hint-line">
            {t("settings.eventsRotation.cap")}
            <input
              className="mem-input events-rotation-panel__num"
              type="number"
              min={0}
              step={1}
              value={capMB}
              disabled={busy}
              onChange={(e) => setCapMB(e.target.value)}
            />
          </label>
          <button
            type="button"
            className="btn btn--sm"
            disabled={busy}
            onClick={() =>
              void apply(async () => {
                await app.SetEventsRotation(Number(factor), Number(capMB));
              })
            }
          >
            {t("settings.eventsRotation.apply")}
          </button>
        </div>
      ) : null}

      {/* Statistic card: over-limit total + the three largest offenders, with
          per-row repair and an all-session multi-round repair on top. */}
      <div className="events-rotation-panel__card">
        <div className="events-rotation-panel__card-head">
          <span className="settings-field__hint-line">
            {t("settings.eventsRotation.card.overCount", { count: inventory?.overCount ?? 0 })}
          </span>
          <span className="events-rotation-panel__actions">
            <button type="button" className="btn btn--small" disabled={busy || !inventory} onClick={() => void refresh()}>
              {t("settings.eventsRotation.card.refresh")}
            </button>
            <button
              type="button"
              className="btn btn--small"
              disabled={busy || !inventory || inventory.overCount === 0}
              onClick={compactAll}
            >
              {t("settings.eventsRotation.card.repairAll")}
            </button>
          </span>
        </div>
        {loadFailed ? (
          <p className="settings-field__hint-line" role="alert">{t("settings.eventsRotation.card.failed")}</p>
        ) : !inventory ? (
          <p className="settings-field__hint-line">{t("settings.loading")}</p>
        ) : topThree.length === 0 ? (
          <p className="settings-field__hint-line">{t("settings.eventsRotation.card.empty")}</p>
        ) : (
          <ul className="events-rotation-panel__list">
            {topThree.map((entry) => (
              <li key={entry.path} className="events-rotation-panel__row">
                <span className="events-rotation-panel__name" title={entry.name || entry.path}>
                  {entry.name || entry.path}
                </span>
                <span className="events-rotation-panel__stat">
                  {formatBytes(entry.eventsBytes)} · ×{entry.ratio.toFixed(1)}
                </span>
                <button type="button" className="btn btn--small" disabled={busy} onClick={() => compactOne(entry.path)}>
                  {t("settings.eventsRotation.card.repair")}
                </button>
              </li>
            ))}
          </ul>
        )}
        {results.length > 0 ? (
          <ul className="events-rotation-panel__results">
            {results.map((res, i) => (
              <li key={`${res.path}-${res.round}-${i}`} className="settings-field__hint-line">
                {res.skipped
                  ? t("settings.eventsRotation.card.skipped", { name: res.name || res.path })
                  : res.error
                    ? t("settings.eventsRotation.card.failedRow", { name: res.name || res.path, error: res.error })
                    : t("settings.eventsRotation.card.doneRow", {
                        name: res.name || res.path,
                        before: formatBytes(res.before),
                        after: formatBytes(res.after),
                        freed: formatBytes(res.freed),
                      })}
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </div>
  );
}

/** The three-way segmented control, kept local so the panel owns its layout. */
function SettingsOptionsLike({ mode, busy, apply }: { mode: RotationMode; busy: boolean; apply: ApplyFn }) {
  const t = useT();
  return (
    <SettingsOptions layout="field" className="set-seg">
      {ROTATION_MODES.map((candidate) => (
        <button
          key={candidate}
          type="button"
          className={`set-seg__btn${mode === candidate ? " set-seg__btn--on" : ""}`}
          disabled={busy}
          onClick={() =>
            void apply(async () => {
              await app.SetEventsAutoRotation(candidate);
            })
          }
        >
          {t(`settings.eventsRotation.mode.${candidate}`)}
        </button>
      ))}
    </SettingsOptions>
  );
}
