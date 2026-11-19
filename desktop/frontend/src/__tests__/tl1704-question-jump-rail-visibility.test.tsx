// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1704-question-jump-rail-visibility.test.tsx
//
// tl1704：右侧问题跳转条（.jump-bar）最下方的刻度/底部锚被输入框区域吞掉。
//
// 根因（非 z-index 冲突）：rail 挂在 .transcript-shell 内（absolute、top:50% 居中），
// composer 在 shell 之外的 <footer> 里，shell 底边 = composer 顶边；而 .main
// overflow:hidden 会裁掉溢出 shell 的部分。tl1699 之后 rail 高度随刻度数增长，
// cap 是 viewport 单位（70vh）——shell 比 rail 矮时（composer 展开/矮窗口），
// 居中 rail 的溢出段（含 top:100% 的 tl1698 底部锚）被裁，观感即「被输入框遮挡」。
// 同族先例：tl1244（question-search-panel 的 70vh cap 同款事故与修法）。
//
// zcode 对照（ConversationTurnNavigator）：rail 物理高度按容器扣边距
// （max-h-[calc(100%-6rem)]），从不越过 composer 区——层级不是靠 z-index 压。
// 本钉锁同语义：rail 高度预算只允许解析到包含块（.transcript-shell），viewport
// 单位 cap 判红；pointer-events 两层结构不动（外壳不挡 composer 点击）。
//
// 验收矩阵：
//   A  rail 全段可见（含最下刻度+底部锚）：高度预算 shell 相对，无 viewport 单位。
//   B  composer 可点：rail 保持 shell 内 absolute；外壳 pointer-events:none、
//      .jump-scroll 才 pointer-events:auto 的两层结构不变。
//   C  composer 展开态：max-height(100%) 随 shell 收缩，底部锚不被裁。
//   D  tl1698 不回归：底部锚渲染与 band 判定既有钉不回归（本钉只加不减）。
//   E  tl1699 不回归：rail 增长公式保留（只换 cap 单位），侧栏 reflow 钉独立。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { QuestionJumpBar } from "../components/QuestionJumpBar";
import { LocaleProvider } from "../lib/i18n";
import type { QuestionAnchor } from "../lib/transcriptGrouping";

const testDir = dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");
// Strip comments so declaration parsing never matches prose inside them.
const styles = read("../styles.css").replace(/\/\*[\s\S]*?\*\//g, "");
const component = read("../components/QuestionJumpBar.tsx");

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

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

console.log("\ntl1704 question jump rail visibility");

// AC-A1 — 组件增长公式的 cap 必须解析到包含块（shell），viewport 单位判红。
// 70vh 不知道 topbar/footer 的真实高度：composer 一展开 shell 就比 rail 矮。
// （剥掉注释再扫，注释里的「70vh」字样不算回归。）
const componentCode = component.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/[^\n]*/g, "");
eq(/\d+v[hw]\b/.test(componentCode), false, "component growth cap no longer uses a viewport unit (70vh-class regression goes red)");
eq(componentCode.includes("calc(100% - 16px)"), true, "component growth cap resolves against the shell (calc(100% - 16px), tl1244 inset)");

// AC-A2 — 样式兜底：默认 240px 高度态也必须受 shell 相对 max-height 约束
//（刻度 ≤20 时组件不写 inline height，矮 shell 下 240px 也会溢出）。
eq(finalDeclaration(".jump-bar", "max-height"), "calc(100% - 16px)", ".jump-bar carries a shell-relative max-height (default 240px state bounded too)");
eq(
  matchingBlocks(".jump-bar").flatMap((block) => [
    ...(block.match(/(?:^|;)\s*height\s*:\s*[^;]*v[hw]\b/g) ?? []),
    ...(block.match(/(?:^|;)\s*max-height\s*:\s*[^;]*v[hw]\b/g) ?? []),
  ]).join(", "),
  "",
  "no rule on .jump-bar may carry a viewport-unit height/max-height (70vh-class regression goes red)",
);

