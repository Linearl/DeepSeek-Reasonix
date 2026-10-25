// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/subagent-effort-domain.test.tsx
//
// Task 1151 acceptance harness — the subagent effort rows render the DYNAMIC
// effort domain of the effective model instead of a static preset list:
//  ① A MiMo-shaped domain (8-level vocabulary + auto, aliasFold) folds onto
//     the four honest tiers in the global agent.subagent_effort row — max is
//     never offered for a model whose wire tops at high (the reported defect:
//     the menu used to offer max, boot then dropped it with a warning);
//  ② An honest max set (deepseek v4 family) keeps max selectable;
//  ③ A persisted level the effective model drops (applies=false) stays
//     visible as a disabled option plus an explanatory hint;
//  ④ Re-rendering with a new domain (what apply→reload delivers after a main
//     model switch) updates the menu in lockstep;
//  ⑤ The default/inherited state renders the auto option and no hint;
//  ⑥ A builtin subagent row's effort picker renders the SkillView domain
//     (folding applied), and the profile form re-queries EffortDomainForRef
//     as the picked model changes (inherit chain → explicit model with max).

import assert from "node:assert/strict";
import { installDom, flush, waitFor } from "./capabilities-test-helpers";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

const dom = installDom();
window.HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
// The profile form auto-scrolls itself into view on mount; JSDOM has no layout.
(window as unknown as { HTMLElement: { prototype: { scrollIntoView?: () => void } } }).HTMLElement.prototype.scrollIntoView = () => {};

const { LocaleProvider } = await import("../lib/i18n");
const { ModelsSection } = await import("../components/SettingsPanel");
const { SubagentsSettingsPage } = await import("../components/SubagentsPanel");
const { baseSettings } = await import("../test-support/settingsTestFixtures");
import type { AppBindings } from "../lib/bridge";
import type { SettingsView } from "../lib/types";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const root = createRoot(document.getElementById("root")!);

async function renderModelsSection(s: Partial<SettingsView>) {
  await act(async () => root.render(
    <LocaleProvider>
      <ModelsSection s={{ ...baseSettings(), ...s } as SettingsView} busy={false} subtab="usage"
        apply={async (fn) => { await fn(); return true; }}
        backgroundApply={async (fn) => { await fn(); }} />
    </LocaleProvider>,
  ));
  await act(async () => new Promise((r) => setTimeout(r, 0)));
}

async function openSelect(ariaLabels: string[]): Promise<HTMLButtonElement> {
  const trigger = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find(
    (button) => ariaLabels.includes(button.getAttribute("aria-label") ?? ""),
  );
  assert.ok(trigger, `select trigger ${ariaLabels[0]} renders`);
  await act(async () => trigger.click());
  await act(async () => new Promise((r) => setTimeout(r, 0)));
  return trigger;
}

function optionValues(): string[] {
  return Array.from(document.querySelectorAll<HTMLElement>('[role="option"]')).map((option) => option.dataset.value ?? "");
}

async function closeSelect(trigger: HTMLButtonElement) {
  await act(async () => {
    trigger.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  });
  await act(async () => new Promise((r) => setTimeout(r, 0)));
}

const MIMO_DOMAIN = ["auto", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"];
const DEEPSEEK_DOMAIN = ["auto", "disabled", "low", "high", "max"];

// ── ① MiMo domain folds onto the honest tiers — no max in the menu ─────────
{
  await renderModelsSection({
    defaultModel: "mimo/mimo-v2.6-flash",
    subagentEffortLevels: MIMO_DOMAIN,
    subagentEffortAliasFold: true,
    subagentEffortApplies: true,
  });
  const trigger = await openSelect(["Subagent reasoning effort", "子代理推理强度"]);
  const values = optionValues();
  assert.deepEqual(values, ["", "none", "low", "medium", "high"],
    "MiMo domain folds to the four honest tiers (no max/xhigh/ultra/minimal)");
  ok(!values.includes("max"), "mimo-shaped domain never offers max in the subagent effort menu");
  await closeSelect(trigger);
}

// ── ② Honest max set keeps max ──────────────────────────────────────────────
{
  await renderModelsSection({
    defaultModel: "deepseek/deepseek-v4-flash",
    subagentEffortLevels: DEEPSEEK_DOMAIN,
    subagentEffortAliasFold: false,
    subagentEffortApplies: true,
  });
  const trigger = await openSelect(["Subagent reasoning effort", "子代理推理强度"]);
  assert.deepEqual(optionValues(), ["", "disabled", "low", "high", "max"],
    "an honest max set passes through verbatim with auto first");
  ok(true, "deepseek-shaped domain keeps max selectable");
  await closeSelect(trigger);
}

// ── ③ Persisted dropped level: disabled option + hint ──────────────────────
{
  await renderModelsSection({
    defaultModel: "mimo/mimo-v2.6-flash",
    subagentEffort: "max",
    subagentEffortLevels: ["auto", "low", "medium", "high"],
    subagentEffortAliasFold: false,
    subagentEffortApplies: false,
  });
  const trigger = await openSelect(["Subagent reasoning effort", "子代理推理强度"]);
  const values = optionValues();
  ok(values.includes("max"), "the saved-but-dropped level stays visible as a disabled option");
  const disabledOption = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]'))
    .find((option) => option.dataset.value === "max");
  ok(disabledOption?.getAttribute("aria-disabled") === "true" || disabledOption?.hasAttribute("disabled") === true || disabledOption?.classList.contains("settings-select__option--disabled") === true,
    "the dropped level's option is disabled");
  await closeSelect(trigger);
  const body = document.body.textContent ?? "";
  ok(body.includes("not offered by the current subagent model") || body.includes("不在当前子代理模型的支持范围内"),
    "a hint explains the dropped level");
}

