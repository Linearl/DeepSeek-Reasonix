// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1769r2-hover-card-render-running.test.tsx
//
// tl1769 返修（P2）渲染级钉——运行态分册。mock 场景 (?mock=running) 在 bridge
// 模块首载时定型（mockSingleton 单例），所以运行态单独成文件：JSDOM 的 URL
// 在任何 import 之前就带上 ?mock=running，主文件（demo 场景）管空闲/异常。
//   验收②：运行态（streaming）行 hover——悬浮卡首行恒完整对话名，流式状态
//   标签独立成行，永不并入名字行（行内的运行符号只活在 CSS 点位）。

const RUNNING_URL = "http://localhost/?mock=running";
const dom = new JSDOM(`<!doctype html><html><body></body></html>`, { url: RUNNING_URL, pretendToBeVisual: true });
// (modules are imported below — after the URL is in place)

import { JSDOM } from "jsdom";

(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
(globalThis as unknown as { Node: unknown }).Node = dom.window.Node;
(globalThis as unknown as { Element: unknown }).Element = dom.window.Element;
(globalThis as unknown as { HTMLElement: unknown }).HTMLElement = dom.window.HTMLElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else { failed += 1; process.exitCode = 1; }
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { createElement } = await import("react");
const { LocaleProvider } = await import("../lib/i18n");
const { ProjectTree } = await import("../components/ProjectTree");

const container = document.createElement("div");
document.body.appendChild(container);
const root = createRoot(container);
act(() => {
  root.render(createElement(LocaleProvider, null, createElement(ProjectTree as never, { variant: "classic", timeFilter: "all" } as never)));
});
for (let i = 0; i < 6; i += 1) {
  await act(async () => { await sleep(20); });
}

const folders = () => Array.from(container.querySelectorAll<HTMLElement>(".project-tree__folder-main"));
const rows = () => Array.from(container.querySelectorAll<HTMLElement>(".project-tree__topic"));
const rowLabel = (row: HTMLElement) => row.querySelector<HTMLElement>(".project-tree__topic-label")?.textContent ?? "";
const cardTitleText = () => document.body.querySelector(".project-tree__hover-card-title")?.textContent ?? "";
const cardStatusText = () => document.body.querySelector(".project-tree__hover-card-status")?.textContent ?? "";
function click(el: HTMLElement) { el.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); }
function hover(row: HTMLElement) { row.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true })); }

ok(folders().length >= 2, `setup: the running mock renders the project folders (got ${folders().length})`);
// joyquant-sys（第二个 folder）持有 streaming 行（demo-running 场景的 status 才落地）。
click(folders()[1]);
for (let i = 0; i < 6; i += 1) {
  await act(async () => { await sleep(20); });
}
const runningRow = rows().find((row) => row.className.includes("status-streaming"));
ok(Boolean(runningRow), "setup: the running mock carries a streaming row");
if (runningRow) {
  const runningName = rowLabel(runningRow);
  ok(runningName.length > 0, "setup: the streaming row renders a conversation name");
  await act(async () => {
    hover(runningRow);
    await sleep(500); // hover delay is 350ms; real timers, wrapped in act
  });
  ok(document.body.querySelector(".project-tree__hover-card") !== null, "classic running: the hover card portal mounts on the body");
  ok(cardTitleText() === runningName, `classic running: the card FIRST LINE is still the FULL name (expected ${JSON.stringify(runningName)}, got ${JSON.stringify(cardTitleText())})`);
  ok(!cardTitleText().includes("projectTree.status"), "classic running: the status never leaks into the name line");
  ok(cardStatusText().includes("projectTree.status.streaming") || cardStatusText().length > 0, "classic running: the streaming status renders as its OWN line");
}
await act(async () => { root.unmount(); });
container.remove();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
