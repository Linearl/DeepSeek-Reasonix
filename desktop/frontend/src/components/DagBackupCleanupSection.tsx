// tl1287⑤ — 迁移备份清理（会话存储卡内，紧随 DAG 迁移卡）。
//
// 前提两条缺一不可（后端强制，前端照实展示）：迁移检查归零 + 至少保留最近
// 一份（*.dagbak-tl1227 是 schema-2 原件，误清即永远回不到迁移前）。其余按
// 时间从新到旧删。删除必须带确认，且确认弹层逐行列出将删文件与体量；
// run 在执行时现算清单（不信任 plan 时刻的列表），与迁移互斥。
// 纯展示 + 调用；安全边界在后端 dag_migration.go。
import { useState } from "react";
import { app, type DagBackupCleanupPlanView } from "../lib/bridge";
import type { Translator } from "../lib/i18n";
import { humanBytes } from "./DagMigrationSection";

type Phase = "idle" | "planning" | "confirming" | "running" | "done";

function formatStamp(modifiedAt: number): string {
  const date = new Date(modifiedAt);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function DagBackupCleanupSection({ t, busy }: { t: Translator; busy: boolean }) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [plan, setPlan] = useState<DagBackupCleanupPlanView | null>(null);
  const [result, setResult] = useState<{ deleted: number; deletedBytes: number; failed: number } | null>(null);
  const [error, setError] = useState("");

  const scan = async () => {
    setError("");
    setResult(null);
    setPhase("planning");
    try {
      const next = await app.PlanDagBackupCleanup();
      setPlan(next);
      setPhase("confirming");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase("idle");
    }
  };

  const run = async () => {
    setPhase("running");
    setError("");
    try {
      const receipt = await app.RunDagBackupCleanup();
      setResult(receipt);
      setPhase("done");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase("confirming");
    }
  };

  const reset = () => {
    setPhase("idle");
    setPlan(null);
    setResult(null);
    setError("");
  };

  return (
    <div data-dag-backup-cleanup="">
      <p className="settings-field__hint-line">{t("settings.dagBackupCleanup.hint")}</p>
      {error !== "" && <p className="settings-field__hint-line">{error}</p>}

      {phase === "idle" && (
        <div className="settings-actions">
          <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={() => void scan()}>
            {t("settings.dagBackupCleanup.scan")}
          </button>
        </div>
      )}

      {phase === "planning" && <p className="settings-field__hint-line">{t("settings.dagBackupCleanup.scanning")}</p>}

      {phase === "confirming" && plan && (
        <div data-dag-backup-cleanup-confirm="">
          {plan.error ? (
            <p className="settings-field__hint-line">{plan.error}</p>
          ) : plan.remainingMigrations > 0 ? (
            <p className="settings-field__hint-line">{t("settings.dagBackupCleanup.blocked", { n: plan.remainingMigrations })}</p>
          ) : plan.deletable.length === 0 ? (
            <p className="settings-field__hint-line">{t("settings.dagBackupCleanup.none")}</p>
          ) : (
            <>
              <p className="settings-field__hint-line">
                {t("settings.dagBackupCleanup.summary", {
                  total: plan.backups.length,
                  totalSize: humanBytes(plan.backups.reduce((sum, b) => sum + b.bytes, 0)),
                  del: plan.deletable.length,
                  delSize: humanBytes(plan.deleteBytes),
                  keepSize: humanBytes(plan.keepBytes ?? 0),
                })}
              </p>
              {plan.deletable.map((b) => (
                <p key={b.path} className="settings-field__hint-line">{`· ${humanBytes(b.bytes)} · ${formatStamp(b.modifiedAt)} · ${b.path}`}</p>
              ))}
            </>
          )}
          <div className="settings-actions">
            {plan.error === "" && plan.remainingMigrations === 0 && plan.deletable.length > 0 && (
              <button type="button" className="btn btn--small" disabled={busy} onClick={() => void run()}>
                {t("settings.dagBackupCleanup.confirm", { del: plan.deletable.length })}
              </button>
            )}
            <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={reset}>
              {t("settings.dagBackupCleanup.cancel")}
            </button>
          </div>
        </div>
      )}

      {phase === "running" && <p className="settings-field__hint-line">{t("settings.dagBackupCleanup.running")}</p>}

      {phase === "done" && result && (
        <div data-dag-backup-cleanup-result="">
          <p className="settings-field__hint-line">
            {t("settings.dagBackupCleanup.done", {
              deleted: result.deleted,
              size: humanBytes(result.deletedBytes),
              failed: result.failed,
            })}
          </p>
          <div className="settings-actions">
            <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={() => void scan()}>
              {t("settings.dagBackupCleanup.scan")}
            </button>
            <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={reset}>
              {t("settings.dagBackupCleanup.cancel")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
