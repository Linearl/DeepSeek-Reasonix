// 任务 910：右下角弹窗统一 —— .rt-notice 组件的共享挂载器。
//
// 三兄弟（性能报告 #performance-report-prompt / 卡顿分析 #hang-analysis-prompt /
// 崩溃入口 #pending-crash-entry）过去各写一份 fixed/440px 骨架 CSS，改一处要改
// 三处，样式漂移即由此而来；且边框/底色是硬编码橙色（不跟主题 token），浅色
// 主题无法联动。设计稿唯一权威
// docs/design/右下角弹窗统一-demo-20261015.html（二、统一提案节）：
//   骨架 = 左 3px 色条 + 图标徽章 + 标题 + 关闭 ×，可选槽位 intro / mono 数据块 /
//   按钮组 / note 脚注；全部色源走 color-mix(…theme token…)。
//   严重度语义固定（谁都不许换色）：崩溃=danger / 卡顿=warn / 性能强调=accent /
//   中性=info。堆叠由 .rt-notice-stack 承载（column-reverse，先出现的在下）。
//
// 本模块只搭 DOM 骨架，不带业务语义：各面（crash.ts / hangPrompt.ts /
// pendingCrashEntry.ts）保留自己的 #id 与族类名（既有测试钩子），仅在其上挂
// rt-notice 结构类与 data-level。

/** 四档严重度，与 styles.css 的 .rt-notice[data-level] 规则一一对应。 */
export type RtNoticeLevel = "info" | "warn" | "danger" | "accent";

/**
 * tl1464 warn 档自动消失窗口：卡顿提示（本族唯一的 warn 常驻面）挂出 30s 后
 * 自动收走——过去 warn 档无超时，只能手动关；error/danger（崩溃入口/全屏
 * overlay）保持手动关闭不设时限。30s 内 hover 不暂停计时（与 useToast 轻通知
 * 的固定定时器对齐；ask 后台 toast 的 hover 重置是后端窗口耦合语义，不通用）。
 */
export const RT_NOTICE_WARN_AUTO_DISMISS_MS = 30_000;

/**
 * warn 档自动消失的收口回调：宿主仍连着文档才执行（手动已关/已换面则静默）。
 * 返回 void；宿主移除由本模块完成。
 */
type RtNoticeAutoDismissAction = () => void;

const autoDismissTimers = new WeakMap<HTMLElement, ReturnType<typeof setTimeout>>();

/**
 * tl1464：为宿主装一枚自动消失定时器（重复 arm 即重置；手动关闭走
 * clearRtNoticeAutoDismiss）。到期回调先于移除执行，供调用方把「自动消失」
 * 归并进手动关闭的同一收口（hangPrompt 记 10min 冷却，防止 90s 轮询立刻重弹）。
 */
export function armRtNoticeAutoDismiss(
  host: HTMLElement,
  ms: number = RT_NOTICE_WARN_AUTO_DISMISS_MS,
  onExpire?: RtNoticeAutoDismissAction,
): void {
  clearRtNoticeAutoDismiss(host);
  const timer = setTimeout(() => {
    autoDismissTimers.delete(host);
    if (!host.isConnected) return;
    onExpire?.();
    if (host.isConnected) host.remove();
  }, ms);
  autoDismissTimers.set(host, timer);
}

/** 撤销自动消失定时器（手动关闭路径调用；未 arm 过则空操作）。 */
export function clearRtNoticeAutoDismiss(host: HTMLElement): void {
  const timer = autoDismissTimers.get(host);
  if (timer) {
    clearTimeout(timer);
    autoDismissTimers.delete(host);
  }
}

/** 图标徽章字形（语言中立，对齐 demo 提案节的四档示例）。 */
const ICONS: Record<RtNoticeLevel, string> = {
  info: "i",
  warn: "⚠",
  danger: "!",
  accent: "↯",
};

export function rtNoticeIcon(level: RtNoticeLevel): string {
  return ICONS[level];
}

/**
 * 取或建右下角统一堆叠容器（#rt-notice-stack）。tl1464 起它是 A/C 两类共用的
 * 唯一右下容器：C 类（.rt-notice，data-kind="diagnostic"）由 rtNoticeHost 挂入，
 * A 类（toast 轻通知 / ask 后台 toast，data-kind="action"）由 React 侧
 * createPortal 挂入同一容器——堆叠 column-reverse 按到达次序交错，后到者在上。
 */
export function ensureRtNoticeStack(): HTMLElement {
  let stack = document.getElementById("rt-notice-stack");
  if (!stack) {
    stack = document.createElement("div");
    stack.id = "rt-notice-stack";
    stack.className = "rt-notice-stack";
    document.body.appendChild(stack);
  }
  return stack;
}

/**
 * 取或建弹窗宿主。宿主挂 .rt-notice + data-level + data-kind="diagnostic"
 * （tl1464：与 A 类操作反馈同住右下堆叠，靠 data-kind 区分——C 类诊断告警
 * 走 rt-notice 骨架，A 类走 toast 轻量样式，互不混用）。后续 paint 用
 * replaceChildren 重排内容；crash-face--collapsed 等既有状态类在重复 paint
 * 时保留（classList.add 幂等，不整体覆写 className）。
 */
export function rtNoticeHost(id: string, level: RtNoticeLevel): HTMLElement {
  const stack = ensureRtNoticeStack();
  let host = document.getElementById(id);
  if (!host) {
    host = document.createElement("div");
    host.id = id;
    host.className = "rt-notice";
    stack.appendChild(host);
  } else if (host.parentElement !== stack) {
    // 宿主可能建于堆叠容器出现之前（旧挂载路径/测试残留）：归位到堆叠里，
    // 否则多条弹窗仍会各自 fixed 叠在一起。
    stack.appendChild(host);
  } else {
    host.classList.add("rt-notice");
  }
  host.dataset.level = level;
  host.dataset.kind = "diagnostic";
  return host;
}

/**
 * 头行：图标徽章 + 标题 + 关闭 ×。标题沿用调用方的族类名（既有测试钩子），
 * 关闭 × 的 aria-label 由调用方按当前 locale 传入（common.close，三语齐）。
 */
export function rtNoticeHead(
  title: HTMLElement,
  level: RtNoticeLevel,
  closeLabel: string,
  onClose: () => void,
): HTMLElement {
  const head = document.createElement("div");
  head.className = "rt-notice__head";
  const icon = document.createElement("span");
  icon.className = "rt-notice__icon";
  icon.textContent = ICONS[level];
  icon.setAttribute("aria-hidden", "true");
  const close = document.createElement("button");
  close.type = "button";
  close.className = "rt-notice__close";
  close.textContent = "✕";
  close.setAttribute("aria-label", closeLabel);
  close.onclick = onClose;
  head.append(icon, title, close);
  return head;
}

/**
 * 挂载骨架：host.replaceChildren(色条, 内容列[head, ...可选槽位])。
 * 可选槽位传 null/undefined 自动跳过（intro / 数据块 / note 按面裁剪）。
 */
export function rtNoticeMount(
  host: HTMLElement,
  head: HTMLElement,
  ...slots: (HTMLElement | null | undefined)[]
): void {
  const bar = document.createElement("div");
  bar.className = "rt-notice__bar";
  const inner = document.createElement("div");
  inner.className = "rt-notice__inner";
  inner.append(head);
  for (const slot of slots) {
    if (slot) inner.append(slot);
  }
  host.replaceChildren(bar, inner);
}
