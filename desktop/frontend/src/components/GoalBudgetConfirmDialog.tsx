// tl1835 — goalMode 激活确认预算弹窗。
//
// 需求（用户 20261127 提议，实验室开关 goal_budget_confirm 默认关）：开关
// 开启时，用户在 composer 激活 goalMode（提交目标文本）不立即激活——先弹窗
// 展示目标名 + 当前默认预算（M 兆 token，可选预设档 + 自定义数值），确认后
// 持久化并当场生效再激活；取消=不激活（本次提交整体中止，草稿原样保留）。
//
// 形态：复用 lab-pick-dialog 壳（563 弹窗家族：portal 到 body、modal-backdrop
// 遮罩、Esc/遮罩点击关闭）+ 通用 .btn/.btn--primary 动作钮——零新 CSS（
// DraftRewriteBudgetDialog 判例）。M 换算只活在本显示层：桥接/配置键保持
// 裸 token 语义不变（向后兼容，tl1312 默认 2M 不动）。

import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Translator } from "../lib/i18n";

/** tl1312 未配置时的默认上限（run_ceiling.go defaultGoalContinuationTokenBudget）。 */
export const GOAL_BUDGET_DEFAULT_TOKENS = 2_000_000;

/** 预设档（M 兆 token）——横排展示序；2M 为默认档（tl1312 旧行为）。 */
export const GOAL_BUDGET_PRESET_M = [1, 2, 5, 10] as const;

/** 输入边界（M）：下界=0.1M（再小几轮重活就停，弹窗意义全无）；
 *  上界=10000M（任何理智运行都到不了，防「把裸 token 当 M 填」的手滑）。 */
export const GOAL_BUDGET_MIN_M = 0.1;
export const GOAL_BUDGET_MAX_M = 10000;

/** 纯函数：裸 token → M 显示值（未配置 0 → tl1312 默认 2M）。导出供测试直击。 */
export function tokensToGoalBudgetM(tokens: number): number {
	if (!Number.isFinite(tokens) || tokens <= 0) return GOAL_BUDGET_DEFAULT_TOKENS / 1_000_000;
	return tokens / 1_000_000;
}

/** 纯函数：M → 裸 token（四舍五入到整 token）。导出供测试直击。 */
export function goalBudgetMToTokens(m: number): number {
	return Math.round(m * 1_000_000);
}

/** 纯函数：原始输入串收敛为合法 M 值；非法（非数/越界/≤0）返回 null。
 *  上下界语义沿用 tl1312：预算是无人值守循环的正上界（负数/0 不是预算）。
 *  导出供测试直击。 */
export function normalizeGoalBudgetInputM(raw: string): number | null {
	const trimmed = raw.trim();
	if (!trimmed) return null;
	const n = Number(trimmed);
	if (!Number.isFinite(n) || n <= 0) return null;
	if (n < GOAL_BUDGET_MIN_M || n > GOAL_BUDGET_MAX_M) return null;
	return n;
}

/** 弹窗展示用的 M 文本：整值去小数点，非整值保留至多两位有效小数。 */
export function formatGoalBudgetM(m: number): string {
	const rounded = Math.round(m * 100) / 100;
	return String(rounded);
}

interface GoalBudgetConfirmDialogProps {
	t: Translator;
	/** 将要激活的目标名（composer 提交文本；超长由样式截断，不做语义改写）。 */
	goalName: string;
	/** 当前默认预算（裸 token；0/缺省 = tl1312 默认 2M）。 */
	currentTokens: number;
	busy: boolean;
	/** 确认：带上换算后的裸 token（可能与当前值相同=未编辑）。 */
	onConfirm: (tokens: number) => void;
	onCancel: () => void;
}

