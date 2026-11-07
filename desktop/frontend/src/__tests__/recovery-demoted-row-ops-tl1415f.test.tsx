// tl1415f harness: the demoted scan-origin main file's 版本行 becomes operable.
//
// Field reversal (package 82, 2026-11-07): the user adopted the recovery line
// (recovery-7fa1f7e5 as Selected); the demoted 10-18 collab main renders as a
// version-area row — with an EMPTY checkbox cell (unselectable) and NO delete
// entry (the old gate `!c.selected && !c.degraded && c.headId` can never hold
// for file-shaped rows, whose headId is permanently empty since tl1355). The
// user could neither tick the row into 「删除所选」 nor delete it inline, and
// 「清理非主线副本」 never reached the file (包 82 打回第②点: 降级副本行无删
// 除入口+复选框不可选中).
//
// tl1415f: file-shaped leftover rows (`!headId && !selected`) get the cleanup
// checkbox and an inline delete routed through deleteBranchOne — the SAME
// gated path as copy deletes (main-pointer double check + file-naming confirm
// + recoverable trash), no new delete path. The batch collection and the
// one-click cleanup read the same model source (buildSessionChainModel +
// bindCopyChains).
//
// Nails:
//  ① the demoted row carries a checkbox; ticking it feeds 删除所选's count
//  ② the demoted row carries a delete entry; the confirm names the file path;
//    confirming calls DeleteRecoveryBranch with exactly that path; nothing
//    else (main line untouched)
//  ③ a degraded-flagged demoted row keeps both entries (降级读数不是删除豁免)
//  ④ the selected row (the adopted main) has neither checkbox nor delete
//  ⑤ the inline delete refuses nothing extra: pre-adoption the demoted row
//    does not exist at all (it IS the main row), so the unreachable-by-design
//    state stays unreachable

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
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

function buttonByText(text: string, exact = false): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    exact ? b.textContent?.trim() === text : b.textContent?.includes(text),
  );
}

function batchDeleteButton(): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.trim().startsWith("Delete selected ("),
  );
}

const MAIN_PATH = "/sessions/gc.jsonl";
const FORK_PATH = "/sessions/gc-recovery-7fa1f7e5deadbeef.jsonl";
const COPY_PATH = "/sessions/gc-recovery-abcdef1234567890.jsonl";

function makeGroup(copies: unknown[]) {
  return {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    directory: "/sessions",
    mainExists: true,
    mainMessages: 12,
    mainTurns: 6,
    mainBytes: 1024,
    mainModified: "2026-10-21T00:33:00Z",
    copies,
  };
}

const COPY_READINGS = (path: string, bytes: number) => ({
  path,
  label: path === FORK_PATH ? "GC fork" : "GC copy 2",
  bytes,
  modified: "2026-10-21T01:00:00Z",
  messages: 8,
  turns: 4,
  shared: 3,
  unique: 0,
  scanned: true,
});

function chainRow(path: string, selected: boolean, degraded = false) {
  return {
    path,
    headId: "",
    label: path === MAIN_PATH ? "GC" : path === FORK_PATH ? "GC fork" : "GC copy 2",
    isMain: path === MAIN_PATH,
    selected,
    covered: false,
    messageCount: path === MAIN_PATH ? 12 : 8,
    turns: path === MAIN_PATH ? 6 : 4,
    lastActivity: path === MAIN_PATH ? "2026-10-21T00:33:00Z" : "2026-10-20T00:00:00Z",
    bytes: path === MAIN_PATH ? 1024 : 512,
    preview: path === MAIN_PATH ? "stale tail" : path === FORK_PATH ? "good line tail" : "copy tail",
    degraded,
  };
}

/** adopted: Selected sits on the recovery copy file; the demoted scan-origin
 *  main file renders as a version-area row (the user's 降级副本行). */
function chainsAdopted(demotedDegraded = false) {
  return {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    chains: [chainRow(MAIN_PATH, false, demotedDegraded), chainRow(FORK_PATH, true), chainRow(COPY_PATH, false)],
    longestPath: "",
    longestHead: "",
    newestPath: "",
    newestHead: "",
  };
}

type Spies = { deleted: string[]; previews: string[] };

