// Run: node node_modules/tsx/dist/cli.mjs --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1544-edit-resend-entry.test.tsx
//
// tl1544 — 运行中编辑历史消息回溯重发的前端入口钉。
//
// 机制（tl1375 调研 B 案，对标 zcode 交互三步合一）：
//   回合后链路已完整（rewindForTabDetailed → sendToTab，useSessionUndo
//   handleEditPrompt 既有行为，零改动）；真实增量 = 运行中入口：实验开关
//   experimentalEditResend 开启时，input.running 不再拒绝编辑，改走
//   EditAndResendForTab 原子编排（中止→治理→fork 截断→编辑重发一次调用）。
//
// 钉面：
//   A1 开关开+running：走 editAndResendForTab，绝不降级 rewindForTabDetailed
//      （steer 保留输出、编辑重发抛弃——语义相反，降级即违背回溯意图）；
//   A2 开关关+running：维持拒绝（return false，现状逐字节）；
//   A3 开关开+running+只读通道：拒绝；
//   A4 开关开+running+编排失败：return false 且不刷 dock/project；
//   A5 回合后（非 running）：开关开关都不改既有链（rewind+send 零回退）；
//   A6 original 反查：checkpointTurn 命中项的 submitText 作为 original 传递。
import assert from "node:assert/strict";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { useSessionUndo } from "../app-runtime/useSessionUndo";
import type { Item } from "../lib/useController";

const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
const root = createRoot(document.getElementById("root")!);

function user(text: string, checkpointTurn: number): Item {
  return { kind: "user", id: `u:${text}`, text, submitText: text, checkpointTurn } as Item;
}

const calls: string[] = [];
let editResendOutcome: { ok: boolean; error?: string; branch?: string } = { ok: true, branch: "fork-path" };
let states!: ReturnType<typeof useSessionUndo>;

function Probe({ running = false, editResendEnabled = false, readOnly = false }: {
  running?: boolean; editResendEnabled?: boolean; readOnly?: boolean;
}) {
  const items: Item[] = [user("first prompt", 1), user("second prompt", 2)];
  states = useSessionUndo({
    activeTabId: "A", activeTabReadOnly: readOnly, items,
    hydratePlaceholderActive: false, controllerReady: true, running,
    messageActionOpen: false, approvalOpen: false, askOpen: false, clearContextPending: false,
    editResendEnabled,
    ports: {
      rewindForTab: async () => { calls.push("rewind"); return true; },
      rewindForTabDetailed: async (_tabId, turn, scope) => {
        calls.push(`detailed:${turn}:${scope}`);
        return { ok: true };
      },
      refreshTabMetas: () => { calls.push("refresh-metas"); },
      undoRewindForTab: async () => { calls.push("undo"); return true; },
      sendToTab: async (_tabId, display, submit, original) => {
        calls.push(`send:${display}|${submit}|${original}`);
      },
      editAndResendForTab: async (_tabId, turn, display, submit, original) => {
        calls.push(`editResend:${turn}|${display}|${submit}|${original}`);
        return editResendOutcome;
      },
      composeInsert: (_tabId, text) => { calls.push(`insert:${text}`); },
      refreshDock: () => { calls.push("dock"); },
      refreshProject: () => { calls.push("project"); },
    },
  });
  return null;
}
const paint = (options?: Parameters<typeof Probe>[0]) => act(async () => root.render(<Probe {...options} />));

const checks: Array<[string, () => void]> = [];

// A1 开关开+running：走原子编排通道，成功后刷 dock/project。
checks.push(["A1 running+开关开 走 EditAndResendForTab（绝不降级 rewind 链）", () => {
  assert.deepEqual(calls, ["editResend:2|second EDITED|second EDITED|second prompt", "dock", "project"]);
}]);

// A2 开关关+running：拒绝（现状）。
checks.push(["A2 running+开关关 return false（现状拒绝零变化）", () => {
  assert.deepEqual(calls, []);
}]);

// A3 只读通道拒绝。
checks.push(["A3 running+开关开+只读 拒绝", () => {
  assert.deepEqual(calls, []);
}]);

// A4 编排失败：false+不刷。
checks.push(["A4 running+编排失败 return false 且零刷新", () => {
  assert.deepEqual(calls, ["editResend:2|second EDITED|second EDITED|second prompt"]);
}]);

// A5 回合后：既有链零回退（开关开也一样走 rewind+send）。
checks.push(["A5 回合后走既有 rewind+send 链（开关开亦然）", () => {
  assert.ok(calls.some((c) => c.startsWith("detailed:2:conversation")), `want detailed call, got ${calls}`);
  assert.ok(calls.some((c) => c.startsWith("send:")), `want send call, got ${calls}`);
  assert.ok(!calls.some((c) => c.startsWith("editResend:")), "idle edits must NOT route through the running orchestration");
}]);

// A6 original 传递：checkpointTurn 反查命中 submitText。
checks.push(["A6 original=checkpointTurn 命中项的 submitText", () => {
  assert.ok(calls.some((c) => c.endsWith("|second prompt")), `want original tail, got ${calls}`);
}]);

let failed = 0;
try {
  // A1
  await paint({ running: true, editResendEnabled: true });
  calls.length = 0;
  let ok = false;
  await act(async () => { ok = await states.handleEditPrompt(2, "second EDITED"); });
  assert.equal(ok, true, "A1 ok");
  checks[0][1]();

  // A2
  await paint({ running: true, editResendEnabled: false });
  calls.length = 0;
  await act(async () => { ok = await states.handleEditPrompt(2, "second EDITED"); });
  assert.equal(ok, false, "A2 refuse");
  checks[1][1]();

  // A3
  await paint({ running: true, editResendEnabled: true, readOnly: true });
  calls.length = 0;
  await act(async () => { ok = await states.handleEditPrompt(2, "second EDITED"); });
  assert.equal(ok, false, "A3 readOnly refuse");
  checks[2][1]();

  // A4
  await paint({ running: true, editResendEnabled: true });
  editResendOutcome = { ok: false, error: "boom" };
  calls.length = 0;
  await act(async () => { ok = await states.handleEditPrompt(2, "second EDITED"); });
  assert.equal(ok, false, "A4 fail path");
  checks[3][1]();
  editResendOutcome = { ok: true, branch: "fork-path" };

  // A5+A6
  await paint({ running: false, editResendEnabled: true });
  calls.length = 0;
  await act(async () => { ok = await states.handleEditPrompt(2, "second EDITED"); });
  assert.equal(ok, true, "A5 ok");
  checks[4][1]();
  checks[5][1]();

  console.log(`  PASS  ${checks.map((c) => c[0]).join("\n  PASS  ")}`);
  console.log(`\n${checks.length} passed, 0 failed`);
} catch (err) {
  failed = 1;
  console.error("FAIL:", err);
  console.error("calls:", calls);
} finally {
  root.unmount();
}
process.exit(failed);
