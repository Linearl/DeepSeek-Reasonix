// tl1227 — DAG 日志迁移动作卡（实验室 → 存储组，kind: action 首例）。
//
// 两步确认照 805 SessionMigrationSection 的范式：第一次点「扫描」只读枚举待迁移
// 会话并列出清单（体积 / 消息数 / 是否正在使用），第二次点「开始迁移」才落盘。
// 进度走 runtime 事件逐条推送；每个会话迁完由后端从磁盘再校验一次（roles 逐条
// + digest），失败会带原因返回且原字节省在 *.dagbak-tl1227。
//
// 纯展示 + 调用；一切安全边界在后端 dag_migration.go 与
// internal/agent.MigrateSessionDAGLog 内（本进程打开的会话一律跳过）。
import { useEffect, useState } from "react";
import { app, type DagMigrationRunView, type DagMigrationScanView } from "../lib/bridge";
import type { Translator } from "../lib/i18n";

type Phase = "idle" | "scanning" | "confirming" | "running" | "done";

const DAG_MIGRATION_PROGRESS_EVENT = "dag-migration:progress";

function humanBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

export function DagMigrationSection({ t, busy, apply }: { t: Translator; busy: boolean; apply: (fn: () => Promise<unknown>) => Promise<boolean> }) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [scan, setScan] = useState<DagMigrationScanView | null>(null);
  const [result, setResult] = useState<DagMigrationRunView | null>(null);
  const [progressLine, setProgressLine] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (phase !== "running") return;
    const off = window.runtime?.EventsOn(DAG_MIGRATION_PROGRESS_EVENT, (...args: unknown[]) => {
      const payload = args[0] as { index?: number; total?: number; label?: string; status?: string } | undefined;
      if (!payload || typeof payload.label !== "string") return;
      setProgressLine(`${payload.index ?? 0}/${payload.total ?? 0} · ${payload.status ?? ""} · ${payload.label}`);
    });
    return () => off?.();
  }, [phase]);

  const scanNow = async () => {
    setError("");
    setResult(null);
    setProgressLine("");
    setPhase("scanning");
    try {
      const next = await app.ScanDagMigration();
      setScan(next);
      setPhase("confirming");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase("idle");
    }
  };

  const run = async () => {
    setError("");
    setProgressLine("");
    setPhase("running");
    await apply(async () => {
      try {
        const next = await app.RunDagMigration();
        setResult(next);
        setPhase("done");
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        setPhase("done");
      }
    });
  };

  const reset = () => {
    setPhase("idle");
    setScan(null);
    setResult(null);
    setProgressLine("");
    setError("");
  };

  const pending = scan?.sessions.filter((s) => s.status === "pending") ?? [];
  const activeRows = scan?.sessions.filter((s) => s.status === "active") ?? [];
  const pendingBytes = pending.reduce((sum, s) => sum + (s.logBytes || 0), 0);

  return (
    <div data-dag-migration="">
      <p className="settings-field__hint-line">{t("settings.dagMigration.hint")}</p>
      {error !== "" && <p className="settings-field__hint-line">{error}</p>}

      {phase === "idle" && (
        <div className="settings-actions">
          <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={() => void scanNow()}>
            {t("settings.dagMigration.scan")}
          </button>
        </div>
      )}

      {phase === "scanning" && <p className="settings-field__hint-line">{t("settings.dagMigration.scanning")}</p>}

      {phase === "confirming" && scan && (
        <div data-dag-migration-confirm="">
          <p className="settings-field__hint-line">
            {scan.error
              ? scan.error
              : pending.length === 0
                ? t("settings.dagMigration.none")
                : t("settings.dagMigration.summary", {
                    pending: pending.length,
                    size: humanBytes(pendingBytes),
                    backups: scan.backups,
                  })}
          </p>
          {pending.map((s) => (
            <p key={s.sessionPath} className="settings-field__hint-line">{`· ${humanBytes(s.logBytes)} · ${s.messages} · ${s.label}`}</p>
          ))}
          {activeRows.length > 0 && (
            <p className="settings-field__hint-line">{t("settings.dagMigration.activeNote", { n: activeRows.length })}</p>
          )}
          {pending.length > 0 && <p className="settings-field__hint-line">{t("settings.dagMigration.backupNote")}</p>}
          <div className="settings-actions">
            {pending.length > 0 && (
              <button type="button" className="btn btn--small" disabled={busy} onClick={() => void run()}>
                {t("settings.dagMigration.confirm")}
              </button>
            )}
            <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={reset}>
              {t("settings.dagMigration.cancel")}
            </button>
          </div>
        </div>
      )}

      {phase === "running" && (
        <div data-dag-migration-running="">
          <p className="settings-field__hint-line">{t("settings.dagMigration.running")}</p>
          <progress aria-label={t("settings.dagMigration.running")} />
          {progressLine !== "" && <p className="settings-field__hint-line">{progressLine}</p>}
        </div>
      )}

      {phase === "done" && (
        <div data-dag-migration-result="">
          {result && (
            <p className="settings-field__hint-line">
              {t("settings.dagMigration.done", {
                migrated: result.migrated,
                active: result.active,
                skipped: result.skipped,
                failed: result.failed,
              })}
            </p>
          )}
          {result && result.sessions.some((s) => s.status === "failed") && (
            <>
              {result.sessions
                .filter((s) => s.status === "failed")
                .map((s) => (
                  <p key={s.sessionPath} className="settings-field__hint-line">{`· ${s.label} · ${s.detail ?? ""}`}</p>
                ))}
            </>
          )}
          <div className="settings-actions">
            <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={() => void scanNow()}>
              {t("settings.dagMigration.rescan")}
            </button>
            <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={reset}>
              {t("settings.dagMigration.cancel")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
