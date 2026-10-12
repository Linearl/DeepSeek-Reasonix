// 任务 826 — 左右边栏 hover 展开与延时折叠的行为 hook。
//
// 语义（对照 dev_13 参考实现 useSidebarBehavior.ts + 本任务差异）：
//   1. 防误触：指针在折叠态的侧栏（或其边缘热区）上停留 300ms 才自动展开
//      ——参考实现同款延迟，扫过不展开。
//   2. 延时回收：指针离开后等 15s（用户指定；参考实现是离开即折）才把侧栏
//      折回去，且只回收「本次 hover 自己展开的」状态——autoExpandedRef 区分
//      自动/手动，手动 toggle 过的状态永不回收（参考实现 autoCollapsedRef
//      同款判据）。
//   3. 离开后 15s 内重新进入 ⇒ 取消回收（人还在用，不该折）。
//   4. 开关关（enabled=false）⇒ 三个回调全部 no-op、不挂任何定时器；App 侧
//      也不渲染热区——铁律 2 零回归。
//
// 展开状态本身（sidebarCollapsed / workspacePanelOpen）仍由调用方持有：本
// hook 只管时序，不碰 store，左右两侧各实例化一份、互不共享任何状态。

import { useCallback, useEffect, useRef } from "react";

/** 防误触延迟：指针停留此时长才自动展开（参考实现 300ms 同款）。 */
export const SIDEBAR_HOVER_EXPAND_DELAY_MS = 300;
/** 离开回收延迟：指针离开此时长后折回（用户指定 15s；参考实现为立即）。 */
export const SIDEBAR_HOVER_COLLAPSE_DELAY_MS = 15_000;

export type SidebarHoverOptions = {
  /** 实验开关（experimentalSidebarHoverLeft/Right 的前端门）。 */
  enabled: boolean;
  /** 当前折叠/关闭状态（调用方 store 的投影）。 */
  collapsed: boolean;
  /** 展开动作（写 store + 持久化，由调用方提供）。 */
  expand: () => void;
  /** 折叠动作（写 store + 持久化，由调用方提供）。 */
  collapse: () => void;
  /** 测试注入口：展开延迟 ms（缺省 300）。 */
  expandDelayMs?: number;
  /** 测试注入口：回收延迟 ms（缺省 15 000）。 */
  collapseDelayMs?: number;
};

export type SidebarHoverBehavior = {
  /** 指针进入侧栏容器 / 折叠态边缘热区。 */
  onEnter: () => void;
  /** 指针离开侧栏容器。 */
  onLeave: () => void;
  /** 用户手动 toggle 时调用：清除自动展开标记（手动状态不被 15s 回收）。 */
  markManual: () => void;
};

export function useSidebarHoverBehavior(options: SidebarHoverOptions): SidebarHoverBehavior {
  const { enabled, collapsed, expand, collapse, expandDelayMs = SIDEBAR_HOVER_EXPAND_DELAY_MS, collapseDelayMs = SIDEBAR_HOVER_COLLAPSE_DELAY_MS } = options;

  // 定时器回调读 ref 而不是闭包捕获，避免过期 state（React 并发下回调
  // 可能拿着旧 props 跑）。
  const enabledRef = useRef(enabled);
  const collapsedRef = useRef(collapsed);
  const expandRef = useRef(expand);
  const collapseRef = useRef(collapse);
  enabledRef.current = enabled;
  collapsedRef.current = collapsed;
  expandRef.current = expand;
  collapseRef.current = collapse;

  // 「当前展开状态是本次 hover 自动展开的」——只有这个标记为真时，离开
  // 15s 后才回收；手动 toggle（markManual）即清除。
  const autoExpandedRef = useRef(false);
  const enterTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const leaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearEnterTimer = useCallback(() => {
    if (enterTimerRef.current !== null) {
      clearTimeout(enterTimerRef.current);
      enterTimerRef.current = null;
    }
  }, []);

  const clearLeaveTimer = useCallback(() => {
    if (leaveTimerRef.current !== null) {
      clearTimeout(leaveTimerRef.current);
      leaveTimerRef.current = null;
    }
  }, []);

  const onEnter = useCallback(() => {
    if (!enabledRef.current) return;
    // 重新进入 ⇒ 一切待定动作作废（挂起中的展开与挂起中的回收都不要了）。
    clearEnterTimer();
    clearLeaveTimer();
    if (!collapsedRef.current) return;
    enterTimerRef.current = setTimeout(() => {
      enterTimerRef.current = null;
      // 延迟窗口内状态可能已变（用户手动展开/关闭了别的东西）：仍折叠才
      // 展开，且此刻才认定「这次展开是自动的」。
      if (!enabledRef.current || !collapsedRef.current) return;
      autoExpandedRef.current = true;
      expandRef.current();
    }, expandDelayMs);
  }, [clearEnterTimer, clearLeaveTimer, expandDelayMs]);

  const onLeave = useCallback(() => {
    if (!enabledRef.current) return;
    // 离开 ⇒ 挂起中的展开作废（指针只是扫过边缘）。
    clearEnterTimer();
    if (!autoExpandedRef.current || collapsedRef.current) return;
    clearLeaveTimer();
    leaveTimerRef.current = setTimeout(() => {
      leaveTimerRef.current = null;
      // 回收到点时再核对一次：仍处于「自动展开 + 展开」态才折回。用户若
      // 在窗口期内手动折叠过（collapsed 已真），这里是 no-op。
      if (!enabledRef.current || !autoExpandedRef.current || collapsedRef.current) return;
      autoExpandedRef.current = false;
      collapseRef.current();
    }, collapseDelayMs);
  }, [clearEnterTimer, clearLeaveTimer, collapseDelayMs]);

  const markManual = useCallback(() => {
    autoExpandedRef.current = false;
  }, []);

  // 开关翻转成关（或卸载）时，把挂起的定时器与自动标记一并清掉——关掉
  // 开关的瞬间不能还残留一个 15s 后突然折叠的定时器。
  useEffect(() => {
    if (enabled) return;
    clearEnterTimer();
    clearLeaveTimer();
    autoExpandedRef.current = false;
  }, [enabled, clearEnterTimer, clearLeaveTimer]);

  useEffect(() => {
    return () => {
      if (enterTimerRef.current !== null) clearTimeout(enterTimerRef.current);
      if (leaveTimerRef.current !== null) clearTimeout(leaveTimerRef.current);
    };
  }, []);

  return { onEnter, onLeave, markManual };
}
