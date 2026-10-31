import assert from "node:assert/strict";
import { purgeTrashBatch, type TrashBatchProgress } from "../lib/trashOperations";
const calls: string[] = [];
let release!: () => void;
const pending = new Promise<void>((resolve) => { release = resolve; });
const paths = ["a", "b", "a", "c"];
// tl1306 断言③异步：批次立即返回未完成的 promise，调用方不被阻塞（下方同步断言
// 在首个条目仍 await 时即可执行），逐条串行落定而非并行。
const progress: TrashBatchProgress[] = [];
const batch = purgeTrashBatch(paths, async (path) => {
  calls.push(path);
  if (path === "a") await pending;
  if (path === "b") throw new Error("busy");
}, (event) => progress.push(event));
paths.push("later");
assert.deepEqual(calls, ["a"], "batch executes sequentially and returns before completion");
release();
const result = await batch;
assert.deepEqual(calls, ["a", "b", "c"], "snapshot excludes new arrivals and duplicate paths");
// tl1306 断言②摘要：succeeded/failed 保持既有形状，failures 逐条携带原因。
assert.deepEqual(result, { succeeded: ["a", "c"], failed: ["b"], failures: [{ path: "b", reason: "busy" }] });
// tl1306 断言①进度：每落定一条回报一次，done 单调推进到 total，成功/失败分计数同步。
assert.deepEqual(progress, [
  { done: 1, total: 3, succeeded: 1, failed: 0 },
  { done: 2, total: 3, succeeded: 1, failed: 1 },
  { done: 3, total: 3, succeeded: 2, failed: 1 },
], "progress reports after each settled item with monotonic done up to total");
const retryCalls: string[] = [];
await purgeTrashBatch(result.failed, async (path) => { retryCalls.push(path); });
assert.deepEqual(retryCalls, ["b"], "retry never repeats known successful deletions");
console.log("PASS trash partial failure, failure reasons, per-item progress and explicit failed-only retry");
