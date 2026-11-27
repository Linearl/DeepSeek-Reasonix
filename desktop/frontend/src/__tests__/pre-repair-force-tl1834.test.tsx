// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/pre-repair-force-tl1834.test.tsx
//
// tl1834 件4: pre-repair 强制删除链路的前端钉（后端钉在 Go
// pre_repair_cleanup_force_test.go）。验收=强制删除链路通：
//  ① 单删：健康闸拒绝（owner session is not healthy）→ 强制删除弹窗出现
//     （列明失败与后果）→ 确认强制 → 桥带 force=true 重调 → 成功回执；
//  ② 单删：拒绝强制 → 桥只调过一次（force=false），错误照旧落错误面
//     （不因新增出口丢失诚实报告）；
//  ③ 单删：物理失败（trash-failed）不弹强制框——force 对物理失败无事可做，
//     不给虚假「下一步」；
//  ④ 批量：失败清单弹窗列明「删了哪些（{ok}）/没删成哪些（清单）」→ 确认
//     强制 → 桥对失败项带 force=true 重调 → ForceDone 回执；
//  ⑤ 批量强制轮仍失败：逐条落面板错误区 + ForceRefused 诚实 toast（不静默吞）；
//  ⑥ 批量拒绝强制：桥只调过一次；owner-unhealthy 警示区照常渲染（既有语义）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings, PreRepairBackupView, PreRepairBatchCleanupView } from "../lib/bridge";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
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

const { RecoveryCopiesSection } = await import("../components/RecoveryCopiesSection");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

function installDom(): HTMLElement {
  document.getElementById("root")?.remove();
  window.localStorage.clear();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes(text),
  );
}

function query(selector: string): HTMLElement | undefined {
  return document.querySelector<HTMLElement>(selector) ?? undefined;
}

function bodyText(): string {
  return document.body.textContent || "";
}

function toastText(): string {
  return document.getElementById("rt-notice-stack")?.textContent || "";
}

function dialogConfirm(): HTMLButtonElement | undefined {
  const actions = document.querySelector<HTMLElement>(".reasonix-confirm-dialog__actions");
  const buttons = actions?.querySelectorAll<HTMLButtonElement>("button");
  return buttons && buttons.length > 0 ? buttons[buttons.length - 1] : undefined;
}

const UNHEALTHY_DETAIL = "the owner session is not healthy (projection_overflow) — this backup is its way back, repair the session first: sick.pre-repair-20261110T010000";

const BACKUPS: PreRepairBackupView[] = [
  { dir: "/sessions/sick.pre-repair-20261110T010000", transcript: "/sessions/sick.pre-repair-20261110T010000/sick.jsonl", ownerPath: "/sessions/sick.jsonl", label: "sick conversation", stamp: "20261110T010000", bytes: 2048, files: 2, modifiedAt: "2026-11-10T01:00:00Z" },
];

type Spies = {
  deleteCalls: [string, boolean][];
  batchCalls: [string[], boolean][];
  deleteScript?: ((dir: string, force: boolean) => Promise<PreRepairDeleteShape>) | undefined;
};

type PreRepairDeleteShape = { trashed: boolean; viaStaging?: boolean; permanentOnly?: boolean; detail?: string };

