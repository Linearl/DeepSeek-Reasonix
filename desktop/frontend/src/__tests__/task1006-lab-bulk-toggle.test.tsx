// 任务 1006 验收（实验室批量开关弹窗）：
//  ① 范围三档（稳定/推荐及以上/可选及以上，无「全部」档）× 行为两轴；
//  ② 重启两选项语义：Apply 回执后出现 立即重启/稍后重启，needsRestart
//     驱动「下次启动生效」文案（987 需重启明示）；
//  ③ 护栏两条：批量关含稳定档警示（①）+ 开发者模式关跳过提示（②）——
//     纯函数判定 + DOM 投影都钉；
//  ④ 三语 locale 键齐全（zh/en/zh-TW 平行）；
//  ⑤ 入口按钮在实验室页 order 行（SettingsPanel 源级断言）。
// Go 侧档位边界/护栏计算的真值测试在 internal/config/lab_bulk_test.go。

import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");

// ── bridge 打桩：app 是 Proxy（get 走 realApp() ?? mock），真替身要挂到
// window.go.main.App —— realApp() 在调用点解析 window.go?.main?.App。────
type PlanFixture = {
  scope: string; on: boolean; targets: string[]; stableInScope: string[];
  skippedStable: string[]; skipped: { id: string; reason: string }[];
  applied?: string[]; needsRestart: boolean;
};
const calls: { plan: unknown[]; apply: unknown[]; restart: number } = { plan: [], apply: [], restart: 0 };
let planFixture: PlanFixture = {
  scope: "recommended", on: true, targets: ["sessionWall"], stableInScope: [],
  skippedStable: [], skipped: [], needsRestart: true,
};
let applyFixture: PlanFixture = { ...planFixture, applied: ["sessionWall"] };
Object.assign(dom.window, {
  go: {
    main: {
      App: {
        LabBulkPlan: async (scope: string, on: boolean) => {
          calls.plan.push([scope, on]);
          return { ...planFixture, scope, on };
        },
        LabBulkApply: async (scope: string, on: boolean) => {
          calls.apply.push([scope, on]);
          return { ...applyFixture, scope, on };
        },
        RestartDesktop: async () => { calls.restart += 1; },
      },
    },
  },
});

const { LabBulkToggleDialog, LAB_BULK_SCOPES, labBulkStableWarning, labBulkStableSkippedNotice } = await import("../components/LabBulkToggleDialog");

console.log("\ntask 1006 lab bulk toggle");

// ── ① 范围三档×行为两轴（无「全部」档）──
ok(LAB_BULK_SCOPES.length === 3, "exactly THREE scopes (no 「all」 — unstable never batched)");
ok(LAB_BULK_SCOPES.includes("stable") && LAB_BULK_SCOPES.includes("recommended") && LAB_BULK_SCOPES.includes("optional"),
  "scopes = stable / recommended / optional");

const rootEl = document.getElementById("root")!;
// 组件收 t: Translator prop —— 本测试断言的是结构（选项/警示/按钮存在性），
// 不是文案本体（文案由 locale 平行测试钉）；直连模块 t（默认 locale）即可。
const { t: rawT } = await import("../lib/i18n");
// React 19：unmount 后 root 不可复用——每轮渲染换新容器 + 新 root。
let root = createRoot(rootEl);
async function remount(node: React.ReactNode) {
  await act(async () => { root.unmount(); });
  rootEl.innerHTML = "";
  root = createRoot(rootEl);
  await act(async () => { root.render(node); });
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
}
async function renderWithT() {
  await remount(<LabBulkToggleDialog t={rawT as never} onClose={() => {}} />);
}

await renderWithT();
const scopeInputs = () => Array.from(document.querySelectorAll<HTMLInputElement>('input[name="lab-bulk-scope"]'));
const behaviorInputs = () => Array.from(document.querySelectorAll<HTMLInputElement>('input[name="lab-bulk-behavior"]'));
ok(scopeInputs().length === 3, "dialog renders THREE scope radios");
ok(behaviorInputs().length === 2, "dialog renders TWO behavior radios (on/off)");
ok(calls.plan.length > 0, "scope/behavior state pulls LabBulkPlan (live preview)");

