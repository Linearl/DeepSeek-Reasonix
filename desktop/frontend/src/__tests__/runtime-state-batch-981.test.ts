// Run: tsx src/__tests__/runtime-state-batch-981.test.ts
//
// 981 二轮（候选2，残余放大链 B）：runtime 通道前端合批——生产单例的 commit
// 按 RUNTIME_COMMIT_BATCH_MS（400ms）合并、窗口内只保最新一帧；内容等价的
// 投影（sessions/topics 逐引用浅比较，引用由 acceptRuntimeState 保住）跳过
// notify。验收判据（1125+1145 块，硬条目）：活跃轮次 60s 内 commit→notify
// ≤ 60s/400ms = 150（修前为每次翻转，无上限）。
// 钉住：① 合批窗口内 N 次 commit 只 notify 一次且落地最新帧；② 等价投影
// 不 notify 但 revision 记账前移；③ fail 丢弃在途 pending（失败比在途更新
// 更新）；④ 生产单例确实以 400ms 合批运行。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { acceptRuntimeState } from "../lib/runtimeStateReducer";
import { createRuntimeStateStore, RUNTIME_COMMIT_BATCH_MS, runtimeStateStore, type RuntimeProjection, type RuntimeState } from "../lib/runtimeStateStore";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

console.log("\nruntime state 981 round-2 commit batching");

const state: RuntimeState = { schemaVersion: 1, runtimeEpoch: "ctrl", revision: 1, phase: "executing", running: true,
  turnId: "turn", turnStatus: "in_progress", turnEventSeq: 1, pendingPrompt: false, cancelRequested: false, cancellable: true, backgroundJobs: 0, activity: "thinking" };
const projection = (revision: number, changes: Partial<RuntimeState> = {}): RuntimeProjection => ({ epoch: "app-batch", revision, topics: [],
  sessions: [{ tabId: "a", scope: "project", workspaceRoot: "/repo", topicId: "t1", sessionPath: "/repo/s.jsonl", sessionGeneration: 1,
    open: true, remote: false, freshness: "synced", state: { ...state, revision, ...changes } }] });

// ── ① 合批窗口：N 次翻转 commit → 1 次 notify，落地最新帧 ──────────────────
{
  const store = createRuntimeStateStore({ batchMs: 25 });
  let notifies = 0;
  store.subscribe(() => { notifies += 1; });
  for (let turn = 1; turn <= 12; turn++) store.commit(projection(turn, { activity: turn % 2 ? "thinking" : "streaming" }));
  ok(notifies === 0, "窗口内 12 次翻转 commit 一次都不 notify（合批中）");
  ok(store.getSnapshot() === undefined || store.getSnapshot()?.revision !== 12, "窗口未落前不暴露中间帧");
  await sleep(80);
  ok(notifies === 1, "窗口结束只 notify 一次");
  ok(store.getSnapshot()?.revision === 12, "落地的是最新一帧（只保最新）");
  // 窗口重开：下一轮翻转照常 notify。
  store.commit(projection(13, { phase: "idle", running: false }));
  await sleep(80);
  ok(notifies === 2 && store.getSnapshot()?.revision === 13, "新窗口重新开闸（延迟 ≤ 一个窗口）");
  process.stdout.write(`  READ  活跃轮次 12 次翻转 commit（同一窗口）→ notify 1 次（修前=12 次；60s 判据 ≤150）\n`);
}

// ── ② flush：立刻落地在途合批（测试同步断言 + 卸载收尾） ───────────────────
{
  const store = createRuntimeStateStore({ batchMs: 400 });
  let notifies = 0;
  store.subscribe(() => { notifies += 1; });
  store.commit(projection(7));
  store.flush();
  ok(notifies === 1 && store.getSnapshot()?.revision === 7, "flush 立即落地 pending（无需等窗口）");
  store.flush();
  ok(notifies === 1, "无 pending 的 flush 是 no-op（不重复 notify）");
}

// ── ③ fail 丢弃在途 pending：失败比在途更新更新 ────────────────────────────
{
  const store = createRuntimeStateStore({ batchMs: 25 });
  let notifies = 0;
  store.subscribe(() => { notifies += 1; });
  store.commit(projection(4));
  store.fail();
  await sleep(80);
  ok(store.getFailed() === true, "fail 后保持降级态（在途 pending 不得把 failed 清回 false）");
  ok(store.getSnapshot()?.revision !== 4, "被丢弃的 pending 不落地");
  store.commit(projection(5));
  store.flush();
  ok(store.getFailed() === false && store.getSnapshot()?.revision === 5, "失败后的下一次 commit 恢复（failed 清除 + notify）");
  ok(notifies === 2, "fail 与恢复各 notify 一次");
}

// ── ④ 内容等价跳过 notify（reducer 保 topics/sessions 引用 + revision 前移） ──
{
  const store = createRuntimeStateStore();
  let notifies = 0;
  store.subscribe(() => { notifies += 1; });
  const withTopic = (revision: number, stateRevision: number): RuntimeProjection => ({
    epoch: "app-eq", revision, topics: [{ scope: "project", workspaceRoot: "/repo", node: { key: "k-t1", kind: "topic", label: "T1", topicId: "t1", children: [] } }],
    sessions: [{ tabId: "a", scope: "project", workspaceRoot: "/repo", topicId: "t1", sessionPath: "/repo/s.jsonl", sessionGeneration: 1,
      open: true, remote: false, freshness: "synced", state: { ...state, revision: stateRevision } }],
  });
  assert.equal(acceptRuntimeState(store, withTopic(1, 1)), "accepted");
  const first = store.getSnapshot();
  ok(notifies === 1, "首个投影照常 notify");
  // 后端重发内容等价的更高 revision 投影（投影记账前进、会话内容未变）。
  assert.equal(acceptRuntimeState(store, withTopic(2, 1)), "accepted");
  ok(notifies === 1, "内容等价投影跳过 notify（硬判据：等价不重渲染）");
  ok(store.getSnapshot()?.revision === 2, "revision 记账仍前移（后续 stale 判定不失真）");
  ok(store.getSnapshot()?.topics[0] === first?.topics[0], "未变 topic 回交旧引用（reducer 保引用）");
  ok(store.getSnapshot()?.sessions[0] === first?.sessions[0], "未变 session 回交旧引用（962 前既有口径）");
  // 内容真正变化时不能误跳。
  assert.equal(acceptRuntimeState(store, withTopic(3, 2)), "accepted");
  ok(notifies === 2 && store.getSnapshot()?.sessions[0].state.revision === 2, "内容变化照常 notify（等价跳过不误伤）");
}

// ── ⑤ 生产单例确实以 RUNTIME_COMMIT_BATCH_MS 合批 ─────────────────────────
{
  ok(RUNTIME_COMMIT_BATCH_MS === 400, "合批窗口 400ms（60s/400=150 判据口径）");
  const source = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "../lib/runtimeStateStore.ts"), "utf8");
  ok(/createRuntimeStateStore\(\{ batchMs: RUNTIME_COMMIT_BATCH_MS \}\)/.test(source), "生产单例以合批参数构造（接线钉住）");
  let notifies = 0;
  const stop = runtimeStateStore.subscribe(() => { notifies += 1; });
  runtimeStateStore.commit(projection(101));
  runtimeStateStore.commit(projection(102));
  ok(notifies === 0, "生产单例窗口内不 notify");
  await sleep(RUNTIME_COMMIT_BATCH_MS + 80);
  ok(notifies === 1 && runtimeStateStore.getSnapshot()?.revision === 102, "生产单例一个窗口只 notify 一次、落最新帧");
  runtimeStateStore.flush();
  stop();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
