import { useSyncExternalStore } from "react";

// tl1508b 修① — 运行状态行（composer run strip）「⋯ N」折叠/展开的用户选择
// 持久化。包 87 复验：首版 runStripExpanded 只是组件内 useState，刷新/重开即
// 回默认折叠，用户选择无法保持。本模块照 toolGroupingPreference 先例落
// localStorage 单键：getSnapshot 首读惰性水合（首帧即取用户上次选择，无闪烁）；
// 存储不可用（加固 webview/隐私态）仅本次运行内存生效；存储值损坏按默认
// （折叠）处理。
// 落点取舍：纯 UI 呈现偏好——折叠只收起时长/tokens/速率/工具汇总读数，状态词
// 与停止出口恒在场，两态轮次行为完全一致（668 同判据：不加 Go 后端字段，
// localStorage 即为持久层）。

const STORAGE_KEY = "reasonix-composer-run-strip-expanded";
const CHANGE_EVENT = "reasonix:composer-run-strip-expanded";

let current = false;
let hydrated = false;
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(CHANGE_EVENT, { detail: current }));
  }
}

export function getComposerRunStripExpanded(): boolean {
  if (!hydrated) {
    hydrated = true;
    try {
      current = localStorage.getItem(STORAGE_KEY) === "expanded";
    } catch {
      current = false; // Storage unavailable (hardened webviews): keep the default.
    }
  }
  return current;
}

export function setComposerRunStripExpanded(value: boolean): void {
  const next = value === true;
  const previous = getComposerRunStripExpanded();
  if (previous === next) return;
  current = next;
  try {
    localStorage.setItem(STORAGE_KEY, next ? "expanded" : "folded");
  } catch {
    // Storage may be unavailable; the in-memory value still applies this run.
  }
  emit();
}

export function useComposerRunStripExpanded(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getComposerRunStripExpanded,
    () => false,
  );
}

// 测试专用：模块级单次水合（hydrated 一次性）在 jsdom 套件里无法按用例重放
// （同一 tsx 进程共享模块注册表，每个用例换新 JSDOM/localStorage）。重置后
// 下一次 getSnapshot 按当块 JSDOM 的 localStorage 重新惰性水合，损坏/缺失/
// 存储不可用三条回退路径才可各自断言。生产代码零调用。
export function __resetComposerRunStripPreferenceForTests(): void {
  current = false;
  hydrated = false;
  // 不清 listeners：订阅者（组件实例）随各自 JSDOM 卸载自清，重置只影响水合。
}
