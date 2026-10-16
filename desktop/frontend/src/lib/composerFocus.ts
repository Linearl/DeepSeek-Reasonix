// 任务 913：App 级 composer 焦点归还共享器。
//
// 背景（根因链之三）：任务 276 给 Composer.focusComposerInput 补了「focus 后
// 校验落位 + 有界帧内重试」，但 App.tsx 的 decision 释放恢复路径仍是单发
// focus()——目标在该帧仍 hidden/inert/disabled（导航遮罩未落完、runtimeState
// 未同步）时静默失败，此后无人归还，用户必须手点。本模块把「落位校验 + 有界
// 重试 + 放弃留痕」收敛成共享器，供 decision 释放与导航遮罩释放两条 App 级
// 路径复用；重试期间焦点被用户点到别处即让位，绝不与用户抢焦点。
import { reportFrontendLog } from "./frontendLog";

// plain textarea 与 rich input（contentEditable）共用同一 id，两条渲染分支
// 任一在树都能被 getElementById 命中（Composer.tsx / RichComposerInput.tsx）。
export const COMPOSER_INPUT_ELEMENT_ID = "composer-input";

const RESTORE_RETRY_FRAMES = 3;

/** 当前焦点是否落在 composer 输入面（textarea / rich input / 宿主内其他元素）。 */
export function composerFocusHeld(): boolean {
  const active = document.activeElement as HTMLElement | null;
  if (!active || active === document.body) return false;
  if (active.id === COMPOSER_INPUT_ELEMENT_ID) return true;
  return Boolean(active.closest?.(".composer-decision-host"));
}

/**
 * 归还焦点到 composer 输入框：单发 focus() 会静默失败，故落位校验后在有界
 * 帧内重试；耗尽落 composer-focus 日志（desktop.log 滚动通道），复发时与
 * 276 三探针对链。重试帧里焦点已落到别的元素（用户点击）即让位。
 */
export function restoreComposerFocus(options?: {
  /** 日志定位标签：decision-release / navigation-release / ... */
  site?: string;
  /** 每帧重试前的放弃条件（新决策面接管、新一轮导航遮罩等）。 */
  abort?: () => boolean;
}, attempt = 0): void {
  const site = options?.site ?? "app";
  if (options?.abort?.()) return;
  const input = document.getElementById(COMPOSER_INPUT_ELEMENT_ID);
  (input as HTMLElement | null)?.focus({ preventScroll: true });
  if (input && document.activeElement === input) return;
  const active = document.activeElement as HTMLElement | null;
  if (active && active !== document.body && active !== input) {
    // 重试间隙焦点被用户点到别处——让位，不抢。
    return;
  }
  if (attempt >= RESTORE_RETRY_FRAMES) {
    reportFrontendLog("composer-focus", "focus restore exhausted retries", `attempt=${attempt + 1} site=${site}`);
    return;
  }
  requestAnimationFrame(() => restoreComposerFocus(options, attempt + 1));
}
