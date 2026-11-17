// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1654r2-model-list-sync.test.tsx
// tl1654r2: rework of the two acceptance rejections on tl1654.
// - Defect 1: the auto fetch committed the live catalog while the panel kept
//   rendering the pre-install model list (the write's settings re-read lost a
//   concurrent-reload race, so setS never landed; the composer picker read the
//   fresh disk config and looked right). Fix pins a dedicated read-back apply
//   queued after the write, so the committed list reaches the card before the
//   success banner claims it — no page reopen needed.
// - Defect 2: the display-label registry (modelRefLabel) kept entries for
//   connections that no longer exist in the catalog snapshot, so deleted or
//   disabled providers kept surfacing their old labels through stale refs.
//   Fix pins the snapshot-sync semantics: entries absent from the latest
//   snapshot are dropped.
import assert from "node:assert/strict";
import { register } from "node:module";
// Stub non-JS asset imports (CSS/YAML/fonts) before any component import runs;
// the last-registered hook executes first, ahead of tsx's defaultLoad.
await register(new URL("../test-support/asset-stub-loader.mjs", import.meta.url).href);
import * as React from "react";
import { JSDOM } from "jsdom";
import { act } from "react";
import type { ProviderView, SettingsView } from "../lib/types";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.sessionStorage = dom.window.sessionStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
window.scrollTo = () => {};
window.matchMedia = (() => ({ matches: true, addEventListener() {}, removeEventListener() {} })) as any;

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

// ---------- Part A: label registry snapshot sync (defect 2) ----------
{
  const { registerModelRefLabels, modelRefDisplayLabel } = await import("../lib/modelRefLabel");
  // The live connection teaches the registry a custom display label.
  registerModelRefLabels([
    { provider: "acme-live-1", providerLabel: "Acme 自定义连接", ref: "acme-live-1/model-a", displayName: "Model A 显示名" },
  ]);
  ok(modelRefDisplayLabel("acme-live-1/model-a").includes("Acme 自定义连接"), "registry resolves a live connection's custom label");
  ok(modelRefDisplayLabel("acme-live-1/model-a").includes("Model A 显示名"), "registry resolves a live model's display name");

  // The connection is deleted: the next catalog snapshot no longer lists it.
  registerModelRefLabels([{ provider: "other", providerLabel: "Other", ref: "other/model-b" }]);
  ok(!modelRefDisplayLabel("acme-live-1/model-a").includes("Acme 自定义连接"), "a deleted connection's custom label is dropped from the registry");
  ok(!modelRefDisplayLabel("acme-live-1/model-a").includes("Model A 显示名"), "a deleted model's display name is dropped from the registry");
  // The ref still resolves into a readable, non-resurrected form (raw segments
  // / built-in vendor label), never the stale custom label.
  ok(modelRefDisplayLabel("acme-live-1/model-a").length > 0, "stale refs still render a readable fallback form");

  // A live connection without a custom label must not be poisoned by an
  // earlier snapshot's entry for the same identity.
  registerModelRefLabels([{ provider: "dual", providerLabel: "Old Label", ref: "dual/model-x", displayName: "Old Name" }]);
  registerModelRefLabels([{ provider: "dual" }, { ref: "dual/model-x" }]);
  ok(!modelRefDisplayLabel("dual/model-x").includes("Old Label") && !modelRefDisplayLabel("dual/model-x").includes("Old Name"), "re-registering without labels clears the previous labels");
}

// ---------- Part B: ProvidersSection wiring (JSDOM) ----------
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ProvidersSection } = await import("../components/SettingsPanel");
const { baseSettings, flushPromises } = await import("../test-support/settingsTestFixtures");
const rootEl = document.getElementById("root")!;

type ApplyCall = { kind: string; change: Record<string, unknown> };
const applyCalls: ApplyCall[] = [];
// Host-level apply log: counts every panel apply (including the no-op
// read-back, which never reaches the bridge so the mock cannot record it).
const hostApplyKinds: string[] = [];
let fetchCatalogImpl: ((provider: ProviderView) => Promise<unknown>) | null = null;
let connectionAddName = "";
// Simulates the panel's view lagging behind the committed config: when set,
// the catalogs write's panel re-read is dropped by the concurrent-reload race
// (the rejection's "picker updated, list did not" form) and only the
// tl1654r2 read-back apply lands the refreshed view. 0 = normal path where
// every apply's re-read reaches the panel.
let viewLagApplies = 3;

