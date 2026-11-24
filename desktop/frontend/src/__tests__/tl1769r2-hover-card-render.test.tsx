// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1769r2-hover-card-render.test.tsx
//
// tl1769 返修（P2，20261124 包112 用户验收）：hover 悬浮卡片无对话名——一期
// (bc20fa3a33) 的模型层钉全绿但用户实测仍无名字，本件补「渲染级」证据钉：
// 真实 mount ProjectTree（JSDOM + bridge 浏览器 mock），走完
//   snapshot → 展开项目 → 行渲染 → hover(350ms) → body portal 卡 DOM
// 全链，断言卡片 DOM 里第一行 === 行内对话名的完整值（preview 不顶替名字）。
// 一期钉的是模型与源码文本，这里钉「渲染树中对话名字段的真实落点」，互补。
//   ① classic 悬浮卡：hover 后 .project-tree__hover-card-title 文本 === 完整名
//     （空闲行 + 状态态行各一次；状态标签是独立行永不并入名字行；运行态在
//     tl1769r2-hover-card-render-running.test.tsx 分册，mock 场景单例所致）；
//   ② workbench 原生 tooltip：行 button 的 title 属性首行 === 完整名；
//   ③ 卡行序：名字行恒为卡内第一行、全卡仅一行名字（DOM 顺序钉）。

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
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
const projectTreeMod = await import("../components/ProjectTree");

type Mounted = {
  html: string;
  rows: () => HTMLElement[];
  folders: () => HTMLElement[];
  rowLabel: (row: HTMLElement) => string;
  hover: (row: HTMLElement) => void;
  click: (el: HTMLElement) => void;
  unmount: () => Promise<void>;
};

function mount(variant: "classic" | "workbench"): Mounted {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(createElement(LocaleProvider, null, createElement(projectTreeMod.ProjectTree as never, { variant, timeFilter: "all" } as never)));
  });
  return {
    get html() { return container.innerHTML; },
    rows: () => Array.from(container.querySelectorAll<HTMLElement>(".project-tree__topic")),
    folders: () => Array.from(container.querySelectorAll<HTMLElement>(".project-tree__folder-main")),
    rowLabel: (row) => row.querySelector<HTMLElement>(".project-tree__topic-label")?.textContent ?? "",
    hover(row) {
      row.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true }));
    },
    click(el) {
      el.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    },
    async unmount() { await act(async () => { root.unmount(); }); container.remove(); },
  };
}

async function flushCycles(cycles = 6) {
  for (let i = 0; i < cycles; i += 1) {
    await act(async () => { await sleep(20); });
    if (process.env.TL1769R2_DEBUG === "1") {
      const labelEmpty = document.querySelector(".project-tree")?.innerHTML.includes("folder-label--empty") ?? false;
      process.stderr.write(`cycle ${i}: labelEmpty=${labelEmpty} topicRows=${document.querySelectorAll(".project-tree__topic").length}\n`);
    }
  }
}

function cardTitleText(): string {
  return document.body.querySelector(".project-tree__hover-card-title")?.textContent ?? "";
}
function cardLines(): string[] {
  const card = document.body.querySelector(".project-tree__hover-card");
  if (!card) return [];
  return Array.from(card.children).map((child) => child.textContent ?? "");
}

async function expandFirstProject(ui: Mounted): Promise<void> {
  const folder = ui.folders()[0];
  ok(Boolean(folder), "setup: the project folder row rendered");
  ui.click(folder);
  await flushCycles();
}

