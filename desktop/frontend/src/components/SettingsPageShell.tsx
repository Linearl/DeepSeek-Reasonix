// tl 1134：设置页外壳——自 SettingsPanel.tsx 下沉（实验室二级 chunk 拆分）。
// SettingsPageShell / settingsPageLayout / settingsTabLabel / SectionProps 被主面板与
// 实验室二级 chunk 两边共用；放独立模块避免实验室 chunk 反向静态依赖主面板。
import type { ReactNode } from "react";
import { useProviderT as useT } from "../lib/providerSettingsLocale";
import type { SettingsTab, SettingsView } from "../lib/types";

export function SettingsPageShell({ s: _s, tab, children }: { s: SettingsView | null; tab: SettingsTab; busy: boolean; apply: (fn: () => Promise<unknown>) => Promise<boolean>; children: ReactNode }) {
  const t = useT();
  return (
    <div aria-label={settingsTabPageTitle(tab, t)} className={`settings-page settings-page--${settingsPageKind(tab)} settings-page--${tab}`} data-layout={settingsPageLayout(tab)}>
      {children}
    </div>
  );
}

export function settingsPageLayout(tab: SettingsTab): "form" | "list" | "detail" | "tool" {
  if (tab === "providers" || tab === "bots") return "detail";
  if (["model-stats", "diagnostics", "hooks", "updates"].includes(tab)) return "tool";
  if (["mcp", "remote", "skills", "subagents", "plugins", "memory", "shortcuts"].includes(tab)) return "list";
  return "form";
}

function settingsPageKind(tab: SettingsTab): "form" | "manager" {
  switch (tab) {
    case "providers":
    case "model-stats":
    case "bots":
    case "hooks":
    case "diagnostics":
    case "mcp":
    case "remote":
    case "skills":
    case "subagents":
    case "plugins":
    case "memory":
    case "appearance":
      return "manager";
    default:
      return "form";
  }
}


function settingsTabPageTitle(id: SettingsTab, t: ReturnType<typeof useT>): string {
  switch (id) {
    case "mcp": return t("settings.tab.mcp");
    case "skills": return t("settings.tab.skills");
    case "plugins": return t("settings.tab.plugins");
    case "memory": return t("settings.tab.memory");
    case "diagnostics": return t("settings.tab.diagnostics");
    case "shortcuts": return t("settings.tab.shortcuts");
    default: return settingsTabLabel(id, t);
  }
}

export type SectionProps = {
  s: SettingsView;
  busy: boolean;
  apply: (fn: () => Promise<unknown>) => Promise<boolean>;
};

export function settingsTabLabel(id: SettingsTab, t: ReturnType<typeof useT>): string {
  switch (id) {
    case "model-stats": return t("settings.modelTab.stats");
    case "general":
      return t("settings.tab.general");
    case "models":
      return t("settings.models.preferences");
    case "providers":
      return t("settings.models.services");
    case "bots":
      return t("settings.tab.bots");
    case "mcp":
      return t("settings.tab.mcp");
    case "remote":
      return t("settings.tab.remote");
    case "localserver":
      return t("localserver.tabLabel");
    case "skills":
      return t("settings.tab.skills");
    case "subagents":
      return t("settings.tab.subagents");
    case "plugins":
      return t("settings.tab.plugins");
    case "experimental":
      return t("settings.tab.experimental");
    case "memory":
      return t("settings.tab.memory");
    case "hooks":
      return t("settings.tab.hooks");
    case "diagnostics":
      return t("settings.tab.diagnostics");
    case "shortcuts":
      return t("settings.tab.shortcuts");
    case "network":
      return t("settings.tab.network");
    case "permissions":
      return t("settings.tab.permissions");
    case "sandbox":
      return t("settings.tab.sandbox");
    case "appearance": return t("settings.tab.appearance");
    case "storage": return t("settings.tab.storage");
    case "updates":
      return t("settings.tab.updates");
  }
}
