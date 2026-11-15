// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/pre-repair-cleanup-tl1603.test.tsx
//
// tl1603: 版本与副本面板「改写前备份」分区的清理能力（在 tl1572 只读面之上）。
// 验收钉五组：
//  ① 单删（可删态）：deletable 行有删除钮 → 确认弹窗言明回收站语义 → 确认
//     后按 dir 调 DeletePreRepairBackup → 重取列表（扫描刷新）；
//  ② 拒删提示：blockReason=sole-copy 的行删除钮禁用、行内落原因文案（不藏
//     hover），点击零桥调用；批量确认框对仅存副本有 kept 注记；
//  ③ 批量清理：勾选 → 清理所所选按勾选集调 DeletePreRepairBackups；清理全组
//     按全组调；回执 failures 逐条落面板（拒绝可见、不静默）；
//  ④ tl1572 只读面零回退：查看钮照常（main==chain 双参）、全程零
//     SetActiveSessionVersion（adopt 不许出现）、枚举失败不拖垮主体；
//  ⑤ 三语与桥申报：新增清理键三语齐全；AppBindings 声明两个新桥；
//     组件不出现裸 os 删除语义（回收站文案钉）。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { AppBindings, PreRepairBackupView, PreRepairBatchCleanupView } from "../lib/bridge";

const testDir = dirname(fileURLToPath(import.meta.url));

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

function queryAll(selector: string): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>(selector));
}

function bodyText(): string {
  return document.body.textContent || "";
}

const BACKUPS: PreRepairBackupView[] = [
  {
    dir: "/sessions/live.pre-repair-20261110T010000",
    transcript: "/sessions/live.pre-repair-20261110T010000/live.jsonl",
    ownerPath: "/sessions/live.jsonl",
    label: "dieted conversation",
    stamp: "20261110T010000",
    bytes: 1048576,
    files: 3,
    modifiedAt: "2026-11-10T01:00:00Z",
    deletable: true,
  },
  {
    dir: "/sessions/archived.pre-repair-20261109T010000",
    transcript: "/sessions/archived.pre-repair-20261109T010000/archived.jsonl",
    ownerPath: "",
    label: "archived conversation",
    stamp: "20261109T010000",
    bytes: 2048,
    files: 2,
    modifiedAt: "2026-11-09T01:00:00Z",
    deletable: true,
  },
  {
    dir: "/sessions/lonely.pre-repair-20261105T020006",
    transcript: "/sessions/lonely.pre-repair-20261105T020006/lonely.jsonl",
    ownerPath: "",
    label: "lonely conversation",
    stamp: "20261105T020006",
    bytes: 512,
    files: 2,
    modifiedAt: "2026-11-05T02:00:06Z",
    deletable: false,
    blockReason: "sole-copy",
  },
];

type Spies = {
  deleteCalls: string[];
  batchCalls: string[][];
  listCalls: number;
  viewCalls: [string, string][];
  activateCalls: string[];
};

function batchRun(overrides: Partial<PreRepairBatchCleanupView> = {}): PreRepairBatchCleanupView {
  return { trashed: 0, trashedBytes: 0, failed: 0, failures: [], ...overrides };
}