function stubBridge(spies: Spies, batchScript?: (dirs: string[], force: boolean) => Promise<PreRepairBatchCleanupView>) {
  const base = {
    ListRecoveryCopyGroups: async () => [],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => ({
      mainPath: "", mainLabel: "", chains: [], longestPath: "", newestPath: "", longestHead: "", newestHead: "",
    }),
    ListSessionRepairRecordsFor: async () => [],
    ListPreRepairBackups: async () => BACKUPS,
    DeletePreRepairBackup: async (dir: string, force: boolean) => {
      spies.deleteCalls.push([dir, force]);
      if (spies.deleteScript) return spies.deleteScript(dir, force);
      if (!force) throw new Error(UNHEALTHY_DETAIL);
      return { trashed: true, viaStaging: false };
    },
    DeletePreRepairBackupPermanent: async () => {},
    DeletePreRepairBackups: async (dirs: string[], force: boolean) => {
      spies.batchCalls.push([dirs, force]);
      if (batchScript) return batchScript(dirs, force);
      return { trashed: 0, trashedBytes: 0, staged: 0, failed: 1, failures: [{ dir: dirs[0], label: "sick conversation", reason: "owner-unhealthy", detail: UNHEALTHY_DETAIL }] };
    },
    PreviewRecoveryChain: async (_mainPath: string, chainPath: string) => ({
      path: chainPath, isMain: true, messageCount: 1, turns: 1, firstUserText: "", tailLines: ["[user]: x"], sharedWithMain: 1, uniqueToChain: 0, lastActivity: "", degraded: false,
    }),
  };
  window.go = { main: { App: base as unknown as AppBindings } } as typeof window.go;
}

async function openPanel(spies: Spies, batchScript?: (dirs: string[], force: boolean) => Promise<PreRepairBatchCleanupView>) {
  stubBridge(spies, batchScript);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <RecoveryCopiesSection />
        </ToastProvider>
      </LocaleProvider>,
    );
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  await act(async () => { query("[data-pre-repair-toggle]")?.click(); });
  await flush();
  return root;
}

console.log("\npre-repair force delete chain (tl1834 item 4)");

// ① 单删强制链：拒绝 → 弹窗 → 确认强制 → force=true 重调 → 成功回执。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [] };
  const root = await openPanel(spies);
  await act(async () => { (query("[data-pre-repair-delete]") as HTMLButtonElement | null)?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); }); // 第一段：确认删除
  await flush();
  ok(spies.deleteCalls.length === 1 && spies.deleteCalls[0][1] === false, "① the first attempt calls the bridge with force=false");
  ok(bodyText().includes("Force delete pre-repair backups?"), "① the force dialog opens after the health-gate refusal");
  ok(bodyText().includes(UNHEALTHY_DETAIL), "① the dialog carries the backend refusal detail");
  ok(bodyText().includes("unhealthy-owner protection gate"), "① the dialog states what force bypasses");
  ok(bodyText().includes("restorable from there"), "① the dialog keeps the recycle-bin semantics");
  await act(async () => { dialogConfirm()?.click(); }); // 第二段：确认强制
  await flush();
  ok(spies.deleteCalls.length === 2 && spies.deleteCalls[1][1] === true, "① confirming force recalls the bridge with force=true");
  ok(toastText().includes("backups sent to the system recycle bin"), "① the success receipt rides the normal trashed toast");
  await act(async () => root.unmount());
}

// ② 拒绝强制：桥只调一次（force=false），错误落错误面（诚实报告不丢）。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [] };
  const root = await openPanel(spies);
  await act(async () => { (query("[data-pre-repair-delete]") as HTMLButtonElement | null)?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); }); // 第一段：确认删除
  await flush();
  // 第二段：拒绝强制——cancel 是弹窗动作条的首个按钮。
  const actions = document.querySelector<HTMLElement>(".reasonix-confirm-dialog__actions");
  const cancel = actions?.querySelectorAll<HTMLButtonElement>("button")[0];
  ok((cancel?.textContent ?? "") === "Cancel", "② the force dialog offers a cancel exit");
  if (cancel) { await act(async () => { cancel.click(); }); await flush(); }
  ok(spies.deleteCalls.length === 1, "② declining force leaves the bridge called once");
  ok(bodyText().includes("deleting the pre-repair backup failed"), "② the plain refusal still lands in the error toast");
  ok(bodyText().includes("owner session is not healthy"), "② the backend reason rides along");
  await act(async () => root.unmount());
}

