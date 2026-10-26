// Run: tsx src/__tests__/session-wall.test.tsx
// Task 505: the session graph wall behind ONE switch
// (experimental_session_wall, default off). Contract under test:
// - palette gate: with the switch off, insertSessionWallEntry returns the SAME
//   array reference (byte-for-byte palette, the 保底 recent-sessions slice);
//   with it on, the "跳转会话" entry lands right after "cmd-reload-runtime"
//   (its right-hand neighbour in the command grid) and appends when the anchor
//   is missing;
// - wall search: palette-style ordered token filter, empty query is a no-op;
// - grouping: by project (workspace buckets + one shared global bucket) and by
//   day (time mode), groups and cards ranked by most recent activity;
// - panel: ≥30 sessions render as a grid wall (200 rendered without timing
//   out), clicking a card jumps via onResume with that session, the search box
//   filters live, Esc closes, closed is null.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}
function ok(value: boolean, label: string) {
  eq(value, true, label);
}

const wall = await import("../lib/sessionWall");
const { SessionWallPanel } = await import("../components/SessionWallPanel");
const { LocaleProvider } = await import("../lib/i18n");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
import type { SessionMeta } from "../lib/types";

console.log("\nsession graph wall (task 505)");

const BASE = new Date(2026, 9, 5, 12, 0, 0).getTime(); // fixed local noon
let nextId = 0;
function session(overrides: Partial<SessionMeta> = {}): SessionMeta {
  nextId += 1;
  const i = nextId;
  return {
    path: `C:/ses/s${String(i).padStart(4, "0")}.jsonl`,
    preview: `Session ${i}`,
    turns: 1,
    createdAt: BASE - i * 60_000,
    lastActivityAt: BASE - i * 60_000,
    modTime: BASE - i * 60_000,
    current: false,
    open: false,
    ...overrides,
  };
}

// 1. Palette gate: off = same array (zero behaviour), on = inserted after the
//    anchor, missing anchor = append.
{
  const legacy = [
    { id: "cmd-new" },
    { id: "cmd-reload-runtime" },
    { id: "cmd-task-center" },
  ];
  const entry = { id: "cmd-session-wall" };
  eq(wall.insertSessionWallEntry(legacy, false, entry), legacy, "gate off: returns the identical array reference (palette byte-for-byte)");
  const on = wall.insertSessionWallEntry(legacy, true, entry);
  eq(on.length, 4, "gate on: one entry added");
  eq(on[2].id, "cmd-session-wall", "gate on: entry sits right after cmd-reload-runtime");
  eq(wall.insertSessionWallEntry([{ id: "cmd-new" }], true, entry)[1].id, "cmd-session-wall", "gate on: missing anchor appends instead of vanishing");
}

// 2. Wall search: ordered token filter, empty query is a no-op.
{
  const a = session({ preview: "修复登录超时", workspaceRoot: "C:/work/alpha" });
  const b = session({ preview: "研究 DAG 缓存", workspaceRoot: "C:/work/beta" });
  const latin = session({ preview: "Fix Login Timeout", workspaceRoot: "C:/work/gamma" });
  ok(wall.applySessionWallQuery([a, b], "  ").length === 2, "query blank: whitespace-only is a no-op (all sessions)");
  eq(wall.applySessionWallQuery([a, b], "dag").length, 1, "single token: matches one session");
  eq(wall.applySessionWallQuery([a, b], "dag 缓存").length, 1, "ordered tokens: both tokens in order");
  eq(wall.applySessionWallQuery([a, b], "缓存 dag").length, 0, "ordered tokens: reversed order does not match");
  eq(wall.applySessionWallQuery([a, b], "alpha").length, 1, "workspace root is searchable");
  eq(wall.applySessionWallQuery([latin], "LOGIN").length, 1, "match is case-insensitive");
}

// 3. Grouping by project: buckets, global bucket label, activity ranking.
{
  const old = session({ workspaceRoot: "C:/work/alpha", lastActivityAt: BASE - 3 * 3600_000 });
  const fresh = session({ workspaceRoot: "C:/work/alpha", lastActivityAt: BASE - 60_000 });
  const beta = session({ workspaceRoot: "C:/work/beta", lastActivityAt: BASE - 30_000 });
  const globalOld = session({ workspaceRoot: "", lastActivityAt: BASE - 6 * 3600_000 });
  const groups = wall.groupSessionsForWall([old, fresh, beta, globalOld], "project", { globalLabel: "全局会话", dayLabel: () => "day" });
  eq(groups.length, 3, "project mode: three buckets (alpha/beta/global)");
  eq(groups[0].label, "beta", "project mode: group with the newest session ranks first");
  eq(groups[0].sessions[0].path, beta.path, "project mode: newest session first inside its group");
  eq(groups[1].label, "alpha", "project mode: folder name as group label");
  eq(groups[1].sessions[0].path, fresh.path, "project mode: activity-desc inside the group");
  eq(groups[2].label, "全局会话", "project mode: shared global bucket uses the localized label");
}

