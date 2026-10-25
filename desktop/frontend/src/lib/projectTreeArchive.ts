import { useCallback, useRef, useState } from "react";
import { app } from "./bridge";
import { invalidateProjectTreeTopicLoads, projectTreeFolderKeyForSession, projectTreeFolderKeyForTopic } from "./projectTreeTopic";
import type { ToastContextValue } from "./toast";
import type { ProjectNode } from "./types";

export { projectTreeWithoutTopics } from "./projectTreeTopic";

type TopicPageState = { nextCursor?: string; loading: boolean };

export type ProjectTreeRefreshOptions = {
  reloadTopicKeys?: string[];
  reloadAllTopics?: boolean;
  onReloadStarted?: () => void;
};

export type ProjectTreeRefresh = (options?: ProjectTreeRefreshOptions) => Promise<void>;

export async function reloadProjectTreeTopics(
  projects: ProjectNode[],
  options: ProjectTreeRefreshOptions | undefined,
  load: (project: ProjectNode) => Promise<void>,
): Promise<void> {
  const keys = new Set(options?.reloadTopicKeys ?? []);
  const targets = projects.filter((project) => options?.reloadAllTopics || keys.has(project.key));
  const pendingLoads = targets.map(load);
  if (pendingLoads.length > 0) options?.onReloadStarted?.();
  await Promise.all(pendingLoads);
}

export function enqueueProjectTreeArchive(previous: Promise<void>, work: () => Promise<void>): Promise<void> {
  return previous.catch(() => undefined).then(work);
}

export function projectTreeTopicArchiveTargetKey(
  scope: "global" | "project",
  workspaceRoot: string,
  topicId: string,
): string {
  return JSON.stringify(["topic", scope, workspaceRoot.trim(), topicId.trim()]);
}

export function projectTreeSessionArchiveTargetKey(sessionPath: string): string {
  return JSON.stringify(["session", sessionPath.trim()]);
}

// tl 1103: the desktop backend marks the archive failures TrashTopicForce can
// actually clear (active-work gate, session-lease-busy family) with this
// stable machine code — same "reasonix_error:" channel as the inbox errors.
// Only marked failures may raise the force-archive confirm; every other
// failure (file locked by a scanner, disk full, …) must surface as a plain
// error and never be steered toward force.
export const TOPIC_ARCHIVE_FORCEABLE_PREFIX = "reasonix_error:topic_archive_forceable: ";

export function isTopicArchiveForceableError(error: unknown): boolean {
  const raw = error instanceof Error ? error.message : String(error);
  return raw.startsWith(TOPIC_ARCHIVE_FORCEABLE_PREFIX);
}

/** The human-readable backend cause with the machine prefix stripped — this is
 * what the confirm dialog shows as the failure reason. */
export function topicArchiveForceableReason(error: unknown): string {
  const raw = error instanceof Error ? error.message : String(error);
  return raw.startsWith(TOPIC_ARCHIVE_FORCEABLE_PREFIX) ? raw.slice(TOPIC_ARCHIVE_FORCEABLE_PREFIX.length) : raw;
}

export async function runProjectTreeArchiveJob({
  archive,
  commit,
  reload,
  finishPending,
  recover,
}: {
  archive: () => Promise<void>;
  commit: () => void;
  reload: () => Promise<void>;
  finishPending: () => void;
  recover: (error: unknown) => Promise<void>;
}): Promise<boolean> {
  try {
    await archive();
  } catch (error) {
    // Failed mutations must become visible to the recovery reload.
    finishPending();
    await recover(error);
    return false;
  }
  try {
    // A tombstone is a post-commit stale-response fence, not an optimistic
    // archive. Installing it only after backend success keeps rejected topics
    // visible throughout the mutation and its recovery reload.
    commit();
    // Keep the visible pending state active until the canonical folder page
    // has landed. The caller may release its stale-response tombstone once
    // that reload has acquired a newer request generation.
    await reload();
    return true;
  } finally {
    finishPending();
  }
}

export function projectTreeTrashingTopics(previous: Set<string>, topicId: string, trashing: boolean): Set<string> {
  const id = topicId.trim();
  if (!id || previous.has(id) === trashing) return previous;
  const next = new Set(previous);
  if (trashing) next.add(id);
  else next.delete(id);
  return next;
}

export function useProjectTreeArchiveState() {
  const topicsRef = useRef<Set<string>>(new Set());
  const tombstonesRef = useRef<Set<string>>(new Set());
  const [topics, setTopics] = useState<Set<string>>(new Set());
  const begin = useCallback((topicId: string) => {
    const id = topicId.trim();
    if (!id || topicsRef.current.has(id)) return false;
    topicsRef.current = projectTreeTrashingTopics(topicsRef.current, id, true);
    setTopics(topicsRef.current);
    return true;
  }, []);
  const commit = useCallback((topicId: string) => {
    tombstonesRef.current = projectTreeTrashingTopics(tombstonesRef.current, topicId, true);
  }, []);
  const end = useCallback((topicId: string) => {
    topicsRef.current = projectTreeTrashingTopics(topicsRef.current, topicId, false);
    tombstonesRef.current = projectTreeTrashingTopics(tombstonesRef.current, topicId, false);
    setTopics(topicsRef.current);
  }, []);
  const releaseTombstone = useCallback((topicId: string) => {
    tombstonesRef.current = projectTreeTrashingTopics(tombstonesRef.current, topicId, false);
  }, []);
  const currentTombstones = useCallback((): ReadonlySet<string> => tombstonesRef.current, []);
  return {
    trashingTopics: topics,
    beginTrashingTopic: begin,
    commitArchiveTombstone: commit,
    endTrashingTopic: end,
    releaseArchiveTombstone: releaseTombstone,
    currentArchiveTombstones: currentTombstones,
  };
}

