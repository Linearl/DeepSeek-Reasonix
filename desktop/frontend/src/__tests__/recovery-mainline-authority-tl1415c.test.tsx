import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

// tl1415c dedicated harness: the four acceptance pins from the user's ruling.
//
//  ① where the panel marks a main line, a switch click that the backend
//    refuses (already-main, or hot-switch refused by active work) surfaces
//    the localized human copy — the raw reasonix_error: code prefix never
//    leaks, the technical cause stays verbatim after the human sentence
//    (dual-track), and unknown refusals pass through unchanged.
//    The end-to-end "panel verdict == gate verdict" consistency is pinned on
//    the Go side with the SAME tab≠canonical fixture
//    (desktop/recovery_authority_tl1415c_test.go): panel non-main row →
//    switch succeeds; panel main line → refused. This harness pins the
//    frontend half of that fixture: Selected (authority-fed) marks the tab's
//    line, the demoted catalog-canonical row is switchable and its switch
//    succeeds.
//  ② (same block) the tab path ≠ catalog canonical shape: the main row IS
//    the tab's line, the scan-origin row carries a live switch entry.
//  ③ same-path dedup: the adopted main file renders exactly ONE row across
//    the whole table (the user's ruling supersedes tl1448's dual-row form;
//    tl1448's safety guarantees re-pinned in recovery-same-stem-binding-tl1448).
//  ④ degraded rows are identity-readable: the enumeration-refused placeholder
//    renders under the "degraded copy" type tag with the file stem, never as
//    a main row; a backend-degraded copy row wears the same type tag.

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

const MAIN_PATH = "/sessions/gc.jsonl";
// The tab's line in the ①② fixture: a real recovery-named copy of the main.
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

function copyReadings(path: string, bytes: number) {
  return {
    path,
    label: path === FORK_PATH ? "GC fork" : "GC copy 2",
    bytes,
    modified: "2026-10-21T01:00:00Z",
    messages: 8,
    turns: 4,
    shared: 3,
    unique: 0,
    scanned: true,
  };
}

function chainRow(path: string, selected: boolean, degraded = false) {
  return {
    path,
    headId: "",
    label: path === MAIN_PATH ? "GC" : path === FORK_PATH ? "GC fork" : "GC copy 2",
    isMain: path === MAIN_PATH,
    selected,
    covered: false,
    messageCount: degraded ? 0 : path === MAIN_PATH ? 12 : 8,
    turns: degraded ? 0 : path === MAIN_PATH ? 6 : 4,
    lastActivity: path === MAIN_PATH ? "2026-10-21T00:33:00Z" : "2026-10-20T00:00:00Z",
    bytes: path === MAIN_PATH ? 1024 : 512,
    preview: degraded ? "" : path === MAIN_PATH ? "main tail" : path === FORK_PATH ? "fork tail" : "copy tail",
    degraded,
  };
}

/** tl1415c fixture (mirrors the Go test): Selected sits on the TAB's line (the
 *  fork copy) while the catalog canonical is the scan origin — the exact shape
 *  that split the two verdicts in the field. */
