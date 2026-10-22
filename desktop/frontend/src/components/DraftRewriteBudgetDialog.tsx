// 任务 1052（草稿优化交互改造）：改写前的上下文长度档位弹窗。
//
// 需求（用户 2026-10-21 拍板）：点击改写不立即改写——先弹窗选注入上下文长度
// （2KB / 5KB / 10KB / 20KB 四档，横排按钮组）+ 取消按钮（关闭弹窗不发起任何
// 请求）；选择后才按档位调 RewriteDraftForTab。动机=1024 固定 10KB 上下文
// 体感慢、成本高，用户接受略微降低便利性换成本与延时改善。
//
// 形态：复用 lab-pick-dialog 壳（563 弹窗家族：portal 到 body、modal-backdrop
// 遮罩、Esc/遮罩点击关闭）+ 通用 .btn/.btn--primary 动作钮——零新 CSS。
// 记住上次选择：localStorage（`composer:draftRewriteBudgetKB`），首次默认 10KB
// （与 1024 旧行为一致）；非法/不可用时一律回落 10KB，读写全 try/catch 降级。

import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Translator } from "../lib/i18n";

/** 四档档位（KB）——横排展示序；10KB 为默认档（1024 旧行为）。 */
export const DRAFT_REWRITE_BUDGET_KBS = [2, 5, 10, 20] as const;
export type DraftRewriteBudgetKB = (typeof DRAFT_REWRITE_BUDGET_KBS)[number];

const DRAFT_REWRITE_BUDGET_STORAGE_KEY = "composer:draftRewriteBudgetKB";

/** 纯函数：把原始存档值收敛到合法档位（非法/缺失 → 10）。导出供测试直击。 */
export function normalizeDraftRewriteBudgetKB(raw: string | null): DraftRewriteBudgetKB {
  const n = Number(raw);
  return (DRAFT_REWRITE_BUDGET_KBS as readonly number[]).includes(n) ? (n as DraftRewriteBudgetKB) : 10;
}

/** 读上次选择的档位；localStorage 不可用/未存/非法一律回落 10KB。 */
export function loadDraftRewriteBudgetKB(): DraftRewriteBudgetKB {
  try {
    return normalizeDraftRewriteBudgetKB(window.localStorage.getItem(DRAFT_REWRITE_BUDGET_STORAGE_KEY));
  } catch {
    // localStorage unavailable (private mode / quota): fall back to the default.
    return 10;
  }
}

/** 记住本次选择的档位（写失败静默——记忆是便利性，不值得打断改写）。 */
export function saveDraftRewriteBudgetKB(kb: number): void {
  try {
    window.localStorage.setItem(DRAFT_REWRITE_BUDGET_STORAGE_KEY, String(kb));
  } catch {
    // ignore — the next open simply falls back to the 10KB default.
  }
}

interface DraftRewriteBudgetDialogProps {
  t: Translator;
  /** 改写进行中（选档后同一次改写的 spin 期间再次点击按钮不会再弹）。 */
  busy: boolean;
  onChoose: (kb: DraftRewriteBudgetKB) => void;
  onClose: () => void;
}

export function DraftRewriteBudgetDialog({ t, busy, onChoose, onClose }: DraftRewriteBudgetDialogProps) {
  const titleId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const restoreRef = useRef<HTMLElement | null>(null);
  const [selectedKB, setSelectedKB] = useState<DraftRewriteBudgetKB>(() => loadDraftRewriteBudgetKB());

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

  return createPortal(
    <div className="modal-backdrop" role="presentation" onClick={onClose}>
      <div
        className="lab-pick-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="lab-pick-dialog__bar">
          <span id={titleId} className="lab-pick-dialog__title">{t("composer.rewriteBudget.title")}</span>
          <button ref={closeRef} type="button" className="btn btn--sm" onClick={onClose}>
            {t("composer.rewriteBudget.close")}
          </button>
        </div>
        <div className="lab-pick-dialog__body">
          <p className="lab-pick-dialog__effect">{t("composer.rewriteBudget.hint")}</p>
          <div className="lab-bulk-dialog__restart-actions" role="group" aria-label={t("composer.rewriteBudget.title")}>
            {DRAFT_REWRITE_BUDGET_KBS.map((kb) => (
              <button
                key={kb}
                type="button"
                className={`btn${selectedKB === kb ? " btn--primary" : ""}`}
                aria-pressed={selectedKB === kb}
                disabled={busy}
                onClick={() => {
                  setSelectedKB(kb);
                  saveDraftRewriteBudgetKB(kb);
                  onChoose(kb);
                }}
              >
                {t("composer.rewriteBudget.tier", { n: kb })}
              </button>
            ))}
          </div>
          <div className="lab-bulk-dialog__actions">
            <button type="button" className="btn" disabled={busy} onClick={onClose}>
              {t("composer.rewriteBudget.cancel")}
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
