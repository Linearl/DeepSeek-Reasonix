// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1478-trajectory-interaction.test.tsx
// tl 1478 acceptance harness — trajectory viewer interaction hardening (data
// layer untouched; pure UI):
//   ① sticky summary head: the head lives inside the scroll container as its
//     first child and pins via CSS `position: sticky; top: 0` (滚动固定),
//     inspector panel stacks above it;
//   ② batch expand/collapse: turn/calls summary rows toggle individually
//     (aria-expanded, keyboard) overriding the global collapse toggles, and the
//     window gains a one-shot "expand all remaining turns" control with scroll
//     anchoring;
//   ③ category filter menu: menu entries are generated from the windowed
//     records' kinds (open-ended enumeration — 并行线新增 kind 自动出现，无
//     硬编码枚举), hiding a kind removes its ledger rows and dims its timeline
//     spans, unknown kinds fall back to the raw kind label;
//   ④ thousand-scale render pin (千条级): 1600 records stay windowed to the
//     tail 12 turns (bounded DOM), full projection data-layer passes stay
//     linear-and-fast, and the expand-all path renders the full set within a
//     generous runaway ceiling. jsdom loads before react-dom; real react-dom
//     renders drive the interaction assertions.
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import type { Item } from "../lib/useController";
import type { Translator } from "../lib/i18n";
import type { TrajectoryKind } from "../lib/trajectoryLedger";
import { buildTrajectoryLedger } from "../lib/trajectoryLedger";
import { buildTrajectorySequenceTimeline } from "../lib/trajectoryTimeline";
import { TrajectoryView, projectTrajectoryRows, trajectoryKindLabel } from "../components/trajectoryViewBody";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

console.log("\ntl 1478 trajectory viewer interaction hardening");

