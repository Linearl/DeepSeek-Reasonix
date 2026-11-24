// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/pre-repair-merge-tl1787.test.tsx
//
// tl1787 对话版本管理三件 + ⑤/A 的前端验收钉：
//  ① 件1：会话行勾选 → 底部「归档到回收站（含全部副本）」固定入口出现
//     （自「更多▾」弹层提出，弹层不再重复挂）→ 二次确认 → 按行调
//     DeleteConversationWithCopies → 列表重取；
//  ② 件2：宿主与备份同桶的归档形态（ownerArchived）行内标注 + 确认弹窗披露
//     （随对话归档）；活宿主/孤儿行文案零回退（tl1603 既有钉不回归）；
//  ③ 件3/B：活宿主行提供「合并回主线」入口（孤儿/归档行不提供）→ 确认弹窗
//     → 调 MergePreRepairBackup → 回执 toast 带接回条数；
//  ④ 件⑤/A：治理卡 ranges 两行接线（pane labLabel 包裹 + 四 setter 桥 +
//     布局两源 + 灯键 + 三语字典）——task562 契约测试钉计数与豁免，本文件钉
//     接线本体。

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

const BACKUPS: PreRepairBackupView[] = [
  {
    dir: "/sessions/live.pre-repair-20261110T010000",
    transcript: "/sessions/live.pre-repair-20261110T010000/live.jsonl",
    ownerPath: "/sessions/live.jsonl",
    spliced: true,
    label: "dieted conversation",
    stamp: "20261110T010000",
    bytes: 1048576,
    files: 3,
    modifiedAt: "2026-11-10T01:00:00Z",
  },
  {
    dir: "/sessions/archived.pre-repair-20261109T010000",
    transcript: "/sessions/archived.pre-repair-20261109T010000/archived.jsonl",
    ownerPath: "/sessions/archived.jsonl",
    ownerArchived: true,
    label: "archived conversation",
    stamp: "20261109T010000",
    bytes: 2048,
    files: 2,
    modifiedAt: "2026-11-09T01:00:00Z",
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
  },
];

const GROUP: RecoveryCopyGroupView = {
  mainPath: "/sessions/dieted.jsonl",
  mainLabel: "dieted conversation",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 42,
  mainTurns: 7,
  mainBytes: 4096,
  mainModified: "2026-11-10T01:00:00Z",
  // 带一条副本：无副本组会落在默认折叠的「已合入」段，行不渲染（1368 夹具
  // 同款形态——todo 段默认展开，勾选可达）。
  copies: [
    {
      path: "/sessions/dieted-recovery-0123456789abcdef.jsonl",
      label: "dieted copy 1",
      bytes: 256,
      modified: "2026-11-10T01:00:00Z",
      messages: 3,
      shared: 1,
      unique: 2,
      scanned: true,
    },
  ],
};

