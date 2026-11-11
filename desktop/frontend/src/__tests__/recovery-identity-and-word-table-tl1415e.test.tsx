import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

// tl1415e dedicated harness: the package-81 retest's two rejections.
//
// ① The identity ("hash") column must have a value on EVERY row. The value
//    chain is headId → recoveryShortName → file stem; tl1415c B1 landed the
//    type tag / badge / reason but the copy row and the adopted main row
//    stopped at level two, so a normally-named degraded copy
//    (gc-20261005-213108-collab.jsonl — the regex in recoveryShortName only
//    knows -recovery-<hex> names) rendered with NO identity at all. The stem
//    level always exists, so every row now shows a stable on-disk identity.
// ② Word-table unification: the row copy must not carry three competing
//    main-line words. Retired: "will win" (promised a copy the main-line
//    identity), "Picked as main" (collided with "selected" and "main"),
//    "Degraded" (read as "demoted from main" while sitting on the main row).
//    The unified table: type words main/copy/version/degraded copy + state
//    words selected/synthesis base/kept after merge/degraded readings/
//    longer/newer/orphan — mutually exclusive, no shared stems. (The ② open
//    path itself is pinned on the Go side: recovery_open_authority_tl1415e_
//    test.go makes ordinary open/restore land on the panel's authority main
//    line, so panel-Selected and tab content cannot split.)

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
  // tl1415d9: expanded-row state persists across mounts; each scenario starts
  // from a clean profile so the previous scenario's expansion cannot leak in.
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
// The retest's field shape: a normally-named degraded copy — level two
// (recoveryShortName) comes up empty on it, which is exactly why the identity
// vanished in the field.
const COLLAB_PATH = "/sessions/gc-20261005-213108-collab.jsonl";
const ADOPTED_PATH = "/sessions/gc-20261005-collab.jsonl";

function makeGroup(copies: unknown[], mainMessages = 12) {
  return {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    directory: "/sessions",
    mainExists: true,
    mainMessages,
    mainTurns: 6,
    mainBytes: 1024,
    mainModified: "2026-10-21T00:33:00Z",
    copies,
  };
}

function copyReadings(path: string, bytes: number, messages = 8) {
  return {
    path,
    label: path === COLLAB_PATH ? "GC collab" : "GC copy 2",
    bytes,
    modified: "2026-10-21T01:00:00Z",
    messages,
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
    label: path === MAIN_PATH ? "GC" : path === COLLAB_PATH ? "GC collab" : "GC copy 2",
    isMain: path === MAIN_PATH,
    selected,
    covered: false,
    messageCount: degraded ? 0 : path === MAIN_PATH ? 12 : 8,
    turns: degraded ? 0 : path === MAIN_PATH ? 6 : 4,
    lastActivity: path === MAIN_PATH ? "2026-10-21T00:33:00Z" : "2026-10-20T00:00:00Z",
    bytes: path === MAIN_PATH ? 1024 : 512,
    preview: degraded ? "" : path === MAIN_PATH ? "main tail" : "copy tail",
    degraded,
  };
}

type Spies = { activated: string[]; failWith?: Error };

