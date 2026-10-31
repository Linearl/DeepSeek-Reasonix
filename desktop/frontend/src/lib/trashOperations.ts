export type TrashFailure = { path: string; reason: string };
export type TrashOperationSummary = { succeeded: string[]; failed: string[]; failures: TrashFailure[] };
/** tl1306：每落定一条回报一次——done 只计已落定（成功+失败），total 为去重后的目标总数。 */
export type TrashBatchProgress = { done: number; total: number; succeeded: number; failed: number };

// tl1306：失败原因要能出现在结果摘要里。桥调用 rejection 可能是 Error、
// 也可能是纯字符串（Wails 把 Go error 透传成消息文本）；空值兜底成可读文案，
// 不让明细里出现 "undefined"。
function trashFailureReason(err: unknown): string {
  if (err instanceof Error && err.message.trim()) return err.message.trim();
  const text = String(err ?? "").trim();
  return text || "unknown error";
}

export async function purgeTrashBatch(paths: string[], purge: (path: string) => Promise<void>, onProgress?: (progress: TrashBatchProgress) => void): Promise<TrashOperationSummary> {
  const targets = [...new Set(paths)];
  const result: TrashOperationSummary = { succeeded: [], failed: [], failures: [] };
  for (const path of targets) {
    try { await purge(path); result.succeeded.push(path); }
    catch (err) {
      result.failed.push(path);
      result.failures.push({ path, reason: trashFailureReason(err) });
    }
    onProgress?.({ done: result.succeeded.length + result.failed.length, total: targets.length, succeeded: result.succeeded.length, failed: result.failed.length });
  }
  return result;
}
