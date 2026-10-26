// 任务 824：boot 启动链打点取证——前端侧上报。
//
// 0930 报告《桌面启动状态恢复慢-根因调研》P5/P6 的缺口：hydrate 各 tab 到达
// 时刻只有 console 侧分段（session-monitor），UI 状态（折叠/分屏/面板）的
// apply 时刻完全没有打点。这里把两类「前端到达」经既有 ReportFrontendLog
// 通道送进 desktop.log 与 Go 侧 boot:summary 汇总。
//
// 纯观测：不改变任何渲染/加载行为；上报是 fire-and-forget，网关不可达时
// frontendLog 的环形缓冲与 console 仍留证据。
//
// 时刻口径：atMs 用 performance.now()（相对本页面导航起点），与 0930 基线
// 「首 tab 1157ms」同口径；Go 侧 wall_ms 是进程口径，两者不可相加。
// tl 1163：每个事件同时带 epochMs（Date.now() 墙钟时刻）——前端与 Go 主进程
// 同机同时钟源，Go 侧用它把事件桥接到进程时间轴（boot:summary 的
// *_at_go_ms 字段），段级 breakdown 才能整条串在一条时间线上归因。

import { reportFrontendLog } from "./frontendLog";

/** 事件的墙钟时刻（与 performance.now 同帧取样，毫秒整数）。 */
function nowEpochMs(): number {
  return Date.now();
}

/**
 * 上报一个 tab 的启动 hydrate 到达（P5）。reason==="startup" 的 hydrate 每次
 * 启动每 tab 恰好一次，由 useController 的 hydrate 完成点调用。
 */
export function reportBootHydrateArrival(tabId: string, hydrateMs: number): void {
  reportFrontendLog(
    "boot-timeline",
    "hydrate arrived",
    `kind=hydrate tab=${tabId} atMs=${Math.round(performance.now())} hydrateMs=${Math.round(hydrateMs)} epochMs=${nowEpochMs()}`,
    "info",
  );
}

export interface BootUiStateSnapshot {
  /** 分屏是否打开（desktop:splitView 的 secondaryTabId 非空）。 */
  splitOpen: boolean;
  sidebarCollapsed: boolean;
  workspacePanelOpen: boolean;
  terminalPanelOpen: boolean;
}

/**
 * 上报持久化 UI 状态在首帧 apply 的时刻与取值（P6，0930 报告标注「未打点未
 * 核行」的取证重点）。App 挂载后调用一次；取值读 store 的已应用状态。
 */
export function reportBootUiStateApplied(snapshot: BootUiStateSnapshot): void {
  const detail = `kind=ui atMs=${Math.round(performance.now())} epochMs=${nowEpochMs()}`
    + ` split=${snapshot.splitOpen ? "open" : "closed"}`
    + ` sidebarCollapsed=${snapshot.sidebarCollapsed ? 1 : 0}`
    + ` workspacePanelOpen=${snapshot.workspacePanelOpen ? 1 : 0}`
    + ` terminalPanelOpen=${snapshot.terminalPanelOpen ? 1 : 0}`;
  reportFrontendLog("boot-timeline", "ui state applied", detail, "info");
}

/**
 * 上报首次内容绘制时刻（tl 1163，竞品 DSH/Trilium 同做法：渲染器回报主进程）。
 * mount 后 paint entry 通常已产生；尚未产生时借 rAF 在首帧后再读一次，仍无
 * 则放弃（best effort——诊断绝不能反噬渲染）。kind=fcp，Go 侧汇总为
 * fcp_at_ms / fcp_at_go_ms。
 */
export function reportBootFirstPaint(): void {
  const findFcp = (): PerformanceEntry | undefined =>
    performance.getEntriesByType("paint").find((entry) => entry.name === "first-contentful-paint");
  const report = (entry: PerformanceEntry): void => {
    reportFrontendLog(
      "boot-timeline",
      "first contentful paint",
      `kind=fcp atMs=${Math.round(entry.startTime)} epochMs=${nowEpochMs()}`,
      "info",
    );
  };
  const entry = findFcp();
  if (entry) {
    report(entry);
    return;
  }
  requestAnimationFrame(() => {
    const late = findFcp();
    if (late) {
      report(late);
    }
  });
}
