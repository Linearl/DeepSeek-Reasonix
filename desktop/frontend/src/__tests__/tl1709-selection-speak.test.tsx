// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1709-selection-speak.test.tsx
//
// tl1709：选中文本加「朗读」按钮——与翻译/解释并列第三项。前端 Web Speech API
//（window.speechSynthesis）即点即读，零后端零网络，系统 TTS 语音。
//
// jsdom 没有 speechSynthesis，本文件全程用 mock（getVoices / speak / cancel
// 三面 + SpeechSynthesisUtterance 桩）。真实性边界：mock 只能钉「逻辑接线」
//（调用发生、参数正确、状态迁移、文案在场），真机朗读音色/语速/语种命中
// 待用户装机实测（WebView2 走系统 TTS）。
//
// 钉面（任务书五钉 + 伴生语义）：
// 1. 容器并列钉：工具条「Add to Chat / Translate / Explain / Read aloud」并列、
//    右键菜单「Copy / Translate / Explain / Read aloud」并列；
// 2. speak 调用钉：点朗读 → speechSynthesis.speak 收到 utterance，文本 =
//    选中文本、lang = 字符集判定（英文 en-US / 中文 zh-CN）；utterance.lang
//    之外还断言按钮 toggle 文案与卡片 speaking 提示；
// 3. toggle 钉：朗读中再点 → speechSynthesis.cancel 被调用、卡片消失、文案复位；
// 4. 无语音包钉：getVoices 返回空 → 卡片提示「No system speech voices were
//    found」；无匹配语种 → 「No speech voice available for {lang} text」；
//    无 speechSynthesis → 「not available in this environment」——不静默失败；
// 5. 截断钉：>MAX_SPEAK_CHARS 的选段 → utterance 文本 ≤ 上限、卡片 truncated
//    文案在场；
// 6. 伴生语义钉：朗读完成（onend）→ 卡片自动关、toggle 复位；Escape 关卡片
//    → cancel 被调（卡片关 = 朗读停）；
// 7. lib 单元钉：detectSpeakLang 语种判定矩阵、speechSupport 四态、
//    speakSelection 先 cancel 再 speak（防叠音）。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { TranscriptSelectionMenu } from "../components/TranscriptSelectionMenu";
import { LocaleProvider } from "../lib/i18n";
import {
  detectSpeakLang,
  speechSupport,
  speakSelection,
  MAX_SPEAK_CHARS,
  type SpeechSupport,
} from "../lib/speech";

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

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function drainFrame(): Promise<void> {
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
  await flushTimers();
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

// ── speechSynthesis mock：getVoices / speak / cancel 三面 + utterance 桩 ──────

class FakeUtterance {
  text: string;
  lang = "";
  onend: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(text: string) {
    this.text = text;
  }
}

type FakeVoice = { lang: string; name: string };

function installSpeechSynthesis(voices: FakeVoice[]) {
  const spoken: FakeUtterance[] = [];
  let cancelCount = 0;
  const synth = {
    getVoices: () => voices,
    speak: (utterance: FakeUtterance) => {
      spoken.push(utterance);
    },
    cancel: () => {
      cancelCount += 1;
    },
    addEventListener: () => {},
    removeEventListener: () => {},
  };
  const host = window as unknown as Record<string, unknown>;
  host.speechSynthesis = synth;
  host.SpeechSynthesisUtterance = FakeUtterance;
  return {
    spoken,
    cancelCount: () => cancelCount,
    endLast: () => {
      const last = spoken[spoken.length - 1];
      if (last?.onend) last.onend();
    },
  };
}

function removeSpeechSynthesis() {
  const host = window as unknown as Record<string, unknown>;
  delete host.speechSynthesis;
  delete host.SpeechSynthesisUtterance;
}

function selectNodeText(node: Node) {
  const range = document.createRange();
  range.selectNodeContents(node);
  const selection = document.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

async function openToolbar(target: HTMLElement) {
  selectNodeText(target.firstChild as Node);
  await act(async () => {
    target.dispatchEvent(new window.MouseEvent("pointerup", { bubbles: true, button: 0 }));
    await drainFrame();
    await flushTimers();
    await flushTimers();
  });
}

function transcriptActionHost(): HTMLElement | null {
  return document.querySelector<HTMLElement>('.transcript-selection-action[data-surface="transcript"]');
}

function toolbarButtons(): HTMLButtonElement[] {
  return [...(transcriptActionHost()?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
}

async function dispatchContextMenu(target: Element): Promise<MouseEvent> {
  const event = new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 120, clientY: 80 });
  await act(async () => {
    target.dispatchEvent(event);
    await flushTimers();
  });
  return event;
}

function menuItems(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>("[role=\"menuitem\"]")];
}

function resultCard(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".transcript-selection-result-card");
}

async function renderMenu(props: Record<string, unknown> = {}) {
  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu
          onAddToChat={(text: string) => void text}
          onQuickAction={async () => "RESULT:stub"}
          quickActionsEnabled
          {...props}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  return root;
}

