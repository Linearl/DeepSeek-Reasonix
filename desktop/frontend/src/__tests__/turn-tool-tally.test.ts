// Run: tsx src/__tests__/turn-tool-tally.test.ts
//
// 任务 1082 验收：本轮工具/文件/检索汇总的计数与文案。
//  1) 12 次工具调用、3 个不同文件、1 次检索 →「12 工具 · 3 文件 · 1 检索」
//     （zh 三段拼接；en 由 locale 键表覆盖）；
//  2) 同一文件被读 5 次 → 文件数计 1（去重断言）；
//  3) 零工具轮（纯对话）→ 文案为 null（不制造噪音）；
//  4) 折叠头共用 countTools：displayItems 口径与改动前的 reduce 逐项一致。

import { t as translate } from "../lib/i18n";
import { zh } from "../locales/zh";
import { countTools, turnTally, turnTallyLabel } from "../lib/turnToolTally";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// 组件测试用的中文翻译器（组件渲染走 LocaleProvider；这里直接按 zh 表拼，
// 避免为纯函数用例拉起 DOM）。
const t: typeof translate = (key, vars) => {
  const template = zh[key as keyof typeof zh] ?? key;
  return template.replace(/\{(\w+)\}/g, (_, name) => (vars?.[name] !== undefined ? String(vars[name]) : `{${name}}`));
};

let toolSeq = 0;
function tool(name: string, args: Record<string, unknown> = {}): Item {
  toolSeq += 1;
  return {
    kind: "tool", id: `t${toolSeq}`, name, args: JSON.stringify(args),
    readOnly: false, status: "done",
  };
}

// ── 1) 12 工具 · 3 文件 · 1 检索 ──────────────────────────────────────────────
{
  const items: Item[] = [
    { kind: "user", id: "u1", text: "改三个文件" },
    tool("read_file", { path: "a.ts" }),
    tool("read_file", { path: "b.ts" }),
    tool("read_file", { path: "c.ts" }),
    tool("write_file", { path: "a.ts", content: "x" }),
    tool("edit_file", { path: "a.ts", old_string: "1", new_string: "2" }),
    tool("edit_file", { path: "b.ts", old_string: "1", new_string: "2" }),
    // grep 的 path 是目录参数（检索范围），不是被改文件——路径聚合一律
    // 排除检索类工具，否则「src」会把文件数虚增成 4。
    tool("grep", { pattern: "TODO", path: "src" }),
    tool("bash", { command: "npm test" }),
    tool("bash", { command: "npm run lint" }),
    tool("task", { description: "子任务" }),
    tool("todo_write", { todos: [] }),
    tool("read_file", { path: "c.ts" }),
  ];
  eq(turnTally(items).tools, 12, "12 次工具调用计 12 工具");
  eq(turnTally(items).files, 3, "12 次调用涉及 3 个不同文件计 3 文件");
  eq(turnTally(items).searches, 1, "1 次检索（grep）计 1 检索");
  eq(
    turnTallyLabel(items, t),
    "12 工具 · 3 文件 · 1 检索",
    "汇总文案为「12 工具 · 3 文件 · 1 检索」",
  );
}

// ── 2) 同一文件被读 5 次 → 文件数 1 ──────────────────────────────────────────
{
  const items: Item[] = [
    tool("read_file", { path: "same.ts" }),
    tool("read_file", { path: "same.ts" }),
    tool("read_file", { path: "same.ts" }),
    tool("read_file", { path: "same.ts" }),
    tool("read_file", { path: "same.ts" }),
  ];
  eq(turnTally(items).files, 1, "同一文件被读 5 次文件数计 1");
  eq(turnTally(items).tools, 5, "5 次读调用计 5 工具");
}

// ── 3) 零工具轮 → 不显示 ─────────────────────────────────────────────────────
{
  const items: Item[] = [
    { kind: "user", id: "u2", text: "纯对话" },
    { kind: "assistant", id: "a2", text: "好的", reasoning: "", streaming: false },
  ];
  eq(turnTallyLabel(items, t), null, "零工具轮汇总不显示（null）");
  eq(turnTally(items).tools, 0, "零工具轮工具数为 0");
}

// ── 4) countTools 与改动前 reduce 口径逐项一致（折叠头回归）──────────────────
{
  const items: Item[] = [
    tool("read_file", { path: "a.ts" }),
    { kind: "assistant", id: "a3", text: "", reasoning: "想一下", streaming: false },
    tool("grep", { pattern: "x" }),
    { kind: "phase", id: "p1", text: "阶段" },
  ];
  const before = items.reduce((n, it) => n + (it.kind === "tool" ? 1 : 0), 0);
  eq(countTools(items), before, "countTools 与改动前的 reduce 口径一致");
  eq(countTools([]), 0, "空 displayItems 的 countTools 为 0");
}

// ── 5) 边界：args 缺失/非法 JSON 不炸、不计文件 ─────────────────────────────
{
  const malformed = tool("edit_file");
  malformed.args = "{oops";
  const items: Item[] = [malformed, tool("move_file", { source_path: "old.ts", destination_path: "new.ts" })];
  const tally = turnTally(items);
  eq(tally.tools, 2, "args 非法的工具调用仍计入工具数（调用真实发生）");
  eq(tally.files, 2, "move_file 的两端路径各计 1（old.ts/new.ts）");
}

if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  process.exit(1);
}
process.stdout.write(`\n  PASS  turn tool tally suite (${passed} assertions)\n`);
