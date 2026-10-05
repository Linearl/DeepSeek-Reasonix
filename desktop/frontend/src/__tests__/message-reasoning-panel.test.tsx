// Run: tsx src/__tests__/message-reasoning-panel.test.tsx

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { AssistantMessage } from "../components/Message";
import { setReasoningSummaryEnabled } from "../lib/reasoningSummaryPreference";
import { applyReasoningDisplayMode } from "../lib/reasoningDisplayPreference";
import { applySessionExperience, hydrateSessionExperience } from "../lib/sessionExperience";

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

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nmessage reasoning panel");

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

hydrateSessionExperience("standard");

type ReasoningItem = React.ComponentProps<typeof AssistantMessage>["item"];

async function render(item: ReasoningItem, props: { defaultExpanded?: boolean } = {}) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <AssistantMessage key={item.id} item={item} defaultExpanded={props.defaultExpanded} />
      </LocaleProvider>,
    );
  });
  // 任务467：reasoning 面板经 lazy 导入挂载。裸 tsx/node 解析动态导入要跨
  // 多个宏任务，单轮 act 只清微任务——首渲染会停在 Suspense 兜底
  // （.reasoning--loading）上，造成首批断言随机的假红。这里循环 flush 直到
  // 兜底消失并连续稳定一轮（有上限，防御真实渲染错误导致的死循环）。
  for (let tick = 0; tick < 100; tick += 1) {
    if (!document.querySelector(".reasoning--loading")) break;
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  }
  if (!document.querySelector(".reasoning--loading")) return;
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  if (document.querySelector(".reasoning--loading")) {
    throw new Error("reasoning panel never left its Suspense fallback");
  }
  await settleMarkdownBody();
}

// 任务467：markdown 是第二层异步——Markdown/MarkdownRenderer/MarkdownHistory
// 全部 lazy，worker 不可用时还要经 onError 落到 legacyMode 主线程解析。已解析
// 的判据是 .md 体出现元素子节点（兜底态只有纯文本节点）。无 .md（折叠/流式
// 纯文本）时立即返回；有界轮询，超时静默放行交给具体断言报错。
async function settleMarkdownBody() {
  for (let tick = 0; tick < 100; tick += 1) {
    const body = document.querySelector(".reasoning__body .md");
    if (!body || body.firstElementChild) return;
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  }
}

async function click(el: Element | null | undefined) {
  await act(async () => {
    el?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  await settleMarkdownBody();
}

// Completed reasoning: collapsed to a one-line summary, Markdown stays
// unmounted until the user asks for it.
await render({
  kind: "assistant",
  id: "a1",
  text: "",
  reasoning: "initial plan\n\n**important trace**\n\n- line one\n- line two\n\n`inline code`",
  streaming: false,
  reasoningComplete: true,
  reasoningDurationMs: 2_600,
});

const header = document.querySelector<HTMLButtonElement>(".reasoning__head");
ok(Boolean(header), "completed reasoning renders a toggle header");
ok(header?.textContent?.includes("thinking") ?? false, "header keeps the reasoning label");
ok(header?.textContent?.includes("lasted 3s") ?? false, "header shows rounded reasoning duration");
ok(!document.querySelector(".reasoning__body"), "completed reasoning is collapsed by default");
ok(!document.querySelector(".reasoning .md"), "collapsed reasoning mounts no Markdown");

const summary = document.querySelector<HTMLButtonElement>(".reasoning-summary");
ok(summary?.tagName === "BUTTON", "collapsed reasoning shows a clickable summary");
ok(summary?.textContent === "initial plan", "completed summary is the first non-blank line");

await click(summary);
ok(document.querySelector(".reasoning__body")?.textContent?.includes("line two") ?? false, "clicking the summary expands the reasoning body");
ok(document.querySelector(".reasoning__body strong")?.textContent === "important trace", "reasoning renders Markdown emphasis");
ok(document.querySelectorAll(".reasoning__body li").length === 2, "reasoning renders Markdown lists");
ok(document.querySelector(".reasoning__body .md-code")?.textContent === "inline code", "reasoning renders Markdown inline code");
ok(!document.querySelector(".reasoning-summary"), "expanded reasoning hides the summary");

await click(document.querySelector(".reasoning__head"));
ok(!document.querySelector(".reasoning__body"), "clicking the header collapses the reasoning body again");
await click(document.querySelector(".reasoning__head"));
ok(document.querySelector(".reasoning__body")?.textContent?.includes("line two") ?? false, "clicking the header expands the reasoning body");

// 任务467：长句/长 token/长 URL 三形态 × 两条渲染路径的挂点断言。
// 断行本身是 CSS 行为（jsdom 不做排版），由 typography-overflow-contract
// 的声明契约保证；这里验证两条路径的 DOM 挂点真实存在、内容不被截断。
const wrapLongUrl = "https://example.invalid/" + "y".repeat(200);
const wrapLongToken = "x".repeat(240);
await render({
  kind: "assistant",
  id: "a-wrap",
  text: "",
  reasoning: `先给结论，再附依据链接：${wrapLongUrl}\n\n\`\`\`\nconst token = "${wrapLongToken}";\n\`\`\``,
  streaming: false,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".reasoning-summary")), "long-content reasoning still collapses to a summary");
await click(document.querySelector(".reasoning-summary"));
ok(Boolean(document.querySelector(".reasoning__body .md")), "completed long-content reasoning mounts the markdown path");
ok(document.querySelector(".reasoning__body .md")?.textContent?.includes(wrapLongUrl) ?? false, "long URL survives intact in the markdown body");
ok(Boolean(document.querySelector(".reasoning__body .code")), "fenced code inside reasoning mounts the code path the wrap rules target");
ok(document.querySelector(".reasoning__body .code")?.textContent?.includes(wrapLongToken) ?? false, "long token survives intact inside reasoning code");

// Standard shows the complete process while it is running.
const streamingLine = "a".repeat(220);
await render({
  kind: "assistant",
  id: "a2",
  text: "",
  reasoning: `first thought\n\n${streamingLine}LATEST_TOKEN`,
  streaming: true,
  reasoningComplete: false,
});
ok(Boolean(document.querySelector(".reasoning__body")), "standard experience expands reasoning while it streams");
ok(!document.querySelector(".reasoning-summary"), "running reasoning never substitutes a collapsed summary");
ok(document.querySelector(".reasoning__body")?.textContent?.endsWith("LATEST_TOKEN") ?? false, "streaming body retains the newest tail of a long line");
ok(document.querySelector(".reasoning__head")?.hasAttribute("data-running") ?? false, "header keeps the running state");
ok(Boolean(document.querySelector(".reasoning__body .reasoning__stream-text")), "streaming body renders the plain-text wrap path element");

await render({
  kind: "assistant",
  id: "a2",
  text: "",
  reasoning: `first thought\n\n${streamingLine}LATEST_TOKEN_NEXT`,
  streaming: true,
  reasoningComplete: false,
});
ok(
  document.querySelector(".reasoning__body")?.textContent?.endsWith("LATEST_TOKEN_NEXT") ?? false,
  "streaming body updates when more text reaches the same long line",
);

// defaultExpanded keeps the previous always-open behavior.
await render({
  kind: "assistant",
  id: "a3",
  text: "",
  reasoning: "initial plan\n\n**important trace**",
  streaming: false,
  reasoningComplete: true,
}, { defaultExpanded: true });
ok(document.querySelector(".reasoning__body strong")?.textContent === "important trace", "defaultExpanded renders the full Markdown directly");
ok(!document.querySelector(".reasoning-summary"), "defaultExpanded skips the summary");

// The removed summary preference remains a compatibility surface, but cannot
// override the canonical Standard experience.
await act(async () => {
  setReasoningSummaryEnabled(false);
});
await render({
  kind: "assistant",
  id: "a4",
  text: "",
  reasoning: "initial plan\n\n**important trace**",
  streaming: false,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".reasoning-summary")), "legacy summary toggle cannot hide the Standard completion summary");
