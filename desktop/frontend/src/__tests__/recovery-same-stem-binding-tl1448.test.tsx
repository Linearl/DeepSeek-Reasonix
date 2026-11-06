// tl1448 harness: the same-stem dual-row shape and the delete-path validation.
//
// Field incident (20261106, the researcher-1 conversation): after an explicit
// adoption (tl1430b) the panel's main row followed Selected onto the recovery
// copy file — but that same file still rendered a regular copy row with a live
// checkbox and delete entry (同 stem 双行, no tie between the two rows), and the
// demoted scan-origin main file rendered as a 版本行 with the generic "copy"
// label. The user deleted what looked like the abnormal line and the backend
// log shows the CURRENT MAIN copy file being trashed. The backend cannot refuse
// it: copy paths have no "selected" concept (recovery_branch_delete.go, 1064
// constraint ① — "the frontend owns disabling the main itself").
//
// Nails:
//  ① the adopted copy row carries the selected badge — the two rows pointing at
//    one file are distinguishable on the table (same-stem dual rows)
//  ② the main-file copy row has no checkbox and no delete entry; a regular copy
//    row keeps both
//  ③ the demoted scan-origin main file's 版本行 names the file stem, not the
//    generic "copy" label (identity, not masquerade)
//  ④ single delete confirms with the target file path and deletes exactly the
//    row's own file
//  ⑤ a checkbox ticked before an adoption (stale selection residue) is filtered
//    out of the batch delete — no dialog, no backend call for the main file
//  ⑥ "clean up non-main copies" skips the adopted main file (the name promises
//    非主线; the filter must know the main pointer)
//  ⑦ preview attribution: each row previews its own file (copy row previews the
//    copy, the main-file copy row previews through its own chain, the demoted
//    main file's version row previews itself)

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

// tl1368 precedent: the whole-conversation delete button also contains "Delete
// selected" as a substring; the branch-batch button always carries "(N)".
function batchDeleteButton(): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.trim().startsWith("Delete selected ("),
  );
}

const MAIN_PATH = "/sessions/gc.jsonl";
// Real recovery naming: the incident's good line, adopted as the main (tl1430b).
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

function chainRow(path: string, selected: boolean) {
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
  };
}

/** adopted: Selected sits on the recovery copy file — the same-stem dual-row
 *  shape (the file renders as the main row AND as a copy row). */
function chainsAdopted() {
  return {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    chains: [chainRow(MAIN_PATH, false), chainRow(FORK_PATH, true), chainRow(COPY_PATH, false)],
    longestPath: "",
    longestHead: "",
    newestPath: "",
    newestHead: "",
  };
}

/** neutral: no adoption anywhere (Selected on the scan origin) — the checkbox
 *  for the fork file legitimately exists, which is what scenario ⑤ ticks before
 *  the adoption lands. */
function chainsNeutral() {
  return {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    chains: [chainRow(MAIN_PATH, true), chainRow(FORK_PATH, false), chainRow(COPY_PATH, false)],
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

type Root = ReturnType<typeof createRoot>;

async function openPanel(): Promise<Root> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  return root;
}

function copyRowFor(path: string): HTMLTableRowElement | undefined {
  // The full path lives in title attributes (the shortname span's hover), not
  // in textContent — match on titles.
  return Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-row--copy")).find((r) =>
    Array.from(r.querySelectorAll<HTMLElement>("[title]")).some((el) => (el.getAttribute("title") || "").includes(path)),
  );
}

console.log("\nsame-stem dual rows + delete path validation (tl1448)");

// ①②③ the same-stem dual-row shape, rendered honestly.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  const root = await openPanel();

  const mainRow = document.querySelector<HTMLTableRowElement>("tr.rc-detail__row--main");
  ok(Boolean(mainRow?.textContent?.includes("recovery-7fa1f7e5") && mainRow?.textContent?.includes("selected")),
    "① the main row follows the adopted copy file (tl1430b regression inside the same-stem shape)");

  const forkCopyRow = copyRowFor(FORK_PATH);
  const plainCopyRow = copyRowFor(COPY_PATH);
  ok(Boolean(forkCopyRow && plainCopyRow), "① both copy rows render (the same file holds a main row AND a copy row)");
  ok(Boolean(forkCopyRow?.textContent?.includes("selected")),
    "① the main-file copy row carries the selected badge — the dual rows are distinguishable");
  ok(!plainCopyRow?.textContent?.includes("selected"),
    "① the regular copy row carries no selected badge");

  ok(!forkCopyRow?.querySelector("input[type=checkbox]"), "② the main-file copy row has no delete-selection checkbox");
  ok(!forkCopyRow?.querySelector(".rc-delete"), "② the main-file copy row has no delete entry (删副本≠删主线)");
  ok(Boolean(plainCopyRow?.querySelector("input[type=checkbox]")), "② the regular copy row keeps its checkbox");
  ok(Boolean(plainCopyRow?.querySelector(".rc-delete")), "② the regular copy row keeps its delete entry");

  const demotedMainVersionRow = Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-chain")).find(
    (r) => r.querySelector(".rc-clip1")?.textContent === "gc",
  );
  ok(Boolean(demotedMainVersionRow), "③ the demoted scan-origin main file's version row names the file stem (gc)");
  ok(Boolean(demotedMainVersionRow && !demotedMainVersionRow.textContent?.includes("copy")),
    "③ that row no longer wears the generic copy label (identity, not masquerade)");

  await act(async () => root.unmount());
}