// ── ① lib 单元钉：语种判定 / 支持四态 / 防叠音 ───────────────────────────────
{
  const dom = installDom();
  eq(detectSpeakLang("hello world this is a plain test"), "en-US", "detectSpeakLang: plain latin reads en-US");
  eq(detectSpeakLang("这是一段中文选区的内容"), "zh-CN", "detectSpeakLang: CJK prose reads zh-CN");
  eq(detectSpeakLang("这段混合文本 mostly 中文字符 dominates"), "zh-CN", "detectSpeakLang: CJK-dominant mixed reads zh-CN");
  eq(detectSpeakLang("Numbers 123 and symbols !? stay latin"), "en-US", "detectSpeakLang: digits/symbols do not flip zh");
  eq(detectSpeakLang(""), "en-US", "detectSpeakLang: empty reads en-US");

  eq(speechSupport("en-US"), "unsupported", "speechSupport: missing speechSynthesis is unsupported");

  const synthRef = installSpeechSynthesis([]);
  eq(speechSupport("en-US"), "no-voices", "speechSupport: empty voice list is no-voices");
  removeSpeechSynthesis();
  installSpeechSynthesis([{ lang: "fr-FR", name: "French" }]);
  eq(speechSupport("en-US"), "no-match", "speechSupport: foreign-only voices is no-match");
  removeSpeechSynthesis();
  installSpeechSynthesis([{ lang: "en-US", name: "English" }, { lang: "zh-CN", name: "Chinese" }]);
  eq(speechSupport("en-US"), "ready", "speechSupport: matching voice is ready");
  eq(speechSupport("zh-CN"), "ready", "speechSupport: zh voice serves zh requests");
  removeSpeechSynthesis();
  const speechRef = installSpeechSynthesis([{ lang: "zh_CN", name: "Legacy underscore lang" }]);
  eq(speechSupport("zh-CN"), "ready", "speechSupport: underscore lang ids normalize to a match");

  // speakSelection：先 cancel（清残留队列防叠音）再 speak；utterance 带 lang。
  let done = 0;
  speakSelection("read me please", "en-US", () => {
    done += 1;
  });
  eq(speechRef.spoken.length, 1, "speakSelection: queues exactly one utterance");
  eq(speechRef.spoken[0]?.text, "read me please", "speakSelection: utterance carries the text");
  eq(speechRef.spoken[0]?.lang, "en-US", "speakSelection: utterance carries the detected lang");
  ok(speechRef.cancelCount() >= 1, "speakSelection: cancels queued speech before speaking");
  speechRef.endLast();
  eq(done, 1, "speakSelection: onend settles the done callback");
  speechRef.spoken[0]!.onerror?.();
  eq(done, 2, "speakSelection: onerror also settles the done callback");

  // 截断（单元层）：> MAX_SPEAK_CHARS 的文本进 utterance 前被裁齐。
  const longText = "word ".repeat(MAX_SPEAK_CHARS);
  speakSelection(longText, "en-US");
  eq(speechRef.spoken[1]?.text.length, MAX_SPEAK_CHARS, "speakSelection: oversized text truncates to MAX_SPEAK_CHARS");
  ok(longText.length > MAX_SPEAK_CHARS, "fixture sanity: the long text really exceeds the cap");

  dom.window.close();
}

