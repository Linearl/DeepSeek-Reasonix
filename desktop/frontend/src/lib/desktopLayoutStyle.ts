// tl 1134：桌面布局样式归一——自 SettingsPanel.tsx 下沉（实验室二级 chunk 拆分）。
// 主面板与实验室二级 chunk 两边共用；App.tsx 有一份独立副本（本件不动）。

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
