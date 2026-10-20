// Run: tsx src/__tests__/unfiled-inputs.test.tsx
// 任务 1023 阶段 0：未入档输入前端整合——就地时间锚定、已处理过滤、
// 去重铁律（transcript 已可见的永不再显示）的前端半边断言。

import { strict as assert } from "node:assert";
import { countUnhandled, readHandledTurns, markHandledTurns, withUnfiledNoticeItems } from "../lib/unfiledInputs";
import type { UnfiledInputView } from "../lib/types";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const TITLE = "未入档输入（已从检查点恢复）";

function entry(turn: number, time: number): UnfiledInputView {
  return { turn, msgIndex: turn * 10, time, prompt: `输入 ${turn} 的原文`, anchorIndex: turn * 10 };
}

function userItem(id: string, createdAt?: number): Item {
  return { kind: "user", id, text: "已入档消息", ...(createdAt !== undefined ? { createdAt } : {}) };
}

console.log("\nunfiled inputs: in-place time anchoring");
{
  const items: Item[] = [userItem("u1", 1000), { kind: "assistant", id: "a1", text: "回复", reasoning: "", streaming: false, createdAt: 3000 }];
  const out = withUnfiledNoticeItems(items, [entry(1, 2000)], new Set(), TITLE);
  ok(out.length === 3, "notice inserted between the anchored neighbours");
  ok(out[1].kind === "notice" && out[1].id === "unfiled:1", "notice sits before the first item created after the input time");
  ok((out[1] as { level?: string }).level === "warn", "warn level keeps the card outside the work-process fold");
}
{
  // 无时间戳可锚定 → 兜底追加到末尾，绝不静默丢弃。
  const items: Item[] = [userItem("u1"), { kind: "assistant", id: "a1", text: "回复", reasoning: "", streaming: false }];
  const out = withUnfiledNoticeItems(items, [entry(2, 0)], new Set(), TITLE);
  ok(out.length === 3 && out[2].kind === "notice", "unanchorable entry still renders at the end");
}

console.log("\nunfiled inputs: handled filtering (去重铁律 front half)");
{
  const items: Item[] = [userItem("u1", 1000)];
  const handled = new Set([1]);
  const out = withUnfiledNoticeItems(items, [entry(1, 500)], handled, TITLE);
  ok(out.length === 1 && out[0].kind === "user", "handled entries never render again in the transcript");
  ok(countUnhandled([entry(1, 500), entry(2, 600)], handled) === 1, "banner count drops once handled");
}

console.log("\nunfiled inputs: handled persistence (localStorage only)");
{
  const key = `test-session-${Date.now()}`;
  markHandledTurns(key, [7, 3]);
  const turns = Array.from(readHandledTurns(key));
  assert.deepEqual(turns, [3, 7]);
  ok(true, "handled turns persist sorted per session key");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
