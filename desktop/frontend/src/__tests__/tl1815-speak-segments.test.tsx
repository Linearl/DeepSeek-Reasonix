// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1815-speak-segments.test.tsx
//
// tl1815：中英混合分段朗读 + 英文语音包缺失的降级与指引。
//
// 现象（用户 CDP 实测）：WebView2 speechSynthesis.getVoices() 只暴露中文语音
//（zh-CN×3），en=[]——英文为主的选区直接报「未找到可朗读 English 文本的语音
// 包」；中英混合整段走中文语音，英文单词发音生硬。
//
// 修复面（任务书三层中的代码两层）：
// 1. 混合分段朗读：按 CJK/非 CJK 切段，各段用对应语言语音依次 utterance
//    （zh 段→zh 语音、en 段→en 语音）——替代单 utterance 二分；
// 2. en 缺失降级：无匹配语种语音不再硬报错——降级用现有语音尽力朗读
//    （utterance.lang 提示引擎），卡片给出「去设置装语音包」三语指引。
//
// jsdom 没有 speechSynthesis，全程 mock（同 tl1709 手法）。真实性边界：
// mock 只能钉「逻辑接线」（切段边界/段队列/语音路由/降级文案在场），真机
// 音色/语速/语种命中待用户装机实测（WebView2 走系统 TTS）。
//
// 钉面：
// A. segmentSpeakText 切段矩阵：纯 zh / 纯 en / 混合（英文词上 en 语音）/
//    数字与标点并段规则（不抖动语音）/ 全角标点跟随 / 引号包裹英文 /
//    文本完整性（切段拼接 === 原文）/ 空串 / 段数上限防御；
// B. findVoiceFor 语音路由：精确匹配优先 / 前缀匹配（zh-HK←zh-CN）/
//    下划线归一 / 缺包返回 null；
// C. speechAvailability 三态 + speechSupport 一致性；
// D. speakSegments 段队列：逐段入队、语音钉挂、最后一次 onend 才结算、
//    中途 onerror 取消余段且只结算一次、缺包段只带 lang 提示、空队列即结算；
// E. 组件层：混合选区多段入队（zh/en 交替）、缺包降级非错误卡 + 指引在场、
//    纯中文零回退（单段、无指引）、截断与指引并存。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { TranscriptSelectionMenu } from "../components/TranscriptSelectionMenu";
import { LocaleProvider } from "../lib/i18n";
import {
  MAX_SPEAK_CHARS,
  MAX_SPEAK_SEGMENTS,
  findVoiceFor,
  segmentSpeakText,
  speakSegments,
  speechAvailability,
  speechSupport,
  type SpeakSegment,
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

// 结构断言（段矩阵）：按 JSON 深比较，避免引用不等误报。
function eqJson(actual: unknown, expected: unknown, label: string) {
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a === b) ok(true, label);
  else ok(false, `${label}: expected ${b}, got ${a}`);
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

// ── speechSynthesis mock（同 tl1709：getVoices / speak / cancel + 桩）────────

class FakeUtterance {
  text: string;
  lang = "";
  voice: unknown;
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
    voices,
    spoken,
    cancelCount: () => cancelCount,
    endAll: () => {
      for (const utterance of [...spoken]) utterance.onend?.();
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

function toolbarButtons(): HTMLButtonElement[] {
  const host = document.querySelector<HTMLElement>('.transcript-selection-action[data-surface="transcript"]');
  return [...(host?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
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

// 段摘要（lang + text），让矩阵断言可读。
function shape(segments: SpeakSegment[]): Array<{ lang: string; text: string }> {
  return segments.map((segment) => ({ lang: segment.lang, text: segment.text }));
}

// ── A. segmentSpeakText 切段矩阵 ─────────────────────────────────────────────
{
  const dom = installDom();

  eq(segmentSpeakText("").length, 0, "segment: empty text yields no segments");

  eqJson(shape(segmentSpeakText("这是一段纯中文的选区内容")), [{ lang: "zh-CN", text: "这是一段纯中文的选区内容" }],
    "segment: pure zh text stays one zh segment");
  eqJson(shape(segmentSpeakText("hello world this is english")), [{ lang: "en-US", text: "hello world this is english" }],
    "segment: pure en text stays one en segment");

  // 英文词上 en 语音：混合句切成 zh|en|zh 三段（空格属 en 侧随词走）。
  eqJson(shape(segmentSpeakText("用 git 提交代码")), [
    { lang: "zh-CN", text: "用" },
    { lang: "en-US", text: " git " },
    { lang: "zh-CN", text: "提交代码" },
  ], "segment: latin word inside zh prose gets its own en segment");

  // 数字并段：裸数字不翻转语音（zh 语音连读数字），整句归 zh。
  eqJson(shape(segmentSpeakText("一共 123 个文件")), [{ lang: "zh-CN", text: "一共 123 个文件" }],
    "segment: bare digits inside zh prose ride the zh voice (no flap)");
  eqJson(shape(segmentSpeakText("版本 2.0 发布了")), [{ lang: "zh-CN", text: "版本 2.0 发布了" }],
    "segment: decimals ride the zh voice");
  eqJson(shape(segmentSpeakText("1. 第一步；2. 第二步")), [{ lang: "zh-CN", text: "1. 第一步；2. 第二步" }],
    "segment: leading list markers ride the zh voice");

  // 带字母的 en 段不并入：Step 1: 前缀保留 en 语音。
  eqJson(shape(segmentSpeakText("Step 1: 打开设置")), [
    { lang: "en-US", text: "Step 1: " },
    { lang: "zh-CN", text: "打开设置" },
  ], "segment: latin prefix keeps the en voice");

  // 全角标点跟随中文：破折号/省略号归 zh 段。
  eqJson(shape(segmentSpeakText("我想想……好吧")), [{ lang: "zh-CN", text: "我想想……好吧" }],
    "segment: zh ellipsis rides the zh run");
  eqJson(shape(segmentSpeakText("中文——English words")), [
    { lang: "zh-CN", text: "中文——" },
    { lang: "en-US", text: "English words" },
  ], "segment: zh em dash rides the zh run before latin");

  // 弯引号包裹英文：孤立引号并入 en 段（zh IME 引号不撕出 zh 碎段）。
  const quoted = segmentSpeakText(`say “hi” loudly`);
  eq(quoted.length, 1, "segment: curly quotes around english merge into the en run");
  eq(quoted[0]?.lang, "en-US", "segment: quoted english reads en");

  // 文本完整性：切段拼接恒等于原文（多组输入）。
  const integrityInputs = [
    "用 git 提交代码，然后 run tests 验证",
    "Step 1: 打开设置 → Time & Language；Step 2: 点击 Speech",
    "混合 texte avec 中文 and numbers 42",
    "　　　全角空格　测试",
  ];
  let integrity = true;
  for (const input of integrityInputs) {
    const joined = segmentSpeakText(input).map((segment) => segment.text).join("");
    if (joined !== input) integrity = false;
  }
  ok(integrity, "segment: concatenation of segment texts equals the input (integrity)");

  // 段数上限防御：1 字 zh + 1 字 en 交替 800 字 → 段数 ≤ 上限且文本完整。
  const pathological = "你a".repeat(400);
  const capped = segmentSpeakText(pathological);
  ok(capped.length <= MAX_SPEAK_SEGMENTS, `segment: pathological alternation caps at ${MAX_SPEAK_SEGMENTS} segments`);
  eq(capped.map((segment) => segment.text).join(""), pathological, "segment: capped segmentation keeps text integrity");

  dom.window.close();
}

// ── B. findVoiceFor 语音路由 ─────────────────────────────────────────────────
{
  const dom = installDom();

  removeSpeechSynthesis();
  eq(findVoiceFor("en-US"), null, "voice: missing speechSynthesis yields null");

  installSpeechSynthesis([]);
  eq(findVoiceFor("en-US"), null, "voice: no voices yields null");
  eq(speechSupport("zh-CN"), "no-voices", "voice: speechSupport agrees with availability (no-voices)");

  installSpeechSynthesis([
    { lang: "zh-HK", name: "Cantonese" },
    { lang: "zh-CN", name: "Mandarin" },
  ]);
  const exact = findVoiceFor("zh-CN");
  eq(exact?.name, "Mandarin", "voice: exact lang match wins over prefix match");
  eq(findVoiceFor("zh-HK")?.name, "Cantonese", "voice: prefix match serves sibling locale");
  eq(findVoiceFor("en-US"), null, "voice: absent language yields null");
  eq(speechSupport("en-US"), "no-match", "voice: speechSupport agrees with availability (no-match)");

  installSpeechSynthesis([{ lang: "zh_CN", name: "Legacy underscore" }]);
  eq(findVoiceFor("zh-CN")?.name, "Legacy underscore", "voice: underscore lang ids normalize");

  installSpeechSynthesis([
    { lang: "en-GB", name: "British" },
    { lang: "en-US", name: "American" },
  ]);
  eq(findVoiceFor("en-US")?.name, "American", "voice: exact en-US preferred over en-GB");

  eq(speechAvailability(), "ready", "voice: availability is ready with voices present");

  dom.window.close();
}

// ── C. speechAvailability 三态 ───────────────────────────────────────────────
{
  const dom = installDom();

  removeSpeechSynthesis();
  eq(speechAvailability(), "unsupported", "availability: missing speechSynthesis is unsupported");

  installSpeechSynthesis([]);
  eq(speechAvailability(), "no-voices", "availability: empty voice list is no-voices");

  installSpeechSynthesis([{ lang: "fr-FR", name: "French" }]);
  eq(speechAvailability(), "ready", "availability: any voice present is ready (per-lang handled by findVoiceFor)");

  dom.window.close();
}

// ── D. speakSegments 段队列 ──────────────────────────────────────────────────
{
  const dom = installDom();
  const speech = installSpeechSynthesis([
    { lang: "zh-CN", name: "Huihui" },
    { lang: "en-US", name: "Zira" },
  ]);

  let done = 0;
  const segments: SpeakSegment[] = [
    { text: "用 ", lang: "zh-CN" },
    { text: "git ", lang: "en-US" },
    { text: "提交", lang: "zh-CN" },
  ];
  speakSegments(segments, () => {
    done += 1;
  });

  eq(speech.spoken.length, 3, "queue: every segment queues its own utterance");
  eq(speech.spoken[0]?.text, "用 ", "queue: segments queue in order (1/3)");
  eq(speech.spoken[1]?.text, "git ", "queue: segments queue in order (2/3)");
  eq(speech.spoken[2]?.text, "提交", "queue: segments queue in order (3/3)");
  eq(speech.spoken[0]?.lang, "zh-CN", "queue: zh segment carries zh lang");
  eq(speech.spoken[1]?.lang, "en-US", "queue: en segment carries en lang");
  eq(speech.spoken[0]?.voice, speech.voices[0], "queue: zh segment pins the zh voice");
  eq(speech.spoken[1]?.voice, speech.voices[1], "queue: en segment pins the en voice");
  eq(done, 0, "queue: done waits while utterances are pending");
  ok(speech.cancelCount() >= 1, "queue: cancels in-flight speech before queueing");

  // 最后一段 end 才结算；前面的 end 不触发。
  speech.spoken[0]?.onend?.();
  eq(done, 0, "queue: intermediate onend does not settle");
  speech.spoken[1]?.onend?.();
  eq(done, 0, "queue: second-to-last onend does not settle");
  speech.spoken[2]?.onend?.();
  eq(done, 1, "queue: final onend settles exactly once");

  dom.window.close();
}

{
  const dom = installDom();

  // 缺包段：只带 lang 提示（voice 不钉），降级由引擎尽力。
  const speech = installSpeechSynthesis([{ lang: "zh-CN", name: "Huihui" }]);
  let done = 0;
  speakSegments([{ text: "english fallback", lang: "en-US" }], () => {
    done += 1;
  });
  eq(speech.spoken.length, 1, "degraded queue: the missing-pack segment still queues");
  eq(speech.spoken[0]?.voice, undefined, "degraded queue: no voice pinned when the pack is absent");
  eq(speech.spoken[0]?.lang, "en-US", "degraded queue: the lang hint stands for engine best effort");
  speech.spoken[0]?.onend?.();
  eq(done, 1, "degraded queue: settles on end");

  // 中途 onerror：取消余段 + 恰好结算一次。
  const speechErr = installSpeechSynthesis([
    { lang: "zh-CN", name: "Huihui" },
    { lang: "en-US", name: "Zira" },
  ]);
  let settled = 0;
  speakSegments([
    { text: "第一段", lang: "zh-CN" },
    { text: "second", lang: "en-US" },
    { text: "第三段", lang: "zh-CN" },
  ], () => {
    settled += 1;
  });
  eq(speechErr.spoken.length, 3, "abort: all three utterances queued up front");
  const cancelsBeforeError = speechErr.cancelCount();
  speechErr.spoken[1]?.onerror?.();
  eq(settled, 1, "abort: first onerror settles immediately");
  ok(speechErr.cancelCount() > cancelsBeforeError, "abort: remaining queue is cancelled");
  speechErr.spoken[2]?.onend?.();
  eq(settled, 1, "abort: late onend after cancel does not double-settle");

  // 空队列：直接结算，不碰 speak。
  const speechEmpty = installSpeechSynthesis([{ lang: "zh-CN", name: "Huihui" }]);
  let emptyDone = 0;
  speakSegments([], () => {
    emptyDone += 1;
  });
  eq(emptyDone, 1, "empty queue: settles immediately");
  eq(speechEmpty.spoken.length, 0, "empty queue: speak never called");

  // 构造器缺失（加固 webview / jsdom 裸环境）：不抛出，结算兜底。
  const speechCtor = installSpeechSynthesis([{ lang: "zh-CN", name: "Huihui" }]);
  (window as unknown as Record<string, unknown>).SpeechSynthesisUtterance = undefined;
  let ctorDone = 0;
  speakSegments([{ text: "boom", lang: "zh-CN" }], () => {
    ctorDone += 1;
  });
  eq(ctorDone, 1, "ctor throw: settles instead of throwing into the caller");
  eq(speechCtor.spoken.length, 0, "ctor throw: nothing queued");

  dom.window.close();
}

// ── E. 组件层：混合选区 / 缺包降级 / 纯中文零回退 ────────────────────────────
{
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  const speech = installSpeechSynthesis([
    { lang: "zh-CN", name: "Huihui" },
    { lang: "en-US", name: "Zira" },
  ]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">用 git 提交代码，然后 run tests</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  ok(readBtn != null, "mixed: Read aloud button renders");
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });

  ok(speech.spoken.length >= 3, "mixed: selection splits into multiple utterances");
  eq(speech.spoken.some((utterance) => utterance.lang === "zh-CN"), true, "mixed: zh segments routed zh");
  eq(speech.spoken.some((utterance) => utterance.lang === "en-US"), true, "mixed: en segments routed en");
  eq(speech.spoken.map((utterance) => utterance.text).join(""), "用 git 提交代码，然后 run tests", "mixed: utterances concatenate back to the selection");
  eq(speech.spoken.every((utterance) => utterance.voice != null), true, "mixed: every segment pins an installed voice");
  const cardBody = resultCard()?.querySelector<HTMLElement>(".transcript-selection-result-card__body");
  ok(cardBody?.dataset.state !== "error", "mixed: speaking card is not an error card");
  ok(resultCard()?.textContent?.includes("Reading aloud") === true, "mixed: speaking notice on screen");
  ok(resultCard()?.textContent?.includes("voice pack was found") === false, "mixed: no install guidance when both packs exist");

  // 自然结束（全部 onend）→ 卡片关、toggle 复位。
  await act(async () => {
    speech.endAll();
    await flushTimers();
  });
  eq(resultCard(), null, "mixed: card auto-closes after the last utterance ends");
  ok(readBtn?.textContent?.includes("Read aloud") === true, "mixed: toggle settles back after the queue drains");

  dom.window.close();
}

{
  // en 包缺失（只有 zh 语音）：英文选区降级朗读 + 指引，不再裸报错。
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  const speech = installSpeechSynthesis([{ lang: "zh-CN", name: "Huihui" }]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">assistant reply in english</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  const cardBody = resultCard()?.querySelector<HTMLElement>(".transcript-selection-result-card__body");
  eq(speech.spoken.length, 1, "en missing: the utterance still queues (degraded)");
  eq(speech.spoken[0]?.lang, "en-US", "en missing: lang hint kept");
  eq(speech.spoken[0]?.voice, undefined, "en missing: no voice pinned");
  ok(cardBody?.dataset.state !== "error", "en missing: not an error card");
  ok(resultCard()?.textContent?.includes("No English voice pack was found") === true, "en missing: zh UI shows install guidance with English interpolated");
  ok(resultCard()?.textContent?.includes("Windows") === true, "en missing: guidance names the settings surface");

  dom.window.close();
}

{
  // 纯中文 + zh 语音：零回退——单段单 utterance，无指引。
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  const speech = installSpeechSynthesis([{ lang: "zh-CN", name: "Huihui" }]);
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\">这是一段纯中文的选区内容</div>");
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(speech.spoken.length, 1, "pure zh: exactly one utterance (zero regression)");
  eq(speech.spoken[0]?.text, "这是一段纯中文的选区内容", "pure zh: utterance carries the whole selection");
  eq(speech.spoken[0]?.lang, "zh-CN", "pure zh: zh lang preserved");
  eq(speech.spoken[0]?.voice, speech.voices[0], "pure zh: zh voice pinned");
  ok(resultCard()?.textContent?.includes("语音包") === false, "pure zh: no guidance noise");

  dom.window.close();
}

{
  // 截断与缺包指引并存：长选区 + 只有 zh 语音 → 卡片同时有 truncated 与指引。
  const dom = installDom();
  (window as unknown as { runtime: unknown }).runtime = {};
  const speech = installSpeechSynthesis([{ lang: "zh-CN", name: "Huihui" }]);
  document.body.insertAdjacentHTML("beforeend", `<div class="msg__body">${"word ".repeat(1100)}</div>`);
  const msgBody = document.querySelector(".msg__body") as HTMLElement;
  await renderMenu();

  await openToolbar(msgBody);
  const readBtn = toolbarButtons().find((button) => button.textContent?.includes("Read aloud"));
  await act(async () => {
    readBtn?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(speech.spoken[0]?.text.length, MAX_SPEAK_CHARS, "truncated+missing: utterance caps at MAX_SPEAK_CHARS");
  ok(resultCard()?.textContent?.includes("truncated") === true, "truncated+missing: truncation notice present");
  ok(resultCard()?.textContent?.includes("No English voice pack was found") === true, "truncated+missing: install guidance present alongside");

  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
process.exit(failed === 0 ? 0 : 1);