function chainsTabVsCanonical() {
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

type Spies = { activated: string[]; failWith?: Error };

function stubBridge(groups: unknown[], chains: unknown, spies: Spies, chainsReject = false) {
  const base = {
    ListRecoveryCopyGroups: async () => groups,
    ListSessionVersionGroups: async () => [
      { mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 1, selected: "", heads: [] },
    ],
    ListRecoveryChains: async () => {
      if (chainsReject) throw new Error("replay refused: budget exceeded");
      return chains;
    },
    ListSessionRepairRecordsFor: async () => [],
    ScanRecoveryCopyGroup: async () => groups[0],
    DeleteRecoveryBranch: async () => {},
    DeleteSessionVersionHead: async () => {},
    PreviewRecoveryChain: async (_mainPath: string, chainPath: string) => ({
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
    }),
    SetActiveSessionVersion: async (req: { path: string }) => {
      spies.activated.push(req.path);
      if (spies.failWith) throw spies.failWith;
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
    await act(async () => {
      document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle")?.click();
    });
    await flush();
    await flush();
  }
  return root;
}

function copyRowFor(path: string): HTMLTableRowElement | undefined {
  return Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-row--copy")).find((r) =>
    Array.from(r.querySelectorAll<HTMLElement>("[title]")).some((el) => (el.getAttribute("title") || "").includes(path)),
  );
}

function panelErrorText(): string {
  return Array.from(document.querySelectorAll(".rc-errors li")).map((el) => el.textContent ?? "").join("\n");
}

console.log("\ntl1415c acceptance: mainline authority, dedup, degraded identity");

// ① already-main refusal renders the human copy (machine prefix stripped,
// technical cause kept verbatim after it).
{
  const spies: Spies = { activated: [], failWith: new Error("reasonix_error:recovery_already_main: this version is already the conversation's main line") };
  stubBridge([makeGroup([copyReadings(FORK_PATH, 2048), copyReadings(COPY_PATH, 4096)])], chainsTabVsCanonical(), spies);
  const root = await openPanel();

  const plainCopyRow = copyRowFor(COPY_PATH);
  const switchBtn = Array.from(plainCopyRow?.querySelectorAll<HTMLButtonElement>("button") ?? [])
    .find((b) => b.textContent?.includes("switch to this version"));
  ok(Boolean(switchBtn && !switchBtn.disabled), "① the non-main copy row offers a live switch entry");
  await act(async () => { switchBtn?.click(); });
  await flush();

  const shown = panelErrorText();
  ok(spies.activated.length === 1 && spies.activated[0] === COPY_PATH, "① the click reached the backend with the row's own file");
  ok(shown.includes("nothing was switched"), "① the human copy leads (already-main: nothing was switched)");
  ok(shown.includes("this version is already the conversation's main line"), "① the technical cause stays verbatim (双轨并存)");
  ok(!shown.includes("reasonix_error:"), "① the machine code prefix never leaks to the UI");

  await act(async () => root.unmount());
}

// ①b the active-work refusal maps to its own human copy (slot ⑥'s second line).
{
  const spies: Spies = { activated: [], failWith: new Error("reasonix_error:recovery_active_work: the current main line is still working; wait for its turn to finish, then switch again") };
  stubBridge([makeGroup([copyReadings(FORK_PATH, 2048), copyReadings(COPY_PATH, 4096)])], chainsTabVsCanonical(), spies);
  const root = await openPanel();

  const plainCopyRow = copyRowFor(COPY_PATH);
  const switchBtn = Array.from(plainCopyRow?.querySelectorAll<HTMLButtonElement>("button") ?? [])
    .find((b) => b.textContent?.includes("switch to this version"));
  await act(async () => { switchBtn?.click(); });
  await flush();

  const shown = panelErrorText();
  ok(shown.includes("still generating its reply"), "①b the active-work refusal names what to do (wait for the turn)");
  ok(shown.includes("the current main line is still working"), "①b the technical cause stays verbatim");
  ok(!shown.includes("reasonix_error:"), "①b no machine prefix leak");

  await act(async () => root.unmount());
}

// ①c unknown refusals pass through unchanged (no invented copy).
{
  const spies: Spies = { activated: [], failWith: new Error("catalog filing unavailable") };
  stubBridge([makeGroup([copyReadings(FORK_PATH, 2048), copyReadings(COPY_PATH, 4096)])], chainsTabVsCanonical(), spies);
  const root = await openPanel();

  const plainCopyRow = copyRowFor(COPY_PATH);
  const switchBtn = Array.from(plainCopyRow?.querySelectorAll<HTMLButtonElement>("button") ?? [])
    .find((b) => b.textContent?.includes("switch to this version"));
  await act(async () => { switchBtn?.click(); });
  await flush();

  ok(panelErrorText().includes("catalog filing unavailable"), "①c an unmapped refusal passes through verbatim");

  await act(async () => root.unmount());
}

// ② the tab path ≠ catalog canonical shape, frontend half of the Go fixture:
// the main row IS the tab's line; the scan-origin row (the OLD visual main)
// keeps a live switch entry and its switch SUCCEEDS.
{
  const spies: Spies = { activated: [] };
  stubBridge([makeGroup([copyReadings(FORK_PATH, 2048), copyReadings(COPY_PATH, 4096)])], chainsTabVsCanonical(), spies);
  const root = await openPanel();

  const mainRow = document.querySelector<HTMLTableRowElement>("tr.rc-detail__row--main");
  ok(Boolean(mainRow?.textContent?.includes("recovery-7fa1f7e5")), "② the main row marks the tab's line (authority-fed Selected)");
  ok(Boolean(mainRow?.textContent?.includes("selected")), "② the main row carries the selected badge");

  const demotedRow = Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-chain")).find(
    (r) => r.querySelector(".rc-clip1")?.textContent === "gc",
  );
  const demotedSwitch = Array.from(demotedRow?.querySelectorAll<HTMLButtonElement>("button") ?? [])
    .find((b) => b.textContent?.includes("switch to this version"));
  ok(Boolean(demotedSwitch && !demotedSwitch.disabled), "② the scan-origin row (old visual main) keeps a live switch entry");

  await act(async () => { demotedSwitch?.click(); });
  await flush();
  ok(spies.activated.length === 1 && spies.activated[0] === MAIN_PATH, "② the panel-marked non-main row's switch reaches the backend");
  ok(panelErrorText() === "", "② the switch succeeds with no refusal (fixture mirrors the Go test's verdict)");

  await act(async () => root.unmount());
}

