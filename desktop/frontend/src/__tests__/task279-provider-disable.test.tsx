// Task 279 acceptance (provider enable/disable switch):
//  1. enumeration assertion — a disabled (hidden) provider's models appear in
//     NO model picker: ModelPicker options (subagent/any settings picker) and
//     allRefs (the ref list feeding selects) both exclude it;
//  2. zero regression — with every provider enabled the outputs equal the
//     unfiltered baseline (iron-rule 2);
//  3. wiring guards — the connections list carries the toggle row and the
//     provider_toggle change kind end-to-end (union + SettingsPanel + mock
//     bridge), and the heartbeat picker's existing hidden filter is kept.
//
// Run: npx tsx src/__tests__/task279-provider-disable.test.tsx

import { JSDOM } from "jsdom";
import React, { act } from "react";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage,
  Node: dom.window.Node, HTMLElement: dom.window.HTMLElement,
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
  cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
  IS_REACT_ACT_ENVIRONMENT: true,
});
(window as unknown as { matchMedia: unknown }).matchMedia = () => ({ matches: true, addEventListener() {}, removeEventListener() {} });

const { createRoot } = await import("react-dom/client");
const { ModelPicker, allRefs } = await import("../components/SettingsPanel");
const { LocaleProvider } = await import("../lib/i18n");

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const mkProvider = (name: string, hidden: boolean) => ({
  name, displayName: `Connection ${name}`, kind: "openai", models: ["m-one", "m-two"],
  keySet: true, apiKeyEnv: "KEY", builtIn: true, added: true, hidden,
});
const settingsWith = (providers: unknown[]) => ({ providers } as never);

// ── allRefs (feeds every <select> of refs: defaults/subagents/…) ──────────
{
  const visible = allRefs(settingsWith([mkProvider("live", false), mkProvider("off", true)])) as string[];
  ok(!visible.some((r) => r.startsWith("off/")), "allRefs: disabled provider has zero refs");
  ok(visible.includes("live/m-one") && visible.includes("live/m-two"), "allRefs: enabled provider refs present");

  // Zero regression: nothing hidden → same as the raw product of providers × models.
  const allOn = allRefs(settingsWith([mkProvider("a", false), mkProvider("b", false)])) as string[];
  assert.deepEqual(allOn, ["a/m-one", "a/m-two", "b/m-one", "b/m-two"]);
  ok(true, "allRefs: all-enabled output equals unfiltered baseline (zero regression)");
}

// ── ModelPicker options (the settings picker used by subagents etc.) ──────
{
  const root = createRoot(document.getElementById("root")!);
  const render = async (providers: unknown[]) => {
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ModelPicker s={settingsWith(providers)} refs={["live/m-one", "live/m-two", "off/m-one"]} value="live/m-one" disabled={false} onPick={() => {}} />
        </LocaleProvider>,
      );
    });
    await act(async () => ((document.querySelector(".settings-model-picker .settings-select") as HTMLButtonElement).click()));
    const labels = Array.from(document.querySelectorAll('[role="option"]')).map((n) => n.textContent ?? "");
    await act(async () => { (document.querySelector(".settings-select-menu, [role=listbox], .settings-select") as HTMLElement | null)?.click(); });
    return labels;
  };

  const mixed = await render([mkProvider("live", false), mkProvider("off", true)]);
  ok(mixed.length > 0, "ModelPicker: renders options for the enabled provider");
  ok(!mixed.some((label) => label.includes("off/") || label.includes("off")), "ModelPicker: disabled provider appears in zero options");

  const allOn = await render([mkProvider("live", false), mkProvider("other", false)]);
  ok(allOn.length >= 2, "ModelPicker: all-enabled keeps every provider selectable (zero regression)");
  await act(async () => root.unmount());
}

// ── wiring guards (toggle row + change kind + heartbeat filter) ───────────
{
  const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
  const settingsPanel = read("../components/SettingsPanel.tsx");
  ok(settingsPanel.includes('kind: "provider_toggle"'), "SettingsPanel wires kind provider_toggle");
  ok(settingsPanel.includes("provider-enabled-row"), "SettingsPanel renders the enable/disable row");
  ok(settingsPanel.includes("Number(Boolean(a.hidden)) - Number(Boolean(b.hidden))"), "connections list sorts disabled providers to the bottom");
  ok(settingsPanel.includes("if (p.hidden) continue;"), "ModelPicker group assembly filters disabled providers");

  const types = read("../lib/modelSettingsTypes.ts");
  ok(types.includes('{ kind: "provider_toggle"; name: string; enabled: boolean }'), "change union carries provider_toggle");

  const bridge = read("../lib/modelSettingsBridge.ts");
  ok(bridge.includes('case "provider_toggle"'), "mock bridge dispatches provider_toggle");

  const heartbeat = read("../custom/features/heartbeat/HeartbeatTaskEditor.tsx");
  ok(heartbeat.includes("provider.hidden"), "heartbeat model-override picker keeps its hidden filter");

  const backend = read("../../../model_settings_api.go");
  ok(backend.includes('case "provider_toggle"'), "backend apply switch carries provider_toggle");
}

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nall checks passed (${passed} assertions)`);