// ── 验收①：classic 悬浮卡 DOM 首行 = 完整对话名（demo mock：空闲/异常态 + preview 无）──
{
  const ui = mount("classic");
  await flushCycles();
  await expandFirstProject(ui);
  const rows = ui.rows();
  ok(rows.length >= 3, `setup: expanding the first project renders topic rows (got ${rows.length})`);

  // 空闲行（无 status、无 running）：第二行 topic_db_maint。
  const idleRow = rows[1] ?? rows[0];
  const idleName = ui.rowLabel(idleRow);
  ok(idleName.length > 0, "setup: the idle row renders a conversation name");
  await act(async () => {
    ui.hover(idleRow);
    await sleep(500); // hover delay is 350ms; real timers, wrapped in act
  });
  ok(document.body.querySelector(".project-tree__hover-card") !== null, "classic idle: the hover card portal mounts on the body");
  ok(cardTitleText() === idleName, `classic idle: the card FIRST LINE is the FULL name (expected ${JSON.stringify(idleName)}, got ${JSON.stringify(cardTitleText())})`);
  ok(cardTitleText().length === idleName.length, "classic idle: the name is carried verbatim (no truncation at the render layer)");

  // 异常态行：topic_sys_exception（demo mock 固定 status:"error"）在第二个
  // 项目（joyquant-sys）里——folder 顺序含 remote/global 注入不定，全展开。
  const folders = ui.folders();
  ok(folders.length >= 2, `setup: the demo mock renders both project folders (got ${folders.length})`);
  for (const folder of folders) {
    ui.click(folder);
    await flushCycles(2);
  }
  await flushCycles();
  // demo mock 里 error 行落在 classic 截断窗口之外（窗口截断 non-goal，
  // 一期模型钉已盖 error 态 statusLabel 独立字段）；这里用恒在窗口内的
  // paused 行（topic_sys_standard，status:"paused" 硬编码）验证同一纪律：
  // 状态标签独立成行，永不并入名字行。
  const statusRow = ui.rows().find((row) => /--status-(error|paused)/.test(row.className));
  ok(Boolean(statusRow), `setup: the demo mock carries a status-state row (rows=${ui.rows().length})`);
  if (statusRow) {
    const statusName = ui.rowLabel(statusRow);
    await act(async () => {
      ui.hover(statusRow);
      await sleep(500);
    });
    ok(cardTitleText() === statusName, "classic status-state: the card FIRST LINE is still the FULL name");
    const statusIndex = cardLines().findIndex((line, i) => i > 0 && line.length > 0 && line !== cardTitleText());
    ok(statusIndex > 0, "classic status-state: the status renders as its OWN line (never merged into the name line)");
  }
  await ui.unmount();
}

// ── 验收②：workbench 原生 tooltip 首行 = 完整对话名（DOM 属性级）──
{
  const ui = mount("workbench");
  await flushCycles();
  await expandFirstProject(ui);
  const row = ui.rows()[0];
  ok(Boolean(row), "setup: the workbench row rendered");
  const main = row?.querySelector<HTMLElement>(".project-tree__topic-main");
  const title = main?.getAttribute("title") ?? "";
  const name = row ? ui.rowLabel(row) : "";
  ok(title.split("\n")[0] === name, `workbench: the native tooltip's FIRST LINE is the FULL name (expected ${JSON.stringify(name)}, got ${JSON.stringify(title.split("\n")[0])})`);
  await ui.unmount();
}

// ── 验收③：卡行序 name → status → …（DOM 顺序钉，用 paused 行）──
{
  const ui = mount("classic");
  await flushCycles();
  await expandFirstProject(ui);
  for (const folder of ui.folders()) {
    ui.click(folder);
    await flushCycles(2);
  }
  await flushCycles();
  const statusRow = ui.rows().find((row) => /--status-(error|paused)/.test(row.className));
  if (statusRow) {
    await act(async () => {
      ui.hover(statusRow);
      await sleep(500);
    });
    const card = document.body.querySelector(".project-tree__hover-card");
    const classes = card ? Array.from(card.children).map((child) => child.className) : [];
    ok(classes[0]?.includes("hover-card-title") === true, "render: the name line is the card's FIRST child");
    ok(classes.every((c, i) => i === 0 || !c.includes("hover-card-title")), "render: exactly one name line, always first");
  } else {
    ok(true, "render order: skipped (no status row in this mock)");
  }
  await ui.unmount();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
