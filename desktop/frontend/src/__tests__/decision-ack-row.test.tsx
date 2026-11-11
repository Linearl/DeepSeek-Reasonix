// Run: tsx src/__tests__/decision-ack-row.test.tsx
// tl1541：「待确认」徽章手动消除按钮——对话内（footer 决策区）的「已确认 /
// 知道了」显式确认动作条。
//
// 验收映射：
// ① 钉：prompt 族决策面（tool_approval / plan_approval / ask / mcp_interaction）
//    渲染「知道了」按钮，点击触发确认回调（→ AcknowledgePendingPromptsForTab，
//    侧栏 pill + tab 条琥珀由后端同源消除）；
// ② 负钉：非 prompt 族决策面（close_active / clear_context / workspace_conflict
//    / extension_form）与无决策面不渲染——它们各有自己的取消/确认语义；
// ③ 语义边界：按钮 title 明示「清除等待并停止回合」，不谎称「已同意」。

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.window.requestAnimationFrame = ((cb: FrameRequestCallback) =>
  setTimeout(() => cb(dom.window.performance.now()), 0)) as unknown as typeof window.requestAnimationFrame;
globalThis.window.cancelAnimationFrame = ((id: number) => clearTimeout(id)) as unknown as typeof window.cancelAnimationFrame;

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

const React = await import("react");
const { createElement } = React;
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
// LocaleProvider 不接 locale prop：测试环境 locale 走 navigator 检测
// （JSDOM 默认 en-US → en）。词条对比一律自洽（渲染产物 vs tFor(实际locale)），
// 语义实质另用同步在册的 en 词典直接钉。
const { LocaleProvider, t, tFor, getLocale } = await import("../lib/i18n");
const { DecisionAckRow, isPromptAckSurface } = await import("../components/DecisionAckRow");

function makeRow(surface: string | null, onClick: () => void) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  return {
    mount: async () => {
      await act(async () => {
        root.render(
          createElement(LocaleProvider, null, createElement(DecisionAckRow, { surface, onAcknowledge: onClick })),
        );
      });
    },
    unmount: async () => {
      await act(async () => { root.unmount(); });
      container.remove();
    },
    container,
  };
}

console.log("\ntl1541：「已确认 / 知道了」动作条（footer 决策区）");

{
  // ① 谓词面：四类 prompt 决策面可确认，其余一概不可
  eq(isPromptAckSurface("tool_approval"), true, "tool_approval 是 prompt 决策面");
  eq(isPromptAckSurface("plan_approval"), true, "plan_approval 是 prompt 决策面");
  eq(isPromptAckSurface("ask"), true, "ask 是 prompt 决策面");
  eq(isPromptAckSurface("mcp_interaction"), true, "mcp_interaction 是 prompt 决策面");
  eq(isPromptAckSurface("close_active"), false, "close_active 不是 prompt 决策面");
  eq(isPromptAckSurface("clear_context"), false, "clear_context 不是 prompt 决策面");
  eq(isPromptAckSurface("workspace_conflict"), false, "workspace_conflict 不是 prompt 决策面");
  eq(isPromptAckSurface("extension_form"), false, "extension_form 不是 prompt 决策面");
  eq(isPromptAckSurface(null), false, "无决策面不可确认");
}
{
  // ① 钉：prompt 族渲染按钮 + 点击触发确认回调
  for (const surface of ["tool_approval", "plan_approval", "ask", "mcp_interaction"] as const) {
    let clicks = 0;
    const row = makeRow(surface, () => { clicks += 1; });
    await row.mount();
    const button = row.container.querySelector(".decision-ack-row button");
    eq(Boolean(button), true, `${surface} 渲染「知道了」按钮`);
    eq(button?.textContent ?? null, t("decision.acknowledge"), `${surface} 按钮文案复用 decision.acknowledge 词条`);
    if (button) {
      await act(async () => {
        button.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      });
    }
    eq(clicks, 1, `${surface} 点击触发确认回调`);
    await row.unmount();
  }
}
{
  // ② 负钉：非 prompt 族与无决策面不渲染任何按钮
  for (const surface of ["close_active", "clear_context", "workspace_conflict", "extension_form", null]) {
    const row = makeRow(surface, () => {});
    await row.mount();
    eq(row.container.querySelector(".decision-ack-row button"), null, `${surface ?? "null"} 不渲染确认按钮`);
    await row.unmount();
  }
}
{
  // ③ 语义边界：title 复用 decision.acknowledgeDesc 词条且实质明示
  // 「清除等待并停止回合」，不谎称已同意（en 词典同步在册，直接钉实质）
  const row = makeRow("ask", () => {});
  await row.mount();
  const button = row.container.querySelector(".decision-ack-row button");
  const title = button?.getAttribute("title") ?? "";
  eq(title, tFor(getLocale())("decision.acknowledgeDesc"), "title 复用 decision.acknowledgeDesc 词条（渲染 locale 自洽）");
  eq(tFor("en")("decision.acknowledgeDesc").includes("stop the running turn"), true, "desc 明示回合将停止（语义不谎称「已同意」）");
  eq(tFor("en")("decision.acknowledgeDesc").includes("clear this wait"), true, "desc 明示清除的是本次等待");
  eq(tFor("en")("decision.acknowledge") === "Got it", true, "按钮词条 en=Got it（「知道了」语义）");
  await row.unmount();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
