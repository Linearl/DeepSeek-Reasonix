// shellPrefs holds the status-bar preferences the shell renders from. They are written
// once per settings load and read by the status bar, which sits far below the wiring that
// loads them — so the value lives here rather than being threaded down (task 38). The
// default for the label style is the fork's "text", matching useDesktopPreferences so the
// two never disagree during startup.

import { create } from "zustand";
import { DEFAULT_STATUS_BAR_ITEMS, normalizeStatusBarItems, type StatusBarItemId } from "../lib/statusBarItems";

type ShellPrefsState = {
  statusBarStyle: "icon" | "text";
  statusBarItems: StatusBarItemId[];
};

export const useShellPrefsStore = create<ShellPrefsState>(() => ({
  statusBarStyle: "text",
  statusBarItems: [...DEFAULT_STATUS_BAR_ITEMS],
}));

export const setStatusBarStyle = (style: string | undefined): void => {
  const next = style === "text" ? "text" : style === "icon" ? "icon" : null;
  if (!next) return;
  useShellPrefsStore.setState((current) => (current.statusBarStyle === next ? current : { statusBarStyle: next }));
};

export const setStatusBarItems = (items: readonly string[] | undefined): void => {
  const next = normalizeStatusBarItems(items);
  useShellPrefsStore.setState((current) =>
    current.statusBarItems.length === next.length && current.statusBarItems.every((id, i) => id === next[i])
      ? current
      : { statusBarItems: next },
  );
};