// 4. Time mode: day buckets, descending day order, localized labels.
{
  const today = session({ lastActivityAt: BASE });
  const yesterday = session({ lastActivityAt: BASE - 86_400_000 });
  const older = session({ lastActivityAt: BASE - 5 * 86_400_000 });
  const groups = wall.groupSessionsForWall([older, today, yesterday], "time", {
    globalLabel: "全局会话",
    dayLabel: (ms) => (ms === today.lastActivityAt ? "today" : ms === yesterday.lastActivityAt ? "yesterday" : "older"),
  });
  eq(groups.length, 3, "time mode: one bucket per day");
  eq(groups.map((g) => g.label).join(","), "today,yesterday,older", "time mode: descending day order");
  eq(groups[0].sessions.length, 1, "time mode: sessions stay in their day bucket");
}

// 4b. tl 1175「仅已打开」filter: off = same array (zero behaviour), on =
//     keeps only open sessions. Persisted via sessionWallPrefs (localStorage).
{
  const openA = session({ preview: "开着 A", open: true });
  const openB = session({ preview: "开着 B", open: true });
  const closedC = session({ preview: "关着 C", open: false });
  const input = [openA, closedC];
  eq(wall.applySessionWallOpenOnly(input, false), input, "openOnly off: returns the identical array reference");
  const on = wall.applySessionWallOpenOnly([openA, openB, closedC], true);
  eq(on.length, 2, "openOnly on: closed sessions drop out");
  eq(on.every((s) => s.open), true, "openOnly on: every survivor is open");
}

