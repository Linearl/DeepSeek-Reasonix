// 队尾体积列修复 harness（协调线 15:0x 块批准，与 1063 的查看面体积列合并执行勿重复）：
// SessionVersionsSection 链表体积列（1063 已落）+ 修复回执的「修复前→修复后」体积对比
// + 动作名标注 done/skipped + repaired=false 不套「已修复」文案。
//
// 本 harness 钉四点：
//  ① v3 版本链表渲染体积列头与每行体积读数（复用 bytes 字段）；
//  ② 修复成功时回执含体积对比（before → after）与动作名的 done/skipped 标注；
//  ③ 体积不变时回执说「无变化」，不显示 0 → 0 之类的假数字；
//  ④ repaired=false 只说「无需修复」，不套「已修复」文案。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

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

const MAIN_PATH = "/sessions/gc.jsonl";

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 4096,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [],
};

function chainSetWithBytes(bytes: number): AppBindings["ListRecoveryChains"] extends () => Promise<infer R> ? R : never {
  return {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    chains: [
      {
        path: MAIN_PATH,
        headId: "",
        label: "GC",
        isMain: true,
        selected: true,
        covered: false,
        messageCount: 12,
        turns: 6,
        lastActivity: "2026-10-21T00:33:00Z",
        bytes,
        preview: "hello",
      },
      {
        path: MAIN_PATH,
        headId: "deadbeefdeadbeef",
        label: "GC fork",
        isMain: true,
        selected: false,
        covered: false,
        messageCount: 4,
        turns: 2,
        lastActivity: "2026-10-20T00:00:00Z",
        bytes: 512,
        preview: "forked",
      },
    ],
    longestPath: MAIN_PATH,
    longestHead: "",
  } as never;
}

function stubBridge(overrides: Partial<AppBindings>, spies: { bytesReads: number }) {
  let bytes = 4096;
  const base = {
    ListRecoveryCopyGroups: async () => [GROUP],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => {
      spies.bytesReads += 1;
      return chainSetWithBytes(bytes);
    },
    ListSessionRepairRecordsFor: async () => [],
    InspectSessionHealthFor: async () => ({ path: MAIN_PATH, healthy: true }),
    RepairSessionFor: async (_path: string, _a: boolean, _b: boolean, diet: boolean) => {
      // diet shrinks the file (bytes read after the repair reflect the change);
      // a plain repair leaves the size alone.
      if (diet) bytes = 2048;
      return {
        path: MAIN_PATH,
        backupDir: "/backup/x",
        actions: [
          { action: "promote_head", status: "done" },
          { action: "fold_images", status: diet ? "done" : "skipped" },
        ],
        healthy: true,
        repaired: true,
      };
    },
    SetActiveSessionVersion: async () => {},
    DeleteRecoveryBranch: async () => {},
    DeleteSessionVersionHead: async () => {},
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openAndExpand(): Promise<Root> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  return root;
}

console.log("\nrepair receipt volume delta + action status (queue-tail volume fix)");

// ① v3 版本链表渲染体积列头与每行体积读数。
{
  const spies = { bytesReads: 0 };
  stubBridge({}, spies);
  const root = await openAndExpand();
  const headerCells = Array.from(document.querySelectorAll(".rc-versions__table thead th")).map((th) => th.textContent || "");
  ok(headerCells.includes("size"), "① the version table renders the volume column header");
  const rows = Array.from(document.querySelectorAll(".rc-versions__table tbody tr"));
  ok(rows.some((r) => r.textContent?.includes("4.0 KB")), "① the main chain's row shows its on-disk size");
  ok(rows.some((r) => r.textContent?.includes("512 B")), "① the fork head's row shows its on-disk size");
  await act(async () => root.unmount());
}

// ② 修复成功时回执含体积对比与 done/skipped 标注。
{
  const spies = { bytesReads: 0 };
  stubBridge({}, spies);
  const root = await openAndExpand();
  const repairDiet = buttonByText("repair and diet");
  ok(Boolean(repairDiet), "② the diet repair entry is present");
  // The repair's click handler owns the receipt write; do NOT re-open the row
  // mid-flight — the reload it triggers collapses the row, and a toggle click
  // racing that collapse can leave the row closed (实测 harness：重开与重载
  // 赛跑时回执时有时无). Just wait for the handler's own deferral to land.
  await act(async () => { repairDiet?.click(); });
  await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
  // The repair reloads the list (onReload), which collapses the row; re-open it
  // so the receipt is on screen for the read. Two flushes: the reload's own
  // lazy chain/record load is an async chain of its own.
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  await act(async () => { await new Promise((r) => setTimeout(r, 30)); });
  const note = document.querySelector(".rc-versions__health")?.textContent || bodyText();
  ok(note.includes("Repaired"), "② a successful repair reports the repaired receipt");
  ok(note.includes("promote_head(done)") && note.includes("fold_images(done)"), "② action names carry their done/skipped status");
  ok(note.includes("4.0 KB") && note.includes("2.0 KB"), "② the receipt shows the before→after size comparison");
  await act(async () => root.unmount());
}

// ③ 体积不变时回执说「无变化」，不显示假数字。
{
  const spies = { bytesReads: 0 };
  stubBridge({}, spies);
  const root = await openAndExpand();
  const repairPlain = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.trim() === "repair",
  );
  await act(async () => { repairPlain?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  const note = document.querySelector(".rc-versions__health")?.textContent || bodyText();
  ok(note.includes("size unchanged"), "③ an unchanged size says so rather than showing a fake delta");
  ok(!note.includes("4.0 KB → 4.0 KB"), "③ no redundant before→after line when nothing shrank");
  await act(async () => root.unmount());
}

// ④ repaired=false 只说「无需修复」。
{
  const spies = { bytesReads: 0 };
  stubBridge(
    {
      RepairSessionFor: async () => ({
        path: MAIN_PATH,
        actions: [],
        healthy: true,
        repaired: false,
      }),
    } as Partial<AppBindings>,
    spies,
  );
  const root = await openAndExpand();
  const repairPlain = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.trim() === "repair",
  );
  await act(async () => { repairPlain?.click(); });
  await flush();
  const note = bodyText();
  ok(note.includes("No repair needed"), "④ repaired=false reports the no-repair-needed receipt");
  ok(!note.includes("Repaired ("), "④ repaired=false never wears the \"Repaired\" wording");
  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
