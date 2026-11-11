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
//    非主线; the filter must know the main pointer) — pinned on BOTH reachable
//    paths: ⑥ the never-expanded session (tl1415b-2 renders session rows
//    collapsed and lazy-loads chain inventory per expanded group, so the
//    cleanup entry must fetch the main pointer itself) and ⑥b the expanded
//    session (chains already in memory; the same filter must still skip it)
//  ⑦ preview attribution: each row previews its own file (copy row previews the
//    copy, the main-file copy row previews through its own chain, the demoted
//    main file's version row previews itself)
//
// Merge adaptation (wt-tl1448 × main-v2-stable tip 71fba891c, tl1415b-2 form):
//  - session rows start COLLAPSED (点2): child rows render only after the
//    session row's expand toggle — openPanel expands for ①-⑤⑦, scenario ⑥
//    stays collapsed on purpose (that is the real-world cleanup flow now);
//  - the preview buttons lost the .rc-chain__preview-btn class (the preview
//    merged into the pick flow, togglePick): ⑦ targets the row's first .rc-ops
//    button and pins its "Preview" label so a reorder cannot silently pass;
//  - chain inventory loads lazily per expanded group: Refresh in ⑤ needs extra
//    flushes before the repointed main filter is visible.
//
// tl1415c adaptation (B2 same-path dedup, user ruling after tl1448): the
// same-path row no longer renders at all — the main row is the ONLY row for
// the adopted file. The dual-row shape this harness pinned (①② badge/entry
// suppression on the main-file COPY row) is superseded, and each superseded
// pin states who carries the guarantee now:
//  - ① "both copy rows render" + "the main-file copy row carries the selected
//    badge" → superseded by dedup: FORK_PATH renders exactly ONE row (the main
//    row, which keeps the selected badge pin). The distinguishability goal is
//    now trivially met — there is no second row to confuse.
//  - ② "no checkbox / no delete / no switch on the main-file copy row" → the
//    row is gone, so the entries are gone with it; the pin becomes "no row
//    except the main row names FORK_PATH". The FUNCTION-level guarantees the
//    entries used to front (main file never in a delete target set) stay
//    pinned by ④⑤⑥ — those exercise preferredMainPathOf gates on the paths
//    that still exist (stale checkbox residue, cleanup, single delete).
//  - ⑦ "the main-file copy row previews its own chain" → the main row's
//    preview now carries that file's preview attribution.

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
  // tl1415d9: 会话行展开态现在跨挂载持久(用户二轮裁决)。每个场景声明
  // 全新档案——上一场景的展开不得流进下一场景的「默认折叠」钉。
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

async function openPanel(expand = true): Promise<Root> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  if (expand) {
    // tl1415b-2 点2: 会话行默认折叠,子行(主线/副本/版本)点展开钮才渲染;
    // 链清单随展开惰性加载,双 flush 等它落地(与 tl1415 harness 同款)。
    await act(async () => {
      document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle")?.click();
    });
    await flush();
    await flush();
  }
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

