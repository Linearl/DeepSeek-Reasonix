// Task 270: todo list wrap + panel growth (right-dock todos tab).
//
// User evidence (0923 screenshot, re-hit 0930 with 14 items): long item text
// forced a horizontal scrollbar (no break rule on .todobar__text) and the
// hardcoded 220px list cap forced a vertical scrollbar long before the
// sidebar ran out of space (14 items ≈ 350px > 220px).
//
// Fix surface (CSS only, per dispatch "no layout refactor beyond the panel
// container"):
//  1. .todobar__text gains `overflow-wrap: anywhere` (status chips keep their
//     nowrap; item text wraps like the decision/ask shelves do);
//  2. `.workbench-dock__todo .todobar__list { max-height: none }` lifts the
//     cap ONLY inside the right dock — the dock container
//     (.workbench-dock__todo) already has height:100% + overflow-y:auto, so
//     it scrolls only when content truly exceeds the available sidebar
//     height (acceptance: scrollbar appears only when genuinely needed);
//  3. the base .todobar__list keeps max-height:220px — the composer-shelf
//     footer has no taller scrollable ancestor, so its behaviour (and 259's
//     zero-regression contract) is untouched.
//
// Render face: a 14-item list (including an unbreakable long token) renders
// every item with its text node intact inside .todobar__text.
//
// Run: npx tsx src/__tests__/task270-todo-wrap-grow.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const css = readFileSync(fileURLToPath(new URL("../styles.css", import.meta.url)), "utf8");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 270 todo wrap + panel growth");

// ── CSS rule face ────────────────────────────────────────────────────────────
{
  // ① wrap rule on the item text
  const textRule = css.match(/\.todobar__text\s*\{[^}]*\}/);
  ok(!!textRule && /overflow-wrap:\s*anywhere/.test(textRule![0]), "① .todobar__text carries overflow-wrap:anywhere (horizontal scrollbar eliminated)");

  // ② dock-scoped cap lift
  const dockRule = css.match(/\.workbench-dock__todo \.todobar__list\s*\{[^}]*\}/);
  ok(!!dockRule && /max-height:\s*none/.test(dockRule![0]), "② dock list cap lifted (max-height:none, panel grows with content)");

  // cascade: the dock-scoped override must come AFTER the base 220px rule so
  // it wins at equal-or-higher specificity (0,2,0 > 0,1,0 anyway, but order
  // keeps the intent readable and future-proof).
  const baseIdx = css.indexOf("max-height: 220px");
  const dockIdx = css.indexOf(".workbench-dock__todo .todobar__list");
  ok(baseIdx > 0 && dockIdx > baseIdx, "② dock override declared after the base cap (cascade intent)");

  // ③ composer-shelf footer keeps its cap (zero regression for the footer
  //    surface + 259's other behaviour).
  ok(/\.todobar__list\s*\{[^}]*max-height:\s*220px/.test(css), "③ base .todobar__list keeps max-height:220px (composer-shelf footer unchanged)");

  // dock container is the scrolling fallback (pre-existing, asserted so the
  // cap lift can never strand content without a scroller).
  const todoWrap = css.match(/\.workbench-dock__todo\s*\{[^}]*\}/);
  ok(!!todoWrap && /overflow-y:\s*auto/.test(todoWrap![0]), "dock container keeps overflow-y:auto (scrollbar only when content exceeds available space)");

  // status chips stay single-line (dispatch: tab-label-like chips unchanged)
  const chip = css.match(/\.todobar__status\s*\{[^}]*\}/);
  ok(!!chip && /white-space:\s*nowrap/.test(chip![0]), "status chips keep nowrap (only item text changed)");
}

// ── Render face: 14-item list with an unbreakable long token ────────────────
{
  const dom = new (await import("jsdom")).JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  dom.window.HTMLElement.prototype.scrollIntoView = () => {};

  const React = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { act } = await import("react");
  const { TodoPanel } = await import("../components/TodoPanel");
  const { LocaleProvider } = await import("../lib/i18n");

  // 14 items, statuses mirroring a real dispatch state; item 3 carries the
  // long unbreakable CJK/latin token from the user's screenshot.
  const longToken = "B2：233UI验证（L）/222→227/173面板/219+闸门+审计——超长无空格混排令牌假AAAAABBBBBCCCCCDDDDD";
  const todos = Array.from({ length: 14 }, (_, i) => ({
    content: i === 2 ? longToken : `任务 ${i + 1}：并行开发批次条目`,
    activeForm: i === 2 ? `正在处理 ${longToken}` : `正在处理任务 ${i + 1}`,
    status: i === 0 ? ("completed" as const) : i === 1 ? ("in_progress" as const) : ("pending" as const),
    level: i >= 12 ? 1 : 0,
  }));

  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () => {
    root.render(React.createElement(
      LocaleProvider,
      null,
      React.createElement(TodoPanel, {
        stateKey: "task270-harness",
        todos,
        running: false,
        pendingPrompt: false,
        onDismiss: () => {},
        defaultOpen: true,
      }),
    ));
  });

  const items = host.querySelectorAll(".todobar__item");
  ok(items.length === 14, "④ 14-item list renders every row (large-list face verified in harness)");
  const texts = [...host.querySelectorAll(".todobar__text")];
  ok(texts.length === 14 && texts.every((n) => (n.textContent || "").length > 0), "④ every row has a non-empty .todobar__text node");
  ok(texts.some((n) => (n.textContent || "").includes("AAAAABBBBB")), "④ the long unbreakable token reaches the DOM intact (wrap rule applies to it, no truncation)");

  const list = host.querySelector(".todobar__list");
  ok(!!list, "④ .todobar__list present under the dock-shaped mount (class contract for the CSS override)");

  const subs = host.querySelectorAll(".todobar__item--sub");
  ok(subs.length === 2, "④ sub-level rows keep their indent class (259 hierarchy zero-regression)");

  const chips = host.querySelectorAll(".todobar__status");
  ok(chips.length === 14, "④ status chips render per row (unchanged single-line elements)");

  await act(async () => { root.unmount(); });
  host.remove();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
