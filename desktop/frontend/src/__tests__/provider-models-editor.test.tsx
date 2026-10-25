import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { applyModelDraft } from "../lib/providerModelDraft";
import { providerVisionModelsForView } from "../lib/providerVisionCapability";
import { modelCapabilityForModel } from "../lib/providerImageInput";
import type { ProviderView, ProviderModelCapabilityView } from "../lib/types";

// tl 1104: 弹窗与上区同为 <dialog>（ProviderModelDialog），补齐 jsdom 缺的
// showModal（与 task468/provider-editor-compact 测试同款 stub）。
const dom = new JSDOM('<!doctype html><div id="root"></div>', { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, localStorage: dom.window.localStorage, requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window), cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window), IS_REACT_ACT_ENVIRONMENT: true });
// 断言文案锁定英文（detectLocale 跟随 navigator.language，本机是中文环境）。
// 先把 react-dom 的 userAgent 探测跑完再换成「克隆 + 英文 language」——jsdom 的
// Navigator getter 校验 receiver 身份，代理/普通克隆都会炸，只能先满足再换。
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
await import("react-dom/client");
const englishNavigator = Object.create(dom.window.navigator);
Object.defineProperty(englishNavigator, "language", { value: "en-US" });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: englishNavigator });
window.HTMLDialogElement.prototype.showModal = function() { this.setAttribute("open", ""); this.querySelector("input")?.focus(); };
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
// 弹窗在组件里是 lazy import——预先加载，避免 act() 期间的 Suspense 悬挂。
await import("../components/ProviderModelDialog");
const { ProviderModelsEditor } = await import("../components/ProviderModelsEditor");
const root = createRoot(document.getElementById("root")!);
const override = { model: "existing", contextWindow: 32768, maxOutputTokens: 4096, vision: null, audio: null, reasoningProtocol: "kimi", supportedEfforts: ["high"], defaultEffort: "high" };
const result = applyModelDraft([override], { model: "renamed", contextWindow: "", maxOutputTokens: -1, vision: null, audio: null }, "existing")[0];
assert.equal(result.reasoningProtocol, "kimi");
assert.deepEqual(result.supportedEfforts, ["high"]);
// Task 1053 rebase: an empty context draft still means "clear to inherited"
// (contextWindow 0) — the 1104 dialog owns that contract with an explicit
// reset button next to the field; the per-model effort vocabulary rides the
// same apply chain via the optional `efforts` field without touching it.
assert.equal(result.contextWindow, 0);
assert.equal(result.maxOutputTokens, -1);
assert.equal(result.vision, null);
// tl 1104: 音频三态随弹窗应用落地，且不动其它 override 字段。
const audioApplied = applyModelDraft([{ ...override, audio: false }], { model: "existing", contextWindow: "32768", maxOutputTokens: 4096, vision: null, audio: true }, "existing")[0];
assert.equal(audioApplied.audio, true);
assert.equal(audioApplied.reasoningProtocol, "kimi", "audio flip must not touch reasoning fields");
assert.deepEqual(applyModelDraft([{ ...override, audio: true }], { model: "existing", contextWindow: "32768", maxOutputTokens: 4096, vision: null, audio: null }, "existing")[0].audio, null, "auto returns to null");
assert.equal(modelCapabilityForModel([{model:"Existing",state:"supported"} as ProviderModelCapabilityView],"existing"), undefined);
assert.deepEqual(providerVisionModelsForView({models:["existing","Existing"],visionModels:[],modelOverrides:[{...override,model:"Existing",vision:true}],modelCapabilities:[]}), ["Existing"]);

let provider = { name: "test", kind: "openai", baseUrl: "https://example.test/v1", models: ["existing"], modelOverrides: [override], modelCapabilities: [], visionModels: [], visionCapability: "configurable", contextWindow: 200000 } as unknown as ProviderView;
let changes = 0;
let resolveFetch: ((value: ProviderModelCapabilityView[]) => void) | undefined;
let rejectTest: ((reason: Error) => void) | undefined;
const render = () => root.render(<LocaleProvider><ProviderModelsEditor provider={provider} disabled={false} canFetch
  onFetch={() => new Promise((resolve) => { resolveFetch = resolve; })}
  onTest={() => new Promise((_, reject) => { rejectTest = reject; })}
  onChange={(models, modelOverrides, modelCapabilities) => { changes++; provider = { ...provider, models, modelOverrides, modelCapabilities }; render(); }}
/></LocaleProvider>);
const click = async (text: string) => {
  const button = Array.from(document.querySelectorAll("button")).find((b) => b.textContent?.trim() === text || b.getAttribute("aria-label") === text);
  assert.ok(button, `button ${text}`);
  await act(async () => { button.focus(); button.click(); });
};
const fill = async (input: HTMLInputElement, value: string) => {
  await act(async () => {
    Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  });
};
const dialog = () => document.querySelector("dialog");
const submit = async () => act(async () => { dialog()!.querySelector("form")!.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true })); });
await act(async () => render());