function stubBridge(groups: unknown[], chains: unknown, spies: Spies) {
  const base = {
    ListRecoveryCopyGroups: async () => groups,
    ListSessionVersionGroups: async () => [
      { mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 1, selected: "", heads: [] },
    ],
    ListRecoveryChains: async () => chains,
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

async function openPanel(): Promise<Root> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  await act(async () => {
    document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle")?.click();
  });
  await flush();
  await flush();
  return root;
}

function copyRowFor(path: string): HTMLTableRowElement | undefined {
  return Array.from(document.querySelectorAll<HTMLTableRowElement>("tr.rc-row--copy")).find((r) =>
    Array.from(r.querySelectorAll<HTMLElement>("[title]")).some((el) => (el.getAttribute("title") || "").includes(path)),
  );
}

function mainRow(): HTMLTableRowElement | undefined {
  return document.querySelector<HTMLTableRowElement>("tr.rc-detail__row--main") ?? undefined;
}

function panelText(): string {
  return document.body.textContent ?? "";
}

console.log("\ntl1415e acceptance: identity (hash) on every row, one exclusive word table");

// ① a normally-named degraded copy row carries the stem identity — the field
//    shape that rendered with no identity at all in the retest.
{
  const spies: Spies = { activated: [] };
  stubBridge(
    [makeGroup([copyReadings(COLLAB_PATH, 2048)])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "GC",
      chains: [chainRow(MAIN_PATH, true), chainRow(COLLAB_PATH, false, true)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
    spies,
  );
  const root = await openPanel();

  const collabRow = copyRowFor(COLLAB_PATH);
  ok(Boolean(collabRow), "① the normally-named degraded copy renders a row");
  ok(Boolean(collabRow?.textContent?.includes("20261005-213108-collab")),
    "① the identity (hash) column has a value on it — level-three stem fallback");
  ok(collabRow?.querySelector(".rc-kindtag")?.textContent === "degraded copy",
    "① the degraded type tag stays (tl1415c B1 form does not regress)");
  ok(Boolean(collabRow?.querySelector(`[title="${COLLAB_PATH}"]`)),
    "① the identity span stays bound to the row's own file (hover carries the full path)");

  await act(async () => root.unmount());
}

// ①b an adopted normally-named main line: the promoted row carries the stem
//    identity too, and the selected badge sits on that same row (the row the
//    Go-side open-path fix now lands the tab on).
{
  const spies: Spies = { activated: [] };
  stubBridge(
    [makeGroup([copyReadings(ADOPTED_PATH, 4096)])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "GC",
      chains: [chainRow(MAIN_PATH, false), chainRow(ADOPTED_PATH, true)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
    spies,
  );
  const root = await openPanel();

  const main = mainRow();
  ok(Boolean(main?.textContent?.includes("20261005-collab")),
    "①b the adopted main row (normally named) names the file — identity, not a bare label");
  ok(Boolean(main?.textContent?.includes("selected")),
    "①b the selected badge sits on the adopted row (panel verdict and the Go open target read one authority)");

  await act(async () => root.unmount());
}

// ①c the switch receipt names the same identity chain — the note matches the
//    row the user clicked even for normally-named copies.
{
  const spies: Spies = { activated: [] };
  stubBridge(
    [makeGroup([copyReadings(COLLAB_PATH, 2048)])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "GC",
      chains: [chainRow(MAIN_PATH, true), chainRow(COLLAB_PATH, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
    spies,
  );
  const root = await openPanel();

  const switchBtn = Array.from(copyRowFor(COLLAB_PATH)?.querySelectorAll<HTMLButtonElement>("button") ?? [])
    .find((b) => b.textContent?.includes("switch to this version"));
  ok(Boolean(switchBtn && !switchBtn.disabled), "①c the copy row offers a live switch entry");
  await act(async () => { switchBtn?.click(); });
  await flush();

  ok(spies.activated.length === 1 && spies.activated[0] === COLLAB_PATH,
    "①c the switch reached the backend with the row's own file");
  ok(Boolean(document.querySelector(".rc-versions__health")?.textContent?.includes("20261005-213108-collab")),
    "①c the receipt names the file identity (same chain the row renders)");

  await act(async () => root.unmount());
}

// ② word table: the merge-pick badge reads "synthesis base"; the retired
//    "Picked as main" appears nowhere.
{
  const spies: Spies = { activated: [] };
  stubBridge(
    [makeGroup([copyReadings(COLLAB_PATH, 2048)])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "GC",
      chains: [chainRow(MAIN_PATH, true), chainRow(COLLAB_PATH, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
    spies,
  );
  const root = await openPanel();

  const previewBtn = Array.from(copyRowFor(COLLAB_PATH)?.querySelectorAll<HTMLButtonElement>("button") ?? [])
    .find((b) => b.textContent?.includes("Preview"));
  await act(async () => { previewBtn?.click(); });
  await flush();
  const confirmBtn = buttonByText("Make this the merged main", true);
  ok(Boolean(confirmBtn), "② the pick confirm dialog opens");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(Boolean(copyRowFor(COLLAB_PATH)?.querySelector(".rc-chain__picked-badge")?.textContent === "synthesis base"),
    "② the picked badge reads synthesis base (does not collide with selected/main)");
  ok(!panelText().includes("Picked as main"), "② the retired 'Picked as main' appears nowhere");

  await act(async () => root.unmount());
}

// ②b the winner badge reads "kept after merge"; "will win" is retired — it
//    promised a copy the main-line identity while the main row said "main".
{
  const spies: Spies = { activated: [] };
  stubBridge(
    [makeGroup([copyReadings(COLLAB_PATH, 4096, 30)], 5)],
    {
      mainPath: MAIN_PATH,
      mainLabel: "GC",
      chains: [chainRow(MAIN_PATH, true), chainRow(COLLAB_PATH, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
    spies,
  );
  const root = await openPanel();

  ok(Boolean(copyRowFor(COLLAB_PATH)?.textContent?.includes("kept after merge")),
    "②b the winner badge reads kept after merge (the honest merge outcome)");
  ok(!panelText().includes("will win"), "②b the retired 'will win' appears nowhere");

  await act(async () => root.unmount());
}

// ②c the degraded badge on the main row reads "degraded readings" — a
//    reading state, not a demotion; the bare "Degraded" is retired.
{
  const spies: Spies = { activated: [] };
  stubBridge(
    // One copy keeps the session in the todo group (default-expanded); with
    // no copies it lands in merged, which starts collapsed and the rows
    // never mount (tl1415b-2 default-folding).
    [makeGroup([copyReadings(COLLAB_PATH, 2048)])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "GC",
      chains: [chainRow(MAIN_PATH, true, true), chainRow(COLLAB_PATH, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
    spies,
  );
  const root = await openPanel();

  ok(Boolean(mainRow()?.textContent?.includes("degraded readings")),
    "②c the main row's degraded badge reads degraded readings (reading state, not demotion)");
  ok(!panelText().includes("Degraded"), "②c the retired bare 'Degraded' appears nowhere");

  await act(async () => root.unmount());
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exitCode = 1;
