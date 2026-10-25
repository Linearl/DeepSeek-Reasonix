// Run: tsx src/__tests__/provider-model-effort-draft.test.ts
//
// Task 1053: the model dialog now carries a per-model effort vocabulary. Pin
// the draft round-trip so a saved list survives an unrelated edit, a cleared
// list actually clears, and a default_effort left outside the new list is
// dropped instead of silently kept (the backend normalizer would drop it
// later anyway — the draft stays honest up front). applyModelDraft preserves
// the other models' entries, so each assertion projects the edited one.
//
// tl 1053 rebase: draft shape follows the 1104 ProviderModelDialog contract
// (contextWindow / maxOutputTokens / vision: boolean|null / audio) with
// `efforts?: string[]` layered on top — the old context/output/vision-string
// shape is gone.

import { applyModelDraft, normalizedEffortSelection } from "../lib/providerModelDraft";
import type { ProviderModelOverrideView } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function edited(overrides: ProviderModelOverrideView[], draft: Parameters<typeof applyModelDraft>[1], original?: string): ProviderModelOverrideView {
  const next = applyModelDraft(overrides, draft, original);
  return next.find((item) => item.model === (original ?? draft.model.trim()))!;
}

function eqOverride(actual: ProviderModelOverrideView, want: Partial<ProviderModelOverrideView>, label: string) {
  const mismatches = Object.entries(want).filter(([key, value]) => JSON.stringify((actual as Record<string, unknown>)[key]) !== JSON.stringify(value));
  if (mismatches.length === 0) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\nprovider model effort draft");

const existing: ProviderModelOverrideView[] = [
  {
    model: "reasoning-model",
    reasoningProtocol: "",
    supportedEfforts: ["low", "xhigh", "max"],
    defaultEffort: "max",
    vision: null,
    contextWindow: 200000,
  },
  { model: "chat-model", reasoningProtocol: "none", supportedEfforts: [], defaultEffort: "", vision: true },
];

eqOverride(
  edited(existing, { model: "reasoning-model", contextWindow: "300000", maxOutputTokens: -1, vision: null, audio: null, efforts: ["low", "xhigh", "max"] }, "reasoning-model"),
  { supportedEfforts: ["low", "xhigh", "max"], defaultEffort: "max", contextWindow: 300000 },
  "an unchanged efforts list keeps the stored vocabulary and default",
);

eqOverride(
  edited(existing, { model: "reasoning-model", contextWindow: "", maxOutputTokens: -1, vision: null, audio: null, efforts: ["low", "max"] }, "reasoning-model"),
  { supportedEfforts: ["low", "max"], defaultEffort: "max", vision: null, contextWindow: 0 },
  "a level list still containing the pinned default keeps it",
);

eqOverride(
  edited(existing, { model: "reasoning-model", contextWindow: "", maxOutputTokens: -1, vision: null, audio: null, efforts: ["low"] }, "reasoning-model"),
  { supportedEfforts: ["low"], defaultEffort: "", vision: null, contextWindow: 0 },
  "a list that no longer contains the pinned default drops it",
);

eqOverride(
  edited(existing, { model: "reasoning-model", contextWindow: "", maxOutputTokens: -1, vision: null, audio: null, efforts: [] }, "reasoning-model"),
  { supportedEfforts: [], defaultEffort: "max", vision: null, contextWindow: 0 },
  "clearing the list keeps the pinned default and marks the vocabulary inherited",
);

eqOverride(
  edited(existing, { model: "new-model", contextWindow: "", maxOutputTokens: -1, vision: null, audio: null, efforts: ["MINIMAL", "  High  ", "auto"] }),
  { supportedEfforts: ["minimal", "high"], defaultEffort: "", contextWindow: 0, maxOutputTokens: -1 },
  "new-model levels normalize case and whitespace and never store auto",
);

eqOverride(
  edited([{ model: "chat-model", reasoningProtocol: "none", supportedEfforts: [], defaultEffort: "", vision: true, maxOutputTokens: 4096 }], { model: "chat-model", contextWindow: "", maxOutputTokens: 4096, vision: null, audio: null }, "chat-model"),
  { reasoningProtocol: "none", supportedEfforts: [], defaultEffort: "", vision: null, audio: null, contextWindow: 0, maxOutputTokens: 4096 },
  "a caller that predates the efforts field keeps the stored vocabulary and reasoning fields",
);

eq(
  applyModelDraft(existing, { model: "reasoning-model", contextWindow: "", maxOutputTokens: -1, vision: null, audio: null, efforts: ["low"] }, "reasoning-model").map((item) => item.model),
  ["chat-model", "reasoning-model"],
  "editing one model preserves the other models' entries",
);

// The shared selection normalizer (dialog apply + upper-zone applyModelDetails).
eq(
  normalizedEffortSelection(["MINIMAL", " High ", "auto"], "minimal"),
  { supportedEfforts: ["minimal", "high"], defaultEffort: "minimal" },
  "normalizedEffortSelection keeps a default that survives normalization",
);
eq(
  normalizedEffortSelection(["low"], "xhigh"),
  { supportedEfforts: ["low"], defaultEffort: "" },
  "normalizedEffortSelection drops an out-of-vocabulary default",
);
eq(
  normalizedEffortSelection([], "max"),
  { supportedEfforts: [], defaultEffort: "max" },
  "normalizedEffortSelection empty list inherits and keeps the pinned default",
);

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
