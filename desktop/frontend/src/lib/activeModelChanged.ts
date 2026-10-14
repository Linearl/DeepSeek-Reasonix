// 任务 872 — 「当前 tab 的活跃模型已切换」信号。
//
// 热切换（ModelSwitcher → SetModelForTab）成功或最新一次尝试失败落定后
// 广播此事件：所有按「当前模型所属 provider」取数的显示面（右侧用量卡、
// 状态栏用量项）同步作废旧 provider 快照并立刻重查。没有这个信号时，旧
// provider 卡要等 60s 轮询才下线；而新 provider 卡（零 I/O 的 family 扫描）
// 先出现——切换瞬间两卡同屏并存，会被误读为「双倍配额」（用户 2026-10-13
// 反馈）。

export const ACTIVE_MODEL_CHANGED_EVENT = "reasonix:active-model-changed";

/** 安全广播：无 window 的环境（node 测试/SSR）静默跳过。事件构造取
 * window 自己的 Event（jsdom 等环境下全局 Event 可能是另一个 realm 的
 * 构造器，dispatchEvent 会拒收）。 */
export function emitActiveModelChanged(): void {
  if (typeof window === "undefined" || typeof window.dispatchEvent !== "function") return;
  const EventCtor = window.Event ?? Event;
  window.dispatchEvent(new EventCtor(ACTIVE_MODEL_CHANGED_EVENT));
}
