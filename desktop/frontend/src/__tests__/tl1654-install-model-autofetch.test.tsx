// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1654-install-model-autofetch.test.tsx
// tl1654: provider configuration save auto-triggers one live model fetch.
// - preset/official install: fetched live list REPLACES the curated static
//   fallback through the narrow catalogs update (pickers stop showing stale
//   built-in models); fetch failure keeps the fallback and surfaces a
//   readable classified error with a manual-entry hint.
// - custom install / edit save: fetched list lands in the review draft only —
//   zero automatic catalog writes, existing saved models untouched.
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

// ---------- Part A: pure decision helpers (no DOM) ----------
const { installedProviderCatalogDraft, newlyAddedProviders, mergedFetchedProviderModels } = await import("../lib/providerModels");

{
  const draft = installedProviderCatalogDraft({ default: "legacy-static", visionModels: ["legacy-static-vision", "live-a"] }, ["live-a", "live-b", "text-embedding-mini"]);
  ok(!!draft && JSON.stringify(draft.models) === JSON.stringify(["live-a", "live-b"]), "install fetch replaces the static fallback with the chat-filtered live list");
  ok(!!draft && draft.default === "live-a", "stale static default collapses to the first live model");
  ok(!!draft && JSON.stringify(draft.visionModels) === JSON.stringify(["live-a"]), "vision list is re-projected onto the live catalog");
}
{
  const draft = installedProviderCatalogDraft({ default: "static", visionModels: [] }, ["  live-a ", "live-a", ""]);
  ok(!!draft && JSON.stringify(draft.models) === JSON.stringify(["live-a"]), "whitespace and duplicate live entries are normalized");
}
{
  const draft = installedProviderCatalogDraft({ default: "static", visionModels: ["static"] }, []);
  ok(draft === null, "empty live list keeps the fallback (replace skipped, manual entry hinted)");
}
{
  const draft = installedProviderCatalogDraft({ default: "static", visionModels: [] }, ["text-embedding-3", "whisper-1"]);
  ok(draft === null, "a non-chat-only live list keeps the fallback instead of saving an empty catalog");
}
{
  const known = new Set(["old-1"]);
  const providers = [
    { name: "old-1", added: true, keySet: true, baseUrl: "https://a/v1", requiresKey: true },
    { name: "new-configured", added: true, keySet: true, baseUrl: "https://b/v1", requiresKey: true },
    { name: "new-unconfigured", added: true, keySet: false, baseUrl: "https://c/v1", requiresKey: true },
    { name: "new-no-url", added: true, keySet: true, baseUrl: "", requiresKey: true },
  ] as unknown as ProviderView[];
  ok(JSON.stringify(newlyAddedProviders(providers, known).map((p) => p.name)) === JSON.stringify(["new-configured"]), "install diff keeps only configured providers with a base URL");
}

// ---------- Part B: ProvidersSection wiring (JSDOM) ----------
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ProvidersSection } = await import("../components/SettingsPanel");
const { baseSettings, flushPromises } = await import("../test-support/settingsTestFixtures");
const rootEl = document.getElementById("root")!;

type ApplyModelSettingsCall = { kind: string; change: Record<string, unknown> };
const applyCalls: ApplyModelSettingsCall[] = [];
let fetchCatalogImpl: ((provider: ProviderView) => Promise<unknown>) | null = null;
let connectionAddName = "";
let connectionAddModels: string[] = [];

