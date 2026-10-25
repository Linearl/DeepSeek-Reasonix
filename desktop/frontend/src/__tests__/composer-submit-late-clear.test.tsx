// Run: node node_modules/.pnpm/tsx@4.23.15/node_modules/tsx/dist/cli.mjs --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-submit-late-clear.test.tsx
//
// tl1162 回归钉（P1：切模型后输入框内容丢失 + 重发内容无响应）。
//
// 事故链（desktop.log 2026-10-25 23:34-23:39）：切模型走 602 暖路径（709ms）后，
// effort=max 落在 deepseek 词表外（level-not-in-vocabulary）→ set-effort fallback
// 全量重建 runtime（61s）→ 提交受理阻塞（controller_submit 27s）。在这类慢受理
// 窗口内：
//   1) submit 主分支 `await onSend` 迟到的 clearSubmittedDraft 无条件清空输入框，
//      把用户在窗口期内改写/重发的新内容一并吞掉（followup/durable/steer 分支
//      均有指纹守卫，唯独这条最高频路径漏了）——「内容直接丢失」；
//   2) submitting latch 期间 Enter 重发被静默 return——「重发无响应」。
//
// 断言面：
//   A. 慢受理窗口内改写输入框 → onSend 返回后新内容保留（修复点）；
//   B. 慢受理窗口内容未动 → 返回后照常清空（提交成功语义不回退）；
//   C. 快路径（onSend 立即返回）→ 照常清空；
//   D. 窗口内切会话再切回（draft epoch bump 迁移链）→ 草稿恢复、迟到清空不吞
//      切回后的改写；
//   E. latch 期间再次提交 → 不重复 onSend（latch 保持）且给出 toast 反馈（不再
//      静默吞）。

import { installDom, installBridgeApp, renderComposer } from "./composerInboxHarness";
import { act } from "react";
import assert from "node:assert/strict";

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

const flushTimers = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
const settle = async () => {
  for (let i = 0; i < 2; i += 1) await new Promise<void>((resolve) => setTimeout(resolve, 20));
};

function setNativeValue(ta: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set;
  if (!setter) throw new Error("native textarea value setter unavailable");
  setter.call(ta, value);
}

async function typeText(ta: HTMLTextAreaElement, value: string) {
  await act(async () => {
    ta.focus();
    setNativeValue(ta, value);
    ta.setSelectionRange(value.length, value.length);
    ta.dispatchEvent(new window.InputEvent("input", { bubbles: true, data: value, inputType: "insertText", isComposing: false }));
    ta.dispatchEvent(new window.Event("change", { bubbles: true }));
    ta.dispatchEvent(new window.KeyboardEvent("keyup", { key: "x", bubbles: true }));
    await flushTimers();
    await settle();
  });
}

function composerInput(): HTMLTextAreaElement {
  const ta = document.querySelector("textarea.composer__input:not([aria-hidden=true])");
  if (!(ta instanceof HTMLTextAreaElement)) throw new Error("composer textarea missing");
  return ta;
}

async function clickSend() {
  await act(async () => {
    (document.querySelector(".composer__btn--send") as HTMLButtonElement).click();
    await flushTimers();
    await settle();
  });
}

function toastText(): string {
  return Array.from(document.querySelectorAll(".toast__text")).map((el) => el.textContent ?? "").join("\n");
}

console.log("\ntl1162 慢受理窗口的迟到清空与 latch 反馈");

// A. 核心钉：窗口内改写输入框，新内容不被迟到的 clearSubmittedDraft 吞掉。
{
  const dom = installDom();
  installBridgeApp({});
  let resolveSend: () => void = () => {};
  let sends = 0;
  const view = await renderComposer({
    onSend: () => new Promise<void>((resolve) => { sends += 1; resolveSend = resolve; }),
  });
  const ta = composerInput();
  await typeText(ta, "hello");
  await clickSend();
  ok(sends === 1, "A: submit dispatched once");
  // 慢受理窗口内：用户以为没发出去，改写输入框（重发内容）。
  await typeText(ta, "hello again (resent)");
  await act(async () => { resolveSend(); await flushTimers(); await settle(); });
  ok(composerInput().value === "hello again (resent)",
    "A: late clearSubmittedDraft keeps text rewritten during the admission window");
  await act(async () => { view.root.unmount(); });
  dom.window.close();
}