// ①②③ the same-stem shape after tl1415c B2 dedup: the adopted file renders
// exactly ONE row (the main row); the copy row for it is gone (superseded
// pins documented in the header — the entries' guarantees ride the rows that
// still exist plus the function-level gates pinned by ④⑤⑥).
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  const root = await openPanel();

  const mainRow = document.querySelector<HTMLTableRowElement>("tr.rc-detail__row--main");
  ok(Boolean(mainRow?.textContent?.includes("recovery-7fa1f7e5") && mainRow?.textContent?.includes("selected")),
    "① the main row follows the adopted copy file and carries the selected badge (tl1430b pin, kept)");

  // tl1415c B2: same-path dedup — the adopted main file's copy row no longer
  // renders. This replaces the old "both copy rows render / selected badge on
  // the copy row" dual-row pins (superseded by the user's dedup ruling).
  const forkCopyRow = copyRowFor(FORK_PATH);
  const plainCopyRow = copyRowFor(COPY_PATH);
  ok(!forkCopyRow, "① tl1415c B2: the adopted main file renders NO copy row (同 path 不重复行)");
  ok(Boolean(plainCopyRow), "① the regular copy row still renders");
  ok(document.querySelectorAll("tr.rc-row--copy").length === 1,
    "① the whole table holds exactly one copy row (the dedup removed the twin)");

  // ② superseded form: no row anywhere except the main row names the main
  // file — checkbox/delete/switch cannot reach a file that has no row. The
  // function-level guarantees (the old pins' real subject) stay on ④⑤⑥.
  const rowsNamingFork = Array.from(document.querySelectorAll<HTMLTableRowElement>("tr")).filter((r) =>
    !r.classList.contains("rc-detail__row--main") &&
    Array.from(r.querySelectorAll<HTMLElement>("[title]")).some((el) => (el.getAttribute("title") || "").includes(FORK_PATH)),
  );
  ok(rowsNamingFork.length === 0, "② no non-main row names the main file (checkbox/delete/switch are unreachable with the row gone)");

  ok(Boolean(plainCopyRow?.querySelector("input[type=checkbox]")), "② the regular copy row keeps its checkbox");
  ok(Boolean(plainCopyRow?.querySelector(".rc-delete")), "② the regular copy row keeps its delete entry");
  ok(Array.from(plainCopyRow?.querySelectorAll("button") ?? []).some((b) => b.textContent?.includes("switch to this version")),
    "② the regular copy row keeps its switch entry");

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
  // tl1415b-2: load() 作废链缓存后按展开组惰性重拉——主线指针(副本行收回勾选
  // 的判据)要等这次补拉落地,三 flush 给足。 */
  await flush();
  await flush();
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

// ⑥ "clean up non-main copies" skips the adopted main file — on the
// never-expanded flow. tl1415b-2 renders sessions collapsed and lazy-loads
// chain inventory per expanded group, so this cleanup click happens with NO
// chain data in memory: the entry must fetch the main pointer itself (b-类
// 回植——指针缺席时过滤回落扫描起点,采纳来的主线文件混进目标集 = tl1448
// 事故形态经新架构复活). openPanel(false) keeps the session collapsed.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048)])], chainsAdopted(), spies);
  const root = await openPanel(false);

  const sessionCheckbox = document.querySelector<HTMLInputElement>("tr.rc-session input[type=checkbox]");
  await act(async () => { sessionCheckbox?.click(); });
  await flush();

  const cleanup = buttonByText("Clean up non-main copies");
  ok(Boolean(cleanup && !cleanup.disabled), "⑥ the cleanup entry is available (collapsed session, never expanded)");
  await act(async () => { cleanup?.click(); });
  await flush();
  await flush();

  ok(spies.deleted.length === 0, "⑥ the adopted main file is never offered to the cleanup (名字承诺「非主线」)");
  // tl1415f supersession: the target set is no longer main-file-only. The
  // demoted scan-origin file (MAIN_PATH, adopted away) and the copy file the
  // chain inventory knows about are both genuine non-main copies — the
  // cleanup's name promise now reaches them (包 82 打回: 降级副本行「清理非
  // 主线副本」够不着). The confirmation opens for the non-empty set; the
  // adopted main file (FORK_PATH) must never be in it or in the calls.
  {
    const dialog = document.querySelector(".reasonix-confirm-dialog");
    ok(Boolean(dialog), "⑥ the cleanup confirms before deleting (non-main copies are real targets)");
    ok(!dialog?.textContent?.includes(FORK_PATH), "⑥ the confirmation never names the adopted main file");
    const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
      .find((b) => b.textContent?.trim() === "Clean up");
    await act(async () => { confirmBtn?.click(); });
    await flush();
    const deletedSet = new Set(spies.deleted);
    ok(deletedSet.has(MAIN_PATH), "⑥ the confirmed cleanup deletes the demoted scan-origin file");
    ok(!deletedSet.has(FORK_PATH), "⑥ the adopted main file is never in the delete calls");
  }

  await act(async () => root.unmount());
}

