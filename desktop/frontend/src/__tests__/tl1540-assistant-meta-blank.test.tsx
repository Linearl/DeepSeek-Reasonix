// Run: node node_modules/tsx/dist/cli.mjs --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1540-assistant-meta-blank.test.tsx
//
// tl1540 — 对话界面段间空块返修（用户截图 20261111：红框空块高≈折叠行 2 倍）。
//
// 机制（诊断结论，file:line 级）：
//   AssistantMessage（Message.tsx）无条件渲染 <div className="msg-meta">，
//   其唯一子项 CardTime 在 createdAt 缺失时返回 null——空壳 div 落地。
//   .msg-meta 带 min-height:24px + margin-top:4px，.msg--assistant 是
//   gap:8px 的纵向 flex：三者相乘 = 正文下方 32px 纯空白。assistant item
//   的 createdAt 是可选字段（useController.ts:336），历史恢复会话普遍缺失
//   → 每个段间红块 = 32px 空条带 + .msg 外距（级联终值 16px，基础层 18px
//   被主题层 16px 覆盖）+ .turn-collapse 4px 外距 = 52px；黄框基线空档
//   （折叠头→正文）= 4+16 = 20px。52/20 = 2.6×，
//   与截图实测（2x DPI 下红块内空 ≈35px 外沿 ≈39.5px、黄块 ≈19.5px、
//   红黄比 ≈2.0-2.3×）吻合。
//
// 修复：createdAt 缺失时整行不渲染（zcode 同款空壳零 DOM 纪律——
//   zcode ConversationRowView.tsx「只在 ToolCallBlock 内返回 null 会留下空
//   RowShell 和多余间距；在行分发处裁剪」；MessageToolbar 亦为
//   complete && !hideActions 条件渲染）。有戳形态 DOM 逐字节不变。
//
// zcode 基准值表（消息流密度对照，packages/ui/src）：
//   Message/MessageContent          gap-2 = 8px        （reasonix .msg--assistant gap 8px 同值）
//   MessageResponse 正文           leading-[1.75]     （reasonix 默认 1.68 / theme-style 1.85，读感档独立）
//   正文首尾段                      mt-0 / mb-0        （reasonix .md > :first/:last-child 0 同值）
//   turn 顶层（状态头↔折叠体↔回答）  gap-5 = 20px       （reasonix .msg 外距 18px ≤ 20，不超基准）
//   工作项组                        gap-4 = 16px
//   折叠头行                        inline-flex 无 min-height，pb-2 = 8px（reasonix .reasoning__head min-height 24px）
//   消息工具行                      mt-4 = 16px，且 complete && !hideActions 才渲染（零空壳）
//   空行壳                          行分发处裁剪，不产 DOM（本件对齐点）
//
// 验收矩阵（可证伪）：
//   A  无 createdAt：.msg-meta 零 DOM（红框空条带消除）；有 createdAt：
//      .msg-meta 照常渲染且时间文本在（有戳形态零回退）。
//   B  CSS 几何预算：空块预算 = .msg 外距 + .turn-collapse 外距 ≤
//      1.2 × .reasoning__head min-height（黄框折叠行）；预算内任一值被
//      回调大即红。.msg-meta 的 min-height/margin-top 声明保持在位
//      （约束的是组件不再发空壳，而非删样式）。密度：.msg 外距 ≤
//      zcode 顶层 gap-5 = 20px。

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { AssistantMessage } from "../components/Message";

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

// ── DOM 全局 ─────────────────────────────────────────────────────────
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: { ...dom.window.navigator, language: "en-US" } });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

type ReasoningItem = React.ComponentProps<typeof AssistantMessage>["item"];

async function render(item: ReasoningItem) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <AssistantMessage key={item.id} item={item} />
      </LocaleProvider>,
    );
  });
  // Markdown 是 lazy 件——让挂起的解析在 act 内落地，避免 act 外完成的告警
  // 噪声（本钉只查 msg-meta 有无，不依赖 .md 内容就位）。
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

// ── A 组件契约：空壳不产 DOM ─────────────────────────────────────────
process.stdout.write("\ntl1540 assistant meta blank — 段间空块清零钉\n");

