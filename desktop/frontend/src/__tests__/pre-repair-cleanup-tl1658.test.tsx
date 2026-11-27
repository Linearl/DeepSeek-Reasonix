// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/pre-repair-cleanup-tl1658.test.tsx
//
// tl1658 对话版本管理清理面双现场修复的判例（叠在 tl1603/tl1646/tl1647 之上）：
//  现场① 批量清理反馈——「清理全组」在 181 项/3.4GB 量级下此前只有「按钮灰
//         死」（且只有清理所选钮变「…」），无进度、无完成汇总。修复钉：
//   ①a 在途可见：全组钮在途变「…」+ 进度行（data-pre-repair-progress）随
//       pre-repair-cleanup:progress 事件渲染「已处理 n/N」；
//   ①b 完成 toast 三态与实际行为一一对应：全成功=已送 N 份；部分成功=成功
//       N·失败 M（tl1647 聚合口径）；全被拒=被拒 N；staged>0 附还原位置披露。
//  现场② 长路径死锁——树内 >MAX_PATH(260) 的备份 Shell 收不进桶，此前单删/
//         批量两条路全堵（保守拒绝变成死锁）。后端两段出路的前端钉：
//   ②a permanentOnly 裁决 → 第二段明示弹窗（文案含「永久删除」+「不可恢复」
//       +原始细节），取消=零调用，确认=才调 DeletePreRepairBackupPermanent；
//   ②b viaStaging 裁决 → 成功 toast 附「还原位置回到中转目录」披露（三态文
//       案与实际行为一致）。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { AppBindings, PreRepairBackupView, PreRepairBatchCleanupView, PreRepairDeleteView } from "../lib/bridge";

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
  },
  {
    dir: "/sessions/deep.pre-repair-20261109T010000",
    transcript: "/sessions/deep.pre-repair-20261109T010000/deep.jsonl",
    ownerPath: "",
    label: "over-max-path conversation",
    stamp: "20261109T010000",
    bytes: 2048,
    files: 2,
    modifiedAt: "2026-11-09T01:00:00Z",
  },
];

type Spies = {
  deleteCalls: string[];
  permanentCalls: string[];
  batchCalls: string[][];
  listCalls: number;
};

function batchRun(overrides: Partial<PreRepairBatchCleanupView> = {}): PreRepairBatchCleanupView {
  return { trashed: 0, trashedBytes: 0, staged: 0, failed: 0, failures: [], ...overrides };
}

/** installRuntimeEvents 装一个可手动派发的事件缝（window.runtime 先例：
 *  DagMigrationSection 的进度事件同形态），返回 (payload) => 派发。 */
function installRuntimeEvents(): (payload: Record<string, unknown>) => void {
  let listener: ((...args: unknown[]) => void) | null = null;
  (globalThis.window as unknown as { runtime?: unknown }).runtime = {
    EventsOn: (_name: string, cb: (...args: unknown[]) => void) => {
      listener = cb;
      return () => {
        listener = null;
      };
    },
  };
  return (payload: Record<string, unknown>) => {
    listener?.(payload);
  };
}

