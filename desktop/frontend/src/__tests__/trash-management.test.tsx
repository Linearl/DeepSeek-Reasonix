import { registerHooks } from "node:module";
registerHooks({ resolve(specifier, context, nextResolve) { return specifier.endsWith(".svg") ? nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url }) : nextResolve(specifier, context); } });
import assert from "node:assert/strict";
import { managementDom } from "../test-support/managementDom";
import type { SessionMeta } from "../lib/types";
const dom = managementDom();
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
const { TrashPage } = await import("../components/TrashPage");
const session = (id: string, recoveryCopy = false): SessionMeta => ({ path: id, title: id, preview: id, turns: 0, createdAt: 1, lastActivityAt: 1, modTime: 1, deletedAt: Date.now(), current: false, open: false, recoveryCopy });
let sessions = [session("a"), session("b"), session("protected", true)];
let latePreview!: () => void;
Object.assign(window, { go: { main: { App: { PreviewSession: (path: string) => path === "a" ? new Promise<never[]>((resolve) => { latePreview = () => resolve([]); }) : Promise.resolve([]) } } } });
const calls: string[] = [];
let listFails = false;
// tl1306：让「b」挂在批次中间，才能观察进行中的进度条与离页完成通知。
let releaseB!: () => void;
const pendingB = new Promise<void>((resolve) => { releaseB = resolve; });
const list = async () => { if (listFails) throw new Error("read failure"); return sessions; };
const purge = async (path: string) => {
  calls.push(path);
  if (path === "b") { await pendingB; throw new Error("busy"); }
  sessions = sessions.filter((item) => item.path !== path);
};
const restore = async (path: string) => { sessions = sessions.filter((item) => item.path !== path); listFails = true; };
const root = createRoot(document.getElementById("root")!);
const render = (active = true) => <LocaleProvider><ToastProvider><TrashPage active={active} onBack={() => {}} list={list} purge={purge} restore={restore} /></ToastProvider></LocaleProvider>;
const button = (text: string) => Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((node) => node.textContent?.trim() === text)!;
await act(async () => root.render(render()));
assert.equal(document.querySelector(".management-modal-backdrop"), null);
await act(async () => (document.querySelectorAll('.hist-item__main')[1] as HTMLButtonElement).click());
await act(async () => latePreview());
assert.equal(document.querySelector('.history-preview__title')?.textContent, "b", "late preview must not replace the selected conversation");
await act(async () => (document.querySelector('.history-clear') as HTMLButtonElement).click());
assert.ok(document.querySelector('[role="dialog"]')?.textContent?.includes("all 2 ordinary conversations"));
assert.equal(document.activeElement?.textContent, "Cancel", "destructive confirmation starts on cancel");
await act(async () => button("Confirm deletion").click());
// tl1306 断言①进度：批次进行中（a 已删、b 挂起）进度条可见且推进到 1/2。
assert.deepEqual(calls, ["a", "b"], "clear excludes recovery data and reaches the hanging item while it is still pending");
const progress = document.querySelector(".trash-batch-progress");
assert.ok(progress, "batch progress bar is visible while clearing");
assert.match(progress!.querySelector(".trash-batch-progress__label")?.textContent ?? "", /Deleted 1\/2/, "progress label reports x/N deleted");
assert.equal(progress!.querySelector(".trash-batch-progress__track")?.getAttribute("aria-valuenow"), "1", "progressbar aria-valuenow tracks settled items");
// tl1306 断言③异步：离页（页面 hidden 不卸载）批次继续跑，未完成不抢 toast。
await act(async () => root.render(render(false)));
assert.equal(document.querySelector(".toast"), null, "no completion toast while the batch is still running");
await act(async () => { releaseB(); });
await act(async () => {});
// tl1306 断言②摘要：完成后 toast + 页内摘要都在，失败项带原因明细；进度条隐藏。
const toast = document.querySelector(".toast");
assert.ok(toast, "completion toast appears when the user is away from the trash page");
assert.ok(toast!.textContent?.includes("Deleted 1; 1 did not complete."), "toast carries the success/failure summary");
assert.ok(document.body.textContent?.includes("Deleted 1; 1 did not complete."));
assert.ok(document.body.textContent?.includes("Failed items:"), "summary lists a failure detail section");
assert.ok(document.body.textContent?.includes("b: busy"), "failure detail names the session and the reason");
assert.equal(document.querySelector(".trash-batch-progress"), null, "progress bar hides once the batch settles");
assert.equal(document.querySelector(".toast-container")?.getAttribute("role"), "status");
// 回到页面：摘要与失败明细仍在，预览选中不被批次破坏。
await act(async () => root.render(render(true)));
assert.equal(document.querySelector('.history-preview__title')?.textContent, "b");
await act(async () => button("Retry failed items").click());
assert.deepEqual(calls, ["a", "b", "b"], "explicit retry only targets failures");
await act(async () => button("Restore").click());
assert.ok(document.body.textContent?.includes("Operation completed, but the list could not refresh."));
assert.equal(document.querySelectorAll('.hist-item').length, 0, "successful restore is retained locally when refresh fails");
assert.ok(sessions.some((item) => item.path === "protected"));
await act(async () => root.render(render(false)));
assert.equal(document.querySelector('[role="dialog"]'), null);
await act(async () => root.unmount());
dom.window.close();
console.log("PASS trash page progress bar, away-page completion toast, failure detail summary and legacy partial-failure flow");
