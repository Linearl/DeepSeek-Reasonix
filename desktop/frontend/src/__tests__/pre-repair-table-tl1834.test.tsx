// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/pre-repair-table-tl1834.test.tsx
//
// tl1834: 改写前备份表的表格形态三件（表头/列对齐/三键排序）。验收钉：
//  ① 表头出现：三个可排序列头（name/time/size，data-pre-repair-sort 锚）
//     + 状态/行内操作两个静态列头；未排序时无 aria-sort（默认态=后端原序）。
//  ② 列对齐：名称列左对齐；时间/大小列右对齐（rc-num + tabular-nums）——
//     DOM 断言钉类名挂接（对齐本身由 CSS grid 模板保证）。
//  ③ 三键排序逻辑钉死：
//     - 名称首键=升序（A→Z），再点降序；
//     - 时间首键=降序（最新在上），再点升序；
//     - 大小首键=降序（最大在上），再点升序；
//     - 换键重置为新键的惯用方向；
//     - 等键行保持后端原序（稳定排序，size desc 的 bravo/delta 同为 300：
//       后端序里 bravo 在前，排序后仍 bravo 在前）。
//  ④ 零回退：排序只动展示序——每行勾选框/查看/删除钮随行走，行数不变，
//     默认态（不点表头）行序=后端原序（tl1603/tl1674b/tl1787 行序钉的面）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings, PreRepairBackupView } from "../lib/bridge";

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

function queryAll<T extends HTMLElement = HTMLElement>(selector: string): T[] {
  return Array.from(document.querySelectorAll<T>(selector));
}

// Fixture：后端原序 [bravo, alpha, delta, charlie]。delta 与 bravo 同为
// 300 字节（稳定排序钉），时间四者互异（时间键全序），alpha/delta 无宿主
// （孤儿徽标落状态列的面）。
const BACKUPS: PreRepairBackupView[] = [
  { dir: "/sessions/bravo.pre-repair-20261110T010000", transcript: "/sessions/bravo.pre-repair-20261110T010000/bravo.jsonl", ownerPath: "/sessions/bravo.jsonl", label: "bravo", stamp: "20261110T010000", bytes: 300, files: 1, modifiedAt: "2026-11-10T01:00:00Z" },
  { dir: "/sessions/alpha.pre-repair-20261109T010000", transcript: "/sessions/alpha.pre-repair-20261109T010000/alpha.jsonl", ownerPath: "", label: "alpha", stamp: "20261109T010000", bytes: 200, files: 1, modifiedAt: "2026-11-09T01:00:00Z" },
  { dir: "/sessions/delta.pre-repair-20261105T020006", transcript: "/sessions/delta.pre-repair-20261105T020006/delta.jsonl", ownerPath: "", label: "delta", stamp: "20261105T020006", bytes: 300, files: 1, modifiedAt: "2026-11-05T02:00:06Z" },
  { dir: "/sessions/charlie.pre-repair-20261101T010000", transcript: "/sessions/charlie.pre-repair-20261101T010000/charlie.jsonl", ownerPath: "/sessions/charlie.jsonl", label: "charlie", stamp: "20261101T010000", bytes: 100, files: 1, modifiedAt: "2026-11-01T01:00:00Z" },
];

type Spies = { listCalls: number };