function stubBridge(spies: Spies, overrides: Partial<AppBindings> = {}, backups: PreRepairBackupView[] | Error = BACKUPS) {
  const base = {
    ListRecoveryCopyGroups: async () => [],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => ({
      mainPath: "", mainLabel: "", chains: [], longestPath: "", newestPath: "", longestHead: "", newestHead: "",
    }),
    ListSessionRepairRecordsFor: async () => [],
    ListPreRepairBackups: async () => {
      spies.listCalls += 1;
      if (backups instanceof Error) throw backups;
      return backups;
    },
    DeletePreRepairBackup: async (dir: string) => {
      spies.deleteCalls.push(dir);
    },
    DeletePreRepairBackups: async (dirs: string[]) => {
      spies.batchCalls.push(dirs);
      return batchRun({ trashed: dirs.length, trashedBytes: 4096 });
    },
    PreviewRecoveryChain: async (mainPath: string, chainPath: string) => {
      spies.viewCalls.push([mainPath, chainPath]);
      return {
        path: chainPath,
        isMain: mainPath === chainPath,
        messageCount: 7,
        turns: 2,
        firstUserText: "hello before the diet",
        tailLines: ["[user]: hello before the diet"],
        sharedWithMain: 7,
        uniqueToChain: 0,
        lastActivity: "",
        degraded: false,
      };
    },
    SetActiveSessionVersion: async (req: { path: string }) => {
      spies.activateCalls.push(req.path);
    },
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openPanel(spies: Spies, overrides: Partial<AppBindings> = {}, backups: PreRepairBackupView[] | Error = BACKUPS) {
  stubBridge(spies, overrides, backups);
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

/** dialogConfirm clicks the confirm dialog's confirm button — the row delete
 *  buttons share the "Delete" label, so the lookup must be scoped to the dialog
 *  (its actions bar is the only place the confirm/cancel pair lives). */
function dialogConfirm(): HTMLButtonElement | undefined {
  const actions = document.querySelector<HTMLElement>(".reasonix-confirm-dialog__actions");
  const buttons = actions?.querySelectorAll<HTMLButtonElement>("button");
  return buttons && buttons.length > 0 ? buttons[buttons.length - 1] : undefined;
}

/** toastText reads the unified toast stack (real ToastProvider mount): the
 *  cleanup receipts ride toasts, so the receipt wording is asserted there. */
function toastText(): string {
  return document.getElementById("rt-notice-stack")?.textContent || "";
}

console.log("\npre-repair backups cleanup (tl1603)");

// ① 单删：可删行 → 确认弹窗 → DeletePreRepairBackup(dir) → 列表重取。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies);
  const deleteButtons = queryAll("[data-pre-repair-delete]");
  ok(deleteButtons.length === 3, "① every backup row offers a delete button");
  const blockedButton = queryAll("[data-pre-repair-delete]").find((b) => (b as HTMLButtonElement).disabled);
  ok(Boolean(blockedButton), "① the sole-copy row's delete button is disabled");
  const deletableButton = deleteButtons.find((b) => !(b as HTMLButtonElement).disabled);
  await act(async () => { deletableButton?.click(); });
  await flush();
  ok(bodyText().includes("Delete this pre-repair backup?"), "① the confirm dialog names the action");
  ok(bodyText().includes("system recycle bin"), "① the confirm says the deletion goes to the recycle bin");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    spies.deleteCalls.length === 1 && spies.deleteCalls[0] === "/sessions/live.pre-repair-20261110T010000",
    `① confirm calls the delete bridge with the row's dir (${JSON.stringify(spies.deleteCalls)})`,
  );
  ok(spies.listCalls >= 2, "① the list rescan runs after the delete (扫描刷新)");
  ok(spies.activateCalls.length === 0, "① deleting never adopts (zero SetActiveSessionVersion)");
  await act(async () => root.unmount());
}

// ①b 拒删点击不发生：disabled 按钮点击零调用（拒绝在 UI 层就不可达）。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies);
  ok(
    bodyText().includes("sole surviving copy"),
    "①b the refusal reason is visible inline on the row (拒删提示不藏 hover)",
  );
  await act(async () => root.unmount());
}

// ② 单删失败：桥拒绝时原因落面板（拒删提示的第二形态：后端权威闸拒绝）。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies, {
    DeletePreRepairBackup: async () => {
      throw new Error("owner session is not healthy (projection_overflow)");
    },
  });
  const deletableButton = queryAll("[data-pre-repair-delete]").find((b) => !(b as HTMLButtonElement).disabled);
  await act(async () => { deletableButton?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    bodyText().includes("owner session is not healthy (projection_overflow)"),
    "② a refused delete surfaces the backend reason in the panel",
  );
  await act(async () => root.unmount());
}

// ③ 批量-所选：勾选两条 → 清理所所选 → 按勾选集调批量桥。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies);
  const checks = queryAll("[data-pre-repair-check]");
  ok(checks.length === 3, "③ every row offers a checkbox");
  ok((query("[data-pre-repair-clean-selected]") as HTMLButtonElement | undefined)?.disabled === true, "③ clean-selected starts disabled");
  await act(async () => { (checks[0] as HTMLInputElement).click(); });
  await act(async () => { (checks[2] as HTMLInputElement).click(); });
  await flush();
  const selectedButton = query("[data-pre-repair-clean-selected]") as HTMLButtonElement;
  ok(selectedButton?.disabled === false, "③ selecting enables clean-selected");
  ok((selectedButton.textContent ?? "").includes("2"), "③ the button carries the selection count");
  await act(async () => { selectedButton.click(); });
  await flush();
  ok(bodyText().includes("Clean pre-repair backups"), "③ the batch confirm dialog opens");
  ok(bodyText().includes("sole surviving copy and will be kept"), "③ the batch confirm names the kept sole-copies");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    spies.batchCalls.length === 1 &&
      spies.batchCalls[0].length === 2 &&
      spies.batchCalls[0][0] === "/sessions/live.pre-repair-20261110T010000" &&
      spies.batchCalls[0][1] === "/sessions/lonely.pre-repair-20261105T020006",
    `③ clean-selected passes exactly the checked dirs (${JSON.stringify(spies.batchCalls)})`,
  );
  await act(async () => root.unmount());
}