// B. 窗口内容未动 → 提交成功照常清空（原语义不回退）。
{
  const dom = installDom();
  installBridgeApp({});
  let resolveSend: () => void = () => {};
  const view = await renderComposer({
    onSend: () => new Promise<void>((resolve) => { resolveSend = resolve; }),
  });
  const ta = composerInput();
  await typeText(ta, "hello");
  await clickSend();
  ok(composerInput().value === "hello", "B: draft still shown while admission is pending");
  await act(async () => { resolveSend(); await flushTimers(); await settle(); });
  ok(composerInput().value === "", "B: unchanged draft is still cleared after admission");
  await act(async () => { view.root.unmount(); });
  dom.window.close();
}

// C. 快路径（正常受理速度）行为不变。
{
  const dom = installDom();
  installBridgeApp({});
  let sends = 0;
  const view = await renderComposer({
    onSend: () => { sends += 1; return Promise.resolve(); },
  });
  const ta = composerInput();
  await typeText(ta, "quick one");
  await clickSend();
  await settle();
  ok(sends === 1 && composerInput().value === "", "C: fast path clears the composer as before");
  await act(async () => { view.root.unmount(); });
  dom.window.close();
}

// D. 窗口内切会话再切回（draft epoch bump 迁移链）：
//    草稿跨 key 迁移恢复；切回后的改写不被迟到清空吞掉。
{
  const dom = installDom();
  installBridgeApp({});
  let resolveSend: () => void = () => {};
  const view = await renderComposer({
    sessionKey: "session-a",
    onSend: () => new Promise<void>((resolve) => { resolveSend = resolve; }),
  });
  const ta = composerInput();
  await typeText(ta, "hello");
  await clickSend();
  // 切到会话 B：epoch bump，A 的草稿（仍是提交时的 hello）随 snapshot 迁移暂存。
  await view.rerender({ sessionKey: "session-b" });
  ok(composerInput().value === "", "D: switching sessions swaps to the other draft");
  // 切回 A：恢复提交时的草稿。
  await view.rerender({ sessionKey: "session-a" });
  ok(composerInput().value === "hello", "D: switching back restores the submitted draft");
  // 用户在切回后改写（重发内容）——迟到清空不得吞掉。
  await typeText(ta, "hello, resent after switch back");
  await act(async () => { resolveSend(); await flushTimers(); await settle(); });
  ok(composerInput().value === "hello, resent after switch back",
    "D: rewrite after switching back survives the late clear");
  await act(async () => { view.root.unmount(); });
  dom.window.close();
}

// E. latch 期间再次提交：不重复 onSend，且给出 toast 反馈（不再静默吞）。
{
  const dom = installDom();
  installBridgeApp({});
  let resolveSend: () => void = () => {};
  let sends = 0;
  const view = await renderComposer({
    onSend: () => new Promise<void>((resolve) => { sends += 1; resolveSend = resolve; }),
  });
  const ta = composerInput();
  await typeText(ta, "first");
  await clickSend();
  ok(sends === 1, "E: first submit dispatched");
  // 慢受理窗口内用户重按发送（Enter 直调 submit，按钮 disabled 不拦键盘路径）。
  await act(async () => {
    ta.focus();
    ta.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    await flushTimers();
    await settle();
  });
  ok(sends === 1, "E: latch still holds - no duplicate onSend");
  ok(/still being admitted/i.test(toastText()), "E: in-flight retry now reports a toast instead of a silent swallow");
  await act(async () => { resolveSend(); await flushTimers(); await settle(); });
  await act(async () => { view.root.unmount(); });
  dom.window.close();
}

assert.equal(failed, 0, "tl1162 regression pins must all pass");
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