await render({ kind: "assistant", id: "tl1540-a1", text: "装机验证通过：零真错误。", reasoning: "", streaming: false });
ok(document.querySelector(".msg--assistant .msg-meta") === null, "A1 无 createdAt：.msg-meta 零 DOM（32px 空条带消除）");
ok(document.querySelector(".msg--assistant .msg__body") !== null, "A2 无 createdAt：正文 .msg__body 照常渲染");

await render({ kind: "assistant", id: "tl1540-a2", text: "15/15 全部在包88 构建链。", reasoning: "", streaming: false, createdAt: 1762000000000 });
const meta = document.querySelector(".msg--assistant .msg-meta");
ok(meta !== null, "A3 有 createdAt：.msg-meta 照常渲染（有戳形态零回退）");
ok((meta?.querySelector(".msg-meta__time")?.textContent ?? "").length > 0, "A4 有 createdAt：时间文本非空");

await render({ kind: "assistant", id: "tl1540-a3", text: "占位", reasoning: "", streaming: false, createdAt: undefined });

// ── B CSS 几何预算：空块 ≤ 1.2× 折叠行 ───────────────────────────────
// jsdom 无布局（同 composer-narrow-overflow-css 前例）：行为断言的等价物 =
// styles.css 契约钉，任何一条被回删/放大越界即红。
const testDir = dirname(fileURLToPath(import.meta.url));
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

function matchingBlocks(selector: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(styles)) !== null) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks;
}

function finalDeclaration(selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const block of matchingBlocks(selector)) {
    const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
    let match: RegExpExecArray | null;
    while ((match = declaration.exec(block)) !== null) {
      value = match[1].trim();
    }
  }
  return value;
}

function firstPx(value: string | undefined): number {
  return Number.parseFloat((value ?? "").match(/(-?[\d.]+)px/)?.[1] ?? "NaN");
}

const metaMinHeight = finalDeclaration(".msg-meta", "min-height");
const metaMarginTop = finalDeclaration(".msg-meta", "margin-top");
// .msg 外距取级联终值：基础层 18px（styles.css 5347）被主题层 16px 覆盖
// （33561 / :root[data-theme-style] 34433）——用户截图即 theme-style 档。
const msgMargin = finalDeclaration(".msg", "margin");
const collapseMargin = finalDeclaration(".turn-collapse", "margin");
const headMinHeight = finalDeclaration(".reasoning__head", "min-height");
const assistantGap = finalDeclaration(".msg--assistant", "gap");

ok(metaMinHeight === "24px", `B1 .msg-meta min-height 24px 在位（有戳形态，实际=${metaMinHeight}）`);
ok(metaMarginTop === "4px", `B2 .msg-meta margin-top 4px 在位（实际=${metaMarginTop}）`);
ok(firstPx(msgMargin) === 16, `B3 .msg 外距级联终值 16px（zcode 顶层 gap-5=20 对齐档，实际=${msgMargin}）`);
ok(firstPx(collapseMargin) === 4, `B4 .turn-collapse 外距 4px（实际=${collapseMargin}）`);
ok(headMinHeight === "24px", `B5 .reasoning__head min-height 24px（黄框折叠行基准，实际=${headMinHeight}）`);

// 空块预算：assistant 段间 = .msg 底外距 + .turn-collapse 顶外距（flow-root
// 行盒包含子外距，行间不塌陷）；对照 = 折叠行 min-height × 1.2。
const blankBudget = firstPx(msgMargin) + firstPx(collapseMargin);
const blankCeiling = firstPx(headMinHeight) * 1.2;
ok(Number.isFinite(blankBudget) && blankBudget <= blankCeiling,
  `B6 空块预算 ${blankBudget}px ≤ 1.2×折叠行 ${blankCeiling}px（红块≈黄行验收①）`);
ok(Number.isFinite(firstPx(msgMargin)) && firstPx(msgMargin) <= 20,
  "B7 密度：.msg 外距 ≤ zcode turn 顶层 gap-5 = 20px（验收②基准不越界）");
ok(assistantGap === "8px", `B8 .msg--assistant gap 8px 保持（zcode Message gap-2 同值；空壳消除后无可乘条带，实际=${assistantGap}）`);

await act(async () => {
  root.unmount();
});
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