// tl 1104 验收①：入口按钮 = 上区同款 SlidersHorizontal 图标 + tooltip「模型设置」。
const optionsButton = document.querySelector<HTMLButtonElement>('button[aria-label="Model settings: existing"]');
assert.ok(optionsButton, "row edit button advertises the unified 'Model settings' label");
assert.equal(optionsButton.getAttribute("aria-haspopup"), "dialog");
assert.equal(optionsButton.title, "Model settings");
assert.ok(optionsButton.querySelector("svg.lucide-sliders-horizontal"), "icon is SlidersHorizontal (same as the upper picker)");

// tl 1104 验收②：添加模式保留——「手动添加」打开 add 变体弹窗（模型 ID 可编辑）。
await click("Add manually");
assert.ok(dialog(), "add dialog opens");
assert.equal(document.activeElement?.tagName, "INPUT");
assert.ok(dialog()!.querySelector(".provider-model-dialog__id input"), "add mode keeps an editable model ID field");
const modelID = dialog()!.querySelector<HTMLInputElement>(".provider-model-dialog__id input")!;
await submit();
assert.match(dialog()!.querySelector('[role="alert"]')!.textContent!, /required/);
await fill(modelID, "existing");
await submit();
assert.match(dialog()!.querySelector('[role="alert"]')!.textContent!, /already added/);
await fill(modelID, "manual");
await submit();
assert.deepEqual(provider.models, ["existing", "manual"]);
assert.equal(provider.modelOverrides?.find((m) => m.model === "manual")?.contextWindow, 0);
assert.equal(provider.modelOverrides?.find((m) => m.model === "manual")?.audio ?? null, null, "add leaves audio on auto");
assert.equal(changes, 1, "one apply = one onChange");

// tl 1104 验收③：6 个可编辑项——模型 ID（编辑态只读）/上下文/输出上限/图片/音频/（关闭的）高速。
await act(async () => { optionsButton.click(); });
for (let i = 0; i < 150 && !dialog(); i++) await new Promise((r) => setTimeout(r, 20));
assert.ok(dialog(), "options button opens the unified dialog");
assert.equal(dialog()!.querySelector(".provider-model-dialog__id input"), null, "existing model ID is immutable in edit mode");
assert.equal(dialog()!.querySelectorAll('input[type="number"]').length, 2, "context window + output limit");
const imageToggle = dialog()!.querySelector<HTMLInputElement>('aside input[type="checkbox"]:not(:disabled):not(:checked)')!;
assert.ok(imageToggle, "image input checkbox editable");
const audioLabel = Array.from(dialog()!.querySelectorAll("aside label")).find((label) => label.textContent?.trim() === "Audio");
assert.ok(audioLabel, "audio input entry present (the field the lower zone lacked)");
const audioToggle = audioLabel!.querySelector<HTMLInputElement>("input")!;
assert.ok(!audioToggle.disabled, "audio input checkbox editable");
await act(async () => { audioToggle.click(); });
assert.equal(audioToggle.checked, true);
assert.equal(dialog()!.querySelector('[data-testid="high-speed-model-toggle"]'), null, "high-speed entry stays hidden (upper zone owns the 318.1 master switch)");
const contextInput = dialog()!.querySelectorAll<HTMLInputElement>('input[type="number"]')[0];
await fill(contextInput, "128000");
await submit();
assert.equal(provider.modelOverrides?.find((m) => m.model === "existing")?.contextWindow, 128000);
assert.equal(provider.modelOverrides?.find((m) => m.model === "existing")?.audio, true, "audio override lands with apply");
assert.equal(provider.modelOverrides?.find((m) => m.model === "existing")?.reasoningProtocol, "kimi", "unrelated reasoning fields survive the dialog apply");

// Escape 关闭弹窗且不落草稿（ProviderModelDialog 的原生 cancel 语义）。
await act(async () => { optionsButton.click(); });
for (let i = 0; i < 150 && !dialog(); i++) await new Promise((r) => setTimeout(r, 20));
assert.ok(dialog());
await act(async () => { dialog()!.dispatchEvent(new dom.window.Event("cancel", { cancelable: true })); });
assert.equal(dialog(), null, "Escape/cancel closes the dialog");
assert.equal(document.querySelector('.provider-model-row__name')?.textContent, "existing", "cancelled edits leave the row intact");

