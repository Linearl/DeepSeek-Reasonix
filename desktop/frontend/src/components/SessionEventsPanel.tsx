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
 *
 * tl 1076: the settings page's inline card is now a compact summary (over-count
 * + repair-all + open-panel button); the full per-session list with its own
 * threshold inputs and per-row repair lives in SessionEventsRepairDialog, a
 * ≥760px modal dialog. Both surfaces share the same backend API and the same
 * refresh/inventory state — they render the same `inventory` object, so the
 * dialog's row count always equals the summary card's count.
 */
export function SessionEventsPanel({ mode, busy, apply }: { mode: RotationMode; busy: boolean; apply: ApplyFn }) {
  const t = useT();
  const [inventory, setInventory] = useState<SessionEventsInventoryView | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [results, setResults] = useState<SessionEventsCompactResult[]>([]);
  const [factor, setFactor] = useState("4");
  const [capMB, setCapMB] = useState("0");
  const [dialogOpen, setDialogOpen] = useState(false);

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
    // Sync only when the server values themselves change: keying on the
    // inventory object would rewrite the inputs on every refresh (including
    // after a repair), clobbering in-progress typing (review finding).
    if (factor === String(inventory?.factor ?? 4) && capMB === String(inventory?.capMB ?? 0)) return;
    if (!inventory) return;
    setFactor(String(inventory.factor));
    setCapMB(String(inventory.capMB));
    // Only re-sync while the user is not editing (auto mode inputs are unmounted otherwise).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inventory?.factor, inventory?.capMB]);

  const compactOne = (path: string) =>
    void apply(async () => {
      try {
        // The backend resolves busy/failures into the result itself (it never
        // rejects with a partial result, which Wails would discard), so the
        // row renders skipped/error/done from res; the catch below only covers
        // a transport-level rejection.
        const res = await app.CompactSessionEvents(path);
        setResults((prev) => [res, ...prev].slice(0, 20));
      } catch (err) {
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
              max={1073741824}
              step={1}
              value={capMB}
              disabled={busy}
              onChange={(e) => setCapMB(e.target.value)}
            />
          </label>
          <button
            type="button"
            className="btn btn--primary btn--sm"
            disabled={busy}
            onClick={() =>
              void apply(async () => {
                await app.SetEventsRotation(Number(factor), Number(capMB));
                // Refresh the marks right away: the over-limit judgment basis
                // just changed, so the card must not sit stale (review finding).
                await refresh();
              })
            }
          >
            {t("settings.eventsRotation.apply")}
          </button>
        </div>
      ) : null}

      {/* tl 1076: the settings page's inline card is now a summary — the
          full per-session list and its own threshold controls live in the
          repair dialog opened by the button below. The settings page keeps
          the over-count, refresh, and repair-all shortcuts so the common
          one-click "fix everything" stays reachable without opening a panel. */}
      <div className="events-rotation-panel__card">
        <div className="events-rotation-panel__card-head">
          <span className="settings-field__hint-line">
            {t("settings.eventsRotation.card.overCount", { count: inventory?.overCount ?? 0 })}
          </span>
          <span className="events-rotation-panel__actions">
            <button type="button" className="btn btn--secondary btn--small" disabled={busy || !inventory} onClick={() => void refresh()}>
              {t("settings.eventsRotation.card.refresh")}
            </button>
            <button
              type="button"
              className="btn btn--primary btn--small"
              disabled={busy || !inventory || inventory.overCount === 0}
              onClick={compactAll}
            >
              {t("settings.eventsRotation.card.repairAll")}
            </button>
            {/* tl 1076 acceptance ②: the settings right column is too narrow
                for the full list; this button opens the same data in a wide
                (≥760px) dialog so the repair panel has room for every row. */}
            <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={() => setDialogOpen(true)}>
              {t("settings.eventsRotation.card.openPanel")}
            </button>
          </span>
        </div>
        {loadFailed ? (
          <p className="settings-field__hint-line" role="alert">{t("settings.eventsRotation.card.failed")}</p>
        ) : !inventory ? (
          <p className="settings-field__hint-line">{t("settings.loading")}</p>
        ) : (
          <p className="settings-field__hint-line">
            {t("settings.eventsRotation.card.summary", { total: inventory.entries.length })}
          </p>
        )}
        {/* tl 1076: the top-three offender list is now the dialog's job (the
            settings right column is too narrow); the summary card keeps the
            over-count and the repair-all shortcut, and points to the dialog
            for per-session repair. */}
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

      {dialogOpen ? (
        <SessionEventsRepairDialog
          mode={mode}
          busy={busy}
          apply={apply}
          inventory={inventory}
          loadFailed={loadFailed}
          results={results}
          factor={factor}
          capMB={capMB}
          setFactor={setFactor}
          setCapMB={setCapMB}
          onRefresh={refresh}
          onCompactOne={compactOne}
          onCompactAll={compactAll}
          onClose={() => setDialogOpen(false)}
        />
      ) : null}
    </div>
  );
}

