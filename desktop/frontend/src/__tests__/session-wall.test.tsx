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
const React = await import("react");
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

process.stdout.write(`\nsession-wall: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