function stubBridge(spies: Spies, overrides: Partial<AppBindings> = {}) {
  const base = {
    ListRecoveryCopyGroups: async () => [],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => ({
      mainPath: "", mainLabel: "", chains: [], longestPath: "", newestPath: "", longestHead: "", newestHead: "",
    }),
    ListSessionRepairRecordsFor: async () => [],
    ListPreRepairBackups: async () => {
      spies.listCalls += 1;
      return BACKUPS;
    },
    DeletePreRepairBackup: async (dir: string): Promise<PreRepairDeleteView> => {
      spies.deleteCalls.push(dir);
      return { trashed: true };
    },
    DeletePreRepairBackupPermanent: async (dir: string) => {
      spies.permanentCalls.push(dir);
    },
    DeletePreRepairBackups: async (dirs: string[]) => {
      spies.batchCalls.push(dirs);
      return batchRun({ trashed: dirs.length, trashedBytes: 4096 });
    },
    PreviewRecoveryChain: async (_mainPath: string, chainPath: string) => ({
      path: chainPath,
      isMain: true,
      messageCount: 7,
      turns: 2,
      firstUserText: "hello",
      tailLines: ["[user]: hello"],
      sharedWithMain: 7,
      uniqueToChain: 0,
      lastActivity: "",
      degraded: false,
    }),
    SetActiveSessionVersion: async () => {},
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openPanel(spies: Spies, overrides: Partial<AppBindings> = {}) {
  stubBridge(spies, overrides);
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
  await act(async () => {
    Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
      .find((b) => b.textContent?.includes("Manage session copies"))
      ?.click();
  });
  await flush();
  await act(async () => {
    query("[data-pre-repair-toggle]")?.click();
  });
  await flush();
  return root;
}

function dialogConfirm(): HTMLButtonElement | undefined {
  const actions = document.querySelector<HTMLElement>(".reasonix-confirm-dialog__actions");
  const buttons = actions?.querySelectorAll<HTMLButtonElement>("button");
  return buttons && buttons.length > 0 ? buttons[buttons.length - 1] : undefined;
}

function toastText(): string {
  return document.getElementById("rt-notice-stack")?.textContent || "";
}

console.log("\npre-repair cleanup feedback + long-path verdict (tl1658)");

// ①a 批量在途可见：全组钮变「…」+ 进度行随事件渲染「1/2 · label」。
// 批量桥挂起（真实形态=分钟级阻塞调用），期间事件逐条派发。
{
  const spies: Spies = { deleteCalls: [], permanentCalls: [], batchCalls: [], listCalls: 0 };
  const emit = installRuntimeEvents();
  let resolveBatch: (() => void) | null = null;
  const root = await openPanel(spies, {
    DeletePreRepairBackups: (dirs: string[]) =>
      new Promise<PreRepairBatchCleanupView>((resolve) => {
        resolveBatch = () => resolve(batchRun({ trashed: dirs.length, trashedBytes: 4096 }));
        spies.batchCalls.push(dirs);
      }),
  });
  await act(async () => {
    query("[data-pre-repair-clean-all]")?.click();
  });
  await flush();
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  ok(
    spies.batchCalls.length === 1 && spies.listCalls === 1 && resolveBatch !== null,
    "①a the batch is in flight (one bridge call, no reload yet)",
  );
  const allButton = query("[data-pre-repair-clean-all]") as HTMLButtonElement;
  ok(allButton?.disabled === true && allButton.textContent === "…", "①a the clean-all button is locked and shows the busy marker while running");
  ok(
    (query("[data-pre-repair-clean-selected]") as HTMLButtonElement | undefined)?.disabled === true,
    "①a the clean-selected button stays locked alongside",
  );
  await act(async () => {
    emit({ index: 1, total: 2, label: "dieted conversation", status: "processing" });
  });
  const progress = query("[data-pre-repair-progress]");
  ok(progress !== undefined, "①a the progress line renders while the batch runs");
  ok(
    (progress?.textContent ?? "").includes("1/2") && (progress?.textContent ?? "").includes("dieted conversation"),
    `①a the progress line carries n/N and the current label (${progress?.textContent ?? ""})`,
  );
  await act(async () => {
    emit({ index: 2, total: 2, label: "over-max-path conversation", status: "trashed" });
  });
  ok(
    (query("[data-pre-repair-progress]")?.textContent ?? "").includes("2/2"),
    "①a the progress line advances with each event",
  );
  await act(async () => {
    resolveBatch?.();
  });
  await flush();
  ok(!query("[data-pre-repair-progress]"), "①a the progress line is gone once the batch completes");
  await act(async () => root.unmount());
  (globalThis.window as unknown as { runtime?: unknown }).runtime = undefined;
}

// ①b-1 全成功 toast（含 staged 披露当 staged>0）。
{
  const spies: Spies = { deleteCalls: [], permanentCalls: [], batchCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    DeletePreRepairBackups: async () => batchRun({ trashed: 2, trashedBytes: 2048, staged: 1 }),
  });
  await act(async () => {
    query("[data-pre-repair-clean-all]")?.click();
  });
  await flush();
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  ok(
    toastText().includes("2 backups sent to the recycle bin") && toastText().includes("staging dir"),
    `①b-1 the all-ok toast counts the trashed and discloses the staged restore location (${toastText()})`,
  );
  ok(!query("[data-pre-repair-progress]"), "①b-1 the progress line is cleared once the batch completes");
  await act(async () => root.unmount());
}

// ①b-2 部分成功 toast：成功 N·失败 M 同条聚合（tl1647 口径；此前只报成功数）。
{
  const spies: Spies = { deleteCalls: [], permanentCalls: [], batchCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    DeletePreRepairBackups: async () =>
      batchRun({
        trashed: 1,
        trashedBytes: 1024,
        failed: 1,
        failures: [{ dir: BACKUPS[1].dir, label: "over-max-path conversation", reason: "owner-unhealthy", detail: "owner session is not healthy (projection_overflow)" }],
      }),
  });
  await act(async () => {
    query("[data-pre-repair-clean-all]")?.click();
  });
  await flush();
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  ok(
    toastText().includes("1 backups sent to the recycle bin") && toastText().includes("1 refused"),
    `①b-2 the partial toast aggregates success and refusal counts in one line (${toastText()})`,
  );
  ok(
    bodyText().includes("over-max-path conversation: owner session is not healthy"),
    "①b-2 the refusal detail still lands in the panel (never silent)",
  );
  await act(async () => root.unmount());
}