(window as unknown as { go: unknown }).go = {
  main: {
    App: {
      async ApplyModelSettings(change: Record<string, unknown>) {
        applyCalls.push({ kind: String(change.kind), change });
        if (change.kind === "connection_add") {
          if (!connectionAddName) connectionAddName = `installed-connection-${applyCalls.length}`;
        }
        return { persisted: true, issues: [], requestId: change.requestId, appliedCatalogs: [] };
      },
      async FetchProviderModelCatalog(provider: ProviderView) {
        if (!fetchCatalogImpl) throw new Error("fetch mock not configured");
        return fetchCatalogImpl(provider);
      },
      async GetModelSettingsRequest() {
        return { persisted: true, issues: [], requestId: "unknown", appliedCatalogs: [] };
      },
    },
  },
};

const liveCatalog = (models: string[]) => models.map((model) => ({ model, inputModalities: ["text"], state: "live", source: "fetched" }));

const presetView = {
  catalog: { brandId: "acme", brandLabel: "Acme", region: "global", product: "api", format: "openai", baseUrl: "https://api.acme.test/v1" },
  id: "acme-openai",
  label: "Acme",
  description: "Acme API",
  keyEnv: "ACME_API_KEY",
  providerNames: ["acme-main"],
  models: ["static-a", "static-b"],
  added: false,
  keySet: false,
  status: "available",
} as unknown as SettingsView["providerPresets"][number];

const STATIC_MODELS = ["deepseek-v4-flash", "deepseek-v4-pro"];
const LIVE_MODELS = ["live-chat-a", "live-chat-b"];

const installedProvider = (models: string[]): ProviderView => ({
  name: connectionAddName || "installed-connection-1",
  displayName: "Installed",
  added: true,
  builtIn: false,
  kind: "openai",
  baseUrl: "https://api.acme.test/v1",
  models: [...models],
  default: models[0] ?? "",
  apiKeyEnv: "REASONIX_CONNECTION_TEST_KEY",
  keySet: true,
  visionModels: [],
  supportedEfforts: [],
  modelCapabilities: [],
  modelCatalogFingerprint: "fp-installed-1",
} as unknown as ProviderView);

let setProvidersExternal: ((next: ProviderView[]) => void) | null = null;
let providersState: ProviderView[] = [];
let root: ReturnType<typeof createRoot> | null = null;

function Harness({ initialProviders, onboarding }: { initialProviders: ProviderView[]; onboarding: boolean }) {
  const [providers, setProviders] = React.useState(initialProviders);
  setProvidersExternal = setProviders;
  providersState = providers;
  const s: SettingsView = { ...baseSettings(), providers, providerPresets: [presetView], officialProviders: [] } as SettingsView;
  const applyHost = async (fn: () => Promise<unknown>) => {
    const idx = hostApplyKinds.length;
    const result = await fn();
    // The write lands first; the panel's re-read (this host callback) follows.
    const writeKind = applyCalls[idx]?.kind ?? (applyCalls.length <= idx ? "no-op-readback" : "unknown");
    hostApplyKinds.push(writeKind);
    if (!connectionAddName) return result;
    const current = providers.some((p) => p.name === connectionAddName);
    if (!current) {
      // The connection_add re-read lands the freshly minted connection with
      // its static preset list — this view always reaches the panel (the
      // auto-fetch effect needs it to resolve the new provider).
      setProviders([installedProvider(STATIC_MODELS)]);
      return result;
    }
    // Later applies re-read the committed config. With the race simulated,
    // the catalogs write's own re-read is dropped; any later apply (the
    // tl1654r2 read-back) lands the committed live list.
    const catalogsIdx = applyCalls.findIndex((c) => c.kind === "catalogs");
    const raceDropsThisRead = viewLagApplies > 0 && catalogsIdx >= 0 && idx === catalogsIdx;
    if (raceDropsThisRead) return result;
    setProviders([installedProvider(LIVE_MODELS)]);
    return result;
  };
  return <LocaleProvider><ProvidersSection s={s} busy={false} apply={applyHost} onboarding={onboarding} /></LocaleProvider>;
}

function renderSection(providers: ProviderView[], opts: { onboarding?: boolean } = {}) {
  providersState = providers;
  if (!root) root = createRoot(rootEl);
  return act(async () => {
    root!.render(<Harness initialProviders={providers} onboarding={opts.onboarding ?? false} />);
  });
}

function resetState() {
  applyCalls.length = 0;
  connectionAddName = "";
  fetchCatalogImpl = null;
  viewLagApplies = 3;
}

async function unmount() {
  if (!root) return;
  await act(async () => root!.unmount());
  root = null;
  rootEl.textContent = "";
}

