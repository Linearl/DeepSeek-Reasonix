// tl1663 harness：设置页只读路径行「打开位置」按钮。
//
// 钉四点（可证伪）：
//  ① 数据位置页四行接线：探针（ResolveRevealablePathForTab）命中 ⇒ 按钮可点，
//     点击走 RevealPath（Windows explorer /select, 选中该文件）；
//  ② 路径缺失（探针空串）⇒ 按钮置灰 + 缺失提示 title，点击零桥调用——
//     decision-patterns.md 未生成时的明确态，绝不给死按钮；
//  ③ 源码契约：决策模式文件位置行、hooks 设置文件行同样接线同一共用钩子；
//  ④ 三语文案齐 + 引用到的 CSS 类均有定义（class-contract 第四维）。
//
// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1663-reveal-location.test.tsx

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

const testDir = dirname(fileURLToPath(import.meta.url));
const srcOf = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");

const labSection = srcOf("../components/ExperimentalLabSection.tsx");
const settingsPanel = srcOf("../components/SettingsPanel.tsx");
const storagePage = srcOf("../components/StorageSettingsPage.tsx");
const revealHook = srcOf("../lib/revealLocation.ts");
const styles = srcOf("../styles.css") + srcOf("../components/SettingsPanel.css");
const locales = ["zh.ts", "zh-TW.ts", "en.ts"].map((name) => srcOf(`../locales/${name}`));

let passed = 0;
let failed = 0;
function ok(condition: boolean, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntl1663 settings reveal-location rows contract (source)");

// ③ 源码契约：三行接同一共用钩子。
ok(revealHook.includes("ResolveRevealablePathForTab") && revealHook.includes("RevealPath"),
  "shared hook probes ResolveRevealablePathForTab and reveals through RevealPath");
ok(labSection.includes('useRevealLocation(s.decisionPatternFilePath ?? "")'),
  "decision-patterns file-location row runs the reveal probe on the resolved path");
ok(labSection.includes('disabled={busy || decisionReveal.target === ""}'),
  "decision-patterns reveal button greys out while the probe is empty (missing file)");
ok(labSection.includes('{t("settings.revealLocation")}'),
  "decision-patterns row labels the button 打开位置");
ok(settingsPanel.includes('useRevealLocation(view?.path ?? "")'),
  "hooks settings-file row runs the same reveal probe");
ok(settingsPanel.includes('disabled={busy || !view?.path || reveal.target === ""}'),
  "hooks reveal button greys out when the settings file is missing");
ok(storagePage.includes("useRevealLocation(value)") && storagePage.includes("<Folder size={16} />"),
  "storage path rows host the folder-icon reveal button");
ok(storagePage.includes('reveal.target === ""'),
  "storage reveal button greys out on an empty probe");

// ④ 三语文案齐。
ok(locales.every((locale) => locale.includes('"settings.revealLocation"')),
  "all three locales define settings.revealLocation");
ok(locales.every((locale) => locale.includes('"settings.revealLocationMissing"')),
  "all three locales define settings.revealLocationMissing");

// class-contract 第四维：引用到的既有类必须有定义（本任务零新增类名）。
ok(styles.includes(".settings-path-value") && styles.includes(".settings-icon-button")
  && styles.includes(".btn--secondary") && styles.includes(".btn--small"),
  "every reused button/path class already has a CSS definition (no new class names)");
ok(!/className="[^"]*(?:reveal-location|open-location)/.test(labSection + settingsPanel + storagePage),
  "no freshly invented class names ride the new buttons");

// ①② 渲染契约：数据位置页四行的按钮态与点击行为。
const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { pretendToBeVisual: true, url: "http://localhost/" });
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

const { StorageSettingsPage } = await import("../components/StorageSettingsPage");
const { LocaleProvider } = await import("../lib/i18n");

const probeCalls: string[] = [];
const revealCalls: string[] = [];

function stubBridge() {
  const base = {
    StorageSettings: async () => ({
      defaultWorkspace: "C:/work",
      statePath: "C:/state",
      cachePath: "C:/missing-cache",
      extensionsPath: "",
    }),
    ListRecoveryCopyGroups: async () => [],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => [],
    ResolveRevealablePathForTab: async (_tabID: string, path: string) => {
      probeCalls.push(path);
      // 权威存在性探针的替身：C:/missing-cache 未生成，其余命中。
      return path === "C:/missing-cache" ? "" : path;
    },
    RevealPath: async (path: string) => {
      revealCalls.push(path);
    },
  };
  window.go = {
    main: {
      App: base as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function renderPage(node: React.ReactNode): Promise<{ root: Root; container: HTMLElement }> {
  document.getElementById("root")?.remove();
  const container = document.createElement("div");
  container.id = "root";
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => { root.render(node); });
  // 探针是 microtask 链，两轮 act 让 then 回调落地、按钮态翻转。
  await act(async () => { await Promise.resolve(); });
  await act(async () => { await Promise.resolve(); });
  return { root, container };
}

console.log("\ntl1663 storage path rows reveal behaviour");

stubBridge();
const { root } = await renderPage(<LocaleProvider><StorageSettingsPage /></LocaleProvider>);

const revealButtons = Array.from(document.querySelectorAll<HTMLButtonElement>("button[aria-label^='Open location:']"));
ok(revealButtons.length === 4, `all four storage rows host a reveal button (got ${revealButtons.length})`);
ok(probeCalls.includes("C:/work") && probeCalls.includes("C:/missing-cache") && !probeCalls.includes(""),
  "probe ran for each non-empty path and skipped the empty one");

const [workspaceBtn, stateBtn, cacheBtn, extensionsBtn] = revealButtons;
ok(Boolean(workspaceBtn) && !workspaceBtn.disabled, "① existing path: reveal button is enabled");
ok(Boolean(stateBtn) && !stateBtn.disabled, "① second probe-hit row: reveal button is enabled too");
ok(Boolean(cacheBtn) && cacheBtn.disabled, "② missing path: reveal button is greyed out");
ok(Boolean(cacheBtn) && cacheBtn.title === "File does not exist; cannot locate it in the file manager",
  "② missing path: button carries the explicit missing-file hint");
ok(Boolean(extensionsBtn) && extensionsBtn.disabled, "② empty path: reveal button stays greyed out");

await act(async () => { workspaceBtn?.click(); });
ok(JSON.stringify(revealCalls) === JSON.stringify(["C:/work"]),
  "① click engages RevealPath with the probed absolute path (explorer /select,)");

const revealCallsBefore = revealCalls.length;
await act(async () => { cacheBtn?.click(); });
ok(revealCalls.length === revealCallsBefore, "② missing path: the greyed-out button never reaches RevealPath");

await act(async () => { root.unmount(); });

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
