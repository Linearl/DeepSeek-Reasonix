// Run: tsx src/__tests__/pre-repair-section-tl1572.test.tsx
//
// tl1572: 版本与副本面板的「改写前备份（pre-repair）」分区。验收钉四点：
//  ① 存量备份目录全量可发现（含宿主已删的孤儿备份；列表为空也渲染分区头，
//     「0 个」本身就是 Diet 通知所指入口的答案）；
//  ② 「查看」=只读预览：以备份内转录自身为两个参数调 PreviewRecoveryChain
//     （main==chain 的只读契约），预览弹窗复用既有形态；
//  ③ 不承诺切回：全程零 SetActiveSessionVersion 调用（adopt 对备份树会被
//     后端拒绝，面板不得提供该入口）；
//  ④ 枚举失败不拖垮面板主体：副本列表照常渲染，分区落一行原因；
//  ⑤ notice 文案与面板能力一致：三语 recovery.noticeSelfHealed 指向
//     「改写前备份」分区的查看，不再承诺「面板中切回」。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { AppBindings, PreRepairBackupView } from "../lib/bridge";

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

function bodyText(): string {
  return document.body.textContent || "";
}

const BACKUPS: PreRepairBackupView[] = [
  {
    dir: "/sessions/demo.pre-repair-20261107T030000",
    transcript: "/sessions/demo.pre-repair-20261107T030000/demo.jsonl",
    ownerPath: "/sessions/demo.jsonl",
    label: "dieted conversation",
    stamp: "20261107T030000",
    bytes: 1048576,
    files: 3,
    modifiedAt: "2026-11-07T03:00:00Z",
  },
  {
    dir: "/sessions/gone.pre-repair-20261105T020006",
    transcript: "/sessions/gone.pre-repair-20261105T020006/gone.jsonl",
    ownerPath: "",
    label: "deleted conversation",
    stamp: "20261105T020006",
    bytes: 2048,
    files: 2,
    modifiedAt: "2026-11-05T02:00:06Z",
  },
];

type Spies = {
  previewCalls: [string, string][];
  activateCalls: string[];
  listCalls: number;
};