/**
 * SessionEventsRepairDialog is tl 1076's second-level repair panel: a modal
 * dialog (≥760px, overriding the shared 440px `.modal` cap) that renders the
 * same inventory/repair actions the pre-1076 inline card showed, but with room
 * for the full list, per-row status badges, and per-row repair buttons. It
 * reuses the parent panel's inventory object and callbacks — the summary card
 * and this dialog always agree on row counts and thresholds because they read
 * one state, not two.
 */
function SessionEventsRepairDialog({
  mode,
  busy,
  apply,
  inventory,
  loadFailed,
  results,
  factor,
  capMB,
  setFactor,
  setCapMB,
  onRefresh,
  onCompactOne,
  onCompactAll,
  onClose,
}: {
  mode: RotationMode;
  busy: boolean;
  apply: ApplyFn;
  inventory: SessionEventsInventoryView | null;
  loadFailed: boolean;
  results: SessionEventsCompactResult[];
  factor: string;
  capMB: string;
  setFactor: (v: string) => void;
  setCapMB: (v: string) => void;
  onRefresh: () => Promise<void>;
  onCompactOne: (path: string) => void;
  onCompactAll: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const allEntries = inventory?.entries ?? [];
  const overEntries = allEntries.filter((entry) => entry.overLimit);

  return (
    <div className="modal-backdrop events-repair-dialog-backdrop" role="presentation" onMouseDown={(e) => {
      if (e.target === e.currentTarget) onClose();
    }}>
      <div className="modal events-repair-dialog" role="dialog" aria-modal="true" aria-label={t("settings.eventsRotation.card.dialogTitle")}>
        <div className="modal__title events-repair-dialog__title">
          <span>{t("settings.eventsRotation.card.dialogTitle")}</span>
          <button type="button" className="btn btn--secondary btn--small" onClick={onClose}>
            {t("common.close")}
          </button>
        </div>
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
                max={1073741824}
                step={1}
                value={capMB}
                disabled={busy}
                onChange={(e) => setCapMB(e.target.value)}
              />
            </label>
            <button
              type="button"
              className="btn btn--primary btn--sm"
              disabled={busy}
              onClick={() =>
                void apply(async () => {
                  await app.SetEventsRotation(Number(factor), Number(capMB));
                  await onRefresh();
                })
              }
            >
              {t("settings.eventsRotation.apply")}
            </button>
          </div>
        ) : null}
        <div className="events-rotation-panel__card-head">
          <span className="settings-field__hint-line">
            {t("settings.eventsRotation.card.overCount", { count: inventory?.overCount ?? 0 })}
          </span>
          <span className="events-rotation-panel__actions">
            <button type="button" className="btn btn--secondary btn--small" disabled={busy || !inventory} onClick={() => void onRefresh()}>
              {t("settings.eventsRotation.card.refresh")}
            </button>
            <button
              type="button"
              className="btn btn--primary btn--small"
              disabled={busy || !inventory || inventory.overCount === 0}
              onClick={onCompactAll}
            >
              {t("settings.eventsRotation.card.repairAll")}
            </button>
          </span>
        </div>
        {loadFailed ? (
          <p className="settings-field__hint-line" role="alert">{t("settings.eventsRotation.card.failed")}</p>
        ) : !inventory ? (
          <p className="settings-field__hint-line">{t("settings.loading")}</p>
        ) : overEntries.length === 0 ? (
          <p className="settings-field__hint-line">{t("settings.eventsRotation.card.empty")}</p>
        ) : (
          <ul className="events-rotation-panel__list events-repair-dialog__list">
            {overEntries.map((entry) => (
              <li key={entry.path} className="events-rotation-panel__row">
                <span className="events-rotation-panel__name" title={entry.path}>
                  {entry.name || entry.path}
                </span>
                <span
                  className={`events-rotation-panel__status${entry.busy ? " events-rotation-panel__status--busy" : ""}`}
                >
                  {t(entry.busy ? "settings.eventsRotation.card.statusBusy" : "settings.eventsRotation.card.statusIdle")}
                </span>
                <span className="events-rotation-panel__stat">
                  {formatBytes(entry.eventsBytes)} · ×{entry.ratio.toFixed(1)}
                </span>
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  disabled={mode === "manual" ? busy : busy || entry.busy}
                  title={
                    entry.busy
                      ? t(
                          mode === "manual"
                            ? "settings.eventsRotation.card.busyHintManual"
                            : "settings.eventsRotation.card.busyHint",
                        )
                      : undefined
                  }
                  onClick={() => {
                    if (mode === "manual" && entry.busy && !window.confirm(t("settings.eventsRotation.card.busyConfirm"))) {
                      return;
                    }
                    onCompactOne(entry.path);
                  }}
                >
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
