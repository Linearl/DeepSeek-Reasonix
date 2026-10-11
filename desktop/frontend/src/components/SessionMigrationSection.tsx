// 任务 805 — storage migration section (settings → 存储版本 card). Hosts both
// routes the storage strategy promised: the v4→v3 backfill (this fork's own
// mirror) and the v5 import (the direction upstream abandoned, task 773's
// kernel). Two-step confirm: the first click loads the dry-run plan and shows
// what would happen (mode, per-action counts, backup note); the second click
// executes. Progress arrives as runtime events; the receipt doubles as the
// restore guidance (backup dir + losses). Pure display + calls; the kernels
// own every safety boundary.

import { useEffect, useState } from "react";
import { app, type SessionStorageMigrationPlanView, type SessionStorageMigrationReportView } from "../lib/bridge";
import type { Translator } from "../lib/i18n";

type MigrationKind = "v4-backfill" | "v5-import";
type MigrationPhase = "idle" | "confirming" | "running" | "done" | "failed";

const MIGRATION_PROGRESS_EVENT = "sessionStorageMigrationProgress";

export function SessionMigrationSection({ t, onRunningChange }: { t: Translator; onRunningChange: (running: boolean) => void }) {
  const [phase, setPhase] = useState<MigrationPhase>("idle");
  const [kind, setKind] = useState<MigrationKind>("v4-backfill");
  const [plan, setPlan] = useState<SessionStorageMigrationPlanView | null>(null);
  const [report, setReport] = useState<SessionStorageMigrationReportView | null>(null);
  const [error, setError] = useState("");
  const [progressLine, setProgressLine] = useState("");
  const [v5Available, setV5Available] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void app.SessionV5StoreAvailable().then(setV5Available).catch(() => setV5Available(false));
  }, []);
  useEffect(() => {
    onRunningChange(phase === "running");
  }, [phase, onRunningChange]);
  useEffect(() => {
    if (phase !== "running") return;
    const off = window.runtime?.EventsOn(MIGRATION_PROGRESS_EVENT, (...args: unknown[]) => {
      const payload = args[0] as { line?: string } | undefined;
      if (payload && typeof payload.line === "string") setProgressLine(payload.line);
    });
    return () => off?.();
  }, [phase]);

  const open = async (which: MigrationKind) => {
    setBusy(true);
    setError("");
    setReport(null);
    setProgressLine("");
    try {
      const next = which === "v4-backfill" ? await app.PlanSessionV4Backfill() : await app.PlanSessionV5Import();
      setKind(which);
      setPlan(next);
      setPhase("confirming");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const run = async () => {
    setBusy(true);
    setPhase("running");
    setError("");
    try {
      const receipt = kind === "v4-backfill" ? await app.RunSessionV4Backfill() : await app.RunSessionV5Import();
      setReport(receipt);
      setPhase(receipt.verified ? "done" : "failed");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase("failed");
    } finally {
      setBusy(false);
    }
  };

  const reset = () => {
    setPhase("idle");
    setPlan(null);
    setReport(null);
    setError("");
    setProgressLine("");
  };

  return (
    <div data-session-migration="">
      {error && phase !== "running" && <p className="settings-field__hint-line">{error}</p>}
      <p className="settings-field__hint-line">{t("settings.migration.hint")}</p>
      {phase === "idle" && (
        <div className="settings-actions">
          <button className="btn btn--secondary btn--small" disabled={busy} onClick={() => void open("v4-backfill")}>
            {t("settings.migration.action")}
          </button>
          {v5Available && (
            <button className="btn btn--secondary btn--small" disabled={busy} onClick={() => void open("v5-import")}>
              {t("settings.migration.v5Action")}
            </button>
          )}
        </div>
      )}
      {v5Available && phase === "idle" && (
        <p className="settings-field__hint-line">{t("settings.migration.v5Hint")}</p>
      )}
      {phase === "confirming" && plan && (
        <div data-session-migration-confirm="">
          <p className="settings-field__hint-line">
            {plan.kind === "v4-backfill" &&
              plan.mode === "full"
              ? `${t("settings.migration.mode.full")} · `
              : plan.kind === "v4-backfill"
                ? `${t("settings.migration.mode.incremental")} · `
                : ""}
            {t("settings.migration.planSummary", {
              append: plan.appendCount,
              rebuild: plan.rebuildCount,
              skip: plan.skipCount,
              conflict: plan.conflictCount,
              fail: plan.failCount,
            })}
          </p>
          <p className="settings-field__hint-line">{t("settings.migration.backupNote")}</p>
          <div className="settings-actions">
            <button className="btn btn--small" disabled={busy} onClick={() => void run()}>
              {t("settings.migration.confirm")}
            </button>
            <button className="btn btn--secondary btn--small" disabled={busy} onClick={reset}>
              {t("settings.migration.cancel")}
            </button>
          </div>
        </div>
      )}
      {phase === "running" && (
        <div data-session-migration-running="">
          <p className="settings-field__hint-line">{t("settings.migration.running")}</p>
          <progress aria-label={t("settings.migration.running")} />
          {progressLine && <p className="settings-field__hint-line">{progressLine}</p>}
        </div>
      )}
      {(phase === "done" || phase === "failed") && (
        <div data-session-migration-result="">
          <p className="settings-field__hint-line">
            {phase === "done" ? t("settings.migration.done") : t("settings.migration.failed")}
          </p>
          {report && report.backup.dir && (
            <p className="settings-field__hint-line">{t("settings.migration.backupDone", { dir: report.backup.dir })}</p>
          )}
          {phase === "done" && report && (
            <p className="settings-field__hint-line">{t("settings.migration.restoreNote")}</p>
          )}
          <div className="settings-actions">
            <button className="btn btn--secondary btn--small" onClick={reset}>
              {t("settings.migration.cancel")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