function stubBridge(overrides: Partial<AppBindings>, spies: Spies, backups: PreRepairBackupView[] | Error = BACKUPS) {
  const base = {
    ListRecoveryCopyGroups: async () => {
      spies.listCalls += 1;
      return [];
    },
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => ({
      mainPath: "", mainLabel: "", chains: [], longestPath: "", longestHead: "", newestPath: "", newestHead: "",
    }),
    ListSessionRepairRecordsFor: async () => [],
    ListPreRepairBackups: async () => {
      if (backups instanceof Error) throw backups;
      return backups;
    },
    PreviewRecoveryChain: async (mainPath: string, chainPath: string) => {
      spies.previewCalls.push([mainPath, chainPath]);
      return {
        path: chainPath,
        isMain: mainPath === chainPath,
        messageCount: 7,
        turns: 2,
        firstUserText: "hello before the diet",
        tailLines: ["[user]: hello before the diet", "[assistant]: ack"],
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
  stubBridge(overrides, spies, backups);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  return root;
}

function preRepairSection(): HTMLElement | null {
  return document.querySelector<HTMLElement>("[data-pre-repair-section]");
}

function preRepairToggle(): HTMLButtonElement | undefined {
  return document.querySelector<HTMLButtonElement>("[data-pre-repair-toggle]") ?? undefined;
}

console.log("\npre-repair backups section (tl1572)");

// ① 分区头恒渲染；计数与展开行为。
{
  const spies: Spies = { previewCalls: [], activateCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  const section = preRepairSection();
  ok(Boolean(section), "① the pre-repair section renders whenever the panel opens");
  const toggle = preRepairToggle();
  ok(Boolean(toggle), "① the section header is a toggle button");
  ok((toggle?.textContent ?? "").includes("Pre-repair backups"), "① the header carries the section name");
  ok((toggle?.textContent ?? "").includes("2"), "① the header shows the backup count (2)");
  ok(!document.querySelector("[data-pre-repair-list]"), "① rows stay collapsed until expanded");
  await act(async () => { toggle?.click(); });
  await flush();
  const list = document.querySelector("[data-pre-repair-list]");
  ok(Boolean(list), "① expanding reveals the backup rows");
  ok(bodyText().includes("dieted conversation"), "① the newest backup's label renders");
  ok(bodyText().includes("deleted conversation"), "① the orphan backup renders too");
  ok(bodyText().includes("only surviving full copy"), "① the orphan carries the only-copy note");
  ok(bodyText().includes("1.0 MB"), "① the row shows its size");
  await act(async () => root.unmount());
}

// ② 查看 = 只读预览（main==chain），弹窗复用既有形态。
{
  const spies: Spies = { previewCalls: [], activateCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  await act(async () => { preRepairToggle()?.click(); });
  await flush();
  const viewButtons = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-pre-repair-view]"));
  ok(viewButtons.length === 2, "② every backup with a transcript offers a view button");
  await act(async () => { viewButtons[0]?.click(); });
  await flush();
  ok(
    spies.previewCalls.length === 1 && spies.previewCalls[0][0] === spies.previewCalls[0][1],
    `② view calls PreviewRecoveryChain with the backup transcript as both args (${JSON.stringify(spies.previewCalls[0])})`,
  );
  ok(bodyText().includes("Pre-repair backup preview"), "② the preview dialog opens with the read-only title");
  ok(bodyText().includes("hello before the diet"), "② the preview body shows the tail content");
  // 收起弹窗（确认键=关闭）。
  await act(async () => { buttonByText("Close")?.click(); });
  await flush();
  ok(spies.activateCalls.length === 0, "② no activate call happens anywhere in the view flow");
  await act(async () => root.unmount());
}

// ③ 不承诺切回：任何交互零 SetActiveSessionVersion。
{
  const spies: Spies = { previewCalls: [], activateCalls: [], listCalls: 0 };
  const root = await openPanel(spies);
  await act(async () => { preRepairToggle()?.click(); });
  await flush();
  const viewButtons = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-pre-repair-view]"));
  await act(async () => { viewButtons[viewButtons.length - 1]?.click(); });
  await flush();
  await act(async () => { buttonByText("Close")?.click(); });
  await flush();
  ok(spies.activateCalls.length === 0, "③ adopting a backup is never wired (zero SetActiveSessionVersion calls)");
  await act(async () => root.unmount());
}

// ④ 空列表与枚举失败都不拖垮面板主体。
{
  const spies: Spies = { previewCalls: [], activateCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {}, []);
  ok((preRepairToggle()?.textContent ?? "").includes("0"), "④ an empty backup set still renders the header with 0");
  await act(async () => { preRepairToggle()?.click(); });
  await flush();
  ok(bodyText().includes("No pre-repair backups found"), "④ the empty state names the situation");
  ok(!document.querySelector("[data-pre-repair-error]"), "④ no error line without a failure");
  await act(async () => root.unmount());
}
{
  const spies: Spies = { previewCalls: [], activateCalls: [], listCalls: 0 };
  const root = await openPanel(spies, {}, new Error("walk refused"));
  ok(!bodyText().includes("Settings failed to load"), "④ an enumeration failure does not fail the whole panel");
  ok(bodyText().includes("listing pre-repair backups failed"), "④ the failure is named in the section");
  ok(bodyText().includes("walk refused"), "④ the backend reason rides along");
  await act(async () => root.unmount());
}

// ⑤ notice 文案与面板能力一致（三语）。
{
  const locales: Record<string, string> = {
    en: readFileSync(resolve(testDir, "../locales/en.ts"), "utf8"),
    zh: readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8"),
    "zh-TW": readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8"),
  };
  for (const key of ["Group", "None", "View", "PreviewTitle", "Orphan", "Failed"]) {
    ok(
      Object.values(locales).every((dict) => dict.includes(`"settings.recoveryCopiesPreRepair${key}"`)),
      `⑤ all three locales carry settings.recoveryCopiesPreRepair${key}`,
    );
  }
  ok(
    locales.zh.includes("「改写前备份」分区查看") && locales["zh-TW"].includes("「改寫前備份」分區檢視") && locales.en.includes("under Pre-repair backups"),
    "⑤ the Diet notice points at the section's view capability",
  );
  ok(
    !locales.zh.includes("面板中切回") && !locales["zh-TW"].includes("面板中切回") && !locales.en.includes("it can be restored in Settings"),
    "⑤ the notice no longer promises an in-panel restore",
  );
  const bridgeSource = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  ok(bridgeSource.includes("ListPreRepairBackups(): Promise<PreRepairBackupView[]>;"), "⑤ AppBindings declares ListPreRepairBackups");
  const sectionSource = readFileSync(resolve(testDir, "../components/RecoveryCopiesSection.tsx"), "utf8");
  ok(sectionSource.includes("app.PreviewRecoveryChain(view.transcript, view.transcript)"), "⑤ the view is the read-only main==chain preview");
  ok(!sectionSource.includes("SetActiveSessionVersion({ scope: \"\", topicId: \"\", path: view."), "⑤ no adopt wiring on backup rows");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
