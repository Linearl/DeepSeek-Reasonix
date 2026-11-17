import { selectSettingsValue, settingsOptionValues } from "./settingsSelectTestUtils";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ProviderCatalogPicker, type CatalogChoice } from "../components/ProviderCatalogPicker";
import { LocaleProvider } from "../lib/i18n";
import data from "../lib/providerCatalog.generated.json";
const dom = new JSDOM('<div id="root"></div>', {url: "http://localhost/", pretendToBeVisual: true});
Object.assign(globalThis, {window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, Event: dom.window.Event, localStorage: dom.window.localStorage, IS_REACT_ACT_ENVIRONMENT: true});
Object.defineProperty(globalThis, "navigator", {configurable: true, value: dom.window.navigator});
window.matchMedia = (() => ({matches: true, addEventListener(){}, removeEventListener(){}})) as any;
const rootEl = document.getElementById("root")!;
const root = createRoot(rootEl);
const choices: CatalogChoice[] = Object.entries(data.catalogs).map(([id, catalog]) => ({
 id, catalog, label: id, keyEnv: `${catalog.region}_KEY`, keySet: false, models: [`${id}-model`], status: "available", statusLabel: "", actionLabel: "Add", canAdd: true,
}));
let installed = "";
let submittedFormat: string | undefined;
const render = (busy = false, rows = choices) => root.render(<LocaleProvider><ProviderCatalogPicker choices={rows} busy={busy} onConnect={(id, _key, _url, format) => {installed = id; submittedFormat = format;}} onView={() => {}} onReset={() => {throw new Error("unexpected reset");}} /></LocaleProvider>);
await act(async () => {render();});
const select = (dimension: string) => rootEl.querySelector<HTMLButtonElement>(`button.settings-select[id$="-${dimension}"]`)!;
async function change(dimension: string, value: string) {
 await selectSettingsValue(select(dimension), value);
}
async function brand(name: string) {
 await act(async () => {Array.from(rootEl.querySelectorAll<HTMLButtonElement>(".provider-catalog__brand")).find(el=>el.textContent===name)!.click();});
}
assert.equal(rootEl.querySelectorAll(".provider-catalog__brand").length, new Set(choices.map(c=>c.catalog.brandId)).size);
await brand("智谱 / Z.AI");
await change("region","cn");
await change("product","coding");
await change("format","anthropic");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value,"https://open.bigmodel.cn/api/anthropic");
await act(async () => { rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.click(); });
assert.equal(installed,"glm-coding-plan-cn-anthropic");
await change("product","api");
assert.equal(select("format").value,"openai");
assert.equal((await settingsOptionValues(select("format"))).length,3,"all common protocols are offered");
assert.equal(select("format").disabled,false);
await change("format","responses");
await act(async () => { rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.click(); });
assert.equal(installed,"glm-cn");
assert.equal(submittedFormat,"responses","custom protocol reaches connection creation");
await change("format","openai");
await change("region","global");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value,"https://api.z.ai/api/paas/v4");
await brand("OpenAI");
await change("format","responses");
await act(async () => {render(true);});
assert.equal(select("format").disabled,true,"mutation lock disables protocol changes");
await act(async () => {render(false,choices.map(c=>c.id==="openai-responses"?{...c,canAdd:false,status:"installed",statusLabel:"Added"}:c));});
assert.equal(rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.disabled,true,"updated installed status cannot submit again");
assert.equal(select("format").value,"responses","host refresh preserves explicit selection");
await change("format","openai");
assert.equal(rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.disabled,false,"another uninstalled route stays available");
await brand("OpenCode");
await change("product","go");
await change("format","anthropic");
assert.ok((await settingsOptionValues(select("variant"))).length>1,"model-scoped routes stay reachable without duplicating brands");
await brand("LM Studio");
assert.equal(rootEl.querySelector('button.settings-select[id$="-region"]'),null,"single-platform providers avoid unnecessary choices");
assert.match(rootEl.textContent!,/local installation/);
await change("format","responses");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value,"http://localhost:1234/v1");
await change("format","anthropic");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value,"http://localhost:1234");
await brand("PPIO / 派欧云");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value,"https://api.ppio.com/openai");
await change("format","anthropic");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value,"https://api.ppio.com/anthropic");
await act(async () => { rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.click(); });
assert.equal(submittedFormat,"anthropic");
// A legacy host can put the Anthropic preset first. New official connections
// must still start with Chat Completions, while explicit choices survive refresh.
const deepseekChoice = choices.find(c => c.catalog.brandId === "deepseek")!;
await act(async () => {render(false, [{...deepseekChoice, catalog: {...deepseekChoice.catalog, format: "anthropic"}}]);});
assert.equal(select("format").value, "openai");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value, "https://api.deepseek.com/v1");
assert.deepEqual(await settingsOptionValues(select("format")), ["openai", "responses", "anthropic"]);
await act(async () => { rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.click(); });
assert.equal(submittedFormat, "openai");
await change("format", "anthropic");
await act(async () => {render(false, [deepseekChoice]);});
assert.equal(select("format").value, "anthropic");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value, "https://api.deepseek.com/anthropic");
// tl1659: MiMo exposes the access-mode (product) dropdown on the China site —
// API / Token Plan switching swaps the prefilled base_url and the key env
// hint, mirroring the Zhipu cn-region shape. Overseas stations stay
// token-plan only.
await act(async () => {render(false, choices.map(c => ({...c, keyEnv: c.id === "mimo-api" || c.id === "mimo-anthropic" ? "MIMO_API_KEY" : c.id.startsWith("mimo-token-plan-") ? "MIMO_TOKEN_PLAN_API_KEY" : c.keyEnv})));});
// The MiMo brand has no icon yet, so its button renders a "M" monogram plus
// the label ("MMiMo") — match by inclusion instead of the exact brand text.
await act(async () => {Array.from(rootEl.querySelectorAll<HTMLButtonElement>(".provider-catalog__brand")).find(el => el.textContent?.includes("MiMo"))!.click();});
const keyHint = () => rootEl.querySelector<HTMLInputElement>('input[id$="-key"]')!.placeholder;
// This harness feeds catalog JSON key order, so the first MiMo choice is the
// Anthropic route (mimo-anthropic); the assertions below pin the switching
// semantics rather than the harness' initial format.
assert.ok(select("product"), "MiMo China site exposes the access-mode dropdown");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value, "https://api.xiaomimimo.com/anthropic");
assert.match(keyHint(), /MIMO_API_KEY/);
assert.equal((await settingsOptionValues(select("product"))).length, 2, "API and Token Plan are both offered");
await change("product", "token");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value, "https://token-plan-cn.xiaomimimo.com/anthropic");
assert.match(keyHint(), /MIMO_TOKEN_PLAN_API_KEY/);
await act(async () => { rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.click(); });
assert.equal(installed, "mimo-token-plan-cn-anthropic");
await change("product", "api");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value, "https://api.xiaomimimo.com/anthropic");
assert.match(keyHint(), /MIMO_API_KEY/);
await act(async () => { rootEl.querySelector<HTMLButtonElement>(".btn--primary")!.click(); });
assert.equal(installed, "mimo-anthropic");
await change("format", "openai");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value, "https://api.xiaomimimo.com/v1", "openai route prefills the /v1 endpoint without manual entry");
await change("region", "sgp");
assert.equal(rootEl.querySelector('button.settings-select[id$="-product"]'), null, "overseas stations keep token-plan as the only product");
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-url"]')!.value, "https://token-plan-sgp.xiaomimimo.com/v1");
await act(async () => root.unmount());
console.log("PASS provider catalog: grouping, constrained selection, exact install, refresh state, local setup, mimo access modes");
