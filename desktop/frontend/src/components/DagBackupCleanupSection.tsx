// tl1287⑤ — 迁移备份清理（会话存储卡内，紧随 DAG 迁移卡）。
//
// 前提两条缺一不可（后端强制，前端照实展示）：迁移检查归零 + 至少保留最近
// 一份（*.dagbak-tl1227 是 schema-2 原件，误清即永远回不到迁移前）。其余按
// 时间从新到旧删。删除必须带确认，且确认弹层逐行列出将删文件与体量；
// run 在执行时现算清单（不信任 plan 时刻的列表），与迁移互斥。
// 纯展示 + 调用；安全边界在后端 dag_migration.go。
//
// tl1573-r2（20261114 用户实测三缺陷返修）：
//  ① 扫描后页面加载失败——崩点在同卡 DagMigrationSection（nil-slice wire），
//    本文件不动流程，只把 plan 数组读数全部 nil 兜底（同族防线）；
//  ② 记录行字号过大——确认清单行挂 dag-backup-cleanup__record（--text-xs
//    数据档），卡内状态行统一 --text-sm（styles.css tl1573-r2 块）；
//  ③ 清理结果改弹出面板+表格——done 态不再内联，改 modal 家族骨架
//    （.modal-backdrop/.modal，980 详情弹窗先例）+ 逐行表格（标题/时间/
//    体积/状态）；行数据吃后端 rows（f735633b6），旧后端无 rows 时由 plan
//    清单合成行（cleanupResultRows，nil 家族防线的一部分）。
//    plan 只读清单→逐行确认→归零门→执行 的原语义零变化。
import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { app, type DagBackupCleanupPlanView, type DagBackupCleanupRunRow, type DagBackupCleanupRunView } from "../lib/bridge";
import type { Translator } from "../lib/i18n";
import { humanBytes } from "./DagMigrationSection";

type Phase = "idle" | "planning" | "confirming" | "running" | "done";

