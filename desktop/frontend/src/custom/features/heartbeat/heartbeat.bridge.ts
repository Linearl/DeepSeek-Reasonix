// Heartbeat panel bridge — typed wrappers around app heartbeat bindings.
// Custom components should import from here instead of calling app.* directly
// so that heartbeat-specific calls are scoped to this feature.

import { app } from "../../../lib/bridge";
import type { HeartbeatTask } from "./heartbeat.types";

interface HeartbeatConfigView {
  revision: number;
  etag: string;
  tasks: HeartbeatTask[];
  // 任务 823 ②: fail-open health surface. Non-empty when the last load saw an
  // invalid config — the engine keeps running its in-memory tasks and the
  // panel renders a resident warning banner instead of a silent empty list.
  configError?: string;
}

let loadedConfigToken: Pick<HeartbeatConfigView, "revision" | "etag"> | null = null;
let loadedTasks: HeartbeatTask[] = [];
let lastConfigError = "";
let configQueue: Promise<void> = Promise.resolve();

// heartbeatConfigError returns the last observed config health error (""
// = healthy). Read after heartbeatListTasks/heartbeatMutateTasks; it is the
// data source for the panel's invalid-config banner.
export function heartbeatConfigError(): string {
  return lastConfigError;
}

function enqueueConfigOperation<T>(operation: () => Promise<T>): Promise<T> {
  const result = configQueue.then(operation, operation);
  configQueue = result.then(() => undefined, () => undefined);
  return result;
}

async function reloadConfig(): Promise<HeartbeatTask[]> {
  const raw = await app.HeartbeatReloadConfig();
  const view = (raw ?? { revision: 0, etag: "", tasks: [] }) as HeartbeatConfigView;
  loadedConfigToken = { revision: view.revision || 0, etag: view.etag || "" };
  loadedTasks = Array.isArray(view.tasks) ? view.tasks : [];
  lastConfigError = view.configError ?? "";
  return loadedTasks;
}

async function saveConfig(tasks: HeartbeatTask[]): Promise<HeartbeatTask[]> {
  const view = await app.HeartbeatSaveConfig({
    revision: loadedConfigToken?.revision || 0,
    etag: loadedConfigToken?.etag || "",
    tasks,
  });
  const saved = (view ?? { revision: 0, etag: "" }) as HeartbeatConfigView;
  loadedConfigToken = { revision: saved.revision || 0, etag: saved.etag || "" };
  loadedTasks = Array.isArray(saved.tasks) ? saved.tasks : tasks;
  lastConfigError = saved.configError ?? "";
  return loadedTasks;
}

export function heartbeatListTasks(): Promise<HeartbeatTask[]> {
  return enqueueConfigOperation(reloadConfig);
}

export function heartbeatMutateTasks(mutate: (tasks: HeartbeatTask[]) => HeartbeatTask[]): Promise<HeartbeatTask[]> {
  return enqueueConfigOperation(async () => {
    if (!loadedConfigToken) await reloadConfig();
    const current = loadedTasks.map((task) => ({ ...task }));
    try {
      return await saveConfig(mutate(current));
    } catch (error) {
      try {
        await reloadConfig();
      } catch {
        // Preserve the original mutation error; the next operation will retry.
      }
      throw error;
    }
  });
}

export function heartbeatTriggerNow(id: string): Promise<void> {
  return app.HeartbeatTriggerNow(id);
}

export function heartbeatGenerateID(): Promise<string> {
  return app.HeartbeatGenerateID();
}
