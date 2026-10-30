// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tool-recovery-panel-readonly-grading.test.tsx
//
// 任务 tl1278（中断恢复「结果未知」标注按只读/写分级）前端契约。
//
// 现象：heartbeat_task_list 这类只读工具中断后，卡片无条件渲染
// 「结果未知（已记录）」，用户困惑「只读工具为什么报未知」。
// 修法（标注/展示层最小改）：记录上的 read_only（后端 beginToolRecovery 从
// 解析后的工具盖章，与 internal/tool 登记表同源）驱动文案分级——
//   验收① 只读记录 → 「只读调用 · 中断（无副作用，可安全忽略）」，整块不再
//          出现「结果未知」字样；
//   验收② 写类记录 → 保持原「结果未知（已记录）」警示与忽略按钮，零改动。
// 忽略按钮族为纯前端状态（1061/1068/1089 契约），本件不动。

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import { act } from "react";
import { createRoot } from "react-dom/client";

// ToolRecoveryPanel imports its own CSS; redirect like the sibling
// tool-recovery-panel-dismiss test does (see its header for the why).
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css") || specifier.endsWith(".svg")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

import type { ToolRecoveryBindings, ToolRecoverySnapshot } from "../lib/toolRecovery";

const { LocaleProvider } = await import("../lib/i18n");
const { ToolRecoveryPanel } = await import("../components/ToolRecoveryPanel");
const store = await import("../lib/toolRecoveryDismissalStore");
const { __resetToolRecoveryDismissalsForTests } = store;
// JSDOM 的 navigator.language 是 en-US，组件测试渲染的是 en 词典（同步、确定性）；
// zh 文案逐字由词典断言钉住（键集三语齐平由 Record<DictKey,string> 编译合同兜底）。
const { zh } = await import("../locales/zh");

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
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

const dom = installDom();
const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");

// 一条只读（heartbeat_task_list，用户实测现象主角）+ 一条写类（write_file）。
function mixedSnapshot(): ToolRecoverySnapshot {
  return {
    silent: false, statistics: {}, sessionPath: "/s/grading.jsonl", runtimeEpoch: "e1", revision: "r1", retryEnabled: false,
    calls: [
      {
        identity: { attempt_id: "a-ro", canonical_tool: "heartbeat_task_list", argument_digest: "d1", resource_scope: "session:s" },
        state: "started", read_only: true,
      },
      {
        identity: { attempt_id: "a-w", canonical_tool: "write_file", argument_digest: "d2", resource_scope: "session:s" },
        state: "unknown", read_only: false,
      },
    ],
  } as unknown as ToolRecoverySnapshot;
}

let probeCalls = 0;
const probeResult: { snapshot: ToolRecoverySnapshot } = { snapshot: mixedSnapshot() };
const bindings: ToolRecoveryBindings = {
  GetToolRecoveryForTab: async () => { probeCalls += 1; return probeResult.snapshot; },
};

const host = document.createElement("div");
rootEl.appendChild(host);
const root = createRoot(host);

async function renderPanel(running: boolean, refreshKey = 0) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToolRecoveryPanel key="k1" tabId="tab-1" sessionKey="k1" running={running} refreshKey={refreshKey} bindings={bindings} />
      </LocaleProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

const panelHtml = () => host.querySelector(".tool-recovery-panel")?.innerHTML ?? "";
const recordCount = () => host.querySelectorAll(".tool-recovery-panel__record-head").length;

// ── 验收①：zh 文案逐字（用户可见的验收措辞） ────────────────────────
ok(zh["toolRecovery.readonlyInterrupted"] === "只读调用 · 中断（无副作用，可安全忽略）",
  "zh locale carries the exact graded read-only copy");

// ── 验收①（机制）：只读记录不再出现「结果未知」警示字样，改挂分级文案 ──
await renderPanel(false);
ok(recordCount() === 2, "both interrupted records render (read-only + write-class)");

const readonlyRow = panelHtml().split("tool-recovery-panel__record-head").find(part => part.includes("heartbeat_task_list")) ?? "";
ok(readonlyRow.includes("Read-only call · interrupted (no side effects; safe to ignore)"),
  "the read-only record renders the graded read-only copy (en dict in this environment)");
ok(!readonlyRow.includes("Outcome unknown"),
  "the read-only record no longer shows the outcome-unknown warning anywhere in its row");

// ── 验收②：写类记录保持原警示与忽略按钮 ─────────────────────────────
const writeRow = panelHtml().split("tool-recovery-panel__record-head").find(part => part.includes("write_file")) ?? "";
ok(writeRow.includes("Outcome unknown (recorded)"), "the write-class record keeps the original outcome-unknown warning");
ok(!writeRow.includes("Read-only call"), "the write-class record is not misgraded as read-only");
ok(host.querySelectorAll(".tool-recovery-panel__dismiss").length === 2,
  "both records keep their dismiss buttons (1061 contract untouched)");
ok(panelHtml().includes("Dismiss all"), "the dismiss-all entry stays on the summary row (1089 contract untouched)");

// 分级是纯展示：本件不改任何交互路径——探查次数即渲染所需的最少次数。
ok(probeCalls === 1, "grading adds zero extra binding probes");

// 收尾顺序与 sibling 一致：先卸载（订阅者清零）再重置 store，避免 act 外更新警告。
await act(async () => { root.unmount(); });
__resetToolRecoveryDismissalsForTests();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
