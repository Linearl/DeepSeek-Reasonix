// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1674b-pre-repair-manual-splice.test.tsx
//
// tl1674b 阶段二：pre-repair 分叉**手动拼接入口**的前端验收钉。
//  ① 入口可达性：活宿主未登记行给「拼接登记」钮；已登记/归档/孤儿行不给
//     （typeof 守卫——旧 harness Partial stub 不渲染）；
//  ② 链预览：确认弹窗分段结构（主线 N 条/公共前缀 k/旧链丢失段 m/主线续段）
//     + HEAD 语义披露 + 元信息承诺；确认后调 MergePreRepairSplice（dir），
//     回执 toast 带来源与读数，列表重取；
//  ③ 无法拼接形态（covered）：弹窗只披露人话原因，确认键=关闭——零登记调用；
//  ④ 健康闸提示替代硬拦：不健康宿主的预览披露 codes，确认仍可登记；
//  ⑤ 行内徽标按登记来源分档（手动/自愈/历史素形态）；
//  ⑥ 契约钉：AppBindings 两桥声明 + PreRepairSplicePreviewView 形态 +
//     三语字典键齐。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { AppBindings, PreRepairBackupView, RecoveryCopyGroupView } from "../lib/bridge";

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

function toastText(): string {
  return document.getElementById("rt-notice-stack")?.textContent || "";
}

// 四行夹具：live=活宿主未登记（拼接钮唯一落点）；splicedManual/splicedAuto/
// splicedLegacy=已登记三档徽标读数；archived/lonely=归档与孤儿（不给钮）。
const BACKUPS: PreRepairBackupView[] = [
  {
    dir: "/sessions/live.pre-repair-20261110T010000",
    transcript: "/sessions/live.pre-repair-20261110T010000/live.jsonl",
    ownerPath: "/sessions/live.jsonl",
    label: "live conversation",
    stamp: "20261110T010000",
    bytes: 4096,
    files: 3,
    modifiedAt: "2026-11-10T01:00:00Z",
  },
  {
    dir: "/sessions/manual.pre-repair-20261109T010000",
    transcript: "/sessions/manual.pre-repair-20261109T010000/manual.jsonl",
    ownerPath: "/sessions/manual.jsonl",
    spliced: true,
    splicedBy: "manual",
    label: "manually spliced conversation",
    stamp: "20261109T010000",
    bytes: 2048,
    files: 2,
    modifiedAt: "2026-11-09T01:00:00Z",
  },
  {
    dir: "/sessions/auto.pre-repair-20261108T010000",
    transcript: "/sessions/auto.pre-repair-20261108T010000/auto.jsonl",
    ownerPath: "/sessions/auto.jsonl",
    spliced: true,
    splicedBy: "auto",
    label: "auto spliced conversation",
    stamp: "20261108T010000",
    bytes: 2048,
    files: 2,
    modifiedAt: "2026-11-08T01:00:00Z",
  },
  {
    dir: "/sessions/legacy.pre-repair-20261107T010000",
    transcript: "/sessions/legacy.pre-repair-20261107T010000/legacy.jsonl",
    ownerPath: "/sessions/legacy.jsonl",
    spliced: true,
    label: "legacy spliced conversation",
    stamp: "20261107T010000",
    bytes: 2048,
    files: 2,
    modifiedAt: "2026-11-07T01:00:00Z",
  },
  {
    dir: "/sessions/archived.pre-repair-20261106T010000",
    transcript: "/sessions/archived.pre-repair-20261106T010000/archived.jsonl",
    ownerPath: "/sessions/archived.jsonl",
    ownerArchived: true,
    label: "archived conversation",
    stamp: "20261106T010000",
    bytes: 512,
    files: 2,
    modifiedAt: "2026-11-06T01:00:00Z",
  },
  {
    dir: "/sessions/lonely.pre-repair-20261105T010000",
    transcript: "/sessions/lonely.pre-repair-20261105T010000/lonely.jsonl",
    ownerPath: "",
    label: "lonely conversation",
    stamp: "20261105T010000",
    bytes: 512,
    files: 2,
    modifiedAt: "2026-11-05T01:00:00Z",
  },
];

