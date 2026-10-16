import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { activeTabMirror } from "./activeTabMirror";
import { composerFocusHeld, restoreComposerFocus } from "../lib/composerFocus";
import { reportFrontendLog } from "../lib/frontendLog";
export type TopicTimeFilter = "all" | "10" | "20" | "1h" | "3h" | "5h" | "1d";

export function useTopicTimeFilter(): [TopicTimeFilter, Dispatch<SetStateAction<TopicTimeFilter>>] {
  const [value, setValue] = useState<TopicTimeFilter>(() => {
    try {
      const saved = localStorage.getItem("projectTree:timeFilter");
      if (saved === "all" || saved === "10" || saved === "20" || saved === "1h" || saved === "3h" || saved === "5h" || saved === "1d") return saved;
    } catch { /* localStorage unavailable */ }
    return "all";
  });
  useEffect(() => {
    try { localStorage.setItem("projectTree:timeFilter", value); } catch { /* ignore */ }
  }, [value]);
  return [value, setValue];
}

export function useDecisionSurfaceFocus(input: {
  surface: string | null;
  activeTabId?: string | null;
  closeOverlays: () => void;
}) {
  const { surface, activeTabId, closeOverlays } = input;
  const previous = useRef<string | null>(null);
  const surfaceRef = useRef<string | null>(surface);
  surfaceRef.current = surface;
  useEffect(() => {
    if (surface) {
      closeOverlays();
      previous.current = surface;
      return;
    }
    const hadSurface = previous.current !== null;
    previous.current = null;
    if (!hadSurface) return;
    const tabAtRelease = activeTabId;
    const frame = requestAnimationFrame(() => {
      if (surfaceRef.current !== null || activeTabMirror().current !== tabAtRelease) return;
      (document.getElementById("composer-input") as HTMLTextAreaElement | null)?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [activeTabId, closeOverlays, surface]);
}

export function useActiveTabUiReset(input: {
  activeTabId?: string | null;
  setClearPending: (value: boolean) => void;
  setInsertTarget: (value: "composer") => void;
}) {
  const { activeTabId, setClearPending, setInsertTarget } = input;
  useEffect(() => {
    setClearPending(false);
    setInsertTarget("composer");
  }, [activeTabId, setClearPending, setInsertTarget]);
}

export function useVerificationRevealReset(input: {
  activeTabId?: string | null;
  completionSummary: unknown;
  sessionPath?: string;
  turnStartAt?: number | null;
  reset: (value: null) => void;
}) {
  const { activeTabId, sessionPath, turnStartAt, reset } = input;
  useEffect(() => { reset(null); }, [activeTabId, sessionPath, reset, turnStartAt]);
}

// 任务 913根因面之一：runtimeTransitioning（导航遮罩）把 composer 宿主整块
// hidden+inert+disabled——浏览器静默收走 textarea 焦点（display:none 下焦点
// 落到 body），且遮罩落下时切走的焦点在导航 settle 后无人归还：decision 释
// 放有恢复路径，导航释放没有。打字中触发程序化导航（快捷键开题/协作跳转/
// 任务监控跳会话/后台 runtime 揭示）后 keystrokes 全落 body，必须手点才能
// 继续——用户 1015 报的「偶有输入框失焦（打字中断/需重新点击）」与日志里
// 15:24-15:26 换 tab 风暴时段吻合。
//
// 归还条件（与 276 自愈同款「不与用户对抗」契约）：遮罩落下时焦点确在
// composer 输入面（focusin/focusout 追踪，遮罩 commit 后 activeElement 已被
// 收走，不能在 effect 里现读）；settle 时焦点空闲（body 或输入框自身）；无
// 新决策面接管（决策面由 decision 释放路径负责）。用户已点走（activeElement
// 是其他元素）绝不抢。
export function useRuntimeTransitionFocusRestore(input: {
  runtimeTransitioning: boolean;
  decisionSurfaceActive: boolean;
}) {
  const { runtimeTransitioning, decisionSurfaceActive } = input;
  const composerHoldsFocusRef = useRef(false);
  const maskingRef = useRef(false);
  const wasFocusedAtMaskStartRef = useRef(false);

  useEffect(() => {
    const syncHolds = () => {
      composerHoldsFocusRef.current = composerFocusHeld();
    };
    document.addEventListener("focusin", syncHolds, { capture: true });
    document.addEventListener("focusout", syncHolds, { capture: true });
    syncHolds();
    return () => {
      document.removeEventListener("focusin", syncHolds, { capture: true });
      document.removeEventListener("focusout", syncHolds, { capture: true });
    };
  }, []);

  useEffect(() => {
    if (runtimeTransitioning) {
      if (!maskingRef.current) wasFocusedAtMaskStartRef.current = composerHoldsFocusRef.current;
      maskingRef.current = true;
      return;
    }
    if (!maskingRef.current) return;
    maskingRef.current = false;
    if (!wasFocusedAtMaskStartRef.current) return;
    wasFocusedAtMaskStartRef.current = false;
    if (decisionSurfaceActive) return;
    const active = document.activeElement;
    if (active && active !== document.body && !composerFocusHeld()) return;
    // 失焦事件本身的正向留痕：这条日志出现 = 遮罩期确实收走过 composer 焦点
    // （用户报的「打字中断」现场），复发时按时间与 tab-switch 对链。
    reportFrontendLog("composer-focus", "focus restore after navigation release");
    restoreComposerFocus({
      site: "navigation-release",
      abort: () => maskingRef.current,
    });
  }, [runtimeTransitioning, decisionSurfaceActive]);
}
