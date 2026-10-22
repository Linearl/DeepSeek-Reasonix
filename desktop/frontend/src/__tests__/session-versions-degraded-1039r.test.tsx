// 任务 1039r harness：v3 版本面板（SessionVersionsSection）展开链路的降级
// 姿态 + 存储页分区的局部失败边界（SectionBoundary）。
//
// 0715 包（含 1039 首轮修复）用户复测仍失败：列表正常渲染，点击展开某会话
// 后整页只剩「设置加载失败。重试」。实证（20261022）：
//  - 后端 32 个多 heads 会话直跑 ListRecoveryChains 全部成功（1.6-5.3s，零
//    错误零降级）——首轮后端降级没有漏；
//  - 复测窗口 desktop.log 无一次 refusing unsafe event-log replay；
//  - 全页失败面只可能来自 ManagementSurface 的 fail-closed SurfaceBoundary
//    （设置 surface 树内任何渲染期同步抛错都被它吞成死话，且不留崩溃报告）。
//
// 本 harness 钉四点（可证伪）：
//  ① 展开遇 ListRecoveryChains reject（重放被拒文案）⇒ 局部 Degraded 行 +
//     原因横幅；页面路径区与分区本体原样存活，整页失败面不出现；
//  ② 后端降级行（degraded=true、计数全零）⇒ 面板渲染「Degraded」标记（首轮
//     缺口：后端发了降级行、面板却与空会话行无从区分）、不标建议链；
//  ③ 分区内渲染期抛错 ⇒ 只在该分区位置出原因横幅+重试，相邻分区与页面存活
//     （整页错误边界降级为局部失败的防御断言）；
//  ④ 分区失败后点重试 ⇒ 重挂成功则横幅消失、内容恢复。

import { JSDOM } from "jsdom";
import { act } from "react";
import type { ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
globalThis.HTMLInputElement = dom.window.HTMLInputElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;

const { StorageSettingsPage, SectionBoundary } = await import("../components/StorageSettingsPage");
const { LocaleProvider } = await import("../lib/i18n");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else failed += 1;
}

function installDom(): HTMLElement {
  document.getElementById("root")?.remove();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

async function flush() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

async function renderPage(node: ReactNode): Promise<{ root: Root; rootEl: HTMLElement }> {
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider>{node}</LocaleProvider>);
  });
  await flush();
  return { root, rootEl };
}

async function unmount(root: Root) {
  await act(async () => { root.unmount(); });
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().includes(text));
}

function bodyText(): string {
  return document.body.textContent || "";
}

const GC_PATH = "/sessions/gc.jsonl";
const REAL_REPLAY_REFUSAL = "session history exceeds safe replay limits: encoded_bytes=49210175, limit=49210174";

const BASE_GROUPS = [
  { mainPath: GC_PATH, mainLabel: "GC开发专员", directory: "/sessions", headCount: 2, selected: "", heads: [] },
  { mainPath: "/sessions/hb.jsonl", mainLabel: "Heartbeat: 决策模式整合", directory: "/sessions", headCount: 3, selected: "", heads: [] },
];

/** stubBridge installs the storage-page face of the bindings; callers override
 *  the per-scenario methods on the returned object. */