export function useProjectTreeArchiveController({
  treeRef,
  topicLoadSeqRef,
  topicPageStateRef,
  updateTopicPageState,
  refreshRef,
  optimisticallyRemoveTopic,
  closeMenu,
  onTopicsChanged,
  showToast,
}: {
  treeRef: { current: ProjectNode[] };
  topicLoadSeqRef: { current: Record<string, number> };
  topicPageStateRef: { current: Record<string, TopicPageState> };
  updateTopicPageState: (key: string, next: TopicPageState) => void;
  refreshRef: { current: ProjectTreeRefresh };
  optimisticallyRemoveTopic: (topicId: string) => void;
  closeMenu: () => void;
  onTopicsChanged?: () => Promise<void> | void;
  showToast: ToastContextValue["showToast"];
}) {
  const {
    trashingTopics,
    beginTrashingTopic,
    commitArchiveTombstone,
    endTrashingTopic,
    releaseArchiveTombstone,
    currentArchiveTombstones,
  } = useProjectTreeArchiveState();
  const sessionTrashingRef = useRef<Set<string>>(new Set());
  const [trashingSessions, setTrashingSessions] = useState<Set<string>>(new Set());
  const archiveQueueRef = useRef<Promise<void>>(Promise.resolve());

  // tl 1103: when the archive fails, `onArchiveFailed` decides how the failure
  // is surfaced — return true after handling it (e.g. the force-archive confirm
  // consumed a lease-busy failure) and the plain error toast is skipped; return
  // false to keep the generic toast. The reload still runs on every path so a
  // failed mutation never leaves stale rows behind.
  const trashTopic = useCallback(async (topicId: string, onArchiveFailed?: (error: unknown) => Promise<boolean> | boolean) => {
    if (!beginTrashingTopic(topicId)) return;
    const folderKey = projectTreeFolderKeyForTopic(treeRef.current, topicId);
    const reloadOptions: ProjectTreeRefreshOptions = {
      reloadTopicKeys: folderKey ? [folderKey] : undefined,
      reloadAllTopics: !folderKey,
      onReloadStarted: () => releaseArchiveTombstone(topicId),
    };
    const invalidatedKeys = folderKey
      ? [folderKey]
      : treeRef.current.filter((node) => node.kind === "project" || node.kind === "global_folder").map((node) => node.key);
    closeMenu();

    const queued = enqueueProjectTreeArchive(archiveQueueRef.current, async () => {
      await runProjectTreeArchiveJob({
        archive: () => app.TrashTopic(topicId),
        commit: () => {
          commitArchiveTombstone(topicId);
          // Fence every load that captured the catalog before backend commit,
          // then remove the topic while the tombstone covers newer arrivals.
          invalidateProjectTreeTopicLoads(topicLoadSeqRef.current, invalidatedKeys);
          for (const key of invalidatedKeys) {
            updateTopicPageState(key, { ...topicPageStateRef.current[key], loading: false });
          }
          optimisticallyRemoveTopic(topicId);
        },
        reload: async () => {
          await refreshRef.current(reloadOptions);
          await Promise.resolve(onTopicsChanged?.()).catch(() => undefined);
        },
        finishPending: () => endTrashingTopic(topicId),
        recover: async (err) => {
          let handled = false;
          try {
            handled = (await onArchiveFailed?.(err)) === true;
          } catch {
            handled = false;
          }
          if (!handled) showToast(err instanceof Error ? err.message : String(err), "error");
          await refreshRef.current(reloadOptions);
        },
      });
    });
    archiveQueueRef.current = queued;
    await queued;
  }, [beginTrashingTopic, closeMenu, commitArchiveTombstone, endTrashingTopic, onTopicsChanged, optimisticallyRemoveTopic, refreshRef, releaseArchiveTombstone, showToast, topicLoadSeqRef, topicPageStateRef, treeRef, updateTopicPageState]);

  const trashSession = useCallback(async (rawSessionPath: string) => {
    const sessionPath = rawSessionPath.trim();
    if (!sessionPath || sessionTrashingRef.current.has(sessionPath)) return;
    const folderKey = projectTreeFolderKeyForSession(treeRef.current, sessionPath);
    const reloadOptions: ProjectTreeRefreshOptions = {
      reloadTopicKeys: folderKey ? [folderKey] : undefined,
      reloadAllTopics: !folderKey,
    };
    sessionTrashingRef.current = projectTreeTrashingTopics(sessionTrashingRef.current, sessionPath, true);
    setTrashingSessions(sessionTrashingRef.current);
    closeMenu();

    const queued = enqueueProjectTreeArchive(archiveQueueRef.current, async () => {
      try {
        await app.DeleteSession(sessionPath);
        await refreshRef.current(reloadOptions);
        await Promise.resolve(onTopicsChanged?.()).catch(() => undefined);
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), "error");
        await refreshRef.current(reloadOptions).catch(() => undefined);
      } finally {
        sessionTrashingRef.current = projectTreeTrashingTopics(sessionTrashingRef.current, sessionPath, false);
        setTrashingSessions(sessionTrashingRef.current);
      }
    });
    archiveQueueRef.current = queued;
    await queued;
  }, [closeMenu, onTopicsChanged, refreshRef, showToast, treeRef]);

  return { trashingTopics, trashingSessions, currentArchiveTombstones, trashTopic, trashSession };
}
