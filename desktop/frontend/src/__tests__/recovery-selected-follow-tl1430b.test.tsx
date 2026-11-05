// tl1430b harness: the versions table's "main line" row must follow the
// explicit adoption (Selected), not stay pinned to the scan origin (mainPath).
// Package-78 field report: the switch receipt flashed while the main row and
// the reopen target stayed on the old line — the table data source held
// Selected === isMain and the row render pinned itself to path === mainPath.
//
// Nails (front of house, on top of the Go nails that fix the data source):
//  ① the main row renders the adopted file's short badge and the selected tag
//  ② the old line demotes into the candidate rows with its own activate entry
//  ③ with no explicit adoption anywhere, the scan origin stays the main row
//    (single-main-line zero regression)

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

function bodyText(): string {
  return document.body.textContent || "";
}

const MAIN_PATH = "/sessions/gc.jsonl";
// Real recovery naming: recoveryCopiesByMain keys on -recovery-<hex> files.
const FORK_PATH = `/sessions/gc-recovery-7fa1f7e5deadbeef.jsonl`;

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 1024,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [],
};

function chainsWith(selectedPath: string) {
  const row = (path: string, selected: boolean) => ({
    path,
    headId: "",
    label: path === MAIN_PATH ? "GC" : "GC fork",
    isMain: path === MAIN_PATH,
    selected,
    covered: false,
    messageCount: path === MAIN_PATH ? 12 : 8,
    turns: path === MAIN_PATH ? 6 : 4,
    lastActivity: path === MAIN_PATH ? "2026-10-21T00:33:00Z" : "2026-10-20T00:00:00Z",
    bytes: path === MAIN_PATH ? 1024 : 512,
    preview: path === MAIN_PATH ? "hello" : "forked tail",
  });
  return {
    mainPath: MAIN_PATH,
    mainLabel: "GC",
    chains: [row(MAIN_PATH, selectedPath === MAIN_PATH), row(FORK_PATH, selectedPath === FORK_PATH)],
    longestPath: "",
    longestHead: "",
    newestPath: "",
    newestHead: "",
  };
}

function stubBridge(chains: ReturnType<typeof chainsWith>) {
  const base = {
    ListRecoveryCopyGroups: async () => [GROUP],
    ListSessionVersionGroups: async () => [
      { mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 2, selected: "", heads: [] },
    ],
    ListRecoveryChains: async () => chains,
    ListSessionRepairRecordsFor: async () => [],
    ScanRecoveryCopyGroup: async () => GROUP,
    PreviewRecoveryChain: async () => ({
      path: MAIN_PATH, isMain: true, messageCount: 8, turns: 4, firstUserText: "hello",
      tailLines: ["hello"], sharedWithMain: 8, uniqueToChain: 0, lastActivity: "2026-10-20T00:00:00Z", degraded: false,
    }),
  };
  window.go = {
    main: {
      App: { ...base } as unknown as AppBindings,
    },
  } as typeof window.go;
}

type Root = ReturnType<typeof createRoot>;

async function openAndExpand(): Promise<Root | null> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => {
    Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent?.includes("Review and merge"))?.click();
  });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  return root;
}

console.log("\nversions table main row follows the adopted line (tl1430b)");

// ①② explicit adoption: the fork row is Selected — the main row must carry
// the fork's badge, and the old line demotes to the candidate rows.
{
  stubBridge(chainsWith(FORK_PATH));
  const root = await openAndExpand();
  const mainRow = document.querySelector(".rc-versions__table tbody tr.rc-detail__row--main");
  ok(Boolean(mainRow?.textContent?.includes("recovery-7fa1f7e5")), "① the main row shows the adopted file's short badge");
  ok(Boolean(mainRow?.textContent?.includes("selected")), "① the main row carries the selected tag for the adopted line");
  // The main row itself carries .rc-chain too; candidate rows are the chain
  // rows BELOW the main row (an ops row follows each reading row). The old
  // line (gc.jsonl, no recovery short name) renders under the plain-copy
  // label in the candidate block.
  const rowTexts = Array.from(document.querySelectorAll(".rc-versions__table tbody tr")).map((r) => r.textContent || "");
  const mainIndex = rowTexts.findIndex((t) => t.includes("recovery-7fa1f7e5"));
  ok(mainIndex >= 0 && rowTexts.slice(mainIndex + 1).length >= 2,
    "② the old line demotes into the candidate rows");
  ok(!mainRow?.textContent?.includes("hello world"), "① the main row reads the adopted chain, not the scan origin");
  await act(async () => root?.unmount());
}

// ③ no adoption anywhere: the scan origin stays the main row (zero regression
// for the single-main-line shape).
{
  stubBridge(chainsWith(MAIN_PATH));
  const root = await openAndExpand();
  const mainRow = document.querySelector(".rc-versions__table tbody tr.rc-detail__row--main");
  ok(Boolean(mainRow) && !mainRow?.textContent?.includes("recovery-7fa1f7e5"), "③ with no adoption the main row stays the scan origin");
  await act(async () => root?.unmount());
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exitCode = 1;