export function GoalBudgetConfirmDialog({ t, goalName, currentTokens, busy, onConfirm, onCancel }: GoalBudgetConfirmDialogProps) {
	const titleId = useId();
	const closeRef = useRef<HTMLButtonElement>(null);
	const restoreRef = useRef<HTMLElement | null>(null);
	const currentM = tokensToGoalBudgetM(currentTokens);
	const [selectedM, setSelectedM] = useState<number>(() => currentM);
	// 自定义输入草稿：空串=跟随预设档选中态；非空=用户正在自定义。
	const [customDraft, setCustomDraft] = useState<string>("");
	const customValue = customDraft.trim() ? normalizeGoalBudgetInputM(customDraft) : null;
	// 自定义串非空但非法 ⇒ 确认禁用（沿用 tl1312 上下界语义）。
	const customInvalid = customDraft.trim().length > 0 && customValue === null;
	const effectiveM = customDraft.trim() && customValue !== null ? customValue : selectedM;

	useEffect(() => {
		restoreRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		closeRef.current?.focus();
		const onKey = (e: KeyboardEvent) => {
			if (e.key !== "Escape") return;
			e.stopPropagation();
			onCancel();
		};
		document.addEventListener("keydown", onKey, true);
		return () => {
			document.removeEventListener("keydown", onKey, true);
			if (restoreRef.current?.isConnected) restoreRef.current.focus();
		};
	}, [onCancel]);

	return createPortal(
		<div className="modal-backdrop" role="presentation" onClick={onCancel}>
			<div
				className="lab-pick-dialog"
				role="dialog"
				aria-modal="true"
				aria-labelledby={titleId}
				onClick={(e) => e.stopPropagation()}
			>
				<div className="lab-pick-dialog__bar">
					<span id={titleId} className="lab-pick-dialog__title">{t("composer.goalBudget.title")}</span>
					<button ref={closeRef} type="button" className="btn btn--sm" onClick={onCancel}>
						{t("common.cancel")}
					</button>
				</div>
				<div className="lab-pick-dialog__body">
					<p className="lab-pick-dialog__effect">{t("composer.goalBudget.hint")}</p>
					<p className="lab-pick-dialog__effect"><strong>{goalName}</strong></p>
					<div className="lab-bulk-dialog__restart-actions" role="group" aria-label={t("composer.goalBudget.title")}>
						{GOAL_BUDGET_PRESET_M.map((presetM) => (
							<button
								key={presetM}
								type="button"
								className={`btn${!customInvalid && effectiveM === presetM ? " btn--primary" : ""}`}
								aria-pressed={!customInvalid && effectiveM === presetM}
								disabled={busy}
								onClick={() => {
									setCustomDraft("");
									setSelectedM(presetM);
								}}
							>
								{t("composer.goalBudget.tier", { n: presetM })}
							</button>
						))}
					</div>
					<label className="lab-bulk-dialog__option">
						<span>{t("composer.goalBudget.customLabel")}</span>
						<input
							type="number"
							min={GOAL_BUDGET_MIN_M}
							max={GOAL_BUDGET_MAX_M}
							step={0.1}
							value={customDraft}
							disabled={busy}
							placeholder={formatGoalBudgetM(currentM)}
							onChange={(e) => setCustomDraft(e.target.value)}
						/>
					</label>
					{customInvalid ? (
						<div className="banner lab-bulk-dialog__warn" role="alert">
							{t("composer.goalBudget.invalid", { min: GOAL_BUDGET_MIN_M, max: GOAL_BUDGET_MAX_M })}
						</div>
					) : null}
					<div className="lab-bulk-dialog__actions">
						<button
							type="button"
							className="btn btn--primary"
							disabled={busy || customInvalid}
							onClick={() => onConfirm(goalBudgetMToTokens(effectiveM))}
						>
							{t("composer.goalBudget.confirm")}
						</button>
						<button type="button" className="btn" disabled={busy} onClick={onCancel}>
							{t("common.cancel")}
						</button>
					</div>
				</div>
			</div>
		</div>,
		document.body,
	);
}

export default GoalBudgetConfirmDialog;