// ④ single delete: confirm names the target file and deletes exactly that file.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  const root = await openPanel();

  const plainCopyRow = copyRowFor(COPY_PATH);
  const deleteBtn = plainCopyRow?.querySelector<HTMLButtonElement>(".rc-delete");
  ok(Boolean(deleteBtn && !deleteBtn.disabled), "④ the regular copy row's delete entry is clickable");
  await act(async () => { deleteBtn?.click(); });
  await flush();

  const dialog = document.querySelector(".reasonix-confirm-dialog");
  ok(Boolean(dialog), "④ single delete asks for confirmation first");
  ok(Boolean(dialog?.textContent?.includes(COPY_PATH)), "④ the confirmation names the target file (删除对象明示)");
  ok(spies.deleted.length === 0, "④ nothing is deleted before the user confirms");

  // Scoped to the dialog: the table's own delete buttons also read "Delete".
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.deleted.length === 1 && spies.deleted[0] === COPY_PATH, "④ the confirmed delete reaches the backend with the row's own file");
  ok(!spies.deleted.includes(FORK_PATH), "④ the current main file is never in the delete calls");

  await act(async () => root.unmount());
}

// ⑤ batch delete: a checkbox ticked before the adoption (stale residue after a
// refresh) is filtered out — no dialog, no backend call for the main file.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsNeutral(), spies);
  const root = await openPanel();

  const forkCheckbox = copyRowFor(FORK_PATH)?.querySelector<HTMLInputElement>("input[type=checkbox]");
  ok(Boolean(forkCheckbox && !forkCheckbox.disabled), "⑤ with no adoption the fork file's checkbox exists (pre-adoption state)");
  await act(async () => { forkCheckbox?.click(); });
  await flush();
  ok((batchDeleteButton()?.textContent || "").includes("(1)"), "⑤ the batch button counts the ticked branch");

  // The adoption lands (another tab switched the main line); the panel reloads.
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  await act(async () => { buttonByText("Refresh")?.click(); });
  await flush();
  ok(!copyRowFor(FORK_PATH)?.querySelector("input[type=checkbox]"), "⑤ after the adoption the fork row's checkbox is gone");
  ok(Boolean(batchDeleteButton()), "⑤ the stale selection residue keeps the batch button alive");

  await act(async () => { batchDeleteButton()?.click(); });
  await flush();

  ok(spies.deleted.length === 0, "⑤ the stale main-file selection never reaches the backend (删除路径校验)");
  ok(!document.querySelector(".reasonix-confirm-dialog"), "⑤ no delete dialog is offered for a main-file-only selection");
  ok(!batchDeleteButton(), "⑤ the stale selection is cleared (no silent dead batch button)");

  await act(async () => root.unmount());
}

// ⑥ "clean up non-main copies" skips the adopted main file.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048)])], chainsAdopted(), spies);
  const root = await openPanel();

  const sessionCheckbox = document.querySelector<HTMLInputElement>("tr.rc-session input[type=checkbox]");
  await act(async () => { sessionCheckbox?.click(); });
  await flush();

  const cleanup = buttonByText("Clean up non-main copies");
  ok(Boolean(cleanup && !cleanup.disabled), "⑥ the cleanup entry is available");
  await act(async () => { cleanup?.click(); });
  await flush();

  ok(spies.deleted.length === 0, "⑥ the adopted main file is never offered to the cleanup (名字承诺「非主线」)");
  ok(!document.querySelector(".reasonix-confirm-dialog"), "⑥ no cleanup confirmation for a main-file-only target set");

  await act(async () => root.unmount());
}

// ⑦ preview attribution: each row previews its own file.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  const root = await openPanel();

  const plainPreview = copyRowFor(COPY_PATH)?.querySelector<HTMLButtonElement>(".rc-chain__preview-btn");
  const forkPreview = copyRowFor(FORK_PATH)?.querySelector<HTMLButtonElement>(".rc-chain__preview-btn");
  ok(Boolean(plainPreview && !plainPreview.disabled), "⑦ the regular copy row's preview is enabled");
  ok(Boolean(forkPreview && !forkPreview.disabled), "⑦ the main-file copy row's preview binds its own chain (not a dead button)");

  await act(async () => { plainPreview?.click(); });
  await flush();
  ok(spies.previews.length === 1 && spies.previews[0] === COPY_PATH, "⑦ the regular copy row previews its own file");

  await act(async () => { buttonByText("Cancel", true)?.click(); });
  await flush();
  await act(async () => { forkPreview?.click(); });
  await flush();
  ok(spies.previews.length === 2 && spies.previews[1] === FORK_PATH, "⑦ the main-file copy row previews the same file's main chain");

  const demotedVersionPreview = Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-chain"))
    .find((r) => r.querySelector(".rc-clip1")?.textContent === "gc")
    ?.querySelector<HTMLButtonElement>(".rc-chain__preview-btn");
  ok(Boolean(demotedVersionPreview && !demotedVersionPreview.disabled), "⑦ the demoted main file's version row previews its own file");

  await act(async () => root.unmount());
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exitCode = 1;
