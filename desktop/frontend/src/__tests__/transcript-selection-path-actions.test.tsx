// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/transcript-selection-path-actions.test.tsx
//
// 任务 1060：会话转录选中路径文字 → 浮动菜单增「用默认应用打开 / 打开所在目录」。
// 验收四条的前端读数：
// 1. 选存在的绝对/相对路径 → 两项出现，点击分别走 OpenLocalPath / RevealPath
//    （与右侧边栏/产物卡片同一组 binding），动作参数=探针返回的绝对路径；
// 2. 相对路径按会话项目根解析由后端探针承担（Go 侧表驱动测试）——此处验证
//    前端把 tabId 与原始候选如实交给探针、动作使用探针解析结果；Global
//    （探针返回空串）不出现两项；
// 3. 非路径文字、不存在的路径（探针空串/抛错）→ 两项不出现（零误报）；
// 4. 形态预筛生效：普通句子/URL 根本不发起探针调用。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { TranscriptSelectionMenu } from "../components/TranscriptSelectionMenu";
import { LocaleProvider } from "../lib/i18n";
import {
  transcriptSelectionStore,
  type TranscriptSelectableRow,
} from "../lib/transcriptSelectionStore";

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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function drainFrame(): Promise<void> {
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
  await flushTimers();
}

