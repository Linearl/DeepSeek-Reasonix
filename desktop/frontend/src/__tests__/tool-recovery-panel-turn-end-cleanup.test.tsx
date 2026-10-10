// Run: tsx src/__tests__/tool-recovery-panel-turn-end-cleanup.test.tsx
//
// 任务 717（中断的工具调用记录自动清理）前端生命周期契约。
//
// 后端半（internal/agent：Run 结束结算 pre-turn pending，resolution=
// dismissed_by_new_turn）是清理的执行者；本测试钉住面板半必须守住的探查
// 时序 —— 用户发消息（running=true）面板即隐藏；turn 结束（running=false /
// refreshKey 变化）面板重新探查，后端已结算（快照为空）则保持隐藏，后端
// 仍有未决（本 turn 自己的中断）则如实再显示。折叠条「新 turn 结束后消失」
// 的用户可见效果 = 后端结算 + 这两条探查路径，任何一条被改坏都会回归成
// 「折叠条永不消失」。

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import { act } from "react";
import { createRoot } from "react-dom/client";

// ToolRecoveryPanel imports its own CSS; redirect like the sibling
// tool-recovery-panel-null-calls test does (see its header for the why).
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

function snapshotWith(attemptId: string): ToolRecoverySnapshot {
  return {
    silent: false, statistics: {}, sessionPath: "/s/a.jsonl", runtimeEpoch: "e1", revision: "r1", retryEnabled: false,
    calls: [{
      identity: { attempt_id: attemptId, canonical_tool: "bash", argument_digest: "d", resource_scope: "session:s" },
      state: "unknown", read_only: false,
    }],
  } as unknown as ToolRecoverySnapshot;
}
const emptySnapshot: ToolRecoverySnapshot = {
  silent: false, statistics: {}, sessionPath: "/s/a.jsonl", runtimeEpoch: "e1", revision: "r2", retryEnabled: false,
  calls: [],
};

// The probe result is switchable between renders: the fake backend "settles"
// (returns empty) once the turn that followed the user's move-on has ended.
const probeResult: { snapshot: ToolRecoverySnapshot } = { snapshot: snapshotWith("a1") };
const bindings: ToolRecoveryBindings = {
  GetToolRecoveryForTab: async () => probeResult.snapshot,
  ResolveToolRecoveryForTab: async () => probeResult.snapshot,
};

const host = document.createElement("div");
rootEl.appendChild(host);
const root = createRoot(host);

type PanelProps = { running: boolean; refreshKey: number };
async function renderPanel(props: PanelProps) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToolRecoveryPanel tabId="tab-1" sessionKey="k1" running={props.running} refreshKey={props.refreshKey} bindings={bindings} />
      </LocaleProvider>,
    );
    // Let the probe promise (microtask) and the setState it triggers land.
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

const visible = () => host.innerHTML.includes("tool-recovery-panel");

// ── 场景 A：717 验收主路径 ──────────────────────────────────────────────
// turn 前：一条遗留记录 → 折叠条在。
await renderPanel({ running: false, refreshKey: 0 });
ok(visible(), "pre-turn: a pending record renders the fold-out");

// 用户发新消息（running=true）：面板立即隐藏 —— 表态即生效，不等到 turn 结束。
await renderPanel({ running: true, refreshKey: 0 });
ok(!visible(), "user sends a message: the fold-out hides while the turn runs");

// turn 结束：后端已把 pre-turn 记录结算（探查返回空）→ 折叠条不再回来。
probeResult.snapshot = emptySnapshot;
await renderPanel({ running: false, refreshKey: 2 });
ok(!visible(), "turn end with a settled snapshot: the fold-out stays gone");

// ── 场景 B：turn 自己留下的中断必须如实显示 ─────────────────────────────
// 折叠条消失来自后端结算，不是面板学会了隐藏：探查仍有未决（本 turn 新产生的
// 中断）时，turn 结束后面板要再显示。
probeResult.snapshot = snapshotWith("a2");
await renderPanel({ running: false, refreshKey: 3 });
ok(visible(), "turn end with a still-pending in-turn interruption: it renders again");

// 下一个 turn 结束把它也结算 → 消失。历史不无限累积的直接展示。
probeResult.snapshot = emptySnapshot;
await renderPanel({ running: false, refreshKey: 4 });
ok(!visible(), "next turn end settles it too: gone again");

// ── 场景 C：running 不变、仅 refreshKey 变化的重探查路径 ────────────────
// 手动路径（636 的 ✕ / 单条已确认）走 resolve 返回的新快照；消息追加（items.length
// 变化）驱动的 refreshKey 重探查是同一渲染规则的另一入口，同样必须生效。
probeResult.snapshot = snapshotWith("a3");
await renderPanel({ running: false, refreshKey: 5 });
ok(visible(), "a fresh pending record renders (running unchanged)");

probeResult.snapshot = emptySnapshot;
await renderPanel({ running: false, refreshKey: 6 });
ok(!visible(), "refreshKey re-probe with an empty snapshot clears it");

await act(async () => { root.unmount(); });
host.remove();
dom.window.close();
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
