export type RuntimeMetaSnapshot = {
  runtime?: { epoch?: string };
  running: boolean;
  turnStartedAt?: number;
  pendingPrompt?: boolean;
  backgroundJobs?: number;
  cancelRequested?: boolean;
  cancellable?: boolean;
  /** 任务461-P7 三级终止: escalation mirror (1 normal / 2 grace / 3 force). */
  stopLevel?: number;
  stopDeadlineUnix?: number;
  turnId?: string;
  turnStatus?: string;
  turnEventSeq?: number;
  turnReplayAfterSeq?: number;
};

export function foregroundRunningFromRuntimeMeta(meta: RuntimeMetaSnapshot): boolean {
  if (typeof meta.cancellable === "boolean") return meta.cancellable;
  if ((meta.backgroundJobs ?? 0) > 0 && !meta.pendingPrompt) return false;
  return Boolean(meta.running);
}
