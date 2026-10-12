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

import { reportFrontendLog } from "./frontendLog";

/**
 * 上报一个 tab 的启动 hydrate 到达（P5）。reason==="startup" 的 hydrate 每次
 * 启动每 tab 恰好一次，由 useController 的 hydrate 完成点调用。
 */
export function reportBootHydrateArrival(tabId: string, hydrateMs: number): void {
  reportFrontendLog(
    "boot-timeline",
    "hydrate arrived",
    `kind=hydrate tab=${tabId} atMs=${Math.round(performance.now())} hydrateMs=${Math.round(hydrateMs)}`,
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
  const detail = `kind=ui atMs=${Math.round(performance.now())}`
    + ` split=${snapshot.splitOpen ? "open" : "closed"}`
    + ` sidebarCollapsed=${snapshot.sidebarCollapsed ? 1 : 0}`
    + ` workspacePanelOpen=${snapshot.workspacePanelOpen ? 1 : 0}`
    + ` terminalPanelOpen=${snapshot.terminalPanelOpen ? 1 : 0}`;
  reportFrontendLog("boot-timeline", "ui state applied", detail, "info");
}
