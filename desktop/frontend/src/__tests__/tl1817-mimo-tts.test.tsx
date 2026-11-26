// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1817-mimo-tts.test.tsx
//
// tl1817：朗读 MiMo TTS 传输层——实验室开关开 + 配置 MiMo key → 朗读走 MiMo
// chat/completions TTS（Go 桥合成，前端只拿 base64 wav）；任何失败自动降级
// 回 tl1709/tl1815 系统 speechSynthesis 链并给可见提示，不阻塞朗读。
//
// jsdom 没有 speechSynthesis / Audio / go 桥，本文件全程用 mock。真实性边界：
// 只能钉「逻辑接线」（检测→走 MiMo / 失败→降级系统链 / 开关两态 / 取消语义），
// 真机合成音质与凭据命中待用户实测。
//
// 钉面（任务书验收映射）：
// 1. lib 单元钉：speakViaMimo 三态（bridge 拒绝→failed；取消后迟到→cancelled；
//    正常播放→played）；cancelMimoTts 断代（新 speak 不受旧代影响）；
// 2. 接线钉（gate on）：点朗读 → app.MimoTtsSynthesize 收到选中文本（整段一次
//    请求）→ Audio 播放 base64 wav → ended → 卡片关、toggle 复位；
// 3. 降级钉（gate on + 合成失败）：speakSegments 收到 utterance（同段队列）+
//    卡片出现 mimoDegraded 可见提示——tl1815 分段链零回退（接位=播放传输层）；
// 4. 可用性优先钉（gate on + 无系统语音包）：MiMo 成功仍照常朗读（MiMo 排在
//    可用性阶梯之前）；gate on + MiMo 失败 + 无语音包 → 卡片硬停提示不静默；
// 5. gate off 零回退钉：默认关 → MimoTtsSynthesize 不被调用、走原系统链；
// 6. 三语钉：settings.mimoTts* 与 selection.quickAction.mimoDegraded 在
//    zh/zh-TW/en 三包齐平非空。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { TranscriptSelectionMenu } from "../components/TranscriptSelectionMenu";
import { LocaleProvider } from "../lib/i18n";
import { cancelMimoTts, speakViaMimo } from "../lib/mimoTts";
import { setMimoTtsEnabled } from "../lib/mimoTtsPreference";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import { MAX_SPEAK_CHARS } from "../lib/speech";

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