const GROUP: RecoveryCopyGroupView = {
  mainPath: "/sessions/live.jsonl",
  mainLabel: "live conversation",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 4,
  mainTurns: 2,
  mainBytes: 4096,
  mainModified: "2026-11-10T01:00:00Z",
  copies: [
    {
      path: "/sessions/live-recovery-0123456789abcdef.jsonl",
      label: "live copy 1",
      bytes: 256,
      modified: "2026-11-10T01:00:00Z",
      messages: 3,
      shared: 1,
      unique: 2,
      scanned: true,
    },
  ],
};

const DIVERGED_PREVIEW = {
  splicable: true,
  kind: "diverged-long-chain",
  sharedPrefix: 2,
  splicePoint: 2,
  segmentStart: 2,
  segmentEnd: 4,
  segmentMessages: 2,
  ownerMessages: 4,
  backupMessages: 4,
  ownerHealthy: true,
  ownerPath: "/sessions/live.jsonl",
  backupDir: BACKUPS[0].dir,
};

type Spies = {
  previewCalls: string[];
  spliceCalls: string[];
  listCalls: number;
};

function stubBridge(spies: Spies, overrides: Partial<AppBindings> = {}) {
  const base = {
    ListRecoveryCopyGroups: async () => [GROUP],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => ({
      mainPath: "", mainLabel: "", chains: [], longestPath: "", newestPath: "", longestHead: "", newestHead: "",
    }),
    ListSessionRepairRecordsFor: async () => [],
    ListPreRepairBackups: async () => {
      spies.listCalls += 1;
      return BACKUPS;
    },
    DeletePreRepairBackup: async () => {},
    DeletePreRepairBackups: async (dirs: string[]) => ({ trashed: dirs.length, trashedBytes: 512, staged: 0, failed: 0, failures: [] }),
    MergePreRepairBackup: async (dir: string) => ({ merged: false, grafted: 0, ownerPath: dir, afterMessages: 0 }),
    PreviewPreRepairSplice: async (dir: string) => {
      spies.previewCalls.push(dir);
      return { ...DIVERGED_PREVIEW };
    },
    MergePreRepairSplice: async (dir: string) => {
      spies.spliceCalls.push(dir);
      return {
        registered: true, kind: "diverged-long-chain", source: "manual",
        splicePoint: 2, segmentMessages: 2, ownerMessages: 4,
        ownerPath: "/sessions/live.jsonl", backupDir: dir,
      };
    },
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
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  await act(async () => { query("[data-pre-repair-toggle]")?.click(); });
  await flush();
  return root;
}

function dialogConfirm(): HTMLButtonElement | undefined {
  const actions = document.querySelector<HTMLElement>(".reasonix-confirm-dialog__actions");
  const buttons = actions?.querySelectorAll<HTMLButtonElement>("button");
  return buttons && buttons.length > 0 ? buttons[buttons.length - 1] : undefined;
}

console.log("\ntl1674b manual splice entry (chain preview / source marker / health notice)");

// ① 入口可达性：拼接钮只落在活宿主未登记行（已登记三档/归档/孤儿全不给）。
{
  const spies: Spies = { previewCalls: [], spliceCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  const spliceButtons = queryAll("[data-pre-repair-splice]");
  ok(spliceButtons.length === 1, `① only the unregistered live-owner row offers the splice entry (got ${spliceButtons.length})`);
  const rows = queryAll("[data-pre-repair-list] > li");
  ok(rows[0]?.querySelector("[data-pre-repair-splice]") != null, "① the live unregistered row carries the button");
  ok(rows[1]?.querySelector("[data-pre-repair-splice]") == null, "① a manually-registered row hides the entry (idempotent)");
  ok(rows[4]?.querySelector("[data-pre-repair-splice]") == null, "① the archived row hides the entry (restore first)");
  ok(rows[5]?.querySelector("[data-pre-repair-splice]") == null, "① the orphan row hides the entry (no main line)");
  // 旧 harness（Partial stub 无两桥）不渲染：全量行零拼接钮。
  await act(async () => root.unmount());
  const root2 = createRoot(installDom());
  stubBridge({ ...spies, previewCalls: [], spliceCalls: [], listCalls: 0 }, {
    PreviewPreRepairSplice: undefined,
    MergePreRepairSplice: undefined,
  } as unknown as Partial<AppBindings>);
  await act(async () => {
    root2.render(
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
  ok(queryAll("[data-pre-repair-splice]").length === 0, "① a harness without the two bridges renders no splice entry (typeof guard)");
  await act(async () => root2.unmount());
}

// ② 链预览+登记：预览弹窗分段结构齐（前缀/丢失段/续段/HEAD 语义/元信息承
//    诺）；确认后按 dir 调登记桥；回执 toast 带 manual 读数；列表重取。
{
  const spies: Spies = { previewCalls: [], spliceCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  const splice = query("[data-pre-repair-splice]") as HTMLButtonElement;
  await act(async () => { splice.click(); });
  await flush();
  ok(spies.previewCalls.length === 1 && spies.previewCalls[0] === BACKUPS[0].dir, "② the preview bridge is called with the row dir first");
  ok(bodyText().includes("Register splice — merge the fork back (metadata)"), "② the dialog names the manual-splice action");
  ok(bodyText().includes("HEAD = the current main"), "② the dialog discloses the HEAD semantics (new chain wins, old chain recorded)");
  ok(bodyText().includes("Main (new-chain HEAD): 4 messages"), "② the preview shows the main-line segment count");
  ok(bodyText().includes("Shared prefix: first 2 messages (identical on both sides)"), "② the preview shows the shared prefix segment");
  ok(bodyText().includes("Old-chain lost segment: 2 messages (backup messages 2–4), recorded right after main message 2"), "② the preview shows the lost old-chain segment with its bounds");
  ok(bodyText().includes("Main continuation: the 2 messages after the splice point stay exactly where they are"), "② the preview shows the untouched main continuation");
  ok(bodyText().includes("Metadata-only"), "② the dialog promises the metadata-only operation (projection untouched)");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    spies.spliceCalls.length === 1 && spies.spliceCalls[0] === BACKUPS[0].dir,
    `② confirm calls MergePreRepairSplice with the row dir (${JSON.stringify(spies.spliceCalls)})`,
  );
  ok(
    toastText().includes("Splice registered (manual): 2 old-chain messages · splice point after main message 2"),
    "② the receipt toast carries the manual source and the segment/splice readouts",
  );
  ok(spies.listCalls >= 2, "② the list rescan runs after the registration");
  await act(async () => root.unmount());
}

// ③ 无法拼接形态（covered）：弹窗披露人话原因，确认键=关闭——零登记调用。
{
  const spies: Spies = { previewCalls: [], spliceCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    PreviewPreRepairSplice: async (dir: string) => ({
      ...DIVERGED_PREVIEW,
      splicable: false,
      kind: "",
      reason: "backup fully contained in the main line (covered; nothing was lost)",
      ownerPath: dir,
    }),
  });
  await act(async () => { (query("[data-pre-repair-splice]") as HTMLButtonElement).click(); });
  await flush();
  ok(bodyText().includes("Cannot splice: backup fully contained in the main line"), "③ the non-splicable verdict names the plain reason");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(spies.spliceCalls.length === 0, "③ the close confirm never calls the registration bridge");
  await act(async () => root.unmount());
}

// ④ 健康闸提示替代硬拦：不健康宿主的预览披露 codes，确认仍可登记。
{
  const spies: Spies = { previewCalls: [], spliceCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {
    PreviewPreRepairSplice: async (dir: string) => ({
      ...DIVERGED_PREVIEW,
      ownerHealthy: false,
      healthCodes: ["projection_overflow"],
      ownerPath: dir,
    }),
  });
  await act(async () => { (query("[data-pre-repair-splice]") as HTMLButtonElement).click(); });
  await flush();
  ok(
    bodyText().includes("did not pass its health check (projection_overflow)"),
    "④ the unhealthy owner surfaces the codes as a notice",
  );
  ok(bodyText().includes("Metadata-only"), "④ the notice coexists with the metadata-only promise (not a gate)");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(spies.spliceCalls.length === 1, "④ the registration still proceeds (提示不硬拦——纯元信息操作)");
  await act(async () => root.unmount());
}

// ⑤ 行内徽标按登记来源分档：manual/auto/历史素形态三读数。
{
  const spies: Spies = { previewCalls: [], spliceCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  const rows = queryAll("[data-pre-repair-list] > li");
  ok(rows[1]?.textContent?.includes("splice registered (manual)"), "⑤ the manual registration badge names the manual trigger");
  ok(rows[2]?.textContent?.includes("splice registered (self-heal)"), "⑤ the auto registration badge names the self-heal trigger");
  ok(rows[3]?.textContent?.includes("splice registered") && !rows[3]?.textContent?.includes("(manual)") && !rows[3]?.textContent?.includes("(self-heal)"), "⑤ a legacy ledger row keeps the plain badge");
  await act(async () => root.unmount());
}

// ⑥ 契约钉：两桥声明 + 预览/回执形态 + 三语字典键齐 + Go 桥存在。
{
  const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  ok(bridge.includes("PreviewPreRepairSplice(dir: string): Promise<PreRepairSplicePreviewView>"), "⑥ the preview bridge is declared on AppBindings");
  ok(bridge.includes("MergePreRepairSplice(dir: string): Promise<PreRepairSpliceReceiptView>"), "⑥ the registration bridge is declared on AppBindings");
  ok(bridge.includes("splicedBy?: string"), "⑥ PreRepairBackupView carries the registration-source wire field");
  ok(bridge.includes("ownerHealthy: boolean"), "⑥ the preview view carries the health-notice readout");
  const component = readFileSync(resolve(testDir, "../components/RecoveryCopiesSection.tsx"), "utf8");
  ok(component.includes('data-pre-repair-splice=""'), "⑥ the row button is wired with its stable hook");
  ok(component.includes('data-pre-repair-splice-segments=""'), "⑥ the chain-preview segments carry their stable hook");
  ok(component.includes('data-pre-repair-splice-health=""'), "⑥ the health notice carries its stable hook");
  const goSplice = readFileSync(resolve(testDir, "../../../../desktop/pre_repair_splice.go"), "utf8");
  ok(goSplice.includes("func (a *App) PreviewPreRepairSplice") && goSplice.includes("func (a *App) MergePreRepairSplice"), "⑥ the Go bridge pair exists (Wails exposure layer)");
  ok(goSplice.includes("PreRepairSpliceSourceManual"), "⑥ the Go bridge registers with the manual source marker");
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(resolve(testDir, `../locales/${locale}.ts`), "utf8");
    const missing = [
      "settings.recoveryCopiesPreRepairSplice",
      "settings.recoveryCopiesPreRepairSpliceHint",
      "settings.recoveryCopiesPreRepairSpliceTitle",
      "settings.recoveryCopiesPreRepairSpliceHead",
      "settings.recoveryCopiesPreRepairSpliceSegOwner",
      "settings.recoveryCopiesPreRepairSpliceSegShared",
      "settings.recoveryCopiesPreRepairSpliceSegLost",
      "settings.recoveryCopiesPreRepairSpliceSegFold",
      "settings.recoveryCopiesPreRepairSpliceSegTail",
      "settings.recoveryCopiesPreRepairSplicePromise",
      "settings.recoveryCopiesPreRepairSpliceHealth",
      "settings.recoveryCopiesPreRepairSpliceNotSplicable",
      "settings.recoveryCopiesPreRepairSpliceDone",
      "settings.recoveryCopiesPreRepairSpliceCovered",
      "settings.recoveryCopiesPreRepairSpliceFailed",
      "settings.recoveryCopiesPreRepairSplicedManual",
      "settings.recoveryCopiesPreRepairSplicedAuto",
    ].filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `⑥ ${locale} carries the tl1674b keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
