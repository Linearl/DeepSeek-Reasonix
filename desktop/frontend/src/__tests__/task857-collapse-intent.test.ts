// Run: tsx src/__tests__/task857-collapse-intent.test.ts
//
// 任务 857 回归钉（纯函数层）：「收起全部工作过程」后的新思考/新块不再自动展开。
//
// 用户现象（24.3 装机反馈，1146 澄清）：collapse-all 确实收起；继续工作一旦出现
// 新 thinking 流式，过程又自动展开。归因：collapse-all 只对当时已存在的 segment
// 写 userOverridden 条目，新 segment（新回合/回答后的新过程段）在 FoldMap 无条目，
// reconcile 按 defaultFoldOpen 给默认值——standard 下运行中即展开；fresh-run 分支
// （entry.running false→true）还会清除已有 override。修复：reconcileFoldEntries
// 增加会话级 collapseIntent 第 5 参（可选，默认 false=原语义），激活时
//   ① 新条目按意图收起并记 userOverridden=true；
//   ② fresh-run 不清除用户 override。
// 本钉同时锚定 intent=false 时两条原行为不变（既有折叠语义零漂移）。

import {
  EMPTY_FOLDS,
  reconcileFoldEntries,
  type FoldSegmentState,
} from "../lib/transcriptRows";
import {
  clearTranscriptFoldOverrideSessionForTest,
  readTranscriptCollapseIntent,
  setTranscriptCollapseIntent,
} from "../lib/transcriptFoldOverrides";

let passed = 0;
let failed = 0;

function ok(cond: unknown, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function segment(key: string, hasRunningWork: boolean, hasOutsideContent = true): FoldSegmentState {
  return { key, hasOutsideContent, hasRunningWork, keepReasoningExpanded: false };
}

console.log("\ntask 857: collapse-all intent gates new-segment defaults and fresh-run resets");

{
  // 锚 A：intent 缺省（不传）= 原语义。新 segment + standard + running → 展开。
  const states = [segment("seg-a", true)];
  const next = reconcileFoldEntries(EMPTY_FOLDS, states, "standard", false);
  ok(next?.get("seg-a")?.open === true, "A: default (no intent) still live-expands a fresh running fold");
  ok(next?.get("seg-a")?.userOverridden === false, "A: default new entry is not user-overridden");
}

{
  // 钉 B（回归本体①）：intent 激活，新 segment（新回合的 thinking 流式）按意图收起。
  const states = [segment("seg-b", true)];
  const next = reconcileFoldEntries(EMPTY_FOLDS, states, "standard", false, true);
  ok(next?.get("seg-b")?.open === false, "B: collapse intent keeps a fresh running fold closed");
  ok(next?.get("seg-b")?.userOverridden === true, "B: intent entry is user-overridden (survives later reconciles)");
  // 收起条目写入后，后续同 segment reconcile（仍 running）不得回弹：
  // 稳态 = reconcile 返回 null（无变化，open 维持 false）。
  const steady = reconcileFoldEntries(next ?? EMPTY_FOLDS, states, "standard", false, true);
  ok(steady === null || steady.get("seg-b")?.open === false,
    "B: the running reconcile tick cannot spring the intent collapse back");
  // 运行结束（running true→false）也保持收起——用户没让它展开。
  const settledStates = [segment("seg-b", false)];
  const settled = reconcileFoldEntries(next ?? EMPTY_FOLDS, settledStates, "standard", false, true);
  ok(settled?.get("seg-b")?.open === false, "B: completion keeps the intent-collapsed fold closed");
}

{
  // 钉 C（回归本体②）：intent 激活，fresh-run（entry.running false→true）不清除
  // 用户 override——覆盖 tab 切回/重挂载后运行中的已收起 segment。
  const prior = new Map([
    ["seg-c", { open: false, userOverridden: true, running: false, keepReasoningExpanded: false }],
  ]);
  const resumed = reconcileFoldEntries(prior, [segment("seg-c", true)], "standard", false, true);
  ok(resumed?.get("seg-c")?.open === false, "C: fresh run under intent keeps the user-collapsed fold closed");
  ok(resumed?.get("seg-c")?.userOverridden === true, "C: fresh run under intent keeps the user override");

  // 锚 C0：同一输入、无 intent → 原行为（清除 override、运行中展开）不变。
  const legacy = reconcileFoldEntries(prior, [segment("seg-c", true)], "standard", false);
  ok(legacy?.get("seg-c")?.open === true, "C0: without intent the fresh run still clears the toggle (legacy pin)");
  ok(legacy?.get("seg-c")?.userOverridden === false, "C0: without intent the fresh run clears the override (legacy pin)");
}

{
  // 钉 D：deep 档下用户显式收起同样压过档位默认——收起意图是用户意愿，
  // 优先于「deep 全展开」的档位规则。
  const next = reconcileFoldEntries(EMPTY_FOLDS, [segment("seg-d", true)], "deep", false, true);
  ok(next?.get("seg-d")?.open === false, "D: intent collapse holds under the deep tier too");
}

{
  // 钉 E：会话级意图表的存取与解除。
  clearTranscriptFoldOverrideSessionForTest("sess-e");
  ok(readTranscriptCollapseIntent("sess-e") === false, "E: intent starts off for an unknown session");
  setTranscriptCollapseIntent("sess-e", true);
  ok(readTranscriptCollapseIntent("sess-e") === true, "E: collapse-all sets the session intent");
  setTranscriptCollapseIntent("sess-e", false);
  ok(readTranscriptCollapseIntent("sess-e") === false, "E: expand/clear lifts the session intent");
}

{
  // 钉 F：意图解除后（expand-all / 用户点开块头），新块恢复默认规则。
  const next = reconcileFoldEntries(EMPTY_FOLDS, [segment("seg-f", true)], "standard", false, false);
  ok(next?.get("seg-f")?.open === true, "F: after the intent is lifted, fresh folds live-expand again");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