// ①b-3 全被拒 toast（既有语义零回退）。
{
  const spies: Spies = { deleteCalls: [], permanentCalls: [], batchCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    DeletePreRepairBackups: async () =>
      batchRun({
        failed: 2,
        failures: [
          { dir: BACKUPS[0].dir, label: "dieted conversation", reason: "owner-unhealthy", detail: "owner session is not healthy" },
          { dir: BACKUPS[1].dir, label: "over-max-path conversation", reason: "owner-unhealthy", detail: "owner session is not healthy" },
        ],
      }),
  });
  await act(async () => {
    query("[data-pre-repair-clean-all]")?.click();
  });
  await flush();
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  // error 档 toast 走组件内 .toast-container（info 档才进右下 #rt-notice-stack
  // portal，toast.tsx 的分档路由）——全被拒是 error 档，断言读对容器。
  const errorToast = document.querySelector(".toast-container")?.textContent || "";
  ok(
    errorToast.includes("2 backups were refused"),
    `①b-3 the all-refused toast stays the refused-count line (${errorToast})`,
  );
  await act(async () => root.unmount());
}

// ②a permanentOnly 兜底：第二段明示弹窗（含「永久删除」+「不可恢复」+细节），
// 取消=零调用；确认=才调永久桥+完成 toast。
{
  const spies: Spies = { deleteCalls: [], permanentCalls: [], batchCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    DeletePreRepairBackup: async (dir: string) => {
      spies.deleteCalls.push(dir);
      return { trashed: false, permanentOnly: true, detail: "items exceed the recycle bin's maximum path length (260 characters): 285" };
    },
  });
  const deleteButtons = queryAll("[data-pre-repair-delete]") as HTMLButtonElement[];
  ok(deleteButtons.length === 2, "②a the fixture rows render with delete buttons");
  await act(async () => {
    deleteButtons[1]?.click();
  });
  await flush();
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  ok(bodyText().includes("Permanently delete this pre-repair backup?"), "②a the second dialog asks for the permanent delete");
  ok(
    bodyText().includes("permanent, unrecoverable delete"),
    "②a the dialog states the deletion is permanent and unrecoverable",
  );
  ok(
    bodyText().includes("exceed the recycle bin's maximum path length"),
    "②a the dialog carries the backend verdict detail (为什么只能永久删)",
  );
  ok(spies.permanentCalls.length === 0, "②a nothing is deleted before the explicit confirm");

  // 确认路径：点第二段弹窗的确认（取消路径见 ②a-cancel 用例）。
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  ok(
    spies.permanentCalls.length === 1 && spies.permanentCalls[0] === BACKUPS[1].dir,
    `②a confirming calls the permanent bridge with the row's dir (${JSON.stringify(spies.permanentCalls)})`,
  );
  ok(
    toastText().includes("Permanently deleted"),
    "②a the done toast states the deletion was permanent (not in the recycle bin)",
  );
  ok(
    !toastText().includes("sent to the recycle bin"),
    "②a the permanent path must NOT claim recycle-bin semantics (三态文案与实际行为一致)",
  );
  await act(async () => root.unmount());
}