async function installPreset() {
  const brandButton = document.querySelector<HTMLButtonElement>(".provider-catalog__brand:nth-child(2)") ?? document.querySelector<HTMLButtonElement>(".provider-catalog__brand");
  await act(async () => brandButton!.click());
  const connect = document.querySelector<HTMLButtonElement>(".prov-card__actions .btn--primary")!;
  await act(async () => connect.click());
  await act(async () => { await flushPromises(); await flushPromises(); });
}

function cardText(): string {
  const card = document.querySelector(".provider-access-card--detail") ?? document.querySelector(".provider-access-card");
  return card?.textContent ?? "";
}

// --- Scenario 1 (defect 1 fix): the read-back apply lands the committed list without reopening ---
{
  resetState();
  fetchCatalogImpl = async () => liveCatalog([...LIVE_MODELS, "text-embedding-mini"]);
  await renderSection([], { onboarding: true });
  await installPreset();

  ok(applyCalls.some((c) => c.kind === "connection_add"), "install save landed first");
  const catalogUpdate = applyCalls.find((c) => c.kind === "catalogs");
  ok(Boolean(catalogUpdate), "the follow-up catalogs write was submitted automatically");
  ok(JSON.stringify((catalogUpdate?.change.catalogs as Array<Record<string, unknown>>)?.[0]?.models) === JSON.stringify(LIVE_MODELS), "the committed catalog carries the live endpoint list");

  // tl1654r2 fix: a dedicated read-back apply is queued after the write so the
  // panel view catches up with the committed config before the banner shows.
  ok(applyCalls.filter((c) => c.kind === "connection_add" || c.kind === "catalogs").length === 2, "no extra writes are issued beyond install + catalog");
  ok(hostApplyKinds.length >= 3, "a post-write read-back apply was queued (tl1654r2 view sync)");
  ok(hostApplyKinds.slice(0, 2).every((k) => k === "connection_add" || k === "catalogs"), "the read-back queues after install + catalog, in order");

  // With the race simulated (view lags two applies), only the read-back lands
  // the committed live list — the card must show it without a page reopen.
  const text = cardText();
  ok(text.includes("live-chat-a") && text.includes("live-chat-b"), "the card model list shows the committed live list without reopening the page");
  ok(!text.includes("deepseek-v4-flash"), "the stale static fallback list is gone from the card");
  const banner = document.querySelector(".provider-card-status");
  ok(Boolean(banner) && /已更新可用模型|Updated available models/.test(banner!.textContent ?? ""), "the success banner still confirms the automatic live fetch");
  await unmount();
}

// --- Scenario 2 (no-race regression): the normal path is unchanged ---
{
  resetState();
  viewLagApplies = 0; // host pushes the refreshed view after every apply (normal reload)
  fetchCatalogImpl = async () => liveCatalog(LIVE_MODELS);
  await renderSection([], { onboarding: true });
  await installPreset();

  ok(applyCalls.some((c) => c.kind === "catalogs"), "normal path still submits the catalogs write");
  const text = cardText();
  ok(text.includes("live-chat-a") && text.includes("live-chat-b"), "normal path: the card shows the live list");
  ok(!text.includes("deepseek-v4-flash"), "normal path: no static fallback residue");
  await unmount();
}

// --- Scenario 3 (defect 1, draft state): a subsequent manual refresh drafts from the committed list ---
{
  resetState();
  viewLagApplies = 0;
  fetchCatalogImpl = async () => liveCatalog(LIVE_MODELS);
  await renderSection([], { onboarding: true });
  await installPreset();
  // Now an edit save on the installed connection: the follow-up auto fetch
  // lands a review draft built from the committed (live) catalog, keeping the
  // selection in sync — no stale static models appearing as selected.
  fetchCatalogImpl = async () => liveCatalog([...LIVE_MODELS, "live-chat-c"]);
  const keyInput = document.querySelector<HTMLInputElement>('input[type="password"]');
  ok(Boolean(keyInput), "the detail card exposes the connection editor key input");
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(keyInput, "brand-new-key");
    keyInput!.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
  const saveButton = document.querySelector<HTMLButtonElement>(".provider-editor-footer .btn--primary")!;
  await act(async () => saveButton.click());
  await act(async () => { await flushPromises(); await flushPromises(); });
  const draft = document.querySelector(".provider-model-draft");
  ok(Boolean(draft), "the manual refresh produced a review draft");
  const draftText = draft?.textContent ?? "";
  ok(draftText.includes("live-chat-a") && draftText.includes("live-chat-b") && draftText.includes("live-chat-c"), "draft candidates track the freshly fetched list");
  ok(!draftText.includes("deepseek-v4-flash"), "the draft carries no stale static models as selected");
  await unmount();
}

await unmount();
console.log(`\ntl1654r2 model list sync: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
