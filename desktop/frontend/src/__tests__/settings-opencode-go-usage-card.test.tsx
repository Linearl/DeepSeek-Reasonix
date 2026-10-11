// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/settings-opencode-go-usage-card.test.tsx
//
// 任务 800 — the settings-side OpenCode Go usage row is a plain member
// switch: label/hint copy on the left, the on/off segment on the right (the
// same layout as every other member row of a merged card). The in-card usage
// detail (three window rows, refresh, degradation notes) is retired — the
// right-dock sidebar card (783) is the authoritative display and owns the
// 163 "switch off = zero requests" gate. Zero skips: every state must render
// without throwing.

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

// 任务 800：设置卡内零网络——无论开关态，这里都不该有任何查询。
// （163 门已整体移到侧栏卡 OpenCodeGoUsageSidebarCard。）
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

async function renderCard(state: { enabled: boolean; busy?: boolean; onToggle?: (on: boolean) => void }) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <SettingsOpenCodeGoUsageCard
          enabled={state.enabled}
          busy={state.busy ?? false}
          onToggle={state.onToggle ?? (() => {})}
        />
      </LocaleProvider>,
    );
  });
  return {
    field: container.querySelector(".settings-field"),
    label: container.querySelector(".settings-field__label"),
    hint: container.querySelector(".settings-field__hint"),
    seg: container.querySelector(".set-seg"),
    buttons: [...container.querySelectorAll<HTMLButtonElement>(".set-seg__btn")],
    dispose: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

console.log("\nopencode-go settings usage row (task 800: plain member switch)");

// 1. on state — copy left, switch right, current segment highlighted.
{
  const view = await renderCard({ enabled: true });
  ok(view.field !== null, "on: the settings field renders");
  ok(view.label?.textContent?.includes("OpenCode Go") ?? false, "on: the label copy renders");
  ok(view.hint !== null && (view.hint.textContent ?? "").length > 0, "on: the hint copy renders beside the switch");
  ok(view.seg !== null && view.buttons.length === 2, "on: the on/off segment renders two buttons");
  ok(view.buttons[1]?.classList.contains("set-seg__btn--on") ?? false, "on: the second segment (开) is highlighted");
  // 明细 DOM 全量负断言——权威展示位在右侧边栏卡。
  ok(container_negative(view), "on: no in-card detail DOM (no card body, no window rows, no refresh)");
  ok(queries === 0, `on: zero network calls from the settings card (${queries})`);
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

// 2. off state — same row, first segment highlighted, still zero queries.
{
  const before = queries;
  const view = await renderCard({ enabled: false });
  ok(view.field !== null, "off: the settings field still renders (switch row is always visible)");
  ok(view.buttons[0]?.classList.contains("set-seg__btn--on") ?? false, "off: the first segment (关) is highlighted");
  ok(queries === before, `off: zero network calls (${queries - before})`);
  await view.dispose();
}

// 3. toggle wiring — clicking a segment calls onToggle with that value; busy
//    disables the segments (the setter stays the pane's apply() call).
{
  const seen: boolean[] = [];
  const view = await renderCard({
    enabled: false,
    onToggle: (on) => seen.push(on),
  });
  await act(async () => {
    view.buttons[1]?.click();
  });
  ok(JSON.stringify(seen) === "[true]", `clicking 开 forwards onToggle(true) (${JSON.stringify(seen)})`);
  await view.dispose();

  const busyView = await renderCard({ enabled: true, busy: true });
  ok(busyView.buttons.every((b) => b.disabled), "busy: both segments are disabled");
  await busyView.dispose();
}

assert.ok(passed >= 10, `expected at least 10 checks, got ${passed}`);
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
