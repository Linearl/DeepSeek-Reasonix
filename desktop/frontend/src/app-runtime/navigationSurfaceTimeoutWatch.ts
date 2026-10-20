// 任务 1026（fork issue #61）：导航遮罩超时兜底计时器。
//
// 背景：遮罩（navigationSurfaceIntent）只有 begin/settle 两态，settle 完全依赖
// 后端导航回调——后端卡住（锁等待/hydrate 不返回）时遮罩永久悬挂，用户只能
// 强退重启两次。本 hook 是纯前端自有出口：遮罩挂起超过时限仍未被 settle，
// 就触发一次 onTimeout(intent)，由调用方清遮罩并给出降级 UI。
//
// 刻意不依赖 onTimeout 的引用稳定性：回调经 ref 透传，计时器只随 intent /
// timeoutMs 变化重置，避免调用方渲染期重建回调导致兜底时限被反复顺延。
import { useEffect, useLayoutEffect, useRef } from "react";
import { NAVIGATION_SURFACE_TIMEOUT_MS } from "../lib/navigationSurfaceTransition";

export function useNavigationSurfaceTimeoutWatch(
  intent: number | null,
  onTimeout: (intent: number) => void,
  timeoutMs: number = NAVIGATION_SURFACE_TIMEOUT_MS,
): void {
  const callbackRef = useRef(onTimeout);
  useLayoutEffect(() => {
    callbackRef.current = onTimeout;
  });
  useEffect(() => {
    if (intent === null) return;
    const watched = intent;
    const timer = setTimeout(() => callbackRef.current(watched), timeoutMs);
    return () => clearTimeout(timer);
  }, [intent, timeoutMs]);
}
