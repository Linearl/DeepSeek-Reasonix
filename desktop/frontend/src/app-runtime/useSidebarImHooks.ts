import { useCallback, useState } from "react";
import { app } from "../lib/bridge";
import type { Translator } from "../lib/i18n";
import type { DesktopStartupSettingsView, SettingsView } from "../lib/types";
import { loadBotRuntimeStatus } from "./botRuntimeAdapter";
import {
  sidebarImConnectionsFromBot,
  sidebarImTopicSourcesFromBot,
  type SidebarImConnection,
  type SidebarImTopicSource,
} from "./sidebarImProjection";

// The IM owner hook lives apart from the projection helpers on purpose. Those helpers are pure
// functions with no React dependency, and the app-layer check enforces that a domain module
// reaching this file cannot transitively reach React. Keeping the hook in the projection file
// made `desktopNavigationOwner` a transitive React dependency through it.

// The desktop bridge is installed before React mounts and never changes, so this is a
// module-level fact rather than per-render state. As a hook-local value it would change on
// every render, which is what eslint flagged when the callbacks read it unlisted.
const NATIVE_RUNTIME = typeof window === "undefined" || Boolean(window.runtime);
export function useSidebarImOwner(t: Translator) {
  const [connections, setConnections] = useState<SidebarImConnection[]>([]);
  const [topicSources, setTopicSources] = useState<Record<string, SidebarImTopicSource>>({});
  const [detailConnectionId, setDetailConnectionId] = useState("");

  const reload = useCallback(async () => {
    const [settings, runtimeStatus] = await Promise.all([
      app.DesktopStartupSettings(),
      loadBotRuntimeStatus(),
    ]);
    setConnections(sidebarImConnectionsFromBot(settings.bot, t, runtimeStatus, NATIVE_RUNTIME));
    setTopicSources(sidebarImTopicSourcesFromBot(settings.bot, t));
  }, [t]);

  const refreshFromSettings = useCallback(
    async (settings: Pick<SettingsView | DesktopStartupSettingsView, "bot">) => {
      const runtimeStatus = await loadBotRuntimeStatus();
      setConnections(sidebarImConnectionsFromBot(settings.bot, t, runtimeStatus, NATIVE_RUNTIME));
      setTopicSources(sidebarImTopicSourcesFromBot(settings.bot, t));
    },
    [t],
  );

  return { connections, setConnections, topicSources, setTopicSources, detailConnectionId, setDetailConnectionId, reload, refreshFromSettings };
}