function stubBridge(spies: Spies) {
  const base = {
    ListRecoveryCopyGroups: async () => {
      spies.listCalls += 1;
      return [];
    },
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => ({
      mainPath: "", mainLabel: "", chains: [], longestPath: "", newestPath: "", longestHead: "", newestHead: "",
    }),
    ListSessionRepairRecordsFor: async () => [],
    ListPreRepairBackups: async () => BACKUPS,
    DeletePreRepairBackup: async () => ({ trashed: true, viaStaging: false }),
    DeletePreRepairBackups: async () => ({ trashed: 0, trashedBytes: 0, staged: 0, failed: 0, failures: [] }),
    PreviewRecoveryChain: async (_mainPath: string, chainPath: string) => ({
      path: chainPath,
      isMain: true,
      messageCount: 1,
      turns: 1,
      firstUserText: "",
      tailLines: ["[user]: x"],
      sharedWithMain: 1,
      uniqueToChain: 0,
      lastActivity: "",
      degraded: false,
    }),
  };
  window.go = {
    main: {
      App: base as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openPanel(spies: Spies) {
  stubBridge(spies);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  await act(async () => {
    (document.querySelector("[data-pre-repair-toggle]") as HTMLButtonElement | null)?.click();
  });
  await flush();
  return root;
}

function rows(): HTMLElement[] {
  return queryAll("[data-pre-repair-list] > li");
}

function rowNames(): string[] {
  return rows().map((li) => li.querySelector(".rc-pretable__name")?.textContent?.trim() ?? "");
}

function sortButton(key: string): HTMLButtonElement | undefined {
  return document.querySelector<HTMLButtonElement>(`[data-pre-repair-sort="${key}"]`) ?? undefined;
}

async function clickSort(key: string) {
  await act(async () => { sortButton(key)?.click(); });
  await flush();
}

function ariaSortOf(key: string): string | null {
  const cell = sortButton(key)?.closest("[role='columnheader']");
  return cell?.getAttribute("aria-sort") ?? null;
}

console.log("\npre-repair backup table form + sorting (tl1834)");

// ① 表头出现；默认态零 aria-sort、零箭头。
{
  const spies: Spies = { listCalls: 0 };
  const root = await openPanel(spies);
  ok(Boolean(document.querySelector("[data-pre-repair-table]")), "① the table container renders");
  ok(Boolean(document.querySelector("[data-pre-repair-head]")), "① the header row renders above the list");
  ok(sortButton("name") != null && sortButton("time") != null && sortButton("size") != null, "① three sortable column headers render (name/time/size)");
  const headerText = document.querySelector("[data-pre-repair-head]")?.textContent ?? "";
  ok(headerText.includes("session / line") && headerText.includes("modified") && headerText.includes("size"), "① header carries the main-table column names");
  ok(headerText.includes("state") && headerText.includes("row actions"), "① header carries the state and ops columns");
  ok(ariaSortOf("name") === null && ariaSortOf("time") === null && ariaSortOf("size") === null, "① no aria-sort before any click (default = backend order)");
  ok(rowNames().join(",") === "bravo,alpha,delta,charlie", `① default row order = backend order (got ${rowNames().join(",")})`);
  await act(async () => root.unmount());
}

// ② 列对齐类名挂接（对齐由 CSS grid 模板 + rc-num 保证，DOM 钉挂接面）。
{
  const spies: Spies = { listCalls: 0 };
  const root = await openPanel(spies);
  const firstRow = rows()[0];
  ok(firstRow.querySelector(".rc-pretable__name") != null, "② the name cell renders in its column slot");
  ok(firstRow.querySelector(".rc-pretable__when.rc-num") != null, "② the time cell is right-aligned (rc-num)");
  ok(firstRow.querySelector(".rc-pretable__size.rc-num") != null, "② the size cell is right-aligned (rc-num)");
  ok(firstRow.querySelector(".rc-pretable__state") != null && firstRow.querySelector(".rc-pretable__ops") != null, "② state and ops cells render per row");
  ok(rows()[1].querySelector(".rc-pretable__state")?.textContent?.includes("original conversation is gone") ?? false, "② the orphan badge lands in the state column");
  await act(async () => root.unmount());
}

// ③ 名称键：首键升序（A→Z），再点降序。
{
  const spies: Spies = { listCalls: 0 };
  const root = await openPanel(spies);
  await clickSort("name");
  ok(rowNames().join(",") === "alpha,bravo,charlie,delta", `③ name first click sorts ascending (got ${rowNames().join(",")})`);
  ok(ariaSortOf("name") === "ascending", "③ aria-sort=ascending on the name header");
  await clickSort("name");
  ok(rowNames().join(",") === "delta,charlie,bravo,alpha", `③ second click flips to descending (got ${rowNames().join(",")})`);
  ok(ariaSortOf("name") === "descending", "③ aria-sort=descending after the flip");
  await act(async () => root.unmount());
}

// ④ 时间键：首键降序（最新在上），再点升序。
{
  const spies: Spies = { listCalls: 0 };
  const root = await openPanel(spies);
  await clickSort("time");
  ok(rowNames().join(",") === "bravo,alpha,delta,charlie", `④ time first click sorts newest-first (got ${rowNames().join(",")})`);
  ok(ariaSortOf("time") === "descending", "④ aria-sort=descending on the time header");
  await clickSort("time");
  ok(rowNames().join(",") === "charlie,delta,alpha,bravo", `④ second click flips to oldest-first (got ${rowNames().join(",")})`);
  ok(ariaSortOf("time") === "ascending", "④ aria-sort=ascending after the flip");
  await act(async () => root.unmount());
}

// ⑤ 大小键：首键降序（最大在上），等键行保持后端原序（稳定排序）；再点升序。
{
  const spies: Spies = { listCalls: 0 };
  const root = await openPanel(spies);
  await clickSort("size");
  ok(rowNames().join(",") === "bravo,delta,alpha,charlie", `⑤ size first click sorts largest-first with stable ties (got ${rowNames().join(",")})`);
  ok(ariaSortOf("size") === "descending", "⑤ aria-sort=descending on the size header");
  await clickSort("size");
  ok(rowNames().join(",") === "charlie,alpha,bravo,delta", `⑤ second click flips to smallest-first, ties still stable (got ${rowNames().join(",")})`);
  await act(async () => root.unmount());
}

// ⑥ 换键重置为新键的惯用方向（size desc → 点 time = time desc）。
{
  const spies: Spies = { listCalls: 0 };
  const root = await openPanel(spies);
  await clickSort("size");
  await clickSort("time");
  ok(ariaSortOf("size") === null, "⑥ the previous key loses aria-sort when another key is clicked");
  ok(ariaSortOf("time") === "descending" && rowNames().join(",") === "bravo,alpha,delta,charlie", "⑥ switching keys resets to that key's default direction");
  await act(async () => root.unmount());
}

// ⑦ 零回退：排序态下每行的勾选/查看/删除钮随行走，行数不变。
{
  const spies: Spies = { listCalls: 0 };
  const root = await openPanel(spies);
  await clickSort("name");
  const all = rows();
  ok(all.length === 4, "⑦ row count unchanged under sorting");
  ok(all.every((li) => li.querySelector("[data-pre-repair-check]") != null), "⑦ every row keeps its checkbox");
  ok(all.every((li) => li.querySelector("[data-pre-repair-view]") != null), "⑦ every row keeps its view button");
  ok(all.every((li) => li.querySelector("[data-pre-repair-delete]") != null), "⑦ every row keeps its delete button");
  const unmount = async () => { await act(async () => root.unmount()); };
  await unmount();
}

console.log(`\ntl1834 pre-repair table: ${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