function formatStamp(modifiedAt: number): string {
  const date = new Date(modifiedAt);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** 表格「标题」列显示文件名；完整路径挂 title（弹面板宽度有限，全路径截断）。 */
function backupBaseName(path: string): string {
  const index = Math.max(path.lastIndexOf("\\"), path.lastIndexOf("/"));
  return index >= 0 ? path.slice(index + 1) : path;
}

const OUTCOME_KEYS = {
  deleted: "settings.dagBackupCleanup.rowDeleted",
  failed: "settings.dagBackupCleanup.rowFailed",
  kept: "settings.dagBackupCleanup.rowKept",
} as const;

/**
 * tl1573-r2 缺陷③：结果表格的行数据。后端 rows 在场直接用（执行时现算的
 * 真相源）；旧后端（包92 及更早，无 rows 字段）用 plan 清单合成——保留份取
 * keepPath，将删份默认已删，details 里点了名的算失败。导出供钉直击。
 */
export function cleanupResultRows(result: DagBackupCleanupRunView, plan: DagBackupCleanupPlanView | null): DagBackupCleanupRunRow[] {
  if (result.rows) return result.rows;
  const rows: DagBackupCleanupRunRow[] = [];
  if (!plan) return rows;
  const kept = plan.backups.find((b) => b.path === plan.keepPath);
  if (kept) rows.push({ path: kept.path, bytes: kept.bytes, modifiedAt: kept.modifiedAt, outcome: "kept" });
  for (const b of plan.deletable) {
    const failed = (result.details ?? []).some((line) => line.startsWith(b.path));
    rows.push({ path: b.path, bytes: b.bytes, modifiedAt: b.modifiedAt, outcome: failed ? "failed" : "deleted" });
  }
  return rows;
}

// tl1337：showHint=false 时不渲染说明行——宿主子项行左列简述
// （settings.sessionStorage.backupGroupDesc）已含 dagbak/保留最近一份要点，
// 防左右重复；键与「保留最近一份」防线仍由 dag-backup-cleanup.test.ts 钉住。
export function DagBackupCleanupSection({ t, busy, showHint = true }: { t: Translator; busy: boolean; showHint?: boolean }) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [plan, setPlan] = useState<DagBackupCleanupPlanView | null>(null);
  const [result, setResult] = useState<DagBackupCleanupRunView | null>(null);
  const [error, setError] = useState("");

  const scan = async () => {
    setError("");
    setResult(null);
    setPhase("planning");
    try {
      const next = await app.PlanDagBackupCleanup();
      // tl1573-r2 缺陷①同族防线：旧后端 nil slice 可送 null 数组，确认层
      // 的 length/reduce/map 读数全部兜成空数组（新后端恒发 []，见
      // dag_migration.go dagBackupCleanupScan 的源头初始化）。
      setPlan({
        ...next,
        backups: next.backups ?? [],
        deletable: next.deletable ?? [],
      });
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

  // tl1573-r2 缺陷③：结果弹面板的焦点与 Escape 管理（LabBulkToggleDialog 先例：
  // 打开时聚焦关闭键、Escape 捕获关闭、卸载恢复原焦点）。
  const titleId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const restoreRef = useRef<HTMLElement | null>(null);
  const resultOpen = phase === "done" && result !== null;
  useEffect(() => {
    if (!resultOpen) return undefined;
    restoreRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      reset();
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      if (restoreRef.current?.isConnected) restoreRef.current?.focus();
    };
    // reset 是每轮渲染重建的闭包；这里只需要随开/关挂卸一次。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resultOpen]);

  return (
    <div data-dag-backup-cleanup="">
      {showHint && <p className="settings-field__hint-line">{t("settings.dagBackupCleanup.hint")}</p>}
      {error !== "" && <p className="settings-field__hint-line">{error}</p>}

      {phase === "idle" && (
        <div className="settings-actions">
          {/* tl1573: 入口按钮改为显式的「DAG 批量清理」——旧标签「扫描迁移备份」
              让清理动作在页面上不可见（用户视角=只有两个扫描按钮、无清理入口）。
              行为不变：点击仍是先 plan（只读清单）→ 逐行确认 → 才删，危险操作
              的两步确认与「保留最近一份」防线原样。done 态的重扫按钮保持
              「扫描迁移备份」语义（那里确实是重扫，不是再次入口）。 */}
          <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={() => void scan()}>
            {t("settings.dagBackupCleanup.batchClean")}
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
                // tl1573-r2 缺陷②：记录行挂数据档（--text-xs），不再继承
                // body 的 --text-base 14px（与左列 10px 简述同排对比突兀）。
                <p key={b.path} className="settings-field__hint-line dag-backup-cleanup__record">{`· ${humanBytes(b.bytes)} · ${formatStamp(b.modifiedAt)} · ${b.path}`}</p>
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

      {phase === "done" && result && createPortal(
        // tl1573-r2 缺陷③：结果改弹出面板（modal 家族骨架）+ 逐行表格
        // （标题/时间/体积/状态）。原内联 done 回执的全部读数（删除份数/释放
        // 体量/失败数/重扫/关闭）原样搬入，[data-dag-backup-cleanup-result]
        // 锚点随面板走。面板在 settings 覆盖层之上（--z-popover-elevated，
        // 980 详情弹窗先例）。
        <div
          className="modal-backdrop dag-cleanup-result-backdrop"
          role="presentation"
          data-dag-backup-cleanup-result=""
          onClick={reset}
        >
          <div
            className="modal dag-cleanup-result"
            role="dialog"
            aria-modal="true"
            aria-labelledby={titleId}
            onClick={(e) => e.stopPropagation()}
          >
            <div className="dag-cleanup-result__bar">
              <h3 id={titleId} className="dag-cleanup-result__title">{t("settings.dagBackupCleanup.resultTitle")}</h3>
              <button ref={closeRef} type="button" className="btn btn--sm" onClick={reset} aria-label={t("settings.dagBackupCleanup.cancel")}>
                {t("settings.dagBackupCleanup.cancel")}
              </button>
            </div>
            <p className="settings-field__hint-line dag-cleanup-result__summary">
              {t("settings.dagBackupCleanup.done", {
                deleted: result.deleted,
                size: humanBytes(result.deletedBytes),
                failed: result.failed,
              })}
            </p>
            <div className="dag-cleanup-result__scroll">
              <table className="dag-cleanup-result__table">
                <thead>
                  <tr>
                    <th scope="col">{t("settings.dagBackupCleanup.colName")}</th>
                    <th scope="col">{t("settings.dagBackupCleanup.colTime")}</th>
                    <th scope="col">{t("settings.dagBackupCleanup.colSize")}</th>
                    <th scope="col">{t("settings.dagBackupCleanup.colStatus")}</th>
                  </tr>
                </thead>
                <tbody>
                  {cleanupResultRows(result, plan).map((row) => (
                    <tr key={row.path}>
                      <td className="dag-cleanup-result__name" title={row.path}>{backupBaseName(row.path)}</td>
                      <td>{formatStamp(row.modifiedAt)}</td>
                      <td>{humanBytes(row.bytes)}</td>
                      <td className={`dag-cleanup-result__status dag-cleanup-result__status--${row.outcome}`} title={row.detail ?? undefined}>
                        {t(OUTCOME_KEYS[row.outcome])}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="modal__actions">
              <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={() => void scan()}>
                {t("settings.dagBackupCleanup.scan")}
              </button>
              <button type="button" className="btn btn--secondary btn--small" disabled={busy} onClick={reset}>
                {t("settings.dagBackupCleanup.cancel")}
              </button>
            </div>
          </div>
        </div>,
        document.body,
      )}
    </div>
  );
}
