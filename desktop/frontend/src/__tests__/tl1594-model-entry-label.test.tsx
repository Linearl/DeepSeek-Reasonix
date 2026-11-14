// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1594-model-entry-label.test.tsx
//
// tl1594 (P2)：模型切换下拉「条目显示端点名」显示缺陷钉。
//
// 缺陷：后端把连接显示名盖进每模型 ModelInfo.displayName（model_catalog.go
// ResolveModel 拷贝链），自定义多模型端点下每组每条都显示端点名（两个模型 →
// 「同组重复两条」同名），deepseek（未设 display_name）恰好正常。
//
// 契约钉死（显示层，模型 id 路由身份不动）：
//   ① 条目 label 阶梯 = 每模型 displayName > 裸 model id；连接名只出现在
//      分组头（providerLabel 阶梯，tl1547 A1）。
//   ② deepseek 形状（两档皆空）零回退：分组头走内置默认标签，条目 = model id。
//   ③ 触发器 ref 显示形式（tl1547 A3）不再出现「连接名/连接名」串线。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ModelSwitcher } from "../components/ModelSwitcher";
import { LocaleProvider } from "../lib/i18n";
import type { ModelInfo } from "../lib/types";

class TestResizeObserver {
  observe() {}
  disconnect() {}
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Node = dom.window.Node;
globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: true,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }),
});

let failures = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  const good = actual === expected;
  if (!good) failures += 1;
  process.stdout.write(`  ${good ? "PASS" : "FAIL"}  ${label}${good ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}\n`);
}
// 与修复后后端一致的目录形状：连接名只在 providerLabel，displayName 缺省
//（Go 侧不再盖章，见 desktop/model_catalog.go tl1594 注释）。
const catalog: ModelInfo[] = [
  { ref: "mimo-tp-1/mimo-v2.6-flash", provider: "mimo-tp-1", providerLabel: "MiMo TP", model: "mimo-v2.6-flash", current: true },
  { ref: "mimo-tp-1/mimo-v2.6-flash-mini", provider: "mimo-tp-1", providerLabel: "MiMo TP", model: "mimo-v2.6-flash-mini", current: false },
  { ref: "deepseek/deepseek-chat", provider: "deepseek", model: "deepseek-chat", current: false },
  // 前端合同保留的每模型显示档（modelRefLabel.ts）：真有值时条目优先显示它。
  { ref: "deepseek/deepseek-reasoner", provider: "deepseek", displayName: "DeepSeek R1", model: "deepseek-reasoner", current: false },
];
(window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
  main: {
    App: {
      ModelsForTab: async () => catalog,
    },
  },
};

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <ModelSwitcher label="mimo-tp-1/mimo-v2.6-flash" tabId="tab-tl1594" onPick={() => true} />
    </LocaleProvider>,
  );
});
await act(async () => {
  (document.querySelector(".modelsw__trigger") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});

// ① 每组条目：无每模型显示名 → 裸 model id；连接名不得串进条目。
const optionTexts = Array.from(document.querySelectorAll<HTMLElement>("[role='option'] .modelsw__model"), (el) => el.textContent?.trim());
eq(JSON.stringify(optionTexts), JSON.stringify(["mimo-v2.6-flash", "mimo-v2.6-flash-mini", "deepseek-chat", "DeepSeek R1"]),
  "entries show model ids (or a genuine per-model name), never the connection label");

// 同组两条不再同名（缺陷的「重复两条」观感）。
const mimoOptions = optionTexts.filter((text) => text === "MiMo TP");
eq(mimoOptions.length, 0, "the renamed endpoint's label no longer repeats across its model entries");

// 分组头保持连接名 / 内置默认标签（tl1547 A1 阶梯不动）。
const groupLabels = Array.from(document.querySelectorAll(".modelsw__group-label"), (el) => el.textContent?.trim());
eq(JSON.stringify(groupLabels), JSON.stringify(["MiMo TP", "DeepSeek"]), "group headers keep the connection label tier");

// ③ 触发器：ref 显示形式的 model 段回退裸 id，不再渲染「MiMo TP/MiMo TP」。
const triggerLabel = (document.querySelector(".modelsw__trigger") as HTMLButtonElement).getAttribute("aria-label") ?? "";
eq(triggerLabel.includes("MiMo TP/MiMo TP"), false, "trigger ref display no longer doubles the connection label");
eq(triggerLabel.includes("MiMo TP/mimo-v2.6-flash"), true, "trigger ref display resolves provider segment and falls back to the bare model id");

await act(async () => root.unmount());
if (failures > 0) {
  process.stdout.write(`\n${failures} failed\n`);
  process.exit(1);
}
console.log("tl1594 model entry label: PASS");
