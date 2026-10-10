import type { Dispatch, SetStateAction } from "react";
import { create } from "zustand";
import type { SettingsInitialFocus } from "../components/SettingsPanel";
import type { SettingsTab } from "../lib/types";
import { applySetState } from "./setState";

export type AppPage = { kind: "workspace" } | { kind: "settings"; tab: SettingsTab } | { kind: "trash" } | { kind: "automation" };
type NavigationState = {
  page: AppPage;
  workspaceFocus: HTMLElement | null;
  generation: number;
  visitedTrash: boolean;
  visitedAutomation: boolean;
  lastSettingsTarget: SettingsTab;
  settingsFocus: SettingsInitialFocus | null;
  automationReturn: boolean;
  openPage: (page: AppPage) => void;
  returnToWorkspace: () => void;
  setSettingsTarget: Dispatch<SetStateAction<SettingsTab | null>>;
  setSettingsFocus: Dispatch<SetStateAction<SettingsInitialFocus | null>>;
  enterConversation: () => void;
  returnFromAutomationLink: (generation: number) => void;
};
// 任务 739：设置页打开钩子。所有设置入口（侧栏/顶栏按钮、命令面板、启动闸门、
// fork 通知跳转）都汇聚到 openPage({kind:"settings"})——它在点击处理器的同步帧
// 里执行，是「数据请求发出时机前移」的唯一收口点。钩子由 App 装配层注册
// （见 lib/settingsPrefetch），store 本体不依赖桥接层，测试环境零耦合。
let settingsOpenHook: (() => void) | null = null;
export function setSettingsOpenHook(hook: (() => void) | null): void {
  settingsOpenHook = hook;
}

export const useAppNavigationStore = create<NavigationState>((set, get) => ({
  page: { kind: "workspace" }, workspaceFocus: null, generation: 0, visitedTrash: false, visitedAutomation: false,
  lastSettingsTarget: "general", settingsFocus: null, automationReturn: false,
  openPage: (page) => {
    if (page.kind === "settings") settingsOpenHook?.();
    set((state) => ({
      page,
      workspaceFocus: state.page.kind === "workspace" && page.kind !== "workspace" && typeof document !== "undefined" ? document.activeElement as HTMLElement | null : state.workspaceFocus,
      generation: state.generation + 1,
      visitedTrash: state.visitedTrash || page.kind === "trash",
      visitedAutomation: state.visitedAutomation || page.kind === "automation",
      lastSettingsTarget: page.kind === "settings" ? page.tab : state.lastSettingsTarget,
      automationReturn: false,
    }));
  },
  returnToWorkspace: () => get().openPage({ kind: "workspace" }),
  enterConversation: () => get().openPage({ kind: "workspace" }),
  setSettingsTarget: (update) => {
    const state = get();
    const target = applySetState(state.page.kind === "settings" ? state.page.tab : null, update);
    if (target === null) state.returnToWorkspace();
    else state.openPage({ kind: "settings", tab: target });
  },
  setSettingsFocus: (update) => set((state) => ({ settingsFocus: applySetState(state.settingsFocus, update) })),
  returnFromAutomationLink: (generation) => {
    if (get().generation === generation) set({ page: { kind: "workspace" }, automationReturn: true });
  },
}));
