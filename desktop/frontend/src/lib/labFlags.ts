// Task 265/262 lab intake flags: render-surface gates whose values the boot
// snapshot resolves (nil-means-on server-side), applied via module setters the
// same way sessionMonitor/splitView do. Pure frontend gates: no store writes,
// no restart-time semantics beyond "boot snapshot".

import { reportFrontendLog } from "./frontendLog";

export type LabFeatureFlag = "questionSearch" | "subagentTps" | "completionSummary" | "quickCommands" | "subagentPolicy" | "subagentPanel" | "sessionWall" | "tabCompress" | "subagentDetail" | "collabHub" | "trajectoryView" | "opencodeGoUsage" | "sidebarHoverLeft" | "sidebarHoverRight" | "workflowPanel" | "perfMonitor" | "approvalSelect" | "tableWrap" | "sendTargetSelect" | "smoothScrollFollow" | "draftRewrite" | "startupSplash";

const defaults: Record<LabFeatureFlag, boolean> = {
  questionSearch: true,
  subagentTps: true,
  completionSummary: true,
  quickCommands: false,
  subagentPolicy: true,
  // Task 495: the subagent panel package (dock tab + ended-card collapse)
  // ships off — same boot-snapshot contract, plain default-false bool.
  subagentPanel: false,
  // Task 505: the session graph wall (palette 跳转会话 entry + grid wall)
  // ships off — with it off the palette item list is byte-for-byte unchanged.
  sessionWall: false,
  // Task 506: tab-strip adaptive compression (tiered tab width once >8 tabs,
  // floor 84px) ships off — with it off the strip keeps the exact fixed widths.
  tabCompress: false,
  // Task 507: subagent detail view ships off — dual state, off = plan C
  // (inline preview expansion + widen affordance), on = plan A (row click
  // opens the read-only in-dock detail view with a back button).
  subagentDetail: false,
  // 任务 856 ③: the unified collaboration-hub entry (信件中心 + 群聊两图标
  // 合一) ships off (铁律 2 — same contract as the task-677 switch it renames;
  // the stored value migrates server-side at decode). With it off the utility
  // row renders exactly the pre-856 layout minus the two merged icons; a flip
  // applies on settings save.
  collabHub: false,
  // 任务 704: the trajectory view ships off (铁律 2). With it off the topicbar
  // renders exactly the pre-704 layout and the transcript is the only surface;
  // a flip applies on settings save (no restart).
  trajectoryView: false,
  // 任务 783: OpenCode Go usage sidebar gate ships off (163 前端门 extended to
  // the right-dock card — while off the card renders nothing and issues no
  // network call); a flip applies on settings save (no restart).
  opencodeGoUsage: false,
  // 任务 826: left-sidebar / right-dock hover-to-expand ship off (铁律 2).
  // With a switch off no hover zone renders and no mouse handler is armed —
  // the sidebar/dock behave exactly as before the feature existed. Each side
  // is independent (separate key, separate timers). A flip applies on
  // settings save (no restart).
  sidebarHoverLeft: false,
  sidebarHoverRight: false,
  // 任务 686: the workflow visualization panel (command-palette entry opening
  // the DAG flow view) ships off (铁律 2). With it off no palette entry renders
  // and the panel chunk never loads — the palette is byte-for-byte unchanged.
  // A flip applies on settings save (no restart).
  workflowPanel: false,
  // 任务 932: 概览面板最下方 events 文件大小行（可观测性展示件）挂在既有
  // 【实验室-可观测性-监控】perf monitor 行（experimental_perf_monitor）下——
  // 不新增独立开关（932 立项约束：倾向并入）。默认关（铁律 2：关闭时概览
  // DOM 与引入前逐字节一致）；开启才渲染该行并读 PerfTimeSeries 落盘投影。
  // 数据源 = perf 采样器同一遍 walk 的 eventsMb 落盘样本（零新增 IO）。
  // A flip applies on settings save (no restart).
  perfMonitor: false,
  // 任务 940: composer 权限选择器形态二选一（experimental_approval_select）
  // ships off (铁律 2) — with it off the permission picker keeps the exact
  // segmented button group; on swaps it for a compact dropdown (zcode-style).
  // A flip applies on settings save (no restart).
  approvalSelect: false,
  // 任务 959 ①: transcript markdown-table re-flow (experimental_table_wrap)
  // ships off (铁律 2) — with it off wide tables keep the exact max-content
  // width + horizontal scroll of the pre-959 layout; on wraps cells to the
  // current pane width. A flip applies on settings save (no restart).
  tableWrap: false,
  // 任务 959 ②: composer split-pane send-target picker form
  // (experimental_send_target_select) ships off (铁律 2) — with it off the
  // left/right/both button group is byte-for-byte unchanged. A flip applies
  // on settings save (no restart).
  sendTargetSelect: false,
  // tl 1165: smooth streaming scroll follow (experimental_smooth_scroll_follow)
  // ships off (铁律 2) — with it off every streaming tail write keeps the
  // instant per-write positioning exactly as before, with no extra listeners
  // or animations; on switches tail follow to native smooth scrollTo behind
  // the DSH ScrollFollow guards (outstanding-target gate, reader-gesture
  // cancel, reduced-motion instant fallback). A flip applies on settings
  // save (no restart).
  smoothScrollFollow: false,
  // 任务 1024：composer 草稿优化（发送左侧改写按钮）。默认关（铁律 2）——
  // 关闭时 composer 工具栏与引入前逐字节一致；保存即换态免重启。
  draftRewrite: false,
  // tl 1132：DSH 式启动遮罩（logo+副文案+点动画，核心 UI 挂载完成后淡出）。
  // 默认关（铁律 2 + 用户 2026-10-24 steer「做成实验特性，允许开关，方便
  // 对比」）——关闭时 splash 不渲染，启动行为与引入前一致；保存即换态。
  startupSplash: false,
};