function stubBridge(overrides: Partial<AppBindings>) {
  const base = {
    StorageSettings: async () => ({
      defaultWorkspace: "C:/work", statePath: "C:/state", cachePath: "C:/cache", extensionsPath: "C:/plugins",
    }),
    ListSessionVersionGroups: async () => BASE_GROUPS,
    ListRecoveryChains: async () => { throw new Error("unreachable"); },
    ListSessionRepairRecordsFor: async () => [],
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

console.log("\nsession versions degraded expand + section boundary (1039r)");

// ① 展开遇拒 ⇒ 局部 Degraded 行 + 原因横幅；整页失败面不出现。
{
  stubBridge({
    ListRecoveryChains: (mainPath: string) =>
      mainPath === GC_PATH
        ? Promise.reject(new Error(REAL_REPLAY_REFUSAL))
        : Promise.resolve({ mainPath, mainLabel: "", chains: [], longestPath: "", longestHead: "" }),
  });

  const { root } = await renderPage(<StorageSettingsPage />);
  ok(bodyText().includes("GC开发专员"), "① panel list renders before the expand");
  ok(Boolean(document.querySelector("input.mem-input")), "① storage path fields are on the page");

  const expand = buttonByText("GC开发专员");
  ok(Boolean(expand), "① expand button for the incident row exists");
  await act(async () => { expand!.click(); });
  await flush();

  const text = bodyText();
  ok(text.includes(REAL_REPLAY_REFUSAL), "① failure banner carries the backend reason (not a dead generic line)");
  ok(text.includes("Degraded"), "① a local Degraded row is rendered for the refused conversation");
  ok(Boolean(document.querySelector("input.mem-input")), "① storage path fields survive the failed expand");
  ok(!document.querySelector("[data-testid='settings-section-failure']"), "① the section boundary did not fire (rejection stays local to the panel)");
  await unmount(root);
}

// ② 后端降级行 ⇒ 「Degraded」标记可见、零计数、不标建议链。
{
  stubBridge({
    ListRecoveryChains: async (mainPath: string) => ({
      mainPath,
      mainLabel: "GC开发专员",
      chains: [{
        path: mainPath, headId: "", label: "GC开发专员", isMain: true, selected: false,
        covered: false, messageCount: 0, turns: 0, lastActivity: "", bytes: 49_210_175,
        preview: "", degraded: true,
      }],
      longestPath: "",
      longestHead: "",
    }),
  });

  const { root } = await renderPage(<StorageSettingsPage />);
  const expand = buttonByText("GC开发专员");
  await act(async () => { expand!.click(); });
  await flush();

  const text = bodyText();
  ok(text.includes("Degraded"), "② backend degraded row is labelled Degraded");
  ok(!text.includes("Suggested"), "② degraded row is never suggested as the longest chain");
  ok(!document.querySelector("[data-testid='settings-section-failure']"), "② a degraded row is not a failure");
  await unmount(root);
}

// ③ 分区内渲染抛错 ⇒ 分区位置出原因横幅，相邻分区与页面存活。
{
  stubBridge({});
  let explode = false;
  function Thrower() {
    if (explode) throw new Error("render boom: synthetic section crash");
    return <div data-testid="thrower-ok">section alive</div>;
  }

  const { root } = await renderPage(
    <>
      <SectionBoundary><Thrower /></SectionBoundary>
      <div data-testid="sibling">sibling alive</div>
    </>,
  );
  ok(bodyText().includes("sibling alive"), "③ sibling renders before the crash");
  ok(bodyText().includes("section alive"), "③ bounded child renders fine before the crash");

  await act(async () => { explode = true; });
  await flush();
  // Re-render the same tree so the boundary's child actually throws this pass.
  await act(async () => {
    root.render(<LocaleProvider>
      <SectionBoundary><Thrower /></SectionBoundary>
      <div data-testid="sibling">sibling alive</div>
    </LocaleProvider>);
  });
  await flush();

  const failure = document.querySelector("[data-testid='settings-section-failure']");
  ok(Boolean(failure), "③ the section failure banner appears where the section was");
  ok((failure?.textContent || "").includes("render boom: synthetic section crash"), "③ the banner names the crash reason");
  ok(bodyText().includes("sibling alive"), "③ the sibling outside the boundary survives");
  await unmount(root);
}

// ④ 分区失败后点重试 ⇒ 故障源撤销后重挂成功、横幅消失。
{
  stubBridge({});
  let explode = true;
  function Thrower() {
    if (explode) throw new Error("render boom: retry case");
    return <div data-testid="thrower-ok">section restored</div>;
  }

  const { root } = await renderPage(
    <SectionBoundary><Thrower /></SectionBoundary>,
  );
  ok(Boolean(document.querySelector("[data-testid='settings-section-failure']")), "④ failure banner is up while the child throws");

  await act(async () => { explode = false; });
  const retry = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-testid='settings-section-failure'] button"))
    .find((b) => b.textContent?.trim() === "Retry");
  ok(Boolean(retry), "④ the failure banner offers Retry");
  await act(async () => { retry!.click(); });
  await flush();

  ok(bodyText().includes("section restored"), "④ retry remounts the section fresh");
  ok(!document.querySelector("[data-testid='settings-section-failure']"), "④ the banner is gone after a successful retry");
  await unmount(root);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
