// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/settings-opencode-go-usage-card.test.tsx
//
// tl1487 四组 — the settings-side OpenCode Go usage row is a plain DISPLAY
// row (glmPlanUsage 同款形态): label + tier badge on the title, hint copy as
// the body. The switch (任务 800 的 on/off segment) and the query are both
// gone from the card — the always-on query lives entirely in the right-dock
// sidebar card (783, family-gated since 799). Zero skips: every state must
// render without throwing.

import assert from "node:assert/strict";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { JSDOM } from "jsdom";
import { SettingsOpenCodeGoUsageCard } from "../components/SettingsOpenCodeGoUsageCard";
import { LocaleProvider } from "../lib/i18n";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  IS_REACT_ACT_ENVIRONMENT: true,
});

// tl1487：设置卡内零网络——无论何态，这里都不该有任何查询。
let queries = 0;
Object.assign(window, {
  go: {
    main: {
      App: {
        GetOpenCodeGoUsage: async () => {
          queries += 1;
          return { tiers: [], note: "" };
        },
      },
    },
  },
});

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

async function renderCard() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <SettingsOpenCodeGoUsageCard />
      </LocaleProvider>,
    );
  });
  return {
    field: container.querySelector(".settings-field"),
    label: container.querySelector(".settings-field__label"),
    hint: container.querySelector(".settings-field__hint"),
    seg: container.querySelector(".set-seg"),
    badge: container.querySelector(".lab-tier-badge"),
    buttons: [...container.querySelectorAll<HTMLButtonElement>(".set-seg__btn")],
    dispose: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

console.log("\nopencode-go settings usage row (tl1487 四组: plain display row, no switch)");

// 1. display row — title + recommended badge + hint copy, NO toggle segment.
{
  const view = await renderCard();
  ok(view.field !== null, "the settings field renders");
  ok(view.label?.textContent?.includes("OpenCode Go") ?? false, "the label copy renders");
  ok(view.badge !== null, "the recommended tier badge renders on the row title (562 ⑤ contract)");
  ok(view.hint !== null && (view.hint.textContent ?? "").length > 0, "the hint copy renders as the row body");
  ok(view.seg === null && view.buttons.length === 0, "tl1487: no on/off segment — the switch row is retired");
  // 明细 DOM 全量负断言——权威展示位在右侧边栏卡。
  ok(container_negative(view), "no in-card detail DOM (no card body, no window rows, no refresh)");
  ok(queries === 0, `zero network calls from the settings card (${queries})`);
  await view.dispose();
}

// container_negative: the retired detail surfaces must not render anywhere.
function container_negative(view: Awaited<ReturnType<typeof renderCard>>) {
  const container = view.field?.closest("body") ?? null;
  if (container === null) return false;
  return (
    container.querySelector('[data-testid="opencode-go-usage-card"]') === null
    && container.querySelector(".opencode-go-usage") === null
    && container.querySelector("[data-window]") === null
    && container.querySelector(".opencode-go-usage__refresh") === null
  );
}

// 2. remount stability — a second mount renders the same display row with no
//    queries and no toggle (关闭态行为不变的替身：任何挂载态都不再有开关)。
{
  const before = queries;
  const view = await renderCard();
  ok(view.field !== null, "remount: the display row renders again");
  ok(view.buttons.length === 0, "remount: still no toggle segments");
  ok(queries === before, `remount: zero network calls (${queries - before})`);
  await view.dispose();
}

assert.ok(passed >= 8, `expected at least 8 checks, got ${passed}`);
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