// Task 651: the tab permission indicator is a three-mode setting, not a bool,
// so it lives beside the boolean lab flags: "badge" (default) keeps the
// plan/goal/auto/yolo text badges, "off" hides the per-tab permission
// indicator entirely, "background" paints the low-opacity (10%) per-mode tab
// background instead of the badges. The server resolves the legacy task-504
// experimental_tab_mode_tint bool into this setting, and the settings save
// re-applies it via the same boot snapshot — no restart.
export type TabPermissionIndicatorMode = "badge" | "off" | "background";

export function normalizeTabPermissionIndicator(value: unknown): TabPermissionIndicatorMode {
  return value === "off" || value === "background" ? value : "badge";
}

let tabPermissionIndicator: TabPermissionIndicatorMode = "badge";

const flags: Record<LabFeatureFlag, boolean> = { ...defaults };
const listeners = new Set<() => void>();

/** Boot-time (or test) entry: replace the whole flag set from the snapshot. */
export function applyLabFlags(next: Partial<Record<LabFeatureFlag, boolean>>): void {
  for (const key of Object.keys(defaults) as LabFeatureFlag[]) {
    flags[key] = next[key] ?? defaults[key];
  }
  reportFrontendLog(
    "desktop-prefs",
    "lab flags",
    `questionSearch=${flags.questionSearch} subagentTps=${flags.subagentTps} completionSummary=${flags.completionSummary} quickCommands=${flags.quickCommands} subagentPanel=${flags.subagentPanel} sessionWall=${flags.sessionWall} tabCompress=${flags.tabCompress} subagentDetail=${flags.subagentDetail} collabHub=${flags.collabHub} trajectoryView=${flags.trajectoryView} opencodeGoUsage=${flags.opencodeGoUsage} sidebarHoverLeft=${flags.sidebarHoverLeft} sidebarHoverRight=${flags.sidebarHoverRight} workflowPanel=${flags.workflowPanel} perfMonitor=${flags.perfMonitor} approvalSelect=${flags.approvalSelect} tableWrap=${flags.tableWrap} sendTargetSelect=${flags.sendTargetSelect} smoothScrollFollow=${flags.smoothScrollFollow} draftRewrite=${flags.draftRewrite} startupSplash=${flags.startupSplash}`,
  );
  for (const listener of listeners) listener();
}

/**
 * Boot-time (or settings-save) entry for the task-651 three-mode tab
 * permission indicator. Unknown/absent values normalize to "badge" — the
 * server already folds the legacy 504 bool in, so the frontend only ever sees
 * the resolved string.
 */
export function applyTabPermissionIndicator(value: unknown): void {
  const next = normalizeTabPermissionIndicator(value);
  if (next === tabPermissionIndicator) return;
  tabPermissionIndicator = next;
  for (const listener of listeners) listener();
}

export function tabPermissionIndicatorMode(): TabPermissionIndicatorMode {
  return tabPermissionIndicator;
}

export function labFlagEnabled(key: LabFeatureFlag): boolean {
  return flags[key];
}

export function onLabFlagsChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