// ⑥b the same guarantee on the expanded flow — chains already in memory (the
// original tl1448 shape); the same filter must skip the adopted main file
// without re-fetching anything.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048)])], chainsAdopted(), spies);
  const root = await openPanel(true);

  const sessionCheckbox = document.querySelector<HTMLInputElement>("tr.rc-session input[type=checkbox]");
  await act(async () => { sessionCheckbox?.click(); });
  await flush();

  const cleanup = buttonByText("Clean up non-main copies");
  ok(Boolean(cleanup && !cleanup.disabled), "⑥b the cleanup entry is available (expanded session)");
  await act(async () => { cleanup?.click(); });
  await flush();
  await flush();

  ok(spies.deleted.length === 0, "⑥b the adopted main file is never offered to the cleanup (expanded path)");
  // tl1415f supersession (expanded path): same contract as ⑥ — the demoted
  // scan-origin file is offered, the adopted main never is.
  {
    const dialog = document.querySelector(".reasonix-confirm-dialog");
    ok(Boolean(dialog), "⑥b the cleanup confirms before deleting (expanded path)");
    ok(!dialog?.textContent?.includes(FORK_PATH), "⑥b the confirmation never names the adopted main file (expanded path)");
    const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
      .find((b) => b.textContent?.trim() === "Clean up");
    await act(async () => { confirmBtn?.click(); });
    await flush();
    const deletedSet = new Set(spies.deleted);
    ok(deletedSet.has(MAIN_PATH), "⑥b the confirmed cleanup deletes the demoted scan-origin file (expanded path)");
    ok(!deletedSet.has(FORK_PATH), "⑥b the adopted main file is never in the delete calls (expanded path)");
  }

  await act(async () => root.unmount());
}

// ⑦ preview attribution: each row previews its own file.
{
  const spies: Spies = { deleted: [], previews: [] };
  stubBridge([makeGroup([COPY_READINGS(FORK_PATH, 2048), COPY_READINGS(COPY_PATH, 4096)])], chainsAdopted(), spies);
  const root = await openPanel();

  // tl1415b-2 合并形态:预览并入 pick 流(togglePick),.rc-chain__preview-btn
  // 类已不存在——行 ops 里预览是第一个按钮(预览/切换/删除次序),再钉
  // 「Preview」标签:首位若漂移到别的入口,这里立刻红而不是错点。
  const previewBtnOf = (row?: HTMLTableRowElement): HTMLButtonElement | undefined => {
    const btn = row?.querySelector<HTMLButtonElement>(".rc-ops button");
    return btn && btn.textContent?.trim() === "Preview" ? btn : undefined;
  };

  const plainPreview = previewBtnOf(copyRowFor(COPY_PATH));
  // tl1415c B2: the main-file copy row is gone; the main ROW now carries that
  // file's preview attribution (the old "main-file copy row previews its own
  // chain" pin, superseded).
  const mainPreview = previewBtnOf(document.querySelector<HTMLTableRowElement>("tr.rc-detail__row--main"));
  ok(Boolean(plainPreview && !plainPreview.disabled), "⑦ the regular copy row's preview is enabled");
  ok(Boolean(mainPreview && !mainPreview.disabled), "⑦ the main row's preview binds the adopted file's chain (not a dead button)");

  await act(async () => { plainPreview?.click(); });
  await flush();
  ok(spies.previews.length === 1 && spies.previews[0] === COPY_PATH, "⑦ the regular copy row previews its own file");

  await act(async () => { buttonByText("Cancel", true)?.click(); });
  await flush();
  await act(async () => { mainPreview?.click(); });
  await flush();
  ok(spies.previews.length === 2 && spies.previews[1] === FORK_PATH, "⑦ the main row previews the adopted file (B2: its copy row is gone)");

  const demotedVersionPreview = previewBtnOf(
    Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-chain")).find(
      (r) => r.querySelector(".rc-clip1")?.textContent === "gc",
    ),
  );
  ok(Boolean(demotedVersionPreview && !demotedVersionPreview.disabled), "⑦ the demoted main file's version row previews its own file");

  await act(async () => root.unmount());
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exitCode = 1;
