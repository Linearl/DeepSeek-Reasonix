import { JSDOM } from 'jsdom';
import { act } from 'react';
import assert from 'node:assert/strict';
// Run: tsx src/__tests__/task468-high-speed-model.test.tsx
// Task 468: the model dialog's high-speed checkbox rides the editor draft and
// lands through the connection save (SaveProvider → saveProviderConfig); the
// 318.1 master switch off hides the entry entirely.
const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost', pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage, Node: dom.window.Node, HTMLElement: dom.window.HTMLElement, requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window), cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window), IS_REACT_ACT_ENVIRONMENT: true });
window.HTMLDialogElement.prototype.showModal = function() { this.setAttribute('open',''); this.querySelector('input')?.focus(); };
const { createRoot } = await import('react-dom/client');
const { ProviderEditor } = await import('../components/SettingsPanel');
const { LocaleProvider } = await import('../lib/i18n');
const root = createRoot(document.getElementById('root')!);

let saved: any;
const baseProvider = { name:'custom', kind:'openai', baseUrl:'https://example.com/v1', models:['fast-1','slow-1'], default:'fast-1', apiKeyEnv:'TEST_KEY', keySet:true, added:true, builtIn:false, visionModels:[], supportedEfforts:[], modelsUrl:'', balanceUrl:'', contextWindow:0, modelOverrides:[] };
const render = (provider:any) => act(async () => root.render(<LocaleProvider><ProviderEditor key={JSON.stringify(provider)} initial={provider} kinds={['openai']} highSpeedSwitchOn={Boolean(provider.__switchOn)} busy={false} onCancel={()=>{}} onSave={async p => { saved=p; }}/></LocaleProvider>));
const draft = (switchOn:boolean) => ({ ...baseProvider, __switchOn: switchOn });
const edit = async (model:string) => {
  const btn=[...document.querySelectorAll<HTMLButtonElement>('.provider-model-draft__option button')].find(b=>b.getAttribute('aria-label')?.endsWith(': '+model));
  assert.ok(btn, `edit button for ${model}`);
  await act(async()=>{btn!.click();});
  // The dialog is React.lazy + Suspense — first import compiles slowly under
  // tsx, so poll for it instead of a fixed sleep.
  for (let i=0;i<150 && !dialog();i++) await new Promise(r=>setTimeout(r,20));
};
const dialog = () => document.querySelector('dialog');
const toggle = () => dialog()!.querySelector<HTMLInputElement>('[data-testid="high-speed-model-toggle"]');
const submitDialog = async () => act(async()=>{dialog()!.querySelector('form')!.dispatchEvent(new window.Event('submit',{bubbles:true,cancelable:true}));});
const saveConnection = async () => { const b=document.querySelector('.provider-editor-footer .btn--primary') as HTMLButtonElement; assert.ok(b && !b.disabled, 'save button enabled'); await act(async()=>b.click()); };
const closeDialog = async () => act(async()=>dialog()!.dispatchEvent(new window.Event('cancel',{cancelable:true})));
const rowBadge = (model:string) => [...document.querySelectorAll('.provider-model-draft__option')].find(n=>n.textContent?.includes(model))?.querySelector('[data-testid="high-speed-row-badge"]');
const typeInto = async (el:HTMLInputElement, value:string) => act(async()=>{
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value')!.set!;
  setter.call(el,value);
  el.dispatchEvent(new window.Event('input',{bubbles:true}));
});

// ①+② Mark via the dialog, save, and the mark lands in the connection payload.
saved=undefined;
await render(draft(true));
await edit('fast-1');
assert.ok(dialog(), 'dialog opens');
assert.ok(toggle(), 'high-speed entry present while the 318.1 master switch is on');
assert.equal(toggle()!.checked,false,'unmarked by default');
await act(async()=>toggle()!.click());
assert.equal(toggle()!.checked,true);
await submitDialog();
assert.equal(dialog(),null,'apply closes the dialog');
assert.ok(rowBadge('fast-1'),'⚡ row badge appears in real time after apply (before save)');
assert.ok(!rowBadge('slow-1'),'unmarked model shows no badge');
await saveConnection();
assert.deepEqual(saved.highSpeedModels,['fast-1'],'① checked mark rides the connection save');

// Display round trip: reopening the saved provider shows the stored mark; an
// unmark applies the same way.
await render({ ...saved, __switchOn:true });
await edit('fast-1');
assert.equal(toggle()!.checked,true,'① reopening shows the stored mark');
await act(async()=>toggle()!.click());
await submitDialog();
await saveConnection();
assert.deepEqual(saved.highSpeedModels,[],'① unmark removes via the same chain');

// ② Anti-wipe: a connection save that never opens the dialog must keep the
// stored marks (the editor draft used to omit the field entirely). Rename the
// connection so the save button enables without touching the dialog.
saved=undefined;
await render({ ...baseProvider, highSpeedModels:['fast-1','slow-1'], __switchOn:true });
assert.ok(rowBadge('fast-1') && rowBadge('slow-1'),'reopening shows stored marks as row badges');
const nameInput=document.querySelector<HTMLInputElement>('.provider-name-input')!;
await typeInto(nameInput,'renamed');
await saveConnection();
assert.equal(saved.displayName,'renamed','② the rename landed');
assert.deepEqual(saved.highSpeedModels,['fast-1','slow-1'],'② save without the dialog keeps every mark');

// ④ Master switch off: the dialog entry is hidden (no way to ADD a mark from
// the UI), but a save keeps stored marks — the master switch gates injection,
// not storage. Make the draft dirty via a model toggle so save is enabled.
saved=undefined;
await render({ ...baseProvider, highSpeedModels:['fast-1'], __switchOn:false });
assert.ok(rowBadge('fast-1'),'④ badge still displays stored marks with master off');
await edit('slow-1');
assert.ok(dialog(), 'dialog still opens for the other fields');
assert.equal(toggle(),null,'④ master off hides the high-speed entry');
await closeDialog();
const slowRow=[...document.querySelectorAll('.provider-model-draft__option')].find(n=>n.textContent?.includes('slow-1'))!;
await act(async()=>{ (slowRow.querySelector('label.provider-model-draft__model input') as HTMLInputElement).click(); });
await saveConnection();
assert.deepEqual(saved.models,['fast-1'],'④ only the toggled model left the list');
assert.deepEqual(saved.highSpeedModels,['fast-1'],'④ master off: stored marks persist, none added');

// Deleting a model drops its mark from the draft (stale-mark guard, UI side).
await render({ ...baseProvider, highSpeedModels:['fast-1'], __switchOn:true });
const row=[...document.querySelectorAll('.provider-model-draft__option')].find(n=>n.textContent?.includes('fast-1'))!;
await act(async()=>{ (row.querySelector('label.provider-model-draft__model input') as HTMLInputElement).click(); });
await saveConnection();
assert.deepEqual(saved.models,['slow-1'],'deleted model leaves the list');
assert.deepEqual(saved.highSpeedModels ?? [],[],'deleted model leaves no stale mark');

await act(async()=>root.unmount());
dom.window.close();
console.log('PASS task468: dialog mark round-trip, real-time row badge, anti-wipe save, master-off hidden, delete drops mark');
