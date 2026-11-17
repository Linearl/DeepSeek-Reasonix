// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1654-deleted-provider-picker.test.tsx
// tl1654 (round 3): the composer model dropdown must not offer a group for a
// provider the user deleted.
//
// The backend stopped rebuilding deleted connections (the "MiMo API" group the
// user reported: a stale access reference used to bring the deleted connection
// back, credential included, and the rebuilt entry reappeared in the dropdown
// under its built-in vendor label). This pin locks the frontend half of that
// contract:
//
//   - a group exists only for a provider the catalog snapshot lists;
//   - the display-name registry cannot resurrect a group for a provider that
//     left the snapshot (tl1654r2 made the registry snapshot-synced);
//   - a connection that is still configured but parked (hidden) keeps its own
//     label for refs — "not in the picker" must not become "not a connection".
import assert from "node:assert/strict";
import { register } from "node:module";
// Stub non-JS asset imports (CSS/YAML/fonts) before any component import runs.
await register(new URL("../test-support/asset-stub-loader.mjs", import.meta.url).href);
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { ModelInfo } from "../lib/types";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
(globalThis as typeof globalThis & { window: Window }).window = dom.window as unknown as Window;
(globalThis as typeof globalThis & { document: Document }).document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
(globalThis as typeof globalThis & { Node: typeof Node }).Node = dom.window.Node;
(globalThis as typeof globalThis & { HTMLElement: typeof HTMLElement }).HTMLElement = dom.window.HTMLElement;
(globalThis as typeof globalThis & { Event: typeof Event }).Event = dom.window.Event;
(globalThis as typeof globalThis & { CustomEvent: typeof CustomEvent }).CustomEvent = dom.window.CustomEvent;
(globalThis as typeof globalThis & { requestAnimationFrame: typeof requestAnimationFrame }).requestAnimationFrame =
  dom.window.requestAnimationFrame.bind(dom.window);
(globalThis as typeof globalThis & { cancelAnimationFrame: typeof cancelAnimationFrame }).cancelAnimationFrame =
  dom.window.cancelAnimationFrame.bind(dom.window);
dom.window.scrollTo = () => {};
dom.window.matchMedia = (() => ({ matches: true, addEventListener() {}, removeEventListener() {} })) as never;
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });

class TestResizeObserver {
  observe() {}
  disconnect() {}
}
(globalThis as typeof globalThis & { ResizeObserver: typeof ResizeObserver }).ResizeObserver =
  TestResizeObserver as unknown as typeof ResizeObserver;

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

// The catalog the backend used to hand out for a deleted-but-rebuilt
// connection, and the one it hands out now that the rebuild is gone.
const catalogWithDeletedProvider: ModelInfo[] = [
  { ref: "mimo-api/mimo-v2.5-pro", provider: "mimo-api", model: "mimo-v2.5-pro", current: false },
  { ref: "mimo-api/mimo-v2.5", provider: "mimo-api", model: "mimo-v2.5", current: false },
  { ref: "mimo-api/mimo-v2-omni", provider: "mimo-api", model: "mimo-v2-omni", current: false },
  { ref: "mimo-token-plan/mimo-v2.6-flash", provider: "mimo-token-plan", providerLabel: "MiMo TP", model: "mimo-v2.6-flash", current: true },
];
const catalogWithoutDeletedProvider: ModelInfo[] = [
  { ref: "mimo-token-plan/mimo-v2.6-flash", provider: "mimo-token-plan", providerLabel: "MiMo TP", model: "mimo-v2.6-flash", current: true },
];

let currentCatalog: ModelInfo[] = catalogWithDeletedProvider;
(window as unknown as { go: unknown }).go = {
  main: {
    App: {
      ModelsForTab: async () => currentCatalog,
    },
  },
};

const { ModelSwitcher } = await import("../components/ModelSwitcher");
const { LocaleProvider } = await import("../lib/i18n");
const { modelRefDisplayLabel, registerModelRefLabels } = await import("../lib/modelRefLabel");

const root = createRoot(document.getElementById("root")!);
const render = (label: string, tabId: string) => (
  <LocaleProvider>
    <ModelSwitcher label={label} tabId={tabId} onPick={() => true} />
  </LocaleProvider>
);
const groupLabels = () => Array.from(document.querySelectorAll(".modelsw__group-label"), (el) => el.textContent ?? "");
const optionTexts = () => Array.from(document.querySelectorAll<HTMLElement>("[role='option']"), (el) => el.textContent?.trim() ?? "");
const openMenu = async () => {
  await act(async () => {
    (document.querySelector(".modelsw__trigger") as HTMLButtonElement).click();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

// 1. While the backend still listed the connection, its group header came from
//    the built-in vendor table ("MiMo API") — the label the user saw.
await act(async () => {
  root.render(render("mimo-token-plan/mimo-v2.6-flash", "tab-a"));
  await new Promise((resolve) => setTimeout(resolve, 0));
});
await openMenu();
ok(groupLabels().some((label) => label.includes("MiMo API")), "a listed connection without a display name shows its built-in vendor label");
ok(optionTexts().some((text) => text.includes("mimo-v2.5-pro")), "the listed connection's models are offered");

// 2. The backend stopped rebuilding the deleted connection: the next snapshot
//    no longer lists it, so its group disappears with it.
currentCatalog = catalogWithoutDeletedProvider;
await act(async () => {
  window.dispatchEvent(new Event("reasonix:model-catalog-changed"));
  await new Promise((resolve) => setTimeout(resolve, 0));
});
await act(async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
});
ok(!groupLabels().some((label) => label.includes("MiMo API")), "a deleted connection leaves no group behind");
ok(!optionTexts().some((text) => text.includes("mimo-v2.5-pro")), "a deleted connection's models leave the picker");
ok(groupLabels().some((label) => label.includes("MiMo TP")), "the live connection keeps its group");
ok(optionTexts().some((text) => text.includes("mimo-v2.6-flash")), "the live connection keeps its models");

// 3. The display-name registry must not resurrect the deleted connection:
//    its custom label is dropped with the snapshot (tl1654r2 contract), and the
//    ref still renders a readable form.
registerModelRefLabels([
  { provider: "acme-deleted", providerLabel: "Acme 已删连接", ref: "acme-deleted/model-a", displayName: "Acme 模型显示名" },
  { provider: "mimo-token-plan", providerLabel: "MiMo TP", ref: "mimo-token-plan/mimo-v2.6-flash" },
]);
ok(modelRefDisplayLabel("acme-deleted/model-a").includes("Acme 已删连接"), "the registry knows the custom label while the connection is listed");
registerModelRefLabels([
  { provider: "mimo-token-plan", providerLabel: "MiMo TP", ref: "mimo-token-plan/mimo-v2.6-flash" },
]);
ok(!modelRefDisplayLabel("acme-deleted/model-a").includes("Acme 已删连接"), "a deleted connection's custom label is dropped from the registry");
ok(modelRefDisplayLabel("acme-deleted/model-a").length > 0, "a stale ref still renders a readable fallback form");

// 4. A connection that is still configured but parked (hidden) is a DIFFERENT
//    state: it never reaches the picker, but its refs keep their own label.
//    "Not listed" must not become "not a connection".
ok(modelRefDisplayLabel("mimo-pro/mimo-v2.5-pro").includes("MiMo Pro"), "a parked connection's refs keep their own label");

await act(async () => root.unmount());
process.stdout.write(`\ntl1654 deleted-provider picker: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
assert.ok(passed > 0);