// ③b 批量-全组：按全组清单调批量桥。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies);
  await act(async () => { query("[data-pre-repair-clean-all]")?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    spies.batchCalls.length === 1 && spies.batchCalls[0].length === 3,
    "③b clean-all hands the whole group to the backend gate",
  );
  await act(async () => root.unmount());
}

// ③c 批量回执：拒绝行逐条落面板（不静默）。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies, {
    DeletePreRepairBackups: async () =>
      batchRun({
        trashed: 1,
        trashedBytes: 1024,
        failed: 1,
        failures: [{ dir: "/sessions/lonely.pre-repair-20261105T020006", label: "lonely conversation", reason: "sole-copy" }],
      }),
  });
  await act(async () => { query("[data-pre-repair-clean-all]")?.click(); });
  await flush();
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    toastText().includes("1 backups sent to the recycle bin"),
    "③c the receipt toast reports what went to the bin (counting trashed only)",
  );
  ok(
    bodyText().includes("lonely conversation: sole surviving copy"),
    "③c refusals land in the panel errors with label+reason (never silent)",
  );
  await act(async () => root.unmount());
}

// ④ tl1572 只读面零回退。
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies);
  const viewButtons = queryAll("[data-pre-repair-view]");
  ok(viewButtons.length === 3, "④ the read-only view buttons survive (tl1572 面)");
  await act(async () => { viewButtons[0]?.click(); });
  await flush();
  ok(
    spies.viewCalls.length === 1 && spies.viewCalls[0][0] === spies.viewCalls[0][1],
    "④ view is still the read-only main==chain preview",
  );
  await act(async () => { buttonByText("Close")?.click(); });
  await flush();
  ok(spies.activateCalls.length === 0, "④ still zero adopt calls anywhere (tl1572 钉)");
  await act(async () => root.unmount());
}
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  const root = await openPanel(spies, {}, new Error("walk refused"));
  ok(!bodyText().includes("Settings failed to load"), "④ an enumeration failure still does not fail the panel");
  ok(!query("[data-pre-repair-clean-all]"), "④ no cleanup buttons without a listing (old harness Partial stubs stay inert)");
  await act(async () => root.unmount());
}
{
  const spies: Spies = { deleteCalls: [], batchCalls: [], listCalls: 0, viewCalls: [], activateCalls: [] };
  // 旧 harness Partial stub（无清理桥）形态：分区照常渲染、清理面不出现。
  const root = await openPanel(spies, {
    DeletePreRepairBackup: undefined,
    DeletePreRepairBackups: undefined,
  } as unknown as Partial<AppBindings>);
  ok(queryAll("[data-pre-repair-delete]").length === 0, "④ rows render without cleanup buttons when the bridge lacks them");
  await act(async () => root.unmount());
}

// ⑤ 三语与桥申报。
{
  const locales: Record<string, string> = {
    en: readFileSync(resolve(testDir, "../locales/en.ts"), "utf8"),
    zh: readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8"),
    "zh-TW": readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8"),
  };
  const keys = [
    "Delete", "DeleteHint", "DeleteTitle", "DeleteFailed", "RecycleNote", "BlockedSoleCopy",
    "CleanSelected", "CleanAll", "CleanTitle", "CleanLead", "CleanKept", "CleanConfirm",
    "CleanDone", "CleanRefused",
  ];
  for (const key of keys) {
    ok(
      Object.values(locales).every((dict) => dict.includes(`"settings.recoveryCopiesPreRepair${key}"`)),
      `⑤ all three locales carry settings.recoveryCopiesPreRepair${key}`,
    );
  }
  const bridgeSource = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  ok(bridgeSource.includes("DeletePreRepairBackup(dir: string): Promise<void>;"), "⑤ AppBindings declares DeletePreRepairBackup");
  ok(bridgeSource.includes("DeletePreRepairBackups(dirs: string[]): Promise<PreRepairBatchCleanupView>;"), "⑤ AppBindings declares DeletePreRepairBackups");
  const sectionSource = readFileSync(resolve(testDir, "../components/RecoveryCopiesSection.tsx"), "utf8");
  ok(
    !sectionSource.includes("DeleteWorkspaceEntriesPermanent") &&
      sectionSource.includes("app.DeletePreRepairBackups("),
    "⑤ cleanup goes through the recycle-bin bridge only (no permanent-delete wiring)",
  );
  ok(
    locales.en.includes("system recycle bin") && locales.zh.includes("系统回收站") && locales["zh-TW"].includes("系統回收桶"),
    "⑤ the recycle-bin semantics is stated in all three locales",
  );
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
