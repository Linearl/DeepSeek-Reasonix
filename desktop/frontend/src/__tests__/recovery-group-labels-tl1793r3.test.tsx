import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

// tl1793r3 dedicated harness: the second-round field rejection (20261125, the
// qwen27b orphan group still showing ids after the first fix landed).
//
// ① The name slots consume the backend-computed labels. The backend resolves
//    the copy's display name (meta topic_title etc. — the probe found
//    "qwen部署" on the field copies) and, since tl1793r3, the group header
//    label too (reverse inheritance when the canonical transcript is gone),
//    but the name cells only rendered the ordinal + the disk-identity short
//    name — exactly the "recovery-19cbd749" the screenshot showed.
// ② The short-name badge stays: it is the pinned tl1415 点12 anchor that maps
//    a panel row back to the on-disk recovery file.
// ③ A stem-fallback label (no title anywhere) renders nothing extra — no
//    duplicated stem text beside the identity badge.
// ④ A mainless group's header hover says the canonical transcript is missing
//    (the row shows a borrowed name; the hover carries the fact).

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

// The field shape: the qwen27b orphan group — canonical transcript gone, two
// recovery copies whose meta carries "qwen部署".
const MAIN_PATH = "/sessions/20261104-072821.000000000-qwen27b.jsonl";
const COPY_A = "/sessions/20261104-072821.000000000-qwen27b-recovery-19cbd74900000000.jsonl";
const COPY_B = "/sessions/20261104-072821.000000000-qwen27b-recovery-5df6de1800000000.jsonl";

function makeGroup(overrides: Record<string, unknown>, copies: unknown[]) {
  return {
    mainPath: MAIN_PATH,
    mainLabel: "qwen部署",
    directory: "/sessions",
    mainExists: false,
    mainMessages: 0,
    mainTurns: 0,
    mainBytes: 0,
    mainModified: "",
    ...overrides,
    copies,
  };
}

function copyReadings(path: string, label: string) {
  return {
    path,
    label,
    bytes: 2048,
    modified: "2026-11-25T01:00:00Z",
    messages: 8,
    turns: 4,
    shared: 0,
    unique: 8,
    scanned: true,
    orphan: true,
  };
}

function chainRow(path: string, selected: boolean, degraded = false) {
  return {
    path,
    headId: "",
    label: "",
    isMain: path === MAIN_PATH,
    selected,
    covered: false,
    messageCount: degraded ? 0 : 8,
    turns: degraded ? 0 : 4,
    lastActivity: degraded ? "" : "2026-11-25T01:00:00Z",
    bytes: degraded ? 0 : 512,
    preview: "",
    degraded,
  };
}

function stubBridge(groups: unknown[], chains: unknown) {
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: async () => groups,
        ListSessionVersionGroups: async () => [
          { mainPath: MAIN_PATH, mainLabel: "qwen部署", directory: "/sessions", headCount: 1, selected: "", heads: [] },
        ],
        ListRecoveryChains: async () => chains,
        ListSessionRepairRecordsFor: async () => [],
        ScanRecoveryCopyGroup: async () => groups[0],
        DeleteRecoveryBranch: async () => {},
        DeleteSessionVersionHead: async () => {},
        PreviewRecoveryChain: async () => ({
          path: MAIN_PATH,
          isMain: true,
          messageCount: 0,
          turns: 0,
          firstUserText: "",
          tailLines: [],
          sharedWithMain: 0,
          uniqueToChain: 0,
          lastActivity: "",
          degraded: true,
        }),
        SetActiveSessionVersion: async () => {},
      } as unknown as AppBindings,
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

function sessionToggle(): HTMLButtonElement | undefined {
  return document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle") ?? undefined;
}

console.log("\ntl1793r3 acceptance: name slots consume backend labels; missing-main hover; identity badge kept");

// ① the field scene: orphan group, titled copies. Group row, main row and
//    copy rows all carry the real title; the copy keeps its short identity.
{
  stubBridge(
    [makeGroup({}, [copyReadings(COPY_A, "qwen部署"), copyReadings(COPY_B, "qwen部署")])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "qwen部署",
      chains: [chainRow(MAIN_PATH, true, true), chainRow(COPY_A, false), chainRow(COPY_B, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
  );
  const root = await openPanel();

  ok(Boolean(sessionToggle()?.textContent?.includes("qwen部署")),
    "① the group row shows the reverse-inherited title (not the raw stem)");
  ok(Boolean(mainRow()?.textContent?.includes("qwen部署")),
    "① the main row's name slot carries the group label");
  const copyRow = copyRowFor(COPY_A);
  ok(Boolean(copyRow?.textContent?.includes("qwen部署")),
    "① the copy row's name slot consumes the backend copy label");
  ok(Boolean(copyRow?.textContent?.includes("recovery-19cbd749")),
    "② the copy row keeps its short disk identity badge (tl1415 点12 pin)");
  ok(Boolean(copyRow?.querySelector(`.rc-copy__shortname[title="${COPY_A}"]`)),
    "② the identity badge stays bound to the row's own file path");

  await act(async () => root.unmount());
}

// ③ a stem-fallback copy label renders nothing extra — the stem string must
//    not appear beside the identity badge (only the badge's title attribute
//    carries the full path).
{
  const stemLabel = "20261104-072821.000000000-qwen27b-recovery-19cbd74900000000";
  stubBridge(
    [makeGroup({}, [copyReadings(COPY_A, stemLabel)])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "qwen部署",
      chains: [chainRow(MAIN_PATH, true, true), chainRow(COPY_A, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
  );
  const root = await openPanel();

  const copyRow = copyRowFor(COPY_A);
  ok(Boolean(copyRow), "③ the stem-fallback copy still renders a row");
  ok(!Boolean(copyRow?.textContent?.includes(stemLabel)),
    "③ the stem-fallback label is not duplicated into the name cell");
  ok(Boolean(copyRow?.textContent?.includes("recovery-19cbd749")),
    "③ the identity badge still names the file (the only stem carrier)");

  await act(async () => root.unmount());
}

// ④ the mainless group's header hover notes the missing canonical transcript;
//    a main-present group's hover does not.
{
  stubBridge(
    [makeGroup({}, [copyReadings(COPY_A, "qwen部署")])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "qwen部署",
      chains: [chainRow(MAIN_PATH, true, true), chainRow(COPY_A, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
  );
  const root = await openPanel();
  ok(Boolean(sessionToggle()?.getAttribute("title")?.includes("canonical transcript missing")),
    "④ the mainless group header hover notes the missing canonical transcript");
  await act(async () => root.unmount());

  stubBridge(
    [makeGroup({ mainExists: true, mainBytes: 1024, mainModified: "2026-11-25T00:00:00Z" }, [copyReadings(COPY_A, "qwen部署")])],
    {
      mainPath: MAIN_PATH,
      mainLabel: "qwen部署",
      chains: [chainRow(MAIN_PATH, true), chainRow(COPY_A, false)],
      longestPath: "",
      longestHead: "",
      newestPath: "",
      newestHead: "",
    },
  );
  const root2 = await openPanel();
  ok(!Boolean(sessionToggle()?.getAttribute("title")?.includes("canonical transcript missing")),
    "④ a main-present group header hover carries no missing note");
  await act(async () => root2.unmount());
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exitCode = 1;