ok(!document.querySelector(".reasoning__body"), "legacy summary toggle keeps completed Markdown lazy");
await click(document.querySelector(".reasoning__head"));
ok(document.querySelector(".reasoning__body strong")?.textContent === "important trace", "the heading still opens full Markdown after a legacy toggle");
await act(async () => {
  setReasoningSummaryEnabled(true);
});
await render({
  kind: "assistant",
  id: "a5",
  text: "",
  reasoning: "initial plan\n\n**important trace**",
  streaming: false,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".reasoning-summary")), "legacy summary enable leaves the canonical preview intact");

await act(async () => {
  hydrateSessionExperience("standard");
});
await render({
  kind: "assistant",
  id: "a-auto",
  text: "",
  reasoning: "live thought",
  streaming: true,
  reasoningComplete: false,
});
ok(Boolean(document.querySelector(".reasoning__body")), "standard mode opens reasoning while it streams");

await render({
  kind: "assistant",
  id: "a-auto",
  text: "answer",
  reasoning: "live thought",
  streaming: true,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".reasoning__body")), "standard mode keeps reasoning open after its first answer token while the turn streams");
ok(!document.querySelector(".reasoning-summary"), "active turn does not replace full reasoning with a summary");

await render({
  kind: "assistant",
  id: "a-auto",
  text: "answer",
  reasoning: "live thought",
  streaming: false,
  reasoningComplete: true,
});
ok(!document.querySelector(".reasoning__body"), "standard mode closes untouched reasoning after completion");
ok(Boolean(document.querySelector(".reasoning-summary")), "standard mode leaves a summary after completion");

await render({
  kind: "assistant",
  id: "a-auto-manual",
  text: "",
  reasoning: "manual thought",
  streaming: true,
  reasoningComplete: false,
});
await click(document.querySelector(".reasoning__head"));
await click(document.querySelector(".reasoning__head"));
await render({
  kind: "assistant",
  id: "a-auto-manual",
  text: "answer",
  reasoning: "manual thought",
  streaming: false,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".reasoning__body")), "manual reasoning expansion survives standard completion");

await act(async () => {
  applySessionExperience("deep");
});
await render({
  kind: "assistant",
  id: "a-expanded",
  text: "",
  reasoning: "kept thought",
  streaming: false,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".reasoning__body")), "deep mode keeps completed reasoning open");
ok(!document.querySelector(".reasoning-summary"), "deep mode never falls back to a summary");
await click(document.querySelector(".reasoning__head"));
ok(!document.querySelector(".reasoning__body"), "a manual collapse still wins inside deep mode");

await act(async () => {
  applyReasoningDisplayMode("hidden");
});
await render({
  kind: "assistant",
  id: "a-hidden",
  text: "answer",
  reasoning: "not shown",
  streaming: false,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".reasoning")), "legacy hidden mode maps to Standard instead of removing reasoning");
await render({
  kind: "assistant",
  id: "a-hidden-only",
  text: "",
  reasoning: "reasoning-only work",
  streaming: false,
  reasoningComplete: true,
});
ok(Boolean(document.querySelector(".msg")), "legacy hidden mode keeps reasoning-only work reachable");
await act(async () => {
  applySessionExperience("standard");
});

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