function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${safeJson(expected)}, got ${safeJson(actual)}`);
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function drainFrame(): Promise<void> {
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
  await flushTimers();
}

// ── Audio stub（play 即成功，onended/onerror 手动触发）──────────────────────
class FakeAudio {
  static instances: FakeAudio[] = [];
  src = "";
  onended: (() => void) | null = null;
  onerror: (() => void) | null = null;
  paused = true;
  playCalls = 0;
  constructor(src: string) {
    this.src = src;
    FakeAudio.instances.push(this);
  }
  play(): Promise<void> {
    this.playCalls += 1;
    this.paused = false;
    return Promise.resolve();
  }
  pause(): void {
    this.paused = true;
  }
  removeAttribute(_name: string): void {}
}

function installAudio(): void {
  (globalThis as unknown as Record<string, unknown>).Audio = FakeAudio;
}

function uninstallAudio(): void {
  delete (globalThis as unknown as Record<string, unknown>).Audio;
  FakeAudio.instances = [];
}

// ── go 桥 stub（realApp() = window.go.main.App，app Proxy 每次现取）─────────
type GoBridgeStub = {
  synthesizeCalls: string[];
  synthesizeImpl: (text: string) => Promise<string>;
  statusCalls: number;
};

function installGoBridge(): GoBridgeStub {
  const stub: GoBridgeStub = {
    synthesizeCalls: [],
    synthesizeImpl: async (text: string) => {
      stub.synthesizeCalls.push(text);
      return "ZmFrZS1hdWRpby1kYXRh"; // base64 shape only
    },
    statusCalls: 0,
  };
  const goHost = {
    main: {
      App: {
        MimoTtsSynthesize: (text: string) => stub.synthesizeImpl(text),
        MimoTtsStatus: () => {
          stub.statusCalls += 1;
          return Promise.resolve({ available: true, endpoint: "https://api.xiaomimimo.com/v1", keyEnv: "MIMO_API_KEY" });
        },
        SetExperimentalMimoTts: () => Promise.resolve(),
      },
    },
  };
  (window as unknown as Record<string, unknown>).go = goHost;
  return stub;
}

function removeGoBridge(): void {
  delete (window as unknown as Record<string, unknown>).go;
}

// ── speechSynthesis mock（与 tl1709 同款三面桩）─────────────────────────────
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
  return [...(document.querySelector<HTMLElement>('.transcript-selection-action[data-surface="transcript"]')?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
}

function speakButton(): HTMLButtonElement | null {
  return toolbarButtons().find((button) => button.dataset.selectionSpeakAction === "toggle") ?? null;
}

function resultCard(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".transcript-selection-result-card");
}

async function renderMenu() {
  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu
          onAddToChat={(text: string) => void text}
          onQuickAction={async () => "RESULT:stub"}
          quickActionsEnabled
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  return root;
}

const SELECTION_TEXT = "需要朗读的选中文本内容";

async function clickSpeak(): Promise<void> {
  await act(async () => {
    speakButton()?.click();
    await flushTimers();
    await flushTimers();
  });
}

// ── ① lib 单元钉 ────────────────────────────────────────────────────────────
{
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  installAudio();

  // bridge 拒绝 → failed（降级信号的源头）。
  {
    const failing = installGoBridge();
    failing.synthesizeImpl = async () => {
      throw new Error("mimo tts: http 401");
    };
    const outcome = await speakViaMimo("随便一段文本");
    eq(outcome, "failed", "speakViaMimo: bridge rejection settles failed");
    removeGoBridge();
  }
  // 取消后迟到的合成 → cancelled（不触发降级）。
  {
    const stub = installGoBridge();
    let release: ((value: string) => void) | null = null;
    stub.synthesizeImpl = () => new Promise<string>((resolve) => {
      release = resolve;
    });
    let outcome: string | null = null;
    const pending = speakViaMimo("取消竞态文本").then((value) => {
      outcome = value;
    });
    cancelMimoTts();
    release?.("b64");
    await pending;
    eq(outcome, "cancelled", "speakViaMimo: cancel supersedes a late synthesis (never degrades on stop)");
    removeGoBridge();
  }
  // 正常播放 → played。
  {
    installGoBridge();
    const pending = speakViaMimo("正常播放文本");
    await flushTimers();
    const audio = FakeAudio.instances[FakeAudio.instances.length - 1];
    ok(Boolean(audio) && audio.playCalls === 1, "speakViaMimo: synthesized wav starts playback exactly once");
    ok(audio.src.startsWith("data:audio/wav;base64,"), "speakViaMimo: playback rides a base64 wav data URI");
    audio.onended?.();
    const outcome = await pending;
    eq(outcome, "played", "speakViaMimo: natural end settles played");
    removeGoBridge();
  }
  uninstallAudio();
  removeGoBridge();
  // 词典不落地（node 进程侧不再需要 window）。
  (globalThis as unknown as Record<string, unknown>).window = undefined as unknown as Window;
}

// ── ②–⑤ 组件接线钉 ─────────────────────────────────────────────────────────
async function componentScenario(options: { gate: boolean; voices: FakeVoice[]; synthesize: (text: string) => Promise<string> }) {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as unknown as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  installAudio();
  const speech = installSpeechSynthesis(options.voices);
  const bridge = installGoBridge();
  // 缺省不覆盖——installGoBridge 默认实现即「记录调用 + 返回 b64」。
  if (options.synthesize) bridge.synthesizeImpl = options.synthesize;
  setMimoTtsEnabled(options.gate);
  const root = await renderMenu();
  // 选区目标放 body（root 之外）且包在 .msg__body 里——选区提取要求目标位于
  // 消息容器内（MESSAGE_SELECTION_SELECTOR），React render 会清空 #root。
  document.body.insertAdjacentHTML("beforeend", "<div class=\"msg__body\"><span id=\"sel-target\">" + SELECTION_TEXT + "</span></div>");
  const target = document.getElementById("sel-target") as HTMLElement;
  await openToolbar(target);
  await clickSpeak();
  return { dom, root, speech, bridge, cleanup: async () => {
    setMimoTtsEnabled(false);
    cancelMimoTts();
    await act(async () => {
      root.unmount();
      await flushTimers();
    });
    uninstallAudio();
    removeGoBridge();
    const host = window as unknown as Record<string, unknown>;
    delete host.speechSynthesis;
    delete host.SpeechSynthesisUtterance;
    delete host.go;
  } };
}

console.log("\ntl1817 mimo tts");

// ② 接线钉：gate on → 合成整段选文 → 播放 → ended → 卡片关。
{
  // 记录数组先于场景声明——synthesize 回调在场景 await 期间就会执行，禁止
  // 引用 scenario 本身（TDZ）。
  const record: string[] = [];
  const scenario = await componentScenario({
    gate: true,
    voices: [{ lang: "zh-CN", name: "Chinese" }],
    synthesize: (text) => {
      record.push(text);
      return Promise.resolve("b64");
    },
  });
  try {
    eq(record.length, 1, "gate on: exactly one MiMo synthesis request");
    eq(record[0], SELECTION_TEXT, "gate on: the full selection rides the request (whole-text transport)");
    eq(FakeAudio.instances.length >= 1, true, "gate on: synthesized audio starts playback");
    const audio = FakeAudio.instances[FakeAudio.instances.length - 1];
    ok(audio.src.includes("b64"), "gate on: playback element carries the returned payload");
    eq(scenario.speech.spoken.length, 0, "gate on: system speechSynthesis stays idle while MiMo plays");
    await act(async () => {
      audio.onended?.();
      await flushTimers();
      await flushTimers();
    });
    eq(resultCard() == null, true, "gate on: natural end closes the speaking card");
  } finally {
    await scenario.cleanup();
  }
}

// ③ 降级钉：合成失败 → 可见提示 + tl1815 分段链照常承接（同 segments）。
{
  const scenario = await componentScenario({
    gate: true,
    voices: [{ lang: "zh-CN", name: "Chinese" }],
    synthesize: async () => {
      throw new Error("mimo tts: http 401");
    },
  });
  try {
    eq(scenario.speech.spoken.length >= 1, true, "degrade: the tl1815 system segment chain takes over on failure");
    ok(Boolean(scenario.speech.spoken[0]?.text?.includes("需要朗读") || scenario.speech.spoken.some((u) => u.text.includes("需要朗读"))), "degrade: the same selection text is replayed via the system chain");
    const card = resultCard();
    ok(Boolean(card?.textContent?.includes(en["selection.quickAction.mimoDegraded"])), "degrade: the visible MiMo degrade notice rides the card (default locale = en; 三语在位由⑥钉承接)");
  } finally {
    await scenario.cleanup();
  }
}

// ④ 可用性优先钉：无系统语音包时 MiMo 成功仍朗读；MiMo 失败才落到硬停提示。
{
  const success = await componentScenario({
    gate: true,
    voices: [] as FakeVoice[],
    // 默认 synthesizeImpl 即「记录调用 + 返回 b64」——勿用不记录的覆盖。
  });
  try {
    eq(success.bridge.synthesizeCalls.length, 1, "no system voices + MiMo ok: synthesis still requested (MiMo rides ahead of the availability ladder)");
    eq(FakeAudio.instances.length >= 1, true, "no system voices + MiMo ok: playback proceeds");
  } finally {
    await success.cleanup();
  }
  const failed = await componentScenario({
    gate: true,
    voices: [],
    synthesize: async () => {
      throw new Error("mimo tts: no credential");
    },
  });
  try {
    ok(Boolean(resultCard()?.textContent?.includes(en["selection.quickAction.speechNoVoices"])), "no system voices + MiMo failed: the hard-stop guidance stays visible (never silent)");
    eq(failed.speech.spoken.length, 0, "no system voices + MiMo failed: nothing is queued on the system chain");
  } finally {
    await failed.cleanup();
  }
}

// ⑤ gate off 零回退钉：默认关 → 全程不触桥，原系统链照旧。
{
  const scenario = await componentScenario({
    gate: false,
    voices: [{ lang: "zh-CN", name: "Chinese" }],
    synthesize: async () => {
      scenario.bridge.synthesizeCalls.push("SHOULD-NOT-CALL");
      return "b64";
    },
  });
  try {
    eq(scenario.bridge.synthesizeCalls.length, 0, "gate off: the MiMo bridge is never touched");
    eq(scenario.speech.spoken.length >= 1, true, "gate off: the system chain reads exactly as before");
  } finally {
    await scenario.cleanup();
  }
}

// 截断上限随行钉（与 tl1709 同上限，MiMo 路径同用 MAX_SPEAK_CHARS 截面）。
ok(MAX_SPEAK_CHARS === 5000, "the MiMo transport inherits the tl1709 selection cap");

// ⑥ 三语钉。
for (const [name, dict] of [["zh", zh], ["zh-TW", zhTW], ["en", en]] as const) {
  const dictRecord = dict as Record<string, string>;
  for (const key of ["settings.mimoTts", "settings.mimoTtsHint", "settings.mimoTts.on", "settings.mimoTts.off", "selection.quickAction.mimoDegraded"]) {
    ok(Boolean(dictRecord[key]?.trim()), `${name}.${key} is present and non-empty`);
  }
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