(window as unknown as { go: unknown }).go = {
  main: {
    App: {
      async ApplyModelSettings(change: Record<string, unknown>) {
        applyCalls.push({ kind: String(change.kind), change });
        if (change.kind === "connection_add" || change.kind === "preset_add" || change.kind === "provider_save") {
          if (!connectionAddName) {
            connectionAddName = change.kind === "provider_save" ? String((change.provider as Record<string, unknown>).name) : `installed-connection-${applyCalls.length}`;
            connectionAddModels = [...(change.kind === "provider_save" ? ((change.provider as Record<string, unknown>).models as string[]) ?? [] : ["deepseek-v4-flash", "deepseek-v4-pro"])];
          }
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
  models: ["deepseek-v4-flash", "deepseek-v4-pro"],
  added: false,
  keySet: false,
  status: "available",
} as unknown as SettingsView["providerPresets"][number];

const installedProvider = (overrides: Partial<ProviderView> = {}): ProviderView => ({
  name: connectionAddName || "installed-connection-1",
  displayName: "Installed",
  added: true,
  builtIn: false,
  kind: "openai",
  baseUrl: "https://api.acme.test/v1",
  models: connectionAddModels.length > 0 ? [...connectionAddModels] : ["deepseek-v4-flash", "deepseek-v4-pro"],
  default: connectionAddModels[0] ?? "deepseek-v4-flash",
  apiKeyEnv: "REASONIX_CONNECTION_TEST_KEY",
  keySet: true,
  visionModels: [],
  supportedEfforts: [],
  modelCapabilities: [],
  modelCatalogFingerprint: "fp-installed-1",
  ...overrides,
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
    await fn();
    // Host semantics: after the save transaction lands, the refreshed settings
    // view (including freshly minted connection identities) reaches the panel
    // before the save promise resolves to the caller.
    if (connectionAddName) {
      const already = providers.some((p) => p.name === connectionAddName);
      if (!already) setProviders([...providers, installedProvider()]);
    }
    return true;
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

async function typeInto(input: HTMLInputElement | null, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
}

function resetState() {
  applyCalls.length = 0;
  connectionAddName = "";
  connectionAddModels = [];
  fetchCatalogImpl = null;
}

async function unmount() {
  if (!root) return;
  await act(async () => root!.unmount());
  root = null;
  rootEl.textContent = "";
}

// --- Scenario 1: preset install → live fetch replaces the static fallback ---
{
  resetState();
  fetchCatalogImpl = async () => liveCatalog(["live-chat-a", "live-chat-b", "text-embedding-mini"]);
  await renderSection([], { onboarding: true });
  // Onboarding opens the add panel in official mode; switch to the preset brand.
  const brandButton = document.querySelector<HTMLButtonElement>(".provider-catalog__brand:nth-child(2)") ?? document.querySelector<HTMLButtonElement>(".provider-catalog__brand");
  await act(async () => brandButton!.click());
  const connect = document.querySelector<HTMLButtonElement>(".prov-card__actions .btn--primary")!;
  ok(!connect.disabled, "preset connect button is enabled with a valid catalog base URL");
  await act(async () => connect.click());
  await act(async () => { await flushPromises(); await flushPromises(); });

  ok(applyCalls.some((c) => c.kind === "connection_add"), "install save landed first");
  const catalogUpdate = applyCalls.find((c) => c.kind === "catalogs");
  ok(Boolean(catalogUpdate), "a follow-up catalogs update was submitted automatically");
  const catalogs = (catalogUpdate?.change.catalogs ?? []) as Array<Record<string, unknown>>;
  ok(catalogs.length === 1 && catalogs[0].name === "installed-connection-1", "the auto update targets the freshly installed connection");
  ok(catalogs[0]?.expectedFingerprint === "fp-installed-1", "the auto update carries the installed fingerprint guard");
  ok(JSON.stringify(catalogs[0]?.models) === JSON.stringify(["live-chat-a", "live-chat-b"]), "saved model list equals the live endpoint list, not the static preset list");
  const banner = document.querySelector(".provider-card-status");
  ok(Boolean(banner) && /已更新可用模型|Updated available models/.test(banner!.textContent ?? ""), "success banner confirms the automatic live fetch");
  await unmount();
}

// --- Scenario 2: preset install fetch fails (401) → fallback stays, readable error + manual hint, no catalog write ---
{
  resetState();
  fetchCatalogImpl = async () => { throw new Error("fetch models: status 401: unauthorized"); };
  await renderSection([], { onboarding: true });
  const brandButton = document.querySelector<HTMLButtonElement>(".provider-catalog__brand:nth-child(2)") ?? document.querySelector<HTMLButtonElement>(".provider-catalog__brand");
  await act(async () => brandButton!.click());
  const connect = document.querySelector<HTMLButtonElement>(".prov-card__actions .btn--primary")!;
  await act(async () => connect.click());
  await act(async () => { await flushPromises(); await flushPromises(); });

  ok(applyCalls.some((c) => c.kind === "connection_add"), "install save landed despite the later fetch failure");
  ok(!applyCalls.some((c) => c.kind === "catalogs"), "a failed fetch never rewrites the model catalog");
  const banner = document.querySelector(".provider-card-status");
  const text = banner?.textContent ?? "";
  ok(/API key|permission|密钥|权限/.test(text), "401 surfaces the auth-classified readable message");
  ok(/手动填写模型 ID|[Ee]nter model IDs manually/.test(text), "the failure banner points at manual model-ID entry");
  await unmount();
}

// --- Scenario 3: preset install fetch returns nothing → fallback stays, empty message + manual hint ---
{
  resetState();
  fetchCatalogImpl = async () => [];
  await renderSection([], { onboarding: true });
  const brandButton = document.querySelector<HTMLButtonElement>(".provider-catalog__brand:nth-child(2)") ?? document.querySelector<HTMLButtonElement>(".provider-catalog__brand");
  await act(async () => brandButton!.click());
  const connect = document.querySelector<HTMLButtonElement>(".prov-card__actions .btn--primary")!;
  await act(async () => connect.click());
  await act(async () => { await flushPromises(); await flushPromises(); });

  ok(!applyCalls.some((c) => c.kind === "catalogs"), "an empty live list never replaces the fallback");
  const banner = document.querySelector(".provider-card-status");
  const text = banner?.textContent ?? "";
  ok(/没有返回可用模型|did not return any available models/.test(text), "empty list surfaces the readable empty message");
  ok(/手动填写模型 ID|[Ee]nter model IDs manually/.test(text), "empty list also points at manual model-ID entry");
  await unmount();
}

// --- Scenario 4: edit save → one auto fetch into the review draft, zero automatic catalog writes ---
{
  resetState();
  fetchCatalogImpl = async () => liveCatalog(["live-edit-a", "live-edit-b"]);
  const existing = {
    name: "conn-edit-1",
    displayName: "Editable",
    added: true,
    builtIn: false,
    kind: "openai",
    baseUrl: "https://api.acme.test/v1",
    requestUrl: "https://api.acme.test/v1/chat/completions",
    models: ["hand-picked"],
    default: "hand-picked",
    apiKeyEnv: "REASONIX_CONNECTION_EDIT_KEY",
    keySet: true,
    visionModels: [],
    supportedEfforts: [],
    modelCapabilities: [],
    modelCatalogFingerprint: "fp-edit-1",
  } as unknown as ProviderView;
  await renderSection([existing]);
  // A detail card expands the editor for its single connection by default.
  // An untouched editor disables save — dirty the draft first (new key).
  const keyInput = document.querySelector<HTMLInputElement>('input[type="password"]')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(keyInput, "brand-new-key");
    keyInput.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
  const saveButton = document.querySelector<HTMLButtonElement>(".provider-editor-footer .btn--primary")!;
  ok(!saveButton.disabled, "editor save is available for an existing connection");
  await act(async () => saveButton.click());
  await act(async () => { await flushPromises(); await flushPromises(); });

  const saveCalls = applyCalls.filter((c) => c.kind === "provider_save");
  ok(saveCalls.length === 1, "the edit save landed exactly once");
  ok(!applyCalls.some((c) => c.kind === "catalogs"), "edit save never auto-writes the catalog (old configuration untouched)");
  const draft = document.querySelector(".provider-model-draft");
  ok(Boolean(draft), "the auto fetch produced a review draft instead of a silent write");
  const draftText = draft?.textContent ?? "";
  ok(draftText.includes("live-edit-a") && draftText.includes("live-edit-b"), "draft candidates contain the live endpoint list");
  ok(draftText.includes("hand-picked"), "existing saved models stay visible and selected in the draft");
  await unmount();
}

// --- Scenario 5: legacy merge semantics guard (existing models never shrink) ---
{
  const merged = mergedFetchedProviderModels(["hand-picked"], ["live-a", "live-b"]);
  ok(JSON.stringify(merged) === JSON.stringify(["hand-picked", "live-a", "live-b"]), "merged fetched models append without removing curated entries");
}

await unmount();
console.log(`\ntl1654 install model autofetch: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