// 4c. tl 1175 panel contract: the「跳转标签页」entry presets the filter
//     (initialOpenOnly), the toggle persists, and the off face keeps every
//     card (current behaviour unchanged).
{
  const sessions: SessionMeta[] = [
    session({ preview: "开着甲", workspaceRoot: "C:/work/alpha", open: true }),
    session({ preview: "开着乙", workspaceRoot: "C:/work/alpha", open: true }),
    session({ preview: "关着丙", workspaceRoot: "C:/work/alpha", open: false }),
  ];
  dom.window.localStorage.clear();
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const view = (props: Record<string, unknown> = {}) =>
    createElement(
      LocaleProvider,
      null,
      createElement(SessionWallPanel, { open: true, load: async () => sessions, onClose: () => {}, onResume: () => {}, ...props }),
    );

  // Preset on (跳转标签页 face): only open sessions render as cards, and the
  // wall is still the grouped grid (not a second list surface).
  await act(async () => {
    root.render(view({ initialOpenOnly: true }));
  });
  eq(container.querySelectorAll(".session-wall__card").length, 2, "openOnly preset: only the 2 open sessions render as cards");
  ok(container.querySelectorAll(".session-wall__grid").length >= 1, "openOnly preset: the surface is still the grid wall");
  const filterBtn = container.querySelector(".session-wall__filter") as HTMLButtonElement;
  ok(filterBtn !== null, "openOnly preset: the filter toggle renders");
  eq(filterBtn?.getAttribute("aria-pressed"), "true", "openOnly preset: toggle reports pressed");

  // Toggling off restores the unfiltered wall (off face = current behaviour).
  await act(async () => {
    filterBtn.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  eq(container.querySelectorAll(".session-wall__card").length, 3, "openOnly toggled off: all 3 cards render (unchanged behaviour)");
  eq((container.querySelector(".session-wall__filter") as HTMLButtonElement)?.getAttribute("aria-pressed"), "false", "openOnly toggled off: toggle reports unpressed");
  eq(dom.window.localStorage.getItem("sessionWall:prefs:v1"), JSON.stringify({ openOnly: false }), "openOnly toggle: the off state persists to localStorage");

  // Reopen with no preset: the persisted value wins.
  await act(async () => {
    root.unmount();
  });
  container.remove();
  const container2 = document.createElement("div");
  document.body.appendChild(container2);
  const root2 = createRoot(container2);
  await act(async () => {
    root2.render(createElement(
      LocaleProvider,
      null,
      createElement(SessionWallPanel, { open: true, load: async () => sessions, onClose: () => {}, onResume: () => {} }),
    ));
  });
  eq(container2.querySelectorAll(".session-wall__card").length, 3, "openOnly reopen: persisted off state applies (3 cards)");

  // Preset on again writes through the manual toggle too: flipping the
  // pre-set filter on persists openOnly=true.
  const btn2 = container2.querySelector(".session-wall__filter") as HTMLButtonElement;
  await act(async () => {
    btn2.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  eq(container2.querySelectorAll(".session-wall__card").length, 2, "openOnly manual toggle on: closed sessions drop");
  eq(dom.window.localStorage.getItem("sessionWall:prefs:v1"), JSON.stringify({ openOnly: true }), "openOnly manual toggle: the on state persists");

  await act(async () => {
    root2.unmount();
  });
  container2.remove();
  dom.window.localStorage.clear();
}

// 4d. tl 1176 collapsible project groups: fold hides the group's cards but
//     keeps the count, persists across restarts, searching auto-expands hit
//     groups, and time mode has no fold heads at all.
{
  const sessions: SessionMeta[] = [
    session({ preview: "甲1", workspaceRoot: "C:/work/alpha", open: false }),
    session({ preview: "甲2", workspaceRoot: "C:/work/alpha", open: false }),
    session({ preview: "乙1", workspaceRoot: "C:/work/beta", open: false }),
  ];
  dom.window.localStorage.clear();
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const mount = async () => {
    await act(async () => {
      root.render(
        createElement(
          LocaleProvider,
          null,
          createElement(SessionWallPanel, { open: true, load: async () => sessions, onClose: () => {}, onResume: () => {} }),
        ),
      );
    });
  };
  await mount();
  eq(container.querySelectorAll(".session-wall__card").length, 3, "fold initial: 3 cards across 2 project groups");
  eq(container.querySelectorAll(".session-wall__group-head").length, 2, "fold initial: project mode renders 2 fold heads");

  // Fold the alpha group (first head): its 2 cards drop out, the count stays.
  const heads = () => [...container.querySelectorAll(".session-wall__group-head")] as HTMLButtonElement[];
  const alphaHead = heads().find((h) => h.textContent?.includes("alpha"));
  ok(alphaHead !== undefined, "fold: the alpha head exists");
  await act(async () => {
    alphaHead!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  eq(container.querySelectorAll(".session-wall__card").length, 1, "fold alpha: only beta's card remains");
  eq(alphaHead!.getAttribute("aria-expanded"), "false", "fold alpha: head reports collapsed");
  ok(alphaHead!.textContent!.includes("2"), "fold alpha: the collapsed row still shows the count (2)");
  eq(JSON.stringify(JSON.parse(dom.window.localStorage.getItem("sessionWall:prefs:v1")!).collapsed), JSON.stringify(["C:/work/alpha"]), "fold alpha: persists to localStorage");

  // Searching auto-expands hit groups: query matching an alpha session shows
  // its card despite the fold, and nothing is written back.
  const input = container.querySelector(".session-wall__input") as HTMLInputElement;
  const type = async (value: string) => {
    await act(async () => {
      // React controlled inputs need the native value setter — assigning
      // el.value directly is swallowed by React's value tracking.
      const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!;
      setter.call(input, value);
      input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
    });
  };
  await type("甲2");
  eq(container.querySelectorAll(".session-wall__card").length, 1, "search vs fold: the hit card in a folded group is visible");
  ok((container.querySelector(".session-wall__card") as HTMLElement).textContent!.includes("甲2"), "search vs fold: the visible card is the hit");
  eq(JSON.stringify(JSON.parse(dom.window.localStorage.getItem("sessionWall:prefs:v1")!).collapsed), JSON.stringify(["C:/work/alpha"]), "search vs fold: the fold memory is untouched");
  await type("");
  eq(container.querySelectorAll(".session-wall__card").length, 1, "search cleared: the persisted fold applies again (beta only)");

  // Time mode: no fold heads (day buckets are not foldable).
  const modeBtns = [...container.querySelectorAll(".session-wall__mode")] as HTMLButtonElement[];
  await act(async () => {
    modeBtns.find((b) => b.getAttribute("aria-pressed") === "false")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  eq(container.querySelectorAll(".session-wall__group-head").length, 0, "time mode: no fold heads");
  eq(container.querySelectorAll(".session-wall__card").length, 3, "time mode: all cards visible");

  // Restart persistence: a fresh root reads the fold back from prefs.
  await act(async () => {
    root.unmount();
  });
  container.remove();
  const container2 = document.createElement("div");
  document.body.appendChild(container2);
  const root2 = createRoot(container2);
  await act(async () => {
    root2.render(
      createElement(
        LocaleProvider,
        null,
        createElement(SessionWallPanel, { open: true, load: async () => sessions, onClose: () => {}, onResume: () => {} }),
      ),
    );
  });
  eq(container2.querySelectorAll(".session-wall__card").length, 1, "fold restart: the persisted fold applies (beta only)");
  const alphaHead2 = ([...container2.querySelectorAll(".session-wall__group-head")] as HTMLButtonElement[]).find((h) =>
    h.textContent?.includes("alpha"),
  );
  ok(alphaHead2 !== undefined, "fold restart: the alpha head exists");
  await act(async () => {
    alphaHead2!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  eq(container2.querySelectorAll(".session-wall__card").length, 3, "fold restart: re-expanding restores all 3 cards");
  eq(JSON.stringify(JSON.parse(dom.window.localStorage.getItem("sessionWall:prefs:v1")!).collapsed), JSON.stringify([]), "fold restart: the emptied fold set persists");

  await act(async () => {
    root2.unmount();
  });
  container2.remove();
  dom.window.localStorage.clear();
}

// 4e. tl 1177 hover excerpts: cards with recentExcerpts render the overlay
//     rows (newest first); cards without them render exactly as before.
{
  const sessions: SessionMeta[] = [
    session({ preview: "老会话", workspaceRoot: "C:/work/alpha", open: false }),
    session({
      preview: "新会话",
      workspaceRoot: "C:/work/alpha",
      open: false,
      recentExcerpts: ["结尾提问三", "结尾提问二", "结尾提问一"],
    }),
  ];
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      createElement(
        LocaleProvider,
        null,
        createElement(SessionWallPanel, { open: true, load: async () => sessions, onClose: () => {}, onResume: () => {} }),
      ),
    );
  });
  const cards = [...container.querySelectorAll(".session-wall__card")];
  eq(cards.length, 2, "excerpts: both cards render");
  const withExcerpts = cards.find((c) => c.querySelector(".session-wall__card-excerpts"));
  ok(withExcerpts !== undefined, "excerpts: the card with recentExcerpts renders the overlay");
  eq(withExcerpts!.querySelectorAll(".session-wall__card-excerpt").length, 3, "excerpts: all 3 rows render");
  eq(
    withExcerpts!.querySelector(".session-wall__card-excerpt")!.textContent,
    "结尾提问三",
    "excerpts: the first row is the newest message",
  );
  const bare = cards.find((c) => !c.querySelector(".session-wall__card-excerpts"));
  ok(bare !== undefined, "excerpts: the card without recentExcerpts has no overlay");
  ok(bare!.getAttribute("title") !== null, "excerpts: the bare card keeps its native title fallback");

  await act(async () => {
    root.unmount();
  });
  container.remove();
}

// 5. Panel: static smoke — the open wall renders its chrome (empty state,
//    because data loads in an effect; effects belong to the root below).
{
  const element = createElement(
    LocaleProvider,
    null,
    createElement(SessionWallPanel, { open: true, load: async () => [], onClose: () => {}, onResume: () => {} }),
  );
  const html = renderToStaticMarkup(element);
  ok(html.includes("session-wall"), "static render: the wall chrome renders while open");
  const css = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
  ok(/\.session-wall__card[^}]*content-visibility:\s*auto/.test(css), "stylesheet keeps the content-visibility paint guard for large walls");
}

// 6-9. Panel behaviours under a real root: load → cards, ≥30 sessions render
//      as the grid wall fast (acceptance ①, run at 200 = acceptance with
//      margin), click jumps with the clicked session, search filters live,
//      Esc closes, closed renders null.
{
  const bigWall: SessionMeta[] = [];
  for (let i = 0; i < 200; i += 1) {
    bigWall.push(session({
      preview: `任务会话 ${i} —— 会话图墙压样本`,
      workspaceRoot: i < 80 ? "C:/work/alpha" : i < 160 ? "C:/work/beta" : "",
      turns: (i % 9) + 1,
    }));
  }
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  let elapsed = -1;
  await act(async () => {
    const t0 = Date.now();
    root.render(
      createElement(
        LocaleProvider,
        null,
        createElement(SessionWallPanel, { open: true, load: async () => bigWall, onClose: () => {}, onResume: () => {} }),
      ),
    );
    elapsed = Date.now() - t0;
  });
  const bigCards = container.querySelectorAll(".session-wall__card").length;
  ok(bigCards >= 200, `wall renders all 200 session cards as a grid (${bigCards} card nodes, ${elapsed}ms)`);
  ok(bigCards >= 30 && elapsed < 5000, `≥30 sessions render without freezing (${bigCards} cards in ${elapsed}ms < 5000ms bound)`);
  ok(container.querySelectorAll(".session-wall__grid").length >= 2, "cards render inside the grid wall (not a list)");
  await act(async () => {
    root.unmount();
  });
  container.remove();
}

// 10-14. Interactive contract at 40 sessions: cards, click-to-jump, live
//        search, Esc close, closed renders null.
{
  const sessions: SessionMeta[] = [];
  for (let i = 0; i < 40; i += 1) {
    sessions.push(session({
      preview: i === 7 ? "独特关键词会议记录" : `普通会话 ${i}`,
      workspaceRoot: i < 20 ? "C:/work/alpha" : "C:/work/beta",
      open: i === 3,
    }));
  }
  const resumed: SessionMeta[] = [];
  let closed = 0;
  const load = async () => sessions;
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const view = (open: boolean) =>
    createElement(
      LocaleProvider,
      null,
      createElement(SessionWallPanel, {
        open,
        load,
        onClose: () => {
          closed += 1;
        },
        onResume: (s) => resumed.push(s),
      }),
    );
  await act(async () => {
    root.render(view(true));
  });
  const cardNodes = () => container.querySelectorAll(".session-wall__card");
  eq(cardNodes().length, 40, "panel: all 40 sessions render as cards");
  ok(container.querySelectorAll(".session-wall__group").length === 2, "panel: grouped into two project buckets");

  // Click jumps straight into the clicked session (including never-opened
  // ones) — the wall hands the session to onResume; the parent closes + navigates.
  const target = cardNodes()[13] as HTMLElement;
  await act(async () => {
    target.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  eq(resumed.length, 1, "click: onResume fires exactly once");
  eq(resumed[0]?.path, sessions[13]?.path, "click: the clicked session is the jump target");
  eq(resumed[0]?.open, false, "click: works for a session that is not currently open");

  // Search filters live (input event on the search box).
  const input = container.querySelector(".session-wall__input") as HTMLInputElement;
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
    setter?.call(input, "独特关键词");
    input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  });
  eq(container.querySelectorAll(".session-wall__card").length, 1, "search: live filter narrows the wall to matches");

  // Reset the query, then Esc closes.
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
    setter?.call(input, "");
    input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  });
  eq(container.querySelectorAll(".session-wall__card").length, 40, "search: clearing the query restores the wall");
  await act(async () => {
    document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  });
  eq(closed, 1, "esc: closes the wall");

  // Closed (open=false) renders nothing — the off/zero-behaviour face. The
  // mount transition keeps the node alive for the 200ms exit animation first,
  // so wait past the timer before asserting the unmount.
  await act(async () => {
    root.render(view(false));
  });
  eq(container.querySelectorAll(".session-wall").length <= 1, true, "closing: at most the exit-animation node remains");
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  eq(container.querySelectorAll(".session-wall").length, 0, "closed: wall renders null (no surface without the switch)");

  await act(async () => {
    root.unmount();
  });
  container.remove();
}

// 15. Task 627: SessionWallBoundary — a wall-sized render crash is contained:
//     the boundary renders null (wall only), reports the crash, and the app
//     around it keeps other children alive.
{
  const { SessionWallBoundary } = await import("../components/SessionWallBoundary");
  const consoleError = console.error;
  console.error = () => {}; // React logs the caught error; keep the run output clean.
  try {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    function Bomb(): never {
      throw new Error("wall render bomb (627 test)");
    }
    await act(async () => {
      root.render(
        createElement(
          "div",
          null,
          createElement("span", { id: "survivor" }, "app"),
          createElement(SessionWallBoundary, null, createElement(Bomb)),
        ),
      );
    });
    ok(container.querySelector("#survivor") !== null, "boundary: sibling content outside the wall survives a wall crash");
    ok(container.querySelectorAll(".session-wall").length === 0, "boundary: crashed wall renders nothing (no crash bleed)");
    await act(async () => {
      root.unmount();
    });
    container.remove();
  } finally {
    console.error = consoleError;
  }
}

process.stdout.write(`\nsession-wall: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