function stubBridge(groups: unknown[], chains: ReturnType<typeof chainsAdopted>, spies: Spies) {
  const base = {
    ListRecoveryCopyGroups: async () => groups,
    ListSessionVersionGroups: async () => [
      { mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 1, selected: "", heads: [] },
    ],
    ListRecoveryChains: async () => chains,
    ListSessionRepairRecordsFor: async () => [],
    ScanRecoveryCopyGroup: async () => groups[0],
    DeleteRecoveryBranch: async (path: string) => {
      spies.deleted.push(path);
    },
    DeleteSessionVersionHead: async () => {},
    PreviewRecoveryChain: async (_mainPath: string, chainPath: string) => {
      spies.previews.push(chainPath);
      return {
        path: chainPath,
        isMain: chainPath === MAIN_PATH,
        messageCount: 8,
        turns: 4,
        firstUserText: "hello",
        tailLines: ["tail"],
        sharedWithMain: 3,
        uniqueToChain: 5,
        lastActivity: "2026-10-20T00:00:00Z",
        degraded: false,
      };
    },
  };
  window.go = {
    main: {
      App: { ...base } as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openPanel(expand = true): Promise<ReturnType<typeof createRoot>> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  if (expand) {
    await act(async () => {
      document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle")?.click();
    });
    await flush();
    await flush();
  }
  return root;
}

function demotedRow(): HTMLTableRowElement | undefined {
  // The stem identity ("gc") opens the name cell; a degraded badge span may
  // follow INSIDE the same clip1 div, so match by prefix, not equality.
  return Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-chain")).find(
    (r) => (r.querySelector(".rc-clip1")?.textContent || "").startsWith("gc"),
  );
}

console.log("\ndemoted scan-origin row operability (tl1415f)");

// ①② the demoted row: checkbox feeds the batch count; inline delete confirms
// with the file path and deletes exactly that file.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  const root = await openPanel();

  const row = demotedRow();
  ok(Boolean(row), "① the demoted scan-origin row renders (stem identity gc)");
  const check = row?.querySelector<HTMLInputElement>("input[type=checkbox]");
  ok(Boolean(check && !check.disabled), "① the demoted row carries an enabled cleanup checkbox");
  await act(async () => { check?.click(); });
  await flush();
  ok((batchDeleteButton()?.textContent || "").includes("(1)"), "① ticking the demoted row feeds 删除所选's count");

  const deleteBtn = row?.querySelector<HTMLButtonElement>(".rc-delete");
  ok(Boolean(deleteBtn && !deleteBtn.disabled), "② the demoted row carries an enabled delete entry");
  await act(async () => { deleteBtn?.click(); });
  await flush();

  const dialog = document.querySelector(".reasonix-confirm-dialog");
  ok(Boolean(dialog), "② the inline delete asks for confirmation first");
  ok(Boolean(dialog?.textContent?.includes(MAIN_PATH)), "② the confirmation names the demoted file's full path");
  ok(spies.deleted.length === 0, "② nothing is deleted before the user confirms");

  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.deleted.length === 1 && spies.deleted[0] === MAIN_PATH, "② the confirmed delete trashes exactly the demoted file (回收语义)");
  ok(!spies.deleted.includes(FORK_PATH) && !spies.deleted.includes(COPY_PATH), "② no other line is in the delete calls");

  await act(async () => root.unmount());
}

// ③ a degraded-flagged demoted row keeps both entries — a degraded READ is not
// a delete exemption; the file is still identifiable and trash-recoverable.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048)])], chainsAdopted(true), spies);
  const root = await openPanel();

  const row = demotedRow();
  ok(Boolean(row?.querySelector(".sv-degraded")), "③ the demoted row wears the degraded badge");
  ok(Boolean(row?.querySelector<HTMLInputElement>("input[type=checkbox]")), "③ the degraded demoted row keeps its checkbox");
  ok(Boolean(row?.querySelector(".rc-delete")), "③ the degraded demoted row keeps its delete entry");

  await act(async () => root.unmount());
}

// ④ the selected line is unreachable from the version area: buildSessionChainModel
// removes the current main chain (the selected row) from the candidate pool, so
// no version-area row ever names the adopted main file or wears the selected
// tag — the checkbox/delete entries cannot reach the standing main from there
// (structural, pinned from the render side; the mid-refresh races stay covered
// by the function-level pointer gates).
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  const root = await openPanel();

  const versionRows = Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-chain"));
  ok(versionRows.length > 0, "④ version-area rows render (fixture sanity)");
  ok(versionRows.every((r) => !(r.textContent || "").includes(FORK_PATH)),
    "④ no version-area row names the adopted main file");
  ok(versionRows.every((r) => !(r.querySelector(".rc-kindtag")?.textContent || "").includes("selected")),
    "④ no version-area row wears the selected tag");
  ok(versionRows.every((r) => !(r.querySelector("input[type=checkbox]")?.checked)),
    "④ no version-area checkbox arrives pre-ticked");

  await act(async () => root.unmount());
}

// ⑤ pre-adoption the demoted row does not exist — the scan-origin main renders
// as the main row, so the file-level delete can never reach the standing main
// through the version area (unreachable by design, pinned).
{
  const spies: Spies = { deleted: [], previews: [] };
  const neutral = {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    chains: [chainRow(MAIN_PATH, true), chainRow(FORK_PATH, false), chainRow(COPY_PATH, false)],
    longestPath: "",
    longestHead: "",
    newestPath: "",
    newestHead: "",
  };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048)])], neutral, spies);
  const root = await openPanel();

  ok(Boolean(document.querySelector("tr.rc-detail__row--main")), "⑤ the scan-origin main renders as the main row");
  ok(!demotedRow(), "⑤ no demoted version-area row exists before an adoption");

  await act(async () => root.unmount());
}

console.log(`\ntl1415f demoted-row ops: ${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