type Spies = {
  archiveCalls: string[];
  mergeCalls: string[];
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
    DeletePreRepairBackups: async (dirs: string[]) => ({ trashed: dirs.length, trashedBytes: 4096, staged: 0, failed: 0, failures: [] }),
    MergePreRepairBackup: async (dir: string) => {
      spies.mergeCalls.push(dir);
      return { merged: true, grafted: 5, ownerPath: "/sessions/live.jsonl", afterMessages: 47 };
    },
    DeleteConversationWithCopies: async (mainPath: string) => {
      spies.archiveCalls.push(mainPath);
      return { mainTrashed: true, copiesArchived: 2 };
    },
    PreviewRecoveryChain: async (_mainPath: string, chainPath: string) => ({
      path: chainPath, isMain: false, messageCount: 7, turns: 2,
      firstUserText: "hello before the diet", tailLines: ["[user]: hello before the diet"],
      sharedWithMain: 7, uniqueToChain: 0, lastActivity: "", degraded: false,
    }),
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

console.log("\ntl1787 conversation version management (archive / archived-owner / merge / ranges UI)");

// ① 件1：勾选会话行 → 底部「归档到回收站（含全部副本）」出现 → 确认 → 桥按
// 行调、列表重取；「更多▾」弹层不再重复挂整对话入口。
{
  const spies: Spies = { archiveCalls: [], mergeCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  const archiveBefore = query("[data-conversation-archive]");
  ok(!archiveBefore, "① the bottom archive entry is absent with nothing selected (现空位)");
  const rowCheck = document.querySelector<HTMLInputElement>(".rc-row--group input[type=checkbox]");
  await act(async () => { rowCheck?.click(); });
  await flush();
  const archive = query("[data-conversation-archive]") as HTMLButtonElement | undefined;
  ok(Boolean(archive), "① selecting a conversation reveals the bottom archive entry");
  ok((archive?.textContent ?? "").includes("Archive to Recycle Bin (with every copy)"), "① the entry names the archive-to-recycle-bin semantics");
  await act(async () => { archive?.click(); });
  await flush();
  ok(bodyText().includes("Archive selected conversations to the Recycle Bin"), "① the confirm dialog names the archive action");
  ok(bodyText().includes("restorable from the trash"), "① the confirm states the recoverable-trash semantics");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    spies.archiveCalls.length === 1 && spies.archiveCalls[0] === GROUP.mainPath,
    `① confirm calls DeleteConversationWithCopies with the row's main path (${JSON.stringify(spies.archiveCalls)})`,
  );
  ok(spies.listCalls >= 2, "① the list rescan runs after the archive");
  await act(async () => root.unmount());
}

// ② 件2：归档形态（ownerArchived）行内标注 + 确认披露；活宿主行无该标注。
{
  const spies: Spies = { archiveCalls: [], mergeCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  const rows = queryAll("[data-pre-repair-list] > li");
  ok(rows[1]?.textContent?.includes("original conversation is archived"), "② the archived-owner row carries the archived marker");
  ok(!rows[0]?.textContent?.includes("original conversation is archived"), "② the live-owner row carries no archived marker");
  const archivedDelete = rows[1]?.querySelector<HTMLButtonElement>("[data-pre-repair-delete]");
  await act(async () => { archivedDelete?.click(); });
  await flush();
  ok(bodyText().includes("moved into the app's trash bucket together"), "② the archived row's confirm discloses the archived-with-conversation shape");
  ok(bodyText().includes("system recycle bin"), "② the recycle-bin semantics still ride along (默认送系统回收站可还原)");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(spies.mergeCalls.length === 0 && spies.archiveCalls.length === 0, "② deleting the archived row rides the delete bridge only");
  await act(async () => root.unmount());
}

// ③ 件3/B：活宿主行提供合并入口；孤儿/归档行不提供；确认后调桥并 toast 读数。
{
  const spies: Spies = { archiveCalls: [], mergeCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  const mergeButtons = queryAll("[data-pre-repair-merge]");
  ok(mergeButtons.length === 1, "③ only the live-owner row offers merge (orphan/archived rows do not)");
  await act(async () => { (mergeButtons[0] as HTMLButtonElement).click(); });
  await flush();
  ok(bodyText().includes("Merge pre-repair backup into the main line"), "③ the merge confirm dialog names the action");
  ok(bodyText().includes("add-only"), "③ the confirm states the graft-only semantics");
  await act(async () => { dialogConfirm()?.click(); });
  await flush();
  ok(
    spies.mergeCalls.length === 1 && spies.mergeCalls[0] === BACKUPS[0].dir,
    `③ confirm calls MergePreRepairBackup with the row's dir (${JSON.stringify(spies.mergeCalls)})`,
  );
  ok(toastText().includes("Grafted 5 messages back into the main line"), "③ the receipt toast carries the grafted count");
  ok(spies.listCalls >= 2, "③ the list rescan runs after the merge");
  await act(async () => root.unmount());
}

// ④ 件⑤/A：治理卡 ranges 两行接线本体（task562 钉计数/豁免，这里钉接线）。
{
  const pane = readFileSync(resolve(testDir, "../components/ExperimentalLabSection.tsx"), "utf8");
  ok(pane.includes('labLabel("rangesCompact"'), "④ the rangesCompact row label wraps the tier badge (562 pane contract)");
  ok(pane.includes('labLabel("modelRangesCompact"'), "④ the modelRangesCompact row label wraps the tier badge");
  ok(pane.includes("app.SetExperimentalRangesCompact"), "④ the rangesCompact switch writes through its setter");
  ok(pane.includes("app.SetExperimentalModelRangesCompact"), "④ the modelRangesCompact switch writes through its setter");
  ok(pane.includes("app.SetModelRangesFoldWindowMinutes") && pane.includes("app.SetModelRangesFoldWindowMax"), "④ the flood-gate knobs write through their setters");

  const layoutDefault = readFileSync(resolve(testDir, "../lab/labLayoutDefault.ts"), "utf8");
  const yaml = readFileSync(resolve(testDir, "../lab/lab-layout.yaml"), "utf8");
  for (const [name, source] of [["default layout", layoutDefault], ["lab-layout.yaml", yaml]] as const) {
    ok(source.includes('id: "rangesCompact"') || source.includes("- id: rangesCompact"), `④ ${name} carries the rangesCompact member`);
    ok(source.includes('"experimentalRangesCompact"') || source.includes("experimentalRangesCompact"), `④ ${name} lights the rangesCompact key`);
    ok(source.includes('id: "modelRangesCompact"') || source.includes("- id: modelRangesCompact"), `④ ${name} carries the modelRangesCompact member`);
  }
  const lightKeys = readFileSync(resolve(testDir, "../lab/labLightKeys.ts"), "utf8");
  ok(lightKeys.includes("experimentalRangesCompact:") && lightKeys.includes("experimentalModelRangesCompact:"), "④ both light predicates are registered (onKeys whitelist)");

  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(resolve(testDir, `../locales/${locale}.ts`), "utf8");
    const missing = [
      "settings.rangesCompact",
      "settings.rangesCompactHint",
      "settings.modelRangesCompact",
      "settings.modelRangesCompactHint",
      "settings.recoveryCopiesPreRepairMerge",
      "settings.recoveryCopiesPreRepairArchived",
      "settings.recoveryCopiesDeleteConversation",
    ].filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `④ ${locale} carries the tl1787 keys (missing: ${JSON.stringify(missing)})`);
  }

  const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  ok(bridge.includes("MergePreRepairBackup(dir: string): Promise<PreRepairMergeView>"), "④ the merge bridge is declared on AppBindings");
  ok(bridge.includes("ownerArchived?: boolean"), "④ PreRepairBackupView carries the ownerArchived wire field");
  ok(bridge.includes("SetExperimentalRangesCompact(enabled: boolean): Promise<void>"), "④ the ranges setter is declared on AppBindings");

  const goPrefs = readFileSync(resolve(testDir, "../../../../desktop/settings_preferences.go"), "utf8");
  ok(goPrefs.includes("func (a *App) SetExperimentalRangesCompact") && goPrefs.includes("func (a *App) SetExperimentalModelRangesCompact"), "④ the Go setter bridges exist (Wails exposure layer)");
}

// ⑤ tl1787 续作：后台自愈拼接的接线钉（开关面 + 档案袋登记标注）。
{
  const spies: Spies = { archiveCalls: [], mergeCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  ok(bodyText().includes("splice registered"), "⑤ the spliced row carries the ledger-registration marker");
  await act(async () => root.unmount());
  const pane = readFileSync(resolve(testDir, "../components/ExperimentalLabSection.tsx"), "utf8");
  ok(pane.includes('labLabel("prepairAutosplice"'), "⑤ the autosplice row label wraps the tier badge (562 pane contract)");
  ok(pane.includes("app.SetExperimentalPrepairAutosplice"), "⑤ the autosplice switch writes through its setter");
  const layoutDefault = readFileSync(resolve(testDir, "../lab/labLayoutDefault.ts"), "utf8");
  const layoutYaml = readFileSync(resolve(testDir, "../lab/lab-layout.yaml"), "utf8");
  for (const [name, source] of [["default layout", layoutDefault], ["lab-layout.yaml", layoutYaml]] as const) {
    ok(source.includes("prepairAutosplice"), `⑤ ${name} carries the autosplice member`);
    ok(source.includes("experimentalPrepairAutosplice"), `⑤ ${name} lights the autosplice key`);
  }
  ok(readFileSync(resolve(testDir, "../lab/labLightKeys.ts"), "utf8").includes("experimentalPrepairAutosplice:"), "⑤ the autosplice light predicate is registered");
  const bridge2 = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  ok(bridge2.includes("SetExperimentalPrepairAutosplice(enabled: boolean): Promise<void>"), "⑤ the autosplice setter is declared on AppBindings");
  ok(bridge2.includes("spliced?: boolean"), "⑤ PreRepairBackupView carries the spliced wire field");
  const goPrefs = readFileSync(resolve(testDir, "../../../../desktop/settings_preferences.go"), "utf8");
  ok(goPrefs.includes("func (a *App) SetExperimentalPrepairAutosplice"), "⑤ the Go autosplice setter bridge exists");
  const goConfig = readFileSync(resolve(testDir, "../../../../internal/config/edit.go"), "utf8");
  ok(goConfig.includes("func PrepairAutospliceLive()"), "⑤ the Go live read exists (sweep tick reads it)");
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(resolve(testDir, `../locales/${locale}.ts`), "utf8");
    const missing = ["settings.prepairAutosplice", "settings.prepairAutospliceHint", "settings.recoveryCopiesPreRepairSpliced"].filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `⑤ ${locale} carries the autosplice keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
