// 任务 1006 — 实验室批量开关弹窗（用户 2026-10-19 设计）。
//
// 范围三档（稳定 / 推荐及以上 / 可选及以上；无「全部」档——未稳定项不进
// 批量，仅可单项手动）× 行为两轴（开 / 关）。确认后调 LabBulkApply 单事务
// 写配置，随后给重启两选项：
//   - 立即重启 = app.RestartDesktop()；
//   - 稍后重启 = 关弹窗——配置已写入，下次启动生效，不打断在飞任务
//     （task 987「需重启明示」由此弹窗统一承接）。
//
// 护栏（与 tl 1005 矩阵联动，后端 PlanLabBulk 计算、此处只投影）：
//   ① 批量关含稳定档 → 确认前警示（StableInScope 非空且行为=关）；
//   ② 开发者模式关时批量关自动跳过稳定档 → 提示跳过清单（SkippedStable）。
//
// 计划每次切范围/行为实时拉取（LabBulkPlan 纯读），确认前用户能看到目标
// 数、护栏警示与逐项跳过原因——「写了什么、没写什么」全程可见。

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Translator } from "../lib/i18n";
import type { LabBulkPlanView } from "../lib/bridge";
import { app } from "../lib/bridge";
import { reportFrontendLog } from "../lib/frontendLog";

export type LabBulkScope = "stable" | "recommended" | "optional";
export type LabBulkBehavior = "on" | "off";

interface LabBulkToggleDialogProps {
  t: Translator;
  onClose: () => void;
}

/** 范围三档（顺序 = 弹窗内展示序；无「全部」档）。 */
export const LAB_BULK_SCOPES: readonly LabBulkScope[] = ["stable", "recommended", "optional"];

const SCOPE_LABEL_KEYS: Record<LabBulkScope, "settings.labBulk.scope.stable" | "settings.labBulk.scope.recommended" | "settings.labBulk.scope.optional"> = {
  stable: "settings.labBulk.scope.stable",
  recommended: "settings.labBulk.scope.recommended",
  optional: "settings.labBulk.scope.optional",
};

/** Skipped reason 码 → 文案键（与后端 LabBulkSkipReason 常量一致）。 */
const SKIP_REASON_KEYS: Record<string, "settings.labBulk.skip.dangerGate" | "settings.labBulk.skip.precondition" | "settings.labBulk.skip.notBool" | "settings.labBulk.skip.noSetter"> = {
  danger_gate: "settings.labBulk.skip.dangerGate",
  precondition: "settings.labBulk.skip.precondition",
  not_bool_switch: "settings.labBulk.skip.notBool",
  no_bulk_setter: "settings.labBulk.skip.noSetter",
};

/** 纯函数导出：护栏①的触发判定（批量关且范围内含稳定档）。导出供测试直击。 */
export function labBulkStableWarning(plan: LabBulkPlanView | null): boolean {
  return Boolean(plan && !plan.on && plan.stableInScope.length > 0);
}

/** 纯函数导出：护栏②的提示判定（批量关且有被自动跳过的稳定档）。 */
export function labBulkStableSkippedNotice(plan: LabBulkPlanView | null): boolean {
  return Boolean(plan && !plan.on && plan.skippedStable.length > 0);
}