// ③ 物理失败不弹强制框：trash-failed 直接落错误面。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [] };
  spies.deleteScript = async () => {
    throw new Error("staged delete failed, the backup was moved back: shell refused");
  };
  const root = await openPanel(spies);
  await act(async () => { (query("[data-pre-repair-delete]") as HTMLButtonElement | null)?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(spies.deleteCalls.length === 1, "③ a physical failure calls the bridge once");
  ok(!bodyText().includes("Force delete pre-repair backups?"), "③ no force dialog for a physical failure (no fake next step)");
  ok(bodyText().includes("shell refused"), "③ the physical failure is reported honestly");
  await act(async () => root.unmount());
}

// ④ 批量强制链：失败清单弹窗 → 确认 → 对失败项 force=true 重调 → 回执。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [] };
  const root = await openPanel(spies, async (dirs) => ({
    trashed: 1, trashedBytes: 512, staged: 0, failed: 1,
    failures: [{ dir: dirs[0], label: "sick conversation", reason: "owner-unhealthy", detail: UNHEALTHY_DETAIL }],
  }));
  await act(async () => { (query("[data-pre-repair-clean-all]") as HTMLButtonElement | null)?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); }); // 第一段：确认批量清理
  await flush();
  ok(spies.batchCalls.length === 1 && spies.batchCalls[0][1] === false, "④ the first batch runs with force=false");
  ok(bodyText().includes("Deleted 1 item(s) this round; 1 failed"), "④ the dialog states what was deleted and what failed");
  ok(bodyText().includes("sick conversation"), "④ the failed item is listed by label");
  await act(async () => { dialogConfirm()?.click(); }); // 第二段：确认强制
  await flush();
  ok(spies.batchCalls.length === 2 && spies.batchCalls[1][1] === true, "④ confirming force recalls the batch bridge with force=true");
  ok(spies.batchCalls[1][0].length === 1 && spies.batchCalls[1][0][0] === BACKUPS[0].dir, "④ the force round targets exactly the failed dir");
  ok(toastText().includes("Force-deleted 1 item(s)"), "④ the force receipt toast fires");
  await act(async () => root.unmount());
}

// ⑤ 强制轮仍失败：逐条落面板错误区 + 诚实 toast（不静默吞）。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [] };
  const root = await openPanel(spies, async (dirs, force) =>
    force
      ? { trashed: 0, trashedBytes: 0, staged: 0, failed: 1, failures: [{ dir: dirs[0], label: "sick conversation", reason: "trash-failed", detail: "the recycle bin refused" }] }
      : { trashed: 0, trashedBytes: 0, staged: 0, failed: 1, failures: [{ dir: dirs[0], label: "sick conversation", reason: "owner-unhealthy", detail: UNHEALTHY_DETAIL }] },
  );
  await act(async () => { (query("[data-pre-repair-clean-all]") as HTMLButtonElement | null)?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  // error 档 toast 走顶部居中容器（tl1464 展示路由），不进右下 #rt-notice-stack
  // ——诚实 toast 断言走 bodyText（error/warn 档的既有断言同款）。
  ok(bodyText().includes("Force delete still failed on 1 item(s)"), "⑤ the still-failed toast fires honestly");
  ok(bodyText().includes("sick conversation: the recycle bin refused"), "⑤ the force-round failure lands in the panel errors (never silent)");
  await act(async () => root.unmount());
}

// ⑥ 批量拒绝强制：桥只调一次；owner-unhealthy 警示区照常（既有语义不变）。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [] };
  const root = await openPanel(spies);
  await act(async () => { (query("[data-pre-repair-clean-all]") as HTMLButtonElement | null)?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  const actions = document.querySelector<HTMLElement>(".reasonix-confirm-dialog__actions");
  const cancel = actions?.querySelectorAll<HTMLButtonElement>("button")[0];
  if (cancel) { await act(async () => { cancel.click(); }); await flush(); }
  ok(spies.batchCalls.length === 1, "⑥ declining force leaves the batch called once");
  ok(bodyText().includes("not cleaned (the owning session is unhealthy"), "⑥ the owner-unhealthy refusal banner still renders (existing semantics)");
  await act(async () => root.unmount());
}

console.log(`\ntl1834 pre-repair force delete: ${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