// 行为切到「关」→ 投影护栏①（含稳定档警示）。
planFixture = {
  ...planFixture, on: false, targets: ["sessionCollab"], stableInScope: ["sessionCollab"],
  skippedStable: [], skipped: [{ id: "fullAccess", reason: "danger_gate" }],
};
await act(async () => {
  behaviorInputs()[1].click();
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
ok(labBulkStableWarning(planFixture) === true, "guardrail① predicate: bulk-off + stable in scope ⇒ warning");
ok(document.querySelector(".lab-bulk-dialog__warn") !== null, "guardrail① warning banner renders in DOM");
ok(document.querySelectorAll(".lab-bulk-dialog__skips li").length === 1, "per-item skip reasons render (danger_gate row)");

// 护栏②：开发者模式关 → 自动跳过稳定档提示。
planFixture = { ...planFixture, skippedStable: ["sessionCollab"], stableInScope: ["sessionCollab"] };
await renderWithT();
await act(async () => {
  behaviorInputs()[1].click();
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
ok(labBulkStableSkippedNotice(planFixture) === true, "guardrail② predicate: dev-mode off ⇒ stable auto-skip notice");
ok(document.querySelector(".lab-bulk-dialog__skip-note") !== null, "guardrail② skip notice renders in DOM");

// ── ② 重启两选项（Apply 回执阶段）──
applyFixture = {
  scope: "recommended", on: false, targets: ["sessionWall"], stableInScope: [],
  skippedStable: [], skipped: [], applied: ["sessionWall", "todoSidebar"], needsRestart: true,
};
await renderWithT();
await act(async () => { behaviorInputs()[1].click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const confirmBtn = document.querySelectorAll<HTMLButtonElement>(".lab-bulk-dialog__actions .btn")[0];
ok(confirmBtn !== undefined, "confirm button present");
await act(async () => { confirmBtn!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
ok(calls.apply.length > 0, "confirm calls LabBulkApply");
ok(document.querySelector(".lab-bulk-dialog__restart") !== null, "receipt phase renders restart block");
const restartBtns = Array.from(document.querySelectorAll<HTMLButtonElement>(".lab-bulk-dialog__restart-actions .btn"));
ok(restartBtns.length === 2, "restart block has exactly TWO options (now / later)");
ok(restartBtns[0].textContent !== undefined && restartBtns[1].textContent !== undefined, "both restart options carry labels");
// 立即重启 → RestartDesktop；稍后重启 → onClose。
await act(async () => { restartBtns[0].click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
ok(calls.restart === 1, "「立即重启」invokes RestartDesktop");

// 稍后重启路径（重新渲染一遍）。
let closed = 0;
await remount(<LabBulkToggleDialog t={rawT as never} onClose={() => { closed += 1; }} />);
{
  const confirm2 = document.querySelectorAll<HTMLButtonElement>(".lab-bulk-dialog__actions .btn")[0];
  await act(async () => { confirm2.click(); });
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  const later = Array.from(document.querySelectorAll<HTMLButtonElement>(".lab-bulk-dialog__restart-actions .btn"))[1];
  ok(later.textContent !== undefined && later.textContent.length > 0, "「稍后重启」option labeled");
  await act(async () => { later.click(); });
  ok(closed === 1, "「稍后重启」closes the dialog (config stays written, next start applies)");
}

// ── ④ 三语 locale 平行 ──
const zh = (await import("../locales/zh")).zh;
const en = (await import("../locales/en")).en;
const zhTW = (await import("../locales/zh-TW")).zhTW;
const bulkKeys = Object.keys(en).filter((k) => k.startsWith("settings.labBulk."));
ok(bulkKeys.length >= 18, `locale table carries the labBulk key family (${bulkKeys.length} keys)`);
for (const k of bulkKeys) {
  if (!(k in zh)) { ok(false, `zh missing ${k}`); }
  if (!(k in zhTW)) { ok(false, `zh-TW missing ${k}`); }
}
ok(bulkKeys.every((k) => k in zh && k in zhTW), "zh / zh-TW carry every labBulk key (三语平行)");

// ── ⑤ 入口按钮 + SettingsPanel 接线（源级）──
const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
ok(panel.includes('t("settings.labBulk.button")'), "lab page renders the bulk-toggle entry button");
ok(panel.includes("<LabBulkToggleDialog"), "SettingsPanel mounts LabBulkToggleDialog");
ok(panel.includes("experimental-lab__bulk-toggle"), "entry button rides the order-row pill class");
ok(!/scope\s+[=:]\s*"all"/.test(panel), "no 「all」 scope anywhere in the wiring");

// 非法 scope 在桥层被拒（Go ParseLabBulkScope 契约——前端不定义 all/unstable）。
ok(!(LAB_BULK_SCOPES as readonly string[]).includes("all"), "scope list has no 'all'");
ok(!(LAB_BULK_SCOPES as readonly string[]).includes("unstable"), "scope list has no 'unstable'");

await act(async () => { root.unmount(); });
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