await click("Test model existing");
await click("Refresh models");
await act(async () => { rejectTest!(new Error("authentication failed")); });
assert.match(document.querySelector('[role="status"]')!.textContent!, /authentication failed/);
const catalog = ["existing", "remote"].map((model) => ({ model, inputModalities: ["text"], state: "unknown", source: "catalog" }));
await act(async () => { resolveFetch!(catalog); });
const checkboxes = document.querySelectorAll<HTMLInputElement>('[role="dialog"] input[type="checkbox"]');
assert.equal(checkboxes[0].disabled, true);
assert.equal(checkboxes[0].checked, true);
await act(async () => checkboxes[1].click());
await click("Add selected models");
assert.deepEqual(provider.models, ["existing", "manual", "remote"]);
// changes 计数：添加手动模型 + 应用 existing 弹窗 + 发现添加 = 3 次 onChange。
assert.equal(changes, 3);

// tl 1053 验收：弹窗内 effort 编辑区——segmented 预设（EFFORT_PRESETS 渲染
// 契约：none/low/medium/high 四档）+ 自定义等级输入（大小写/空白归一，永不存
// auto）+ 已存词表预选 + apply 落 override.supportedEfforts（default 仍在
// 词表内则保留）。
await act(async () => { optionsButton.click(); });
for (let i = 0; i < 150 && !dialog(); i++) await new Promise((r) => setTimeout(r, 20));
assert.ok(dialog(), "reopen the unified dialog for the effort editor");
const segButtons = Array.from(dialog()!.querySelectorAll<HTMLButtonElement>(".provider-add-segmented .provider-add-segmented__item"));
assert.deepEqual(segButtons.map((b) => b.textContent?.trim()), ["none", "low", "medium", "high"], "EFFORT_PRESETS contract: four segmented levels in the dialog");
const highButton = segButtons.find((b) => b.textContent?.trim() === "high")!;
assert.equal(highButton.getAttribute("aria-selected"), "true", "stored vocabulary preselects its levels");
assert.ok(dialog()!.textContent?.includes("Custom levels — this model declares its own vocabulary."), "declared-vocabulary hint when a list is stored");
const lowButton = segButtons.find((b) => b.textContent?.trim() === "low")!;
await act(async () => lowButton.click());
assert.equal(lowButton.getAttribute("aria-selected"), "true", "toggling a preset selects it");
const customInput = dialog()!.querySelector<HTMLInputElement>('input[placeholder="Custom level (press Enter)"]')!;
assert.ok(customInput, "custom effort level input present");
await fill(customInput, "  XHigh  ");
await act(async () => customInput.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
assert.ok(Array.from(dialog()!.querySelectorAll("label.set-check")).find((label) => label.textContent?.trim() === "xhigh"), "custom level normalized (lowercase/trim) and listed");
assert.equal(Array.from(dialog()!.querySelectorAll("label.set-check")).some((label) => label.textContent?.trim() === "auto"), false, "auto is never stored as a level");
await submit();
const existingOverride = provider.modelOverrides?.find((m) => m.model === "existing");
assert.deepEqual([...(existingOverride?.supportedEfforts ?? [])].sort(), ["high", "low", "xhigh"], "applied vocabulary lands on the override");
assert.equal(existingOverride?.defaultEffort, "high", "in-vocabulary default survives the apply");
// 空词表模型（manual 无声明、连接也无词表）→ 无预选 + 「未声明」提示。
const manualRowButton = Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-label="Model settings: manual"]'))[0];
assert.ok(manualRowButton, "manual model row has its own settings entry");
await act(async () => { manualRowButton.click(); });
for (let i = 0; i < 150 && !dialog(); i++) await new Promise((r) => setTimeout(r, 20));
assert.ok(dialog(), "manual model dialog opens");
assert.equal(Array.from(dialog()!.querySelectorAll<HTMLButtonElement>(".provider-add-segmented .provider-add-segmented__item")).some((b) => b.getAttribute("aria-selected") === "true"), false, "no levels preselected without a stored vocabulary");
assert.ok(dialog()!.textContent?.includes("No vocabulary declared here or at the connection level"), "inheritance hint when nothing is declared");
await act(async () => { dialog()!.dispatchEvent(new dom.window.Event("cancel", { cancelable: true })); });
assert.equal(dialog(), null);

await click("Refresh models");
await act(async () => { provider = { ...provider, baseUrl: "https://other.test/v1" }; render(); });
await act(async () => { resolveFetch!(catalog); });
assert.equal(document.querySelector('[role="dialog"]'), null, "old endpoint discovery must not reopen a dialog");
await act(async () => root.unmount());
dom.window.close();
console.log("PASS tl1104: unified SlidersHorizontal entry, ProviderModelDialog with audio, add-mode kept, 6 editable items");
