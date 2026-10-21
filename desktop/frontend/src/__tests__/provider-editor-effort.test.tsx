// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/provider-editor-effort.test.tsx
//
// Task 1042 (issue #62) acceptance harness — the model config page's explicit
// effort-level editor:
//  ① A provider that declares NO supported_efforts (empty = "protocol
//     defaults") starts on the "Protocol defaults" behavior, its editor
//     collapsed, and the save payload carries an empty list (no phantom
//     levels);
//  ② Picking "Customize levels" expands the editor, offers the four preset
//     tiers plus a custom-entry input, and — critically — a level added
//     through it lands in the saved ProviderView.supportedEfforts. This is
//     the exact gap the issue reported: a non-built-in provider previously
//     had NO way to declare an effort vocabulary at all (the read-only
//     useState at SettingsPanel.tsx:9952), so /effort stayed
//     "not configurable" with no UI to fix it;
//  ③ A "none" reasoning protocol collapses the editor — that provider has no
//     effort knob by definition — and forces the saved list empty;
//  ④ The model-preference effort row (the second surface of the same gap)
//     renders a select sourced from the connection's declared
//     supported_efforts and disables itself with an explanatory hint when
//     the connection declares none.

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { act } from "react";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  Node: dom.window.Node,
  HTMLElement: dom.window.HTMLElement,
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
  cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
  IS_REACT_ACT_ENVIRONMENT: true,
});
window.HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); this.querySelector("input")?.focus(); };
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
const { createRoot } = await import("react-dom/client");
const { ProviderEditor, ModelsSection } = await import("../components/SettingsPanel");
const { LocaleProvider } = await import("../lib/i18n");
const { baseSettings } = await import("../test-support/settingsTestFixtures");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