// ②a-cancel 取消路径：明示弹窗点取消=零调用、无 toast。
{
  const spies: Spies = { deleteCalls: [], permanentCalls: [], batchCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    DeletePreRepairBackup: async (dir: string) => {
      spies.deleteCalls.push(dir);
      return { trashed: false, permanentOnly: true, detail: "refused" };
    },
  });
  await act(async () => {
    (queryAll("[data-pre-repair-delete]")[1] as HTMLButtonElement)?.click();
  });
  await flush();
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  const actions = document.querySelector<HTMLElement>(".reasonix-confirm-dialog__actions");
  const buttons = actions?.querySelectorAll<HTMLButtonElement>("button");
  const cancel = buttons && buttons.length > 0 ? buttons[0] : undefined;
  await act(async () => {
    cancel?.click();
  });
  await flush();
  ok(spies.permanentCalls.length === 0, "②a-cancel cancelling the explicit dialog deletes nothing");
  ok(!toastText().includes("Permanently deleted"), "②a-cancel no permanent-done toast on cancel");
  await act(async () => root.unmount());
}

// ②b viaStaging 裁决：成功 toast 附还原位置披露（文案与实际行为一致）。
{
  const spies: Spies = { deleteCalls: [], permanentCalls: [], batchCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    DeletePreRepairBackup: async (dir: string) => {
      spies.deleteCalls.push(dir);
      return { trashed: true, viaStaging: true };
    },
  });
  await act(async () => {
    (queryAll("[data-pre-repair-delete]")[1] as HTMLButtonElement)?.click();
  });
  await flush();
  await act(async () => {
    dialogConfirm()?.click();
  });
  await flush();
  ok(
    spies.deleteCalls.length === 1 && spies.permanentCalls.length === 0,
    "②b a staged delete never touches the permanent bridge",
  );
  ok(
    toastText().includes("1 backups sent to the recycle bin") && toastText().includes("staging dir"),
    `②b the staged delete toast keeps the recycle-bin claim and discloses the transit restore (${toastText()})`,
  );
  await act(async () => root.unmount());
}

// ⑥ 契约申报：三语新键齐全 + 桥声明 + 进度事件名接线。
{
  const locales: Record<string, string> = {
    en: readFileSync(resolve(testDir, "../locales/en.ts"), "utf8"),
    zh: readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8"),
    "zh-TW": readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8"),
  };
  const keys = [
    "CleanPartial", "CleanProgress", "StagedNote",
    "PermanentTitle", "PermanentWarn", "PermanentConfirm", "PermanentDone",
  ];
  for (const key of keys) {
    ok(
      Object.values(locales).every((dict) => dict.includes(`"settings.recoveryCopiesPreRepair${key}"`)),
      `⑥ all three locales carry settings.recoveryCopiesPreRepair${key}`,
    );
  }
  ok(
    Object.values(locales).every((dict) => dict.includes('"settings.recoveryCopiesPreRepairPermanentWarn"')),
    "⑥ the permanent warning key exists in every locale",
  );
  const zhLine = locales.zh.split("\n").find((l) => l.includes('"settings.recoveryCopiesPreRepairPermanentWarn"')) ?? "";
  const twLine = locales["zh-TW"].split("\n").find((l) => l.includes('"settings.recoveryCopiesPreRepairPermanentWarn"')) ?? "";
  ok(zhLine.includes("不可恢复"), "⑥ zh permanent warning carries 不可恢复");
  ok(twLine.includes("不可恢復"), "⑥ zh-TW permanent warning carries 不可恢復");
  const bridgeSource = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  ok(bridgeSource.includes("DeletePreRepairBackupPermanent(dir: string, force: boolean): Promise<void>;"), "⑥ AppBindings declares DeletePreRepairBackupPermanent");
  ok(bridgeSource.includes("permanentOnly?: boolean;"), "⑥ PreRepairDeleteView carries the permanentOnly verdict");
  ok(bridgeSource.includes('"permanent-only"'), "⑥ PreRepairCleanupFailureReason carries the permanent-only token");
  const sectionSource = readFileSync(resolve(testDir, "../components/RecoveryCopiesSection.tsx"), "utf8");
  ok(sectionSource.includes('"pre-repair-cleanup:progress"'), "⑥ the section subscribes the tl1658 progress event");
  ok(sectionSource.includes("data-pre-repair-progress"), "⑥ the progress line has a stable hook for the panel");
  ok(
    locales.zh.includes("正在清理") && locales.en.includes("Cleaning"),
    "⑥ the progress wording exists in zh and en locales",
  );
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