// AC-B — composer 可点：rail 仍是 shell 内 absolute（包含块前提不漂移），
// 外壳 pointer-events:none / 内层 .jump-scroll auto 的两层结构不动。
const hostBlock = matchingBlocks(".transcript-shell > .jump-bar").join(";");
eq(/(?:^|;)\s*position\s*:\s*absolute/.test(hostBlock), true, "rail stays absolutely anchored inside .transcript-shell (host shell unchanged)");
eq(finalDeclaration(".jump-bar", "pointer-events"), "none", "rail shell ignores pointer events (composer clicks never blocked by the bar)");
eq(finalDeclaration(".jump-scroll", "pointer-events"), "auto", "inner .jump-scroll stays interactive (rail itself still clickable)");
// 组件宿主断言：挂载点仍是 .transcript-shell（tl1698 既有钉的镜像前提）。
const transcript = read("../components/Transcript.tsx");
eq(/className=\{`transcript-shell/.test(transcript), true, "rail host .transcript-shell (position:relative, viewport-bounded) stays the mounting shell");

// AC-C — 渲染级：增长态（>20 刻度）inline height 用 shell 相对 cap；默认态
// 不写 inline height（交给 CSS max-height 兜底）。jsdom 渲染锁接线形态。
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.localStorage = dom.window.localStorage;

const questions: QuestionAnchor[] = Array.from({ length: 40 }, (_, turn) => ({
  id: `q${turn}`,
  text: `question ${turn}`,
  turn,
  loaded: true,
}));
const root = createRoot(document.getElementById("root")!);
const flushTimers = () => new Promise((resolve) => setTimeout(resolve, 0));

await act(async () => {
  root.render(
    <LocaleProvider>
      <QuestionJumpBar loadedQuestions={questions} totalQuestions={40} activeTurn={39} onJump={() => {}} onJumpBottom={() => {}} />
    </LocaleProvider>,
  );
  await flushTimers();
});

const bar = document.querySelector(".jump-bar") as HTMLElement;
const inlineHeight = bar?.style.height ?? "";
// jsdom 会把 min(504px, calc(100% - 16px)) 规范成 min(504px, 100% - 16px)
// （丢 calc 字样但保留 shell 相对预算），断言按规范后的形式锁语义。
eq(inlineHeight.includes("vh"), false, "grown rail inline height carries no viewport unit");
eq(/100%\s*-\s*16px/.test(inlineHeight), true, "grown rail inline height caps at the shell (100% - 16px)");
eq(document.querySelectorAll(".jump-item--bottom").length, 1, "tl1698 bottom anchor still renders with the bounded rail");
// 底部锚复用 .jump-item class（继承定位/负 hover 语义），40 个采样刻度 + 1 锚 = 41。
eq(document.querySelectorAll(".jump-item").length, 41, "bottom anchor reuses the .jump-item family class (40 sampled markers + 1 anchor)");
eq(document.querySelectorAll(".jump-item:not(.jump-item--bottom)").length, 40, "tl1699 growth formula keeps one marker per sampled turn (40 = sampled set size)");

await act(async () => {
  root.render(
    <LocaleProvider>
      <QuestionJumpBar loadedQuestions={questions.slice(0, 3)} totalQuestions={3} activeTurn={2} onJump={() => {}} />
    </LocaleProvider>,
  );
  await flushTimers();
});
const smallBar = document.querySelector(".jump-bar") as HTMLElement;
eq(smallBar?.style.height ?? "", "", "short rail keeps the CSS height:240px default (inline height untouched)");
eq(finalDeclaration(".jump-bar", "max-height"), "calc(100% - 16px)", "default-height rail is bounded by the shell max-height");
// tl1698 镜像：不传 onJumpBottom 就不渲染底部锚（渲染接线条件 onJumpBottom && total > 0）。
eq(document.querySelectorAll(".jump-item--bottom").length, 0, "no bottom anchor renders when onJumpBottom is not wired");

await act(async () => root.unmount());
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