function installDom() {
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
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

function selectNodeText(node: Node) {
  const range = document.createRange();
  range.selectNodeContents(node);
  const selection = document.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function transcriptActionHost(): HTMLElement | null {
  return document.querySelector<HTMLElement>('.transcript-selection-action[data-surface="transcript"]');
}

function transcriptActionState(): string | undefined {
  return transcriptActionHost()?.dataset.state;
}

async function openSelectionToolbar(msgBody: HTMLElement) {
  selectNodeText(msgBody.firstChild as Node);
  await act(async () => {
    msgBody.dispatchEvent(new window.MouseEvent("pointerup", { bubbles: true, button: 0 }));
    // 探针是异步链（effect→resolve→setState）：多轮 flush 让它在 act 内落地。
    await drainFrame();
    await flushTimers();
    await flushTimers();
  });
}

function pathActionButtons(): HTMLButtonElement[] {
  return [...(transcriptActionHost()?.querySelectorAll<HTMLButtonElement>("[data-selection-path-action]") ?? [])];
}

type PathStub = {
  probes: Array<{ tabID: string; path: string }>;
  opened: string[];
  revealed: string[];
  stub: { main: { App: Record<string, (...args: unknown[]) => Promise<unknown>> } };
};

function installPathStub(resolvedFor: (tabID: string, path: string) => string): PathStub {
  const probes: Array<{ tabID: string; path: string }> = [];
  const opened: string[] = [];
  const revealed: string[] = [];
  const stub = {
    main: {
      App: {
        ResolveOpenablePathForTab: async (tabID: string, path: string) => {
          probes.push({ tabID, path });
          return resolvedFor(tabID, path);
        },
        OpenLocalPath: async (path: string) => { opened.push(path); },
        RevealPath: async (path: string) => { revealed.push(path); },
      },
    },
  };
  (window as unknown as { go: unknown }).go = stub;
  return { probes, opened, revealed, stub };
}

async function settleProbe(): Promise<void> {
  await act(async () => {
    await flushTimers();
    await flushTimers();
  });
}

console.log("\ntranscript selection path actions (task 1060)");

{
  // 场景 A：无 window.go（纯浏览器/dev mock 环境）——mock 探针返回空串，
  // 两项绝不出现；普通文字选中菜单保持原样（回归保护）。
  const dom = installDom();
  document.body.insertAdjacentHTML(
    "beforeend",
    '<div class="msg__body">D:\\out\\weekly.md</div>',
  );
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu onAddToChat={() => {}} />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  await openSelectionToolbar(msgBody);
  eq(transcriptActionState(), "open", "A: 选中打开浮动工具条");
  eq(pathActionButtons().length, 0, "A: 无 window.go（mock 探针空串）时两项不渲染");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

{
  // 场景 B：探针确认存在的绝对路径 → 两项出现；打开/定位各走对应 binding，
  // 参数=探针返回的绝对路径；动作后选区清空、工具条收起。
  const dom = installDom();
  const s = installPathStub((tabID, path) => {
    if (path === "D:\\out\\weekly.md") return "D:\\out\\weekly.md";
    if (tabID === "proj-tab" && path.startsWith("docs/")) return `D:\\proj\\${path.replace(/\//g, "\\")}`;
    return ""; // 其余（含 Global 会话的相对路径）一律拒绝
  });
  document.body.insertAdjacentHTML(
    "beforeend",
    '<div class="msg__body">D:\\out\\weekly.md</div>',
  );
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu onAddToChat={() => {}} tabId="tab-1" />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  await openSelectionToolbar(msgBody);
  const buttons = pathActionButtons();
  eq(buttons.length, 2, "B: 探针确认存在 → 两项出现");
  eq(buttons[0]?.textContent?.includes("Open with default application"), true, "B: 第一项=用默认应用打开（与产物卡片同文案）");
  eq(buttons[1]?.textContent?.includes("Reveal in file manager"), true, "B: 第二项=打开所在目录（与产物卡片同文案）");
  eq(s.probes.length, 1, "B: 探针恰好调用一次");
  eq(s.probes[0]?.tabID, "tab-1", "B: 探针收到显式 tabId");
  eq(s.probes[0]?.path, "D:\\out\\weekly.md", "B: 探针收到原始路径候选");

  await act(async () => {
    buttons[0]?.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true, button: 0 }));
    await flushTimers();
  });
  await act(async () => {
    buttons[0]?.click();
    await flushTimers();
  });
  eq(s.opened.length, 1 && s.opened[0] === "D:\\out\\weekly.md" ? 1 : 0, "B: 打开动作走 OpenLocalPath(探针返回的绝对路径)");
  eq(s.revealed.length, 0, "B: 打开动作不触碰 RevealPath");
  eq(transcriptActionState(), "closed", "B: 动作后工具条收起");
  eq(document.getSelection()?.isCollapsed, true, "B: 动作后选区清空");

  // 重新选中 → 定位动作。
  await openSelectionToolbar(msgBody);
  const revealBtn = pathActionButtons().find((b) => b.dataset.selectionPathAction === "reveal");
  await act(async () => {
    revealBtn?.click();
    await flushTimers();
  });
  eq(s.revealed.length === 1 && s.revealed[0] === "D:\\out\\weekly.md", true, "B: 定位动作走 RevealPath(同一绝对路径)");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

{
  // 场景 C：相对路径——探针按会话项目根解析并返回绝对路径，动作使用解析结果；
  // Global 会话（探针空串）不出现两项。
  const dom = installDom();
  const s = installPathStub((tabID, path) =>
    tabID === "proj-tab" && path.startsWith("docs/")
      ? `D:\\proj\\${path.replace(/\//g, "\\")}`
      : "",
  );
  document.body.insertAdjacentHTML(
    "beforeend",
    '<div class="msg__body">docs/task/notes.md</div>' +
      '<div class="msg__body" id="global-tab">docs/task/notes.md</div>',
  );
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  const globalBody = document.querySelector("#global-tab") as HTMLElement;
  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu onAddToChat={() => {}} tabId="proj-tab" />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  await openSelectionToolbar(msgBody);
  const buttons = pathActionButtons();
  eq(buttons.length, 2, "C: 项目会话相对路径 → 两项出现");
  await act(async () => {
    buttons[0]?.click();
    await flushTimers();
  });
  eq(s.opened[0] === "D:\\proj\\docs\\task\\notes.md", true, "C: 动作参数=探针按项目根解析后的绝对路径");

  // Global：探针拒绝相对路径返回空串 → 两项不出现。
  await act(async () => { root.unmount(); });
  const root2 = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root2.render(
      <LocaleProvider>
        <TranscriptSelectionMenu onAddToChat={() => {}} tabId="global-tab" />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  await openSelectionToolbar(globalBody);
  eq(pathActionButtons().length, 0, "C: Global 会话相对路径（探针空串）不出现两项");
  await act(async () => { root2.unmount(); });
  dom.window.close();
}

{
  // 场景 D：零误报——普通句子/URL 不发起探针；探针空串或抛错的路径形态不显示。
  const dom = installDom();
  const s = installPathStub(() => "");
  document.body.insertAdjacentHTML(
    "beforeend",
    '<div class="msg__body">this sentence has no separator</div>' +
      '<div class="msg__body" id="url-text">see https://example.com/docs</div>' +
      '<div class="msg__body" id="missing-path">D:\\out\\ghost.md</div>',
  );
  const plain = document.querySelector(".msg__body") as HTMLElement;
  const urlText = document.querySelector("#url-text") as HTMLElement;
  const missingPath = document.querySelector("#missing-path") as HTMLElement;
  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu onAddToChat={() => {}} tabId="tab-d" />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  await openSelectionToolbar(plain);
  eq(pathActionButtons().length, 0, "D: 普通句子不出现两项");
  eq(s.probes.length, 0, "D: 形态预筛拦下普通句子（探针零调用）");

  await openSelectionToolbar(urlText);
  eq(pathActionButtons().length, 0, "D: URL 不出现两项");
  eq(s.probes.length, 0, "D: 形态预筛拦下 URL（探针零调用）");

  await openSelectionToolbar(missingPath);
  eq(pathActionButtons().length, 0, "D: 不存在的路径（探针空串）不出现两项");
  eq(s.probes.length, 1, "D: 路径形态照常探针（判定权在后端）");

  // 探针抛错（意外 stat 失败）同样静默隐藏。
  (s.stub.main.App.ResolveOpenablePathForTab as (tabID: string, path: string) => Promise<string>) = async () => {
    throw new Error("stat exploded");
  };
  await openSelectionToolbar(missingPath);
  eq(pathActionButtons().length, 0, "D: 探针抛错不出现两项（不渲染死按钮）");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

{
  // 场景 E：逻辑选区（跨行快照）——探针解析快照文本后判定。
  const dom = installDom();
  const s = installPathStub((_tabID, path) => (path === "docs/plan.md" ? "D:\\proj\\docs\\plan.md" : ""));
  document.body.insertAdjacentHTML(
    "beforeend",
    '<div class="transcript__row" data-row-key="row-a"><div class="msg__body" data-transcript-selectable="message">see docs/plan.md</div></div>' +
      '<div class="transcript__row" data-row-key="row-b"><div class="msg__body" data-transcript-selectable="message">for details</div></div>',
  );
  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu resetKey="tab-logical" onAddToChat={() => {}} tabId="tab-logical" />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  const rows: TranscriptSelectableRow[] = [
    { rowKey: "row-a", sourceText: "see docs/plan.md", contentRevision: 1, resolveText: async () => "see docs/plan.md" },
    { rowKey: "row-b", sourceText: "for details", contentRevision: 1, resolveText: async () => "for details" },
  ];
  await act(async () => {
    transcriptSelectionStore.clear("test-reset");
    transcriptSelectionStore.beginNative("tab-logical");
    transcriptSelectionStore.promoteToLogical(
      "tab-logical",
      { rowKey: "row-a", textOffset: 4, affinity: "forward" },
      { rowKey: "row-a", textOffset: 16, affinity: "forward" },
      rows,
    );
    transcriptSelectionStore.settleLogical();
    await flushTimers();
  });
  eq(transcriptActionState(), "open", "E: 逻辑选区打开工具条");
  const buttons = pathActionButtons();
  eq(buttons.length, 2, "E: 逻辑选区中的路径 → 两项出现");
  eq(s.probes[0]?.path === "docs/plan.md", true, "E: 探针收到快照解析出的路径候选");
  await act(async () => {
    transcriptSelectionStore.clear("test-cleanup");
    root.unmount();
  });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
