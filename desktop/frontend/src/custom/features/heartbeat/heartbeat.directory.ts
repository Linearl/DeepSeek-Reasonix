import { useEffect, useMemo, useState } from "react";
import { app } from "../../../lib/bridge";
// tl1851 ①: bound-conversation display names. The bare topicId on the task
// list badge and in the editor becomes「项目名-会话名」; the id drops to a
// tooltip / small text. The name resolves through the SAME directory the
// picker's search already lists (ListAddressableSessions), whose backend title
// already follows the CustomTitle > TopicTitle > file-stem priority the
// sidebar uses (tl1793) — so a session rename follows on the next directory
// fetch (panel activation / task reload), with zero new title sources.
//
// Project names come from ListWorkspaces — the same map the task list already
// builds for its scope labels; global conversations get the shared
// "heartbeat.scopeGlobal" label ("Global" / "全局"), matching the
// "Global-向上反馈问题" shape the request names as the example.

export interface HeartbeatSessionEntry {
  topicId: string;
  title: string;
  scope?: string;
  workspaceRoot?: string;
}

export interface HeartbeatSessionDirectory {
  /** topicId -> directory entry, for every conversation this machine knows. */
  byTopic: Record<string, HeartbeatSessionEntry>;
  /** workspaceRoot -> project display name (ListWorkspaces). */
  workspaceNames: Record<string, string>;
}

export const emptySessionDirectory: HeartbeatSessionDirectory = { byTopic: {}, workspaceNames: {} };

/**
 * Fetches the session directory + workspace names while `active` holds. The
 * directory re-fetches whenever `revision` changes (the panel's task list
 * object identity changes on every load/save/trigger reload), which is what
 * makes a renamed session follow its display without a page reload.
 */
export function useHeartbeatSessionDirectory(active: boolean, revision: unknown): HeartbeatSessionDirectory {
  const [directory, setDirectory] = useState<HeartbeatSessionDirectory>(emptySessionDirectory);
  useEffect(() => {
    if (!active) return;
    let cancelled = false;
    void Promise.all([
      app.ListAddressableSessions().catch(() => []),
      app.ListWorkspaces().catch(() => []),
    ]).then(([sessions, workspaces]) => {
      if (cancelled) return;
      const byTopic: Record<string, HeartbeatSessionEntry> = {};
      for (const item of sessions ?? []) {
        if (!item.topicId) continue;
        byTopic[item.topicId] = {
          topicId: item.topicId,
          title: item.title || item.topicId,
          scope: item.scope,
          workspaceRoot: item.workspaceRoot,
        };
      }
      const workspaceNames: Record<string, string> = {};
      for (const ws of workspaces ?? []) {
        if (ws.path) workspaceNames[ws.path] = ws.name;
      }
      setDirectory({ byTopic, workspaceNames });
    });
    return () => { cancelled = true; };
  }, [active, revision]);
  return directory;
}

/**
 *「项目名-会话名」for a bound topicId, or a fallback. Resolution order:
 * 1. directory entry → project name (workspace map, else path tail) + title;
 * 2. no directory entry (racing fetch / foreign id) → the raw id, exactly the
 *    pre-tl1851 display, so nothing regresses when a name cannot be resolved.
 */
export function heartbeatBoundTopicLabel(
  topicId: string,
  directory: HeartbeatSessionDirectory,
  globalLabel: string,
): { name: string; resolved: boolean } {
  const entry = directory.byTopic[topicId];
  if (!entry) return { name: topicId, resolved: false };
  const isProject = entry.scope === "project" && entry.workspaceRoot;
  const project = isProject
    ? (directory.workspaceNames[entry.workspaceRoot as string]
      || (entry.workspaceRoot as string).split("/").pop()
      || (entry.workspaceRoot as string))
    : globalLabel;
  return { name: `${project}-${entry.title}`, resolved: true };
}

/** Convenience: memoized label resolver bound to a directory snapshot. */
export function useHeartbeatBoundTopicLabel(directory: HeartbeatSessionDirectory, globalLabel: string) {
  return useMemo(() => {
    return (topicId: string): { name: string; resolved: boolean } =>
      heartbeatBoundTopicLabel(topicId, directory, globalLabel);
  }, [directory, globalLabel]);
}