// ③ same-path dedup: the adopted main file renders exactly ONE row.
{
  const spies: Spies = { activated: [] };
  stubBridge([makeGroup([copyReadings(FORK_PATH, 2048), copyReadings(COPY_PATH, 4096)])], chainsTabVsCanonical(), spies);
  const root = await openPanel();

  const rowsNamingFork = Array.from(document.querySelectorAll<HTMLTableRowElement>("tr")).filter((r) =>
    r.textContent?.includes("recovery-7fa1f7e5") ||
    Array.from(r.querySelectorAll<HTMLElement>("[title]")).some((el) => (el.getAttribute("title") || "").includes(FORK_PATH)),
  );
  ok(rowsNamingFork.length === 1 && rowsNamingFork[0].classList.contains("rc-detail__row--main"),
    "③ the main file renders exactly one row (the main row) — 同 path 不重复行");
  ok(document.querySelectorAll("tr.rc-row--copy").length === 1, "③ exactly one copy row remains in the table");

  await act(async () => root.unmount());
}

// ④ degraded rows are identity-readable: the enumeration-refused placeholder
// renders under the degraded-copy tag with the file stem, never as a main row.
{
  const spies: Spies = { activated: [] };
  stubBridge([makeGroup([copyReadings(FORK_PATH, 2048)])], null, spies, true);
  const root = await openPanel();

  const mainRow = document.querySelector<HTMLTableRowElement>("tr.rc-detail__row--main");
  ok(Boolean(mainRow && !mainRow.textContent?.includes("Degraded")), "④ the placeholder set never poses as the main row");

  const placeholder = Array.from(document.querySelectorAll<HTMLTableRowElement>("tr")).find((r) =>
    r.querySelector(".rc-kindtag")?.textContent === "degraded copy",
  );
  ok(Boolean(placeholder), "④ the enumeration-refused placeholder wears the degraded-copy type tag");
  ok(Boolean(placeholder?.textContent?.includes("gc")), "④ that row names the file stem (alternative identity, no hash)");
  ok(Boolean(placeholder?.querySelector(".sv-degraded")), "④ the degraded badge with its hover reason is present");

  await act(async () => root.unmount());
}

// ④b a backend-degraded copy row wears the same type tag on the copy row.
{
  const spies: Spies = { activated: [] };
  const chains = {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    chains: [chainRow(MAIN_PATH, true), chainRow(FORK_PATH, false, true), chainRow(COPY_PATH, false)],
    longestPath: "",
    longestHead: "",
    newestPath: "",
    newestHead: "",
  };
  stubBridge([makeGroup([copyReadings(FORK_PATH, 2048), copyReadings(COPY_PATH, 4096)])], chains, spies);
  const root = await openPanel();

  const forkRow = copyRowFor(FORK_PATH);
  ok(forkRow?.querySelector(".rc-kindtag")?.textContent === "degraded copy",
    "④b the degraded copy row's type tag reads degraded copy (not the plain copy label)");

  await act(async () => root.unmount());
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exitCode = 1;
