// tl1774 (issue #105): 裸 base 保存前示警 + 确认后保存。判例沿用
// provider-editor-compact：jsdom + act 驱动 ProviderEditor，en 语区钉死。
import { JSDOM } from 'jsdom';
import { act } from 'react';
import assert from 'node:assert/strict';
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost', pretendToBeVisual: true });
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage, Node: dom.window.Node, HTMLElement: dom.window.HTMLElement, requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window), cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window), IS_REACT_ACT_ENVIRONMENT: true });
window.HTMLDialogElement.prototype.showModal = function() { this.setAttribute('open',''); this.querySelector('input')?.focus(); };
const { createRoot } = await import('react-dom/client');
const { ProviderEditor } = await import('../components/SettingsPanel');
const { LocaleProvider } = await import('../lib/i18n');
const root = createRoot(document.getElementById('root')!);
let saved: any;
// 已存连接的裸 base 形态（issue #105 的 LocalAI 现场）：显式 request_url 裸地址。
const provider = { name:'localai', kind:'openai', baseUrl:'http://192.168.1.50:8080', requestUrl:'http://192.168.1.50:8080', chatUrl:'http://192.168.1.50:8080', models:['model-a'], default:'model-a', apiKeyEnv:'TEST_KEY', keySet:true, added:true, builtIn:false, visionModels:[], supportedEfforts:[], modelsUrl:'', balanceUrl:'', contextWindow:0, modelOverrides:[] };
await act(async () => root.render(<LocaleProvider><ProviderEditor initial={provider as any} kinds={['openai']} busy={false} onCancel={()=>{}} onSave={async p => { saved=p; }}/></LocaleProvider>));
const urlInput = document.querySelector('.provider-url-input') as HTMLInputElement;
const save = document.querySelector('.provider-editor-footer .btn--primary') as HTMLButtonElement;
const banner = () => document.querySelector('.provider-connection-field .banner--warning');
async function change(input: HTMLInputElement, value: string) { await act(async()=>{Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value')!.set!.call(input,value);input.dispatchEvent(new window.Event('input',{bubbles:true}));}); }

assert.equal(urlInput.value,'http://192.168.1.50:8080','bare base opens as typed');
assert.ok(banner(),'bare base warns on editor open');
assert.match(banner()!.textContent ?? '', /\/chat\/completions/,'warning names the protocol path');
const apply = banner()!.querySelector('button') as HTMLButtonElement;
assert.equal(apply.title,'http://192.168.1.50:8080/chat/completions','apply carries the contract-layer suggestion');

await change(urlInput,'http://10.0.0.9');
assert.ok(banner(),'typed bare base still warns');
assert.equal(save.disabled,false,'warning never blocks confirm-to-save');
await act(async()=>save.click());
assert.equal(saved.requestUrl,'http://10.0.0.9','confirmed custom bare path saves as typed');

await act(async()=>apply.click());
assert.equal((document.querySelector('.provider-url-input') as HTMLInputElement).value,'http://10.0.0.9/chat/completions','apply rewrites to the suggested URL');
assert.equal(banner(),null,'warning clears after apply');

await change(urlInput,'http://10.0.0.9/v1/chat/completions');
assert.equal(banner(),null,'correctly filled address stays silent');
assert.equal(save.disabled,false,'correct address keeps save enabled');
console.log('tl1774 provider editor path shape: bare-base warning, confirm-to-save, apply and silence all passed.');