export function LabBulkToggleDialog({ t, onClose }: LabBulkToggleDialogProps) {
  const titleId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

  const [scope, setScope] = useState<LabBulkScope>("recommended");
  const [behavior, setBehavior] = useState<LabBulkBehavior>("on");
  const [plan, setPlan] = useState<LabBulkPlanView | null>(null);
  const [busy, setBusy] = useState(false);
  // 执行回执非 null ⇒ 进入重启两选项阶段（配置已写入）。
  const [receipt, setReceipt] = useState<LabBulkPlanView | null>(null);

  useEffect(() => {
    restoreRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      onClose();
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      if (restoreRef.current?.isConnected) restoreRef.current.focus();
    };
  }, [onClose]);

  // 范围×行为变化 → 实时拉计划（纯读，不写配置）。失败只降级为空计划，
  // 弹窗不白屏（确认按钮仍可用，Apply 自带错误面）。
  useEffect(() => {
    if (receipt) return undefined;
    let alive = true;
    void app
      .LabBulkPlan(scope, behavior === "on")
      .then((v) => {
        if (alive) setPlan(v);
      })
      .catch((e: unknown) => {
        reportFrontendLog("settings", "lab bulk plan failed", String(e));
        if (alive) setPlan(null);
      });
    return () => {
      alive = false;
    };
  }, [scope, behavior, receipt]);

  const [error, setError] = useState<string | null>(null);

  const confirm = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const v = await app.LabBulkApply(scope, behavior === "on");
      setReceipt(v);
    } catch (e) {
      reportFrontendLog("settings", "lab bulk apply failed", String(e));
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [scope, behavior]);

  const restartNow = useCallback(async () => {
    setBusy(true);
    try {
      await app.RestartDesktop();
    } catch (e) {
      reportFrontendLog("settings", "lab bulk restart failed", String(e));
      setBusy(false);
    }
  }, []);

  const targetCount = plan?.targets.length ?? 0;
  const skippedCount = (plan?.skipped.length ?? 0) + (plan?.skippedStable.length ?? 0);

  return createPortal(
    <div className="modal-backdrop lab-pick-dialog__backdrop" role="presentation" onClick={onClose}>
      <div
        className="lab-pick-dialog lab-bulk-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="lab-pick-dialog__bar">
          <span id={titleId} className="lab-pick-dialog__title">{t("settings.labBulk.title")}</span>
          <button ref={closeRef} type="button" className="btn btn--sm" onClick={onClose}>
            {t("settings.forkFeaturesIntro.close")}
          </button>
        </div>
        <div className="lab-pick-dialog__body">
          {receipt ? (
            /* ── 阶段二：重启两选项（配置已写入；987 需重启明示）── */
            <div className="lab-bulk-dialog__restart">
              <p className="lab-bulk-dialog__restart-note">{t("settings.labBulk.restartNote", { n: receipt.applied?.length ?? 0 })}</p>
              <div className="lab-bulk-dialog__restart-actions">
                <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void restartNow()}>
                  {t("settings.restartNow")}
                </button>
                <button type="button" className="btn" disabled={busy} onClick={onClose}>
                  {t("settings.labBulk.restartLater")}
                </button>
              </div>
            </div>
          ) : (
            <>
              {/* ── 范围三档（无「全部」档）── */}
              <fieldset className="lab-bulk-dialog__axis">
                <legend className="lab-bulk-dialog__legend">{t("settings.labBulk.scopeLabel")}</legend>
                {LAB_BULK_SCOPES.map((s) => (
                  <label key={s} className="lab-bulk-dialog__option">
                    <input
                      type="radio"
                      name="lab-bulk-scope"
                      checked={scope === s}
                      onChange={() => setScope(s)}
                      disabled={busy}
                    />
                    <span>{t(SCOPE_LABEL_KEYS[s])}</span>
                  </label>
                ))}
              </fieldset>

              {/* ── 行为两轴 ── */}
              <fieldset className="lab-bulk-dialog__axis">
                <legend className="lab-bulk-dialog__legend">{t("settings.labBulk.behaviorLabel")}</legend>
                {(["on", "off"] as const).map((b) => (
                  <label key={b} className="lab-bulk-dialog__option">
                    <input
                      type="radio"
                      name="lab-bulk-behavior"
                      checked={behavior === b}
                      onChange={() => setBehavior(b)}
                      disabled={busy}
                    />
                    <span>{t(b === "on" ? "settings.labBulk.behavior.on" : "settings.labBulk.behavior.off")}</span>
                  </label>
                ))}
              </fieldset>

              {/* ── 计划摘要：目标数 / 跳过数（写了什么、没写什么全程可见）── */}
              <p className="lab-bulk-dialog__summary" data-empty={targetCount === 0 ? "true" : "false"}>
                {targetCount === 0
                  ? t("settings.labBulk.summaryEmpty")
                  : t("settings.labBulk.summary", { n: targetCount })}
                {skippedCount > 0 ? ` · ${t("settings.labBulk.skippedCount", { n: skippedCount })}` : ""}
              </p>

              {/* ── 护栏①：批量关含稳定档 → 警示（与单点关闭口径一致）── */}
              {labBulkStableWarning(plan) ? (
                <div className="banner lab-bulk-dialog__warn" role="alert">
                  {t("settings.labBulk.stableWarning", { n: plan?.stableInScope.length ?? 0 })}
                </div>
              ) : null}

              {/* ── 护栏②：开发者模式关 → 批量关自动跳过稳定档并提示 ── */}
              {labBulkStableSkippedNotice(plan) ? (
                <div className="banner lab-bulk-dialog__skip-note" role="status">
                  {t("settings.labBulk.stableSkipped", { n: plan?.skippedStable.length ?? 0 })}
                </div>
              ) : null}

              {/* ── 逐项跳过原因（危险门/前置/非布尔/无批量面）── */}
              {plan && plan.skipped.length > 0 ? (
                <ul className="lab-bulk-dialog__skips">
                  {plan.skipped.map((s) => (
                    <li key={s.id}>
                      <code>{s.id}</code>
                      <span>{t(SKIP_REASON_KEYS[s.reason] ?? "settings.labBulk.skip.noSetter")}</span>
                    </li>
                  ))}
                </ul>
              ) : null}

              {error ? <div className="banner lab-bulk-dialog__error" role="alert">{error}</div> : null}

              <div className="lab-bulk-dialog__actions">
                <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void confirm()}>
                  {t("settings.labBulk.confirm")}
                </button>
                <button type="button" className="btn" disabled={busy} onClick={onClose}>
                  {t("common.cancel")}
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}

export default LabBulkToggleDialog;
