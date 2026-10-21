// Run: tsx src/__tests__/unfiled-inputs.test.tsx
// 任务 1023 阶段 0：未入档输入前端整合——就地时间锚定、已处理过滤、
// 去重铁律（transcript 已可见的永不再显示）的前端半边断言。

import { JSDOM } from "jsdom";
import { strict as assert } from "node:assert";
import { readFile } from "node:fs/promises";
import { countUnhandled, readHandledTurns, markHandledTurns, withUnfiledNoticeItems } from "../lib/unfiledInputs";
import type { UnfiledInputView } from "../lib/types";
import type { Item } from "../lib/useController";

// jsdom provides localStorage（已处理标记的持久化半边用它验证）
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { localStorage: Storage }).localStorage = dom.window.localStorage;

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

console.log("\nunfiled inputs: batch mark-all-handled (task 1045)");
{
  // 41 条逐条点是用户 2026-10-21 实证痛点；批量入口把全部未处理 turn 一次交
  // markHandledTurns（turns 数组形参，localStorage 一次写入）。
  const key = `batch-session-${Date.now()}`;
  const entries: UnfiledInputView[] = [1, 2, 3, 4, 5].map((turn) => entry(turn, 1000 + turn));
  markHandledTurns(key, entries.map((e) => e.turn));
  const handled = readHandledTurns(key);
  ok(countUnhandled(entries, handled) === 0, "batch mark-all-handled clears every pending entry");
  ok(handled.size === 5, "batch write covers all five turns in one localStorage write");
  // 与单条 markHandledTurns 合流无冲突：先批量再单条补写同一 turn 不重复、不报错。
  markHandledTurns(key, [3]);
  ok(readHandledTurns(key).size === 5, "re-marking a batched turn is a no-op (idempotent merge)");
}

console.log("\nunfiled inputs: fold contract source of truth (task 1045)");
{
  // 致命面修复的核心契约：组件文件与 CSS 折叠阈值必须同源（都在 FOLDED_LINES=6
  // 行）。这里读两份源文件做交叉核对——改任一侧忘改另一侧即在此处炸红。
  const banner = await readFile(new URL("../components/UnfiledInputsBanner.tsx", import.meta.url), "utf8");
  const styles = await readFile(new URL("../styles.css", import.meta.url), "utf8");
  ok(banner.includes('const [expanded, setExpanded] = useState(false)') && banner.includes("unfiled-card__body--open\" : \"\""), "body starts collapsed; --open class only after an explicit expand click");
  ok(styles.includes(".unfiled-card__body") && styles.includes("max-height: 9.6em"), "CSS fold cap (max-height 9.6em ≈ 6 lines) present for the collapsed body");
  ok(styles.includes(".unfiled-card__body--open") && styles.includes("max-height: 64em"), "expanded body keeps a hard cap (64em) so a 50KB prompt can never squat the transcript");
  ok(styles.includes(".unfiled-card__head") && styles.includes("position: sticky"), "head row (title + actions) is sticky so buttons never sink below the fold");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
