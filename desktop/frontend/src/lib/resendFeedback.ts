// tl1342②/③: resend feedback decisions, extracted pure so the button-face
// contract is unit-testable without mounting App or the banner.
//
// 背景（20261101 现场）：失败消息的「重发」按钮按下后要么静默返回（guard
// 分支零反馈），要么再次失败也静默——用户按了不知道发没发出去。修法：
// ①发送面（sendToTab）回传结算结果（SendTurnOutcome）；
// ②本文件把「结算结果 → 用户可见反馈」收敛成纯函数，成功/降级/失败三条
//   路都有明确文案，失败带原因；
// ③横幅多条一键全部重发是逐条顺序走同一结算通道，汇总计数也在这里算。

import { errorMessage } from "./controllerNotices";

/** Localized strings a caller supplies so this module stays locale-free. */
export interface ResendFeedbackStrings {
  /** "发送失败 — 消息未送达" face. */
  sendFailed: string;
  /** Confirmed resend copy. */
  resendSuccess: string;
  /** Degraded-into-queue resend copy. */
  resendDegraded: string;
}

export interface ResendFeedback {
  level: "info" | "warn";
  text: string;
}

/**
 * Maps one settled send to the toast the user must always see now.
 * - confirmed: the admission receipt arrived — the resend is really running.
 * - degraded: the refusal degraded into the guidance queue (message kept).
 * - refused: neither — the toast carries the backend reason verbatim.
 * - outcome undefined: sendToTab threw before/without a receipt (tab gone,
 *   runtime not ready) — same refused face with the thrown reason.
 */
export function describeResendOutcome(
  outcome: { confirmed: boolean; degraded: boolean; error?: string } | undefined,
  thrown: unknown,
  s: ResendFeedbackStrings,
): ResendFeedback {
  if (outcome?.confirmed) return { level: "info", text: s.resendSuccess };
  if (outcome?.degraded) return { level: "info", text: s.resendDegraded };
  const reason = outcome?.error || errorMessage(thrown);
  return { level: "warn", text: reason ? `${s.sendFailed}: ${reason}` : s.sendFailed };
}

/**
 * tl1342③: resend the banner's recovered inputs ONE BY ONE through the same
 * outcome-aware channel (order matters — they all target one session's turn
 * gate). Returns the delivered/queued vs refused counts plus the indexes that
 * succeeded — successes are NOT a prefix (a refused entry can sit between
 * delivered ones), so the caller marks handled by index, never by head-slice.
 */
export async function runBulkResend(
  prompts: readonly string[],
  resend: (prompt: string) => Promise<boolean>,
): Promise<{ ok: number; failed: number; deliveredIndexes: number[] }> {
  let ok = 0;
  let failed = 0;
  const deliveredIndexes: number[] = [];
  for (let index = 0; index < prompts.length; index += 1) {
    let delivered = false;
    try {
      delivered = await resend(prompts[index]);
    } catch {
      delivered = false;
    }
    if (delivered) {
      ok += 1;
      deliveredIndexes.push(index);
    } else {
      failed += 1;
    }
  }
  return { ok, failed, deliveredIndexes };
}