// ── ② 钉1：容器并列渲染（工具条 + 右键菜单） ─────────────────────────────────
{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  installSpeechSynthesis([{ lang: "en-US", name: "English" }, { lang: "zh-CN", name: "Chinese" }]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply text</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;

  const root = await renderMenu();

  await openToolbar(msgBody);
  const buttons = toolbarButtons().map((button) => button.textContent ?? "");
  ok(buttons.some((label) => label.includes("Add to Chat")), "toolbar: Add to Chat present");
  ok(buttons.some((label) => label.includes("Translate")), "toolbar: Translate present");
  ok(buttons.some((label) => label.includes("Explain")), "toolbar: Explain present");
  ok(buttons.some((label) => label.includes("Read aloud")), "toolbar: Read aloud present as the third quick action");

  // 并列次序钉：Read aloud 排在 Translate/Explain 之后（第三项），路径按钮组之前。
  const readIdx = buttons.findIndex((label) => label.includes("Read aloud"));
  const explainIdx = buttons.findIndex((label) => label.includes("Explain"));
  const translateIdx = buttons.findIndex((label) => label.includes("Translate"));
  ok(readIdx > explainIdx && explainIdx > translateIdx, "toolbar: Read aloud sits right after Translate/Explain");

  await act(async () => { root.unmount(); });
  dom.window.close();
}

{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  installSpeechSynthesis([{ lang: "en-US", name: "English" }]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply text</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  selectNodeText(msgBody.firstChild as Node);
  await dispatchContextMenu(msgBody);
  const items = menuItems().map((item) => item.textContent ?? "");
  eq(items.length, 4, "context menu: Copy + Translate + Explain + Read aloud");
  ok(items.some((label) => label.includes("Read aloud")), "context menu: Read aloud present");
  ok(items.some((label) => label.includes("Translate")) && items.some((label) => label.includes("Explain")), "context menu: translate/explain unchanged");

  dom.window.close();
}

// ── ③ 钉2+3：speak 调用 / 语种自动 / toggle cancel ───────────────────────────
{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  const speech = installSpeechSynthesis([
    { lang: "en-US", name: "English" },
    { lang: "zh-CN", name: "Chinese" },
  ]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply text</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  // 英文选段：点朗读 → speak 被调，utterance 文本/语种正确。
  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  ok(readBtn != null, "toolbar exposes the Read aloud button");
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(speech.spoken.length, 1, "read aloud: speechSynthesis.speak called once");
  eq(speech.spoken[0]?.text, "assistant reply text", "read aloud: utterance text is the selection");
  eq(speech.spoken[0]?.lang, "en-US", "read aloud: latin selection auto-detects en-US");
  ok(readBtn?.textContent?.includes("Stop") === true, "read aloud: button flips to Stop while speaking");
  ok(resultCard()?.textContent?.includes("Reading aloud") === true, "read aloud: speaking card is on screen");

  // toggle：再点 Stop → cancel 被调、卡片消失、文案复位。
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  ok(speech.cancelCount() >= 1, "toggle: second click calls speechSynthesis.cancel");
  eq(resultCard(), null, "toggle: speaking card closes");
  ok(readBtn?.textContent?.includes("Read aloud") === true, "toggle: button flips back to Read aloud");
  eq(speech.spoken.length, 1, "toggle: no second utterance queued by the stop click");

  // 自然结束：onend → 卡片自动关、按钮复位（不依赖用户点击）。
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(speech.spoken.length, 2, "restart: a fresh utterance is queued");
  await act(async () => {
    speech.endLast();
    await flushTimers();
  });
  eq(resultCard(), null, "natural end: speaking card auto-closes");
  ok(readBtn?.textContent?.includes("Read aloud") === true, "natural end: toggle state settles back");

  // 中文选段：语种自动判 zh-CN。
  (msgBody.firstChild as Text).textContent = "这是一段中文的选区内容";
  await openToolbar(msgBody);
  const readBtnZh = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtnZh?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(speech.spoken[2]?.lang, "zh-CN", "read aloud: CJK selection auto-detects zh-CN");
  eq(speech.spoken[2]?.text, "这是一段中文的选区内容", "read aloud: zh utterance carries the selection");

  dom.window.close();
}

// ── ④ 钉4：无语音包 / 无匹配语种 / 环境不支持——提示在场，不静默失败 ──────────
{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  installSpeechSynthesis([]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply text</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  const card = resultCard();
  ok(card != null, "no voices: the click still lands in a visible card");
  const cardBody = card?.querySelector<HTMLElement>(".transcript-selection-result-card__body");
  ok(cardBody?.dataset.state === "error", "no voices: card paints the error state");
  ok(card?.textContent?.includes("No system speech voices were found") === true, "no voices: explicit notice in the card");
  eq(cardBody?.textContent?.includes("Reading aloud"), false, "no voices: never shows the speaking indicator");

  dom.window.close();
}

{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  installSpeechSynthesis([{ lang: "fr-FR", name: "French" }]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply text</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  ok(resultCard()?.textContent?.includes("No speech voice available for English text") === true, "no match: {lang} interpolated into the notice");

  dom.window.close();
}

{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  installSpeechSynthesis([{ lang: "en-US", name: "English" }]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">这是一段中文的选区内容</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  // 中文文本 + 只有英文语音包：插值显示内容语言「中文」。
  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  ok(resultCard()?.textContent?.includes("中文") === true, "no match (zh text): the notice names the content language 中文");

  dom.window.close();
}

{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  removeSpeechSynthesis();
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply text</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  ok(readBtn != null, "unsupported: the button itself still renders");
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  ok(resultCard()?.textContent?.includes("not available in this environment") === true, "unsupported: environment notice in the card");

  dom.window.close();
}

// ── ⑤ 钉5：组件层长文本截断 + truncated 文案 ─────────────────────────────────
{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  const speech = installSpeechSynthesis([{ lang: "en-US", name: "English" }]);
  document.body.insertAdjacentHTML("beforeend", `<div class="msg__body">${"word ".repeat(1100)}</div>`);
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(speech.spoken.length, 1, "oversized selection: one truncated utterance");
  eq(speech.spoken[0]?.text.length, MAX_SPEAK_CHARS, `oversized selection: utterance caps at ${MAX_SPEAK_CHARS} chars`);
  ok(resultCard()?.textContent?.includes("truncated") === true, "oversized selection: truncation notice renders in the card");

  dom.window.close();
}

// ── ⑥ 伴生语义：卡片被 Escape 关闭 = 朗读停止 ────────────────────────────────
{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  const speech = installSpeechSynthesis([{ lang: "en-US", name: "English" }]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply text</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  ok(resultCard() != null, "escape companion: speaking card is open");
  await act(async () => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  eq(resultCard(), null, "escape companion: card closes");
  ok(speech.cancelCount() >= 1, "escape companion: closing the card stops the playback");

  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
process.exit(failed === 0 ? 0 : 1);