dom.window.matchMedia = (() => ({ matches: true, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
const root = createRoot(document.getElementById("root")!);

const baseProvider = {
  name: "custom",
  kind: "openai",
  baseUrl: "https://example.com/v1",
  models: ["model-a"],
  default: "model-a",
  apiKeyEnv: "TEST_KEY",
  keySet: true,
  added: true,
  builtIn: false,
  visionModels: [],
  supportedEfforts: [] as string[],
  defaultEffort: "",
  modelsUrl: "",
  balanceUrl: "",
  contextWindow: 1_000_000,
  modelOverrides: [],
};

let saved: any;
async function renderEditor(provider: typeof baseProvider) {
  saved = undefined;
  await act(async () => root.render(
    <LocaleProvider>
      <ProviderEditor initial={provider as any} kinds={["openai"]} busy={false} onCancel={() => {}}
        onSave={async (p) => { saved = p; }} />
    </LocaleProvider>,
  ));
  await act(async () => new Promise((r) => setTimeout(r, 0)));
}
async function touchUrl() {
  const ctx = document.querySelector<HTMLInputElement>('input[type="number"]')!;
  const next = String(Number(ctx.value) + 1);
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(ctx, next);
    ctx.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
}
const advanced = () => document.querySelector<HTMLDetailsElement>(".provider-editor-advanced")!;
const save = () => document.querySelector<HTMLButtonElement>(".provider-editor-footer .btn--primary")!;
async function openAdvanced() {
  await act(async () => { advanced().open = true; });
}
async function clickSave() {
  await touchUrl();
  await act(async () => save().click());
  await act(async () => new Promise((r) => setTimeout(r, 10)));
}

// ── ① Protocol-defaults baseline ────────────────────────────────────────────
await renderEditor(baseProvider);
await openAdvanced();
const behaviorSelect = () => document.querySelector<HTMLButtonElement>('button[aria-label="Effort behavior"],button[aria-label="effort 行为"]')!;
ok(Boolean(behaviorSelect()), "advanced section exposes the effort-behavior select");
ok(behaviorSelect().querySelector(".settings-select__value")?.textContent === "Protocol defaults",
  "a provider with no declared levels starts on protocol defaults");
ok(document.querySelector('button[aria-label="Supported efforts"]') === null,
  "no level editor renders while on protocol defaults");
await clickSave();
assert.equal(saved.supportedEfforts.length, 0, "protocol defaults saves an empty list");
assert.equal(saved.defaultEffort, "", "protocol defaults saves an empty default");

// ── ② Declaring a vocabulary — presets + custom entry ───────────────────────
await renderEditor(baseProvider);
await openAdvanced();
await act(async () => behaviorSelect().click());
const customOption = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]'))
  .find((option) => option.dataset.value === "custom")!;
ok(Boolean(customOption), "customize-levels option exists");
await act(async () => customOption.click());
const presetGroup = () => document.querySelector<HTMLElement>('[role="group"][aria-label="Supported efforts"],[role="group"][aria-label="支持的 effort 等级"]')!;
ok(Boolean(presetGroup()), "picking customize expands the preset tier group");
const highButton = Array.from(presetGroup().querySelectorAll<HTMLButtonElement>("button"))
  .find((button) => button.textContent?.trim() === "high")!;
ok(highButton.getAttribute("aria-selected") === "true", "a preset starts selected once customized");
const customInput = document.querySelector<HTMLInputElement>('input[placeholder="Custom level (press Enter)"],input[placeholder="自定义等级（按回车）"]')!;
ok(Boolean(customInput), "custom-level input renders");
await act(async () => {
  Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(customInput, "deliberate");
  customInput.dispatchEvent(new window.Event("input", { bubbles: true }));
  customInput.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
});
const customCheckbox = Array.from(document.querySelectorAll<HTMLInputElement>(".set-check input[type=checkbox]"))
  .find((box) => box.parentElement?.textContent?.trim() === "deliberate")!;
ok(Boolean(customCheckbox), "a custom-entered level renders as a removable checkbox");
await clickSave();
assert.ok(saved.supportedEfforts.includes("high"), "preset level high lands in the save payload");
assert.ok(saved.supportedEfforts.includes("deliberate"), "custom level lands in the save payload");

// ── ③ "none" protocol collapses the editor ──────────────────────────────────
// A "none" protocol is reached by picking it in the capability-mode select,
// exactly how a user sets it — matching section 2's interaction path.
await renderEditor({ ...baseProvider, baseUrl: "https://none.example.com/v1", supportedEfforts: ["high"], defaultEffort: "high" });
await openAdvanced();
const protocolSelect = () => document.querySelector<HTMLButtonElement>('button[aria-label="Model capability mode"]')!;
ok(Boolean(protocolSelect()), "capability-mode select renders");
await act(async () => protocolSelect().click());
const noneOption = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]'))
  .find((option) => option.dataset.value === "none")!;
ok(Boolean(noneOption), "plain-chat (none) option exists in the capability-mode select");
await act(async () => noneOption.click());
await act(async () => new Promise((r) => setTimeout(r, 0)));
ok(document.querySelector('[role="group"][aria-label="Supported efforts"]') === null,
  "a none-protocol provider never offers the level editor");
await clickSave();
assert.equal(saved.reasoningProtocol, "none", "capability mode reflects the plain-chat pick");
assert.equal(saved.supportedEfforts.length, 0, "none protocol forces an empty list on save");
assert.equal(saved.defaultEffort, "", "none protocol clears the default on save");

// ── ④ Model-preference effort row ───────────────────────────────────────────
{
  const withVocabulary = {
    ...baseSettings(),
    defaultModel: "custom/model-a",
    providers: [{ ...baseProvider, supportedEfforts: ["low", "high"], defaultEffort: "high" }],
  };
  await act(async () => root.render(
    <LocaleProvider>
      <ModelsSection s={withVocabulary as any} busy={false} subtab="usage"
        apply={async (fn) => { await fn(); return true; }}
        backgroundApply={async (fn) => { await fn(); }} />
    </LocaleProvider>,
  ));
  const effortRow = document.querySelector<HTMLButtonElement>('button[aria-label="Reasoning effort"],button[aria-label="推理力度"]')!;
  ok(Boolean(effortRow), "model preference page renders the default-model effort row");
  ok(!effortRow.disabled, "an effort row for a declared vocabulary is enabled");
  await act(async () => effortRow.click());
  const levels = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]')).map((option) => option.dataset.value);
  assert.deepEqual(levels, ["", "low", "high"], "levels come from the connection's declared vocabulary with auto first");

  const withoutVocabulary = { ...withVocabulary, providers: [{ ...baseProvider, supportedEfforts: [] }] };
  await act(async () => root.render(
    <LocaleProvider>
      <ModelsSection s={withoutVocabulary as any} busy={false} subtab="usage"
        apply={async (fn) => { await fn(); return true; }}
        backgroundApply={async (fn) => { await fn(); }} />
    </LocaleProvider>,
  ));
  const disabledRow = document.querySelector<HTMLButtonElement>('button[aria-label="Reasoning effort"],button[aria-label="推理力度"]')!;
  ok(disabledRow.disabled, "an effort row without a declared vocabulary is disabled");
  ok(document.body.textContent?.includes("does not declare an effort vocabulary") === true ||
    document.body.textContent?.includes("未声明 effort 等级") === true,
    "the disabled row explains how to enable it");
}

await act(async () => root.unmount());
process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