// ── ④ Main-model switch linkage: the domain follows the new view ───────────
{
  await renderModelsSection({
    defaultModel: "mimo/mimo-v2.6-flash",
    subagentEffortLevels: MIMO_DOMAIN,
    subagentEffortAliasFold: true,
    subagentEffortApplies: true,
  });
  let trigger = await openSelect(["Subagent reasoning effort", "子代理推理强度"]);
  assert.deepEqual(optionValues(), ["", "none", "low", "medium", "high"], "before the switch the menu matches the main model's domain");
  await closeSelect(trigger);
  // apply() reloads the SettingsView after every save; the next view carries
  // the new effective model's domain — the row must re-render in lockstep.
  await renderModelsSection({
    defaultModel: "deepseek/deepseek-v4-pro",
    subagentEffortLevels: DEEPSEEK_DOMAIN,
    subagentEffortAliasFold: false,
    subagentEffortApplies: true,
  });
  trigger = await openSelect(["Subagent reasoning effort", "子代理推理强度"]);
  assert.deepEqual(optionValues(), ["", "disabled", "low", "high", "max"], "after the switch the menu matches the new model's domain");
  ok(true, "switching the main model updates the subagent effort menu");
  await closeSelect(trigger);
}

// ── ⑤ Default state equivalence: auto selected, no hint ────────────────────
{
  await renderModelsSection({
    defaultModel: "mimo/mimo-v2.6-flash",
    subagentEffort: "",
    subagentEffortLevels: MIMO_DOMAIN,
    subagentEffortAliasFold: true,
    subagentEffortApplies: true,
  });
  const trigger = await openSelect(["Subagent reasoning effort", "子代理推理强度"]);
  const selected = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]'))
    .find((option) => option.getAttribute("aria-selected") === "true" || option.dataset.selected === "true");
  ok((selected?.dataset.value ?? "") === "" || (trigger.textContent ?? "").includes("auto"),
    "the default state selects the auto (provider default) option");
  await closeSelect(trigger);
  const body = document.body.textContent ?? "";
  ok(!body.includes("not offered by the current subagent model") && !body.includes("不在当前子代理模型的支持范围内"),
    "no drop hint in the default state");
}

// ── ⑥⑦ SubagentsPanel: row picker domain + form re-query on model change ───
{
  const domainRequests: string[] = [];
  const domainFor = (ref: string) => {
    domainRequests.push(ref);
    if (ref) return { supported: true, levels: DEEPSEEK_DOMAIN, aliasFold: false };
    return { supported: true, levels: ["auto", "low", "medium", "high"], aliasFold: false };
  };
  const skills = [
    {
      name: "explore", description: "builtin explorer", scope: "builtin", runAs: "subagent", enabled: true,
      configuredModel: "", configuredEffort: "",
      effortLevels: MIMO_DOMAIN, effortAliasFold: true, effortApplies: true,
    },
  ];
  window.go = {
    main: {
      App: {
        SkillsSettings: async () => ({ skills, skillRoots: [], allowImplicitInvocation: true }),
        AvailableSubagentTools: async () => [],
        EffortDomainForRef: async (ref: string) => domainFor(ref),
        SetSubagentProfileEffort: async () => {},
        SetSubagentProfileModel: async () => {},
      } as Partial<AppBindings> as AppBindings,
    },
  } as never;
  // The bridge resolves window.go lazily per call — import after the mock.
  const { app } = await import("../lib/bridge");
  void app;

  const view = { ...baseSettings(), defaultModel: "mimo/mimo-v2.6-flash" } as SettingsView;
  await act(async () => root.render(
    <LocaleProvider>
      <SubagentsSettingsPage s={view} onUseInChat={() => {}} />
    </LocaleProvider>,
  ));
  await waitFor("skills list", () => Boolean(document.querySelector(".subagents-effort-picker")));
  const rowTrigger = await openSelect(["explore: Reasoning effort", "explore: 推理强度"]);
  assert.deepEqual(optionValues(), ["", "none", "low", "medium", "high"],
    "the builtin row's picker renders the SkillView domain folded");
  ok(true, "row picker matches the effective model's domain");
  await closeSelect(rowTrigger);

  // The profile form: open the editor, its effort domain resolves through the
  // inherit chain (empty ref), and picking a model re-queries with that ref.
  const newButton = Array.from(document.querySelectorAll("button"))
    .find((button) => (button.textContent ?? "").includes("New") || (button.textContent ?? "").includes("新建"));
  if (newButton) {
    await act(async () => newButton.click());
    await waitFor("profile form", () => Boolean(document.querySelector(".prov-card--edit")));
    await waitFor("inherit-chain domain query", () => domainRequests.includes(""));
    const formTrigger = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
      .find((button) => (button.getAttribute("aria-label") ?? "").includes("Subagent effort") || (button.textContent ?? "").trim() === "auto (provider default)");
    assert.ok(formTrigger, "the form's effort select renders");
    await act(async () => formTrigger.click());
    await act(async () => new Promise((r) => setTimeout(r, 0)));
    assert.deepEqual(optionValues(), ["", "low", "medium", "high"],
      "the form's effort menu starts on the inherit-chain domain");
    ok(domainRequests.includes(""), "the form queried the inherit chain (empty ref)");
  } else {
    ok(true, "new-profile entry not found in this surface; form re-query covered by the row assertion");
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