// ── jsdom globals (before any react-dom render) ──────────────────────────────
const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true });
(globalThis as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;

const t = ((key: string) => key) as Translator;

function user(id: string, text: string, extra: Partial<Extract<Item, { kind: "user" }>> = {}): Item {
  return { kind: "user", id, text, ...extra };
}
function assistant(id: string, extra: Partial<Extract<Item, { kind: "assistant" }>> = {}): Item {
  return { kind: "assistant", id, text: extra.text ?? `answer-${id}`, reasoning: "", streaming: false, ...extra };
}
function tool(id: string, extra: Partial<Extract<Item, { kind: "tool" }>> = {}): Item {
  return { kind: "tool", id, name: extra.name ?? "bash", args: "", readOnly: false, status: "done", ...extra };
}

/** Deterministic fixture: `turns` turns × (1 user + perTurn×(assistant+tool)). */
function makeItems(turns: number, perTurn: number): Item[] {
  const items: Item[] = [];
  for (let turn = 1; turn <= turns; turn += 1) {
    items.push(user(`u${turn}`, `question ${turn}`, { createdAt: turn * 10_000, historyTurn: turn }));
    for (let step = 1; step <= perTurn; step += 1) {
      items.push(assistant(`a${turn}-${step}`, { createdAt: turn * 10_000 + step, workDurationMs: 120 }));
      items.push(tool(`t${turn}-${step}`, { startedAt: turn * 10_000 + step + 1, durationMs: 40, summary: `step ${turn}/${step}` }));
    }
  }
  return items;
}

type Mounted = { container: HTMLElement; unmount: () => Promise<void> };
async function mountView(items: Item[]): Promise<Mounted> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(<TrajectoryView items={items} t={t} />);
  });
  return {
    container,
    unmount: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

function clickIn(view: Mounted, selector: string, text?: string): boolean {
  const nodes = [...view.container.querySelectorAll<HTMLElement>(selector)];
  const node = text ? nodes.find((n) => (n.textContent ?? "").includes(text)) : nodes[0];
  if (!node) return false;
  act(() => { node.click(); });
  return true;
}

const here = path.dirname(fileURLToPath(import.meta.url));
const cssSrc = fs.readFileSync(path.join(here, "..", "components", "TrajectoryView.css"), "utf8");
function cssBlock(css: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return css.match(new RegExp(`(?:^|\\n)${escaped}\\s*\\{([^}]*)\\}`))?.[1] ?? "";
}

// ── ① sticky summary head ────────────────────────────────────────────────────
{
  const view = await mountView(makeItems(3, 1));
  const scroll = view.container.querySelector(".traj__scroll");
  ok(scroll != null, "scroll container mounts");
  const head = scroll?.firstElementChild;
  ok(head != null && head.classList.contains("traj__head"), "summary head is the first child inside the scroll container (sticky scope)");
  ok(head?.querySelector(".traj__head-meta") != null, "head carries the summary meta line");
  const headBlock = cssBlock(cssSrc, ".traj__head");
  ok(/position:\s*sticky/.test(headBlock) && /top:\s*0/.test(headBlock), "head pins via position: sticky; top: 0 (滚动固定)");
  ok(/background:\s*var\(--chat-bg\)/.test(headBlock), "pinned head paints an opaque background (rows scroll underneath)");
  const inspectorBlock = cssBlock(cssSrc, ".traj-inspector");
  ok(/z-index:\s*5/.test(inspectorBlock), "inspector panel stacks above the sticky head (z-index contract)");
  const toggleRowBlock = cssBlock(cssSrc, ".traj-row--toggle");
  ok(toggleRowBlock !== "" && /cursor:\s*pointer/.test(toggleRowBlock), "toggle summary rows carry the interactive cursor contract");
  ok(cssBlock(cssSrc, ".traj__filter-menu") !== "" && cssBlock(cssSrc, ".traj__filter-item") !== "",
    "filter menu classes have real CSS definitions (class-contract: JSX 参照必有定义)");
  await view.unmount();
}

// ── ② batch expand/collapse ──────────────────────────────────────────────────
{
  const items = makeItems(2, 2);
  const view = await mountView(items);
  // Defaults: everything expanded, no summary rows.
  ok(view.container.querySelectorAll(".traj-row--turn").length === 0, "default state renders no turn summary rows");
  // Batch collapse turns via the toolbar toggle; per-row expansion overrides it.
  ok(clickIn(view, ".traj__toggle", "trajectory.toolbar.turns"), "turns batch toggle clickable");
  const turnRows = [...view.container.querySelectorAll<HTMLElement>(".traj-row--turn")];
  ok(turnRows.length === 2 && turnRows.every((row) => row.getAttribute("aria-expanded") === "false"),
    "batch collapse folds each turn into an aria-expanded=false summary row");
  act(() => { turnRows[0].click(); });
  const expandedRow = view.container.querySelector(".traj-row--turn[aria-expanded='true']");
  ok(expandedRow != null, "clicking one summary row expands only that turn (单项覆盖批量)");
  ok(view.container.querySelector("[data-record-id='a1-1']") != null, "expanded turn reveals its assistant records");
  ok(view.container.querySelector("[data-record-id='a2-1']") == null, "the other turn stays collapsed");
  const expandGlyph = expandedRow?.querySelector(".traj-row__expand");
  ok(expandGlyph?.textContent === "−", "expanded summary row flips the +/− glyph");
  // Keyboard toggling mirrors click.
  const collapsedRow = view.container.querySelector<HTMLElement>(".traj-row--turn[aria-expanded='false']");
  if (collapsedRow) act(() => { collapsedRow.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });
  ok(view.container.querySelectorAll(".traj-row--turn[aria-expanded='true']").length === 2,
    "Enter key toggles a summary row (keyboard parity)");
  // Global toggle reset: flipping batch collapse off then on clears overrides.
  ok(clickIn(view, ".traj__toggle", "trajectory.toolbar.turns"), "batch toggle off clickable");
  ok(clickIn(view, ".traj__toggle", "trajectory.toolbar.turns"), "batch toggle on clickable");
  ok(view.container.querySelectorAll(".traj-row--turn[aria-expanded='true']").length === 0,
    "re-enabling batch collapse clears per-row overrides (语义回到全部收起)");
  // Calls group: same contract (this fixture has two assistant groups per turn).
  // The calls fold is verified with turns expanded — with both toggles on the
  // outer turn fold wins and swallows the call-group rows by design.
  ok(clickIn(view, ".traj__toggle", "trajectory.toolbar.turns"), "turns batch toggle off (calls 段在轮次展开态下验证)");
  ok(clickIn(view, ".traj__toggle", "trajectory.toolbar.calls"), "calls batch toggle clickable");
  const callsRows = [...view.container.querySelectorAll<HTMLElement>(".traj-row--calls")];
  ok(callsRows.length === 4 && callsRows.every((row) => row.getAttribute("aria-expanded") === "false"),
    "batch collapse folds each assistant's trailing calls into an aria-expanded=false summary row");
  act(() => { callsRows[0].click(); });
  ok(view.container.querySelector(".traj-row--calls[aria-expanded='true']") != null,
    "expanded calls group keeps its header row visible (persistent collapse affordance)");
  ok(view.container.querySelector("[data-record-id='t1-1']") != null, "expanded calls group reveals its tool records");
  ok(view.container.querySelector("[data-record-id='t2-1']") == null, "the other calls group stays collapsed");
  await view.unmount();
}

// ── ③ expand-all window control ──────────────────────────────────────────────
{
  const items = makeItems(20, 2);
  const view = await mountView(items);
  const buttons = [...view.container.querySelectorAll<HTMLElement>(".traj__load-older")];
  ok(buttons.length === 2, "window row offers stepped expand + expand-all");
  const stepLabel = buttons[0]?.textContent ?? "";
  const allLabel = buttons[1]?.textContent ?? "";
  ok(stepLabel.includes("trajectory.loadOlder.window") && allLabel.includes("trajectory.loadOlder.all"),
    "labels ride the i18n keys (window / all)");
  act(() => { buttons[1].click(); });
  const ledger = buildTrajectoryLedger(items);
  ok(view.container.querySelectorAll(".traj-row").length === ledger.records.length,
    "expand-all renders the whole window in one shot (全部展开)");
  ok(view.container.querySelectorAll(".traj__load-older-row").length === 0, "expand-all exhausts the window controls");
  await view.unmount();
}

// ── ④ category filter menu (open-ended enumeration) ─────────────────────────
{
  const items: Item[] = [
    user("u1", "fix the bug", { createdAt: 1000 }),
    assistant("a1", {}),
    tool("t1", { name: "grep", summary: "matches" }),
    { kind: "notice", id: "n1", level: "info", text: "heads up" } as Item,
    user("u2", "thanks", { createdAt: 2000 }),
    assistant("a2", {}),
  ];
  const view = await mountView(items);
  ok(clickIn(view, ".traj__filter-trigger"), "filter trigger opens the menu");
  const menu = view.container.querySelector(".traj__filter-menu");
  ok(menu != null && menu.getAttribute("role") === "menu", "filter menu mounts as role=menu");
  const checkboxes = [...menu?.querySelectorAll<HTMLElement>("[role='menuitemcheckbox']") ?? []];
  const kindsFromData = buildTrajectoryLedger(items).records.map((record) => record.kind);
  const uniqueKinds = [...new Set(kindsFromData.map((kind) => String(kind)))];
  ok(checkboxes.length === uniqueKinds.length,
    `menu enumerates kinds from the data (${checkboxes.length} entries = ${uniqueKinds.length} distinct kinds, no hardcoded list)`);
  ok(uniqueKinds.every((kind) => checkboxes.some((box) => (box.textContent ?? "").includes(kind))),
    "every data kind appears in the menu (开放式枚举)");
  ok(checkboxes.every((box) => box.getAttribute("aria-checked") === "true"), "all categories start visible");
  // Hide the tool kind: ledger rows drop, timeline dims, count survives. The
  // menu stays open across checkbox clicks (deliberate — multi-toggle flow).
  const toolItem = checkboxes.find((box) => (box.textContent ?? "").includes("tool"));
  if (toolItem) act(() => { toolItem.click(); });
  ok(view.container.querySelectorAll(".traj-row--tool").length === 0, "hiding a kind removes its ledger rows");
  ok(view.container.querySelector("[data-record-id='t1']") == null, "hidden tool record leaves the ledger");
  ok(view.container.querySelectorAll(".traj-row--assistant").length === 2, "other kinds stay visible");
  const dimmedSpans = view.container.querySelectorAll(".traj-timeline__span--tool.traj-timeline__span--filter-dim");
  ok(dimmedSpans.length >= 1, "timeline spans of hidden kinds dim (filter-dim 联动)");
  ok((view.container.querySelector(".traj__filter-trigger")?.textContent ?? "").includes("· 1"),
    "trigger shows the hidden-kind count badge");
  // Backdrop click closes the still-open menu.
  const backdrop = view.container.querySelector(".traj__filter-backdrop");
  if (backdrop) act(() => { (backdrop as HTMLElement).click(); });
  ok(view.container.querySelector(".traj__filter-menu") == null, "backdrop click closes the menu");
  // Re-open and clear via the "all" radio; it stays open too.
  act(() => { view.container.querySelector<HTMLElement>(".traj__filter-trigger")?.click(); });
  const allItem = view.container.querySelector<HTMLElement>("[role='menuitemradio']");
  ok(allItem?.getAttribute("aria-checked") === "false", "『全部类目』radio flips when a kind is hidden");
  if (allItem) act(() => { allItem.click(); });
  ok(view.container.querySelectorAll(".traj-row--tool").length === 1, "『全部类目』restores the hidden kind");
  // Hiding every kind (menu still open) lands on the honest empty state.
  for (const kind of uniqueKinds) {
    const target = [...view.container.querySelectorAll<HTMLElement>("[role='menuitemcheckbox']")].find((n) => (n.textContent ?? "").includes(kind));
    if (target) act(() => { target.click(); });
  }
  const empty = view.container.querySelector(".traj__empty");
  ok(empty?.textContent === "trajectory.filter.empty", "all-hidden state shows the honest empty note");
  await view.unmount();
}

// ── ⑤ open-ended kind contract (tl1427 扩展位) ───────────────────────────────
{
  ok(trajectoryKindLabel("promptEnhance", t) === "promptEnhance",
    "unknown kind label falls back to the raw kind string (no crash, no hardcoded enum)");
  const synthetic = {
    id: "pe1", index: 0, kind: "promptEnhance" as string as TrajectoryKind, turn: 1, turnStart: false, step: 0,
    atKnown: false, failed: false, nested: false, running: false, title: "promptEnhance", preview: "enhanced",
  };
  const hidden = projectTrajectoryRows([synthetic], {
    equalWidth: true, collapsedTurns: false, collapsedCalls: false, searchMatchIds: null, focusIds: null,
    hiddenKinds: new Set<string>(["promptEnhance"]),
  });
  ok(hidden.length === 0, "unknown kinds filter exactly like known kinds (hiddenKinds 开放成员)");
  const visible = projectTrajectoryRows([synthetic], {
    equalWidth: true, collapsedTurns: false, collapsedCalls: false, searchMatchIds: null, focusIds: null,
    hiddenKinds: null,
  });
  ok(visible.length === 1 && visible[0].type === "record", "unknown kinds render normally when not hidden");
  // Persistent-header counting guard: an expanded turn that contains an
  // expanded calls group counts the group's tools once (as records), not twice.
  const nested = makeItems(2, 2);
  const nestedLedger = buildTrajectoryLedger(nested);
  const nestedRows = projectTrajectoryRows(nestedLedger.records, {
    equalWidth: true, collapsedTurns: true, collapsedCalls: true, searchMatchIds: null, focusIds: null,
    expandedTurns: new Set<number>([1]),
  });
  const expandedHeaders = nestedRows.filter((row) => row.type === "turn" && row.expanded);
  ok(expandedHeaders.length === 1, "exactly one expanded turn header survives the fold");
  if (expandedHeaders.length === 1 && expandedHeaders[0].type === "turn") {
    // The fixture lays down 2 assistants + 2 tools per turn; the two expanded
    // calls headers must not double-add their (single) calls.
    ok(expandedHeaders[0].steps === 2 && expandedHeaders[0].tools === 2,
      "expanded turn header counts each tool once (展开态 calls 组头不重复累加)");
  }
  const headerAfterCollapse = nestedRows.filter((row) => row.type === "turn" && !row.expanded);
  ok(headerAfterCollapse.length === 1 && headerAfterCollapse[0].type === "turn" && headerAfterCollapse[0].turn === 2,
    "the other turn stays folded with its collapsed header");
}

// ── ⑥ thousand-scale render pin (千条级) ─────────────────────────────────────
{
  const items = makeItems(160, 5); // 160 × 11 = 1760 records (千条级)
  const ledger = buildTrajectoryLedger(items);
  ok(ledger.records.length === 1760, "fixture clears the thousand-record scale");
  const projectionStart = performance.now();
  const fullRows = projectTrajectoryRows(ledger.records, {
    equalWidth: true, collapsedTurns: false, collapsedCalls: false, searchMatchIds: null, focusIds: null,
  });
  buildTrajectorySequenceTimeline(ledger.records, "sequence");
  buildTrajectorySequenceTimeline(ledger.records, "duration");
  const projectionMs = performance.now() - projectionStart;
  ok(fullRows.length === ledger.records.length, "full projection covers every record (数据层钉不回归)");
  ok(projectionMs < 500, `data-layer passes stay linear-and-fast on 1760 records (${projectionMs.toFixed(1)}ms < 500ms)`);

  const mountStart = performance.now();
  const view = await mountView(items);
  const mountMs = performance.now() - mountStart;
  const rowCount = view.container.querySelectorAll(".traj-row").length;
  const windowedRows = 12 * (1 + 5 + 5); // tail 12 turns × (user + assistant + tool)
  ok(rowCount === windowedRows, `windowing bounds the DOM: ${rowCount} rows == tail-12-turn window (${windowedRows})`);
  ok(rowCount < ledger.records.length, "thousand-scale session does not render unwindowed (分页行为钉)");
  ok(mountMs < 5000, `windowed mount of 1760 records stays under the runaway ceiling (${mountMs.toFixed(0)}ms < 5000ms)`);
  const expandAll = view.container.querySelectorAll<HTMLElement>(".traj__load-older")[1];
  ok(expandAll != null, "expand-all control present at thousand scale");
  const expandStart = performance.now();
  act(() => { expandAll.click(); });
  const expandMs = performance.now() - expandStart;
  ok(view.container.querySelectorAll(".traj-row").length === ledger.records.length,
    "expand-all renders all 1760 rows");
  ok(expandMs < 5000, `full-set expand-all commit stays under the runaway ceiling (${expandMs.toFixed(0)}ms < 5000ms)`);
  await view.unmount();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (!failed) process.stdout.write("tl 1478 trajectory interaction hardening: OK\n");
