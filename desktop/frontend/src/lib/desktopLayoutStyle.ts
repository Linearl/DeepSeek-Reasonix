// tl 1134：桌面布局样式归一——自 SettingsPanel.tsx 下沉（实验室二级 chunk 拆分）。
// 主面板与实验室二级 chunk 两边共用；tl1191 起 App.tsx 也改为共用本模块
// （App.tsx 原有一份逐字节等价的本地副本，随模式分包一并收口）。

import { loadCachedLayoutStyle } from "./layoutPreferences";
import classicStylesheetURL from "../styles/mode-classic.css?url";
import workbenchStylesheetURL from "../styles/mode-workbench.css?url";
import creationStylesheetURL from "../styles/mode-creation.css?url";

export type DesktopLayoutStyle = "classic" | "workbench" | "creation";

export function normalizeDesktopLayoutStyle(style: string | undefined): DesktopLayoutStyle {
  if (style === "classic") return "classic";
  // Creation was dropped upstream (9deafd5eb) and its picker restored in
  // 2ed751a46, but this normalizer kept the drop: every read of
  // sandbox-style "creation" folded back to workbench, so the restored seg
  // silently snapped back to Workbench, App never gained app--creation, and
  // the settings bench stalled waiting for that class. Keep the restored
  // picker honest — accept what the picker and App.tsx both offer.
  if (style === "creation") return "creation";
  return "workbench";
}

// tl1191（2026-10-28 修）：per-mode stylesheets 改为 ?url + <link> 注入。
// 原实现用 dynamic import 让 Vite 自动注入 CSS chunk；实测在 Wails 自定义
// 协议下产物存在却不生效（症状：utility-row 的 flex 丢失 ⇒ 侧栏工具键竖排、
// 输入框重影）。主 styles.css 一直走 ?url + 手动 <link>（main.tsx），本模块
// 对齐同一机制：按需注入语义不变（只加载当前模式的文件），启动加速保留。
const injectedModeLinks = new Map<string, Promise<void>>();

function linkStylesheetOnce(href: string): Promise<void> {
  const existing = injectedModeLinks.get(href);
  if (existing) return existing;
  const job = new Promise<void>((resolve, reject) => {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = href;
    link.onload = () => resolve();
    link.onerror = () => reject(new Error(`failed to load mode stylesheet: ${href}`));
    document.head.appendChild(link);
  });
  injectedModeLinks.set(href, job);
  return job;
}

export function loadModeStylesheet(mode: DesktopLayoutStyle): Promise<unknown> {
  switch (mode) {
    case "classic":
      return linkStylesheetOnce(classicStylesheetURL);
    case "creation":
      return linkStylesheetOnce(creationStylesheetURL);
    default:
      return linkStylesheetOnce(workbenchStylesheetURL);
  }
}

// tl1191: startup prefetch for the boot parallel-load set (main.tsx
// mountApp). Reads the same synchronous first-paint cache as the
// desktopLayoutStyle initializer (task 40's no-flash rule) so the active
// mode's stylesheet — and, for classic, the lazy chrome chunk — is already
// in flight before React mounts; a mode the cache guessed wrong about gets
// loaded by App's effect instead. Failure here degrades to unstyled-mode
// rendering and must not block the boot sequence (main.tsx allSettled).
export function startupModeAssetPrefetch(): Promise<unknown>[] {
  const mode = normalizeDesktopLayoutStyle(loadCachedLayoutStyle() ?? "workbench");
  const jobs: Promise<unknown>[] = [loadModeStylesheet(mode)];
  if (mode === "classic") {
    jobs.push(import("../components/AppChrome"), import("../components/TabOverviewPanel"));
  }
  return jobs;
}
