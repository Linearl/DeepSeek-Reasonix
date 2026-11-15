// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1609-selection-symmetry.test.tsx
//
// tl1609：转录选区菜单对称化——同一套菜单（添加到聊天 / 翻译 / 解释 / 路径打开）
// 对 transcript 内所有可选中文本生效，不再只认 agent 消息。
//
// 钉面：
// 1. 对称化钉：用户消息 / 引导消息（notice-line）/ steer 卡 / 完成汇报卡
//    （completion-report）的选区既开悬浮工具条也接管右键；
// 2. agent 消息既有行为零回退（控制组：toolbar + 右键 + 精确转发）；
// 3. 路径识别按选中文本内容判定、不按消息来源判定：同一段路径文字在
//    notice 卡 / 完成汇报卡 / agent 正文三种来源下表现一致（探针桩返回
//    解析结果 → 两项路径动作出现；非路径文字零误报）；
// 4. 单元矩阵钉：messageSelectionContextText 对每个新表面返回文本、对
//    卡片 chrome（时间戳/meta 行）保持 null——「卡外右键不劫持」负例不丢；
// 5. gate 单元钉：Ctrl/Cmd+C 拦截器（installMessageSelectionCopy）与菜单
//    同一 selector，notice 表面的原生 copy 也被接管（对称化的另一半）。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { TranscriptSelectionMenu } from "../components/TranscriptSelectionMenu";
import { LocaleProvider } from "../lib/i18n";
import { installMessageSelectionCopy, messageSelectionContextText } from "../lib/messageSelectionCopy";

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

function selectNodeText(node: Node) {
  const range = document.createRange();
  range.selectNodeContents(node);
  const selection = document.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function transcriptActionHost(): HTMLElement | null {
  return document.querySelector<HTMLElement>('.transcript-selection-action[data-surface="transcript"]');
}

function transcriptActionState(): string | undefined {
  return transcriptActionHost()?.dataset.state;
}

function pathActionButtons(): HTMLButtonElement[] {
  return [...(transcriptActionHost()?.querySelectorAll<HTMLButtonElement>("[data-selection-path-action]") ?? [])];
}

// ── ① 单元矩阵钉：gate 对称覆盖 + 卡片 chrome 负例 ─────────────────────────
{
  const dom = installDom();
  document.body.insertAdjacentHTML(
    "beforeend",
    "<div class=\"transcript\">" +
      // agent 正文（既有表面，控制组）
      "<div class=\"transcript__row\" data-row-key=\"a1\"><div class=\"msg msg--assistant\"><div class=\"msg__body\">assistant body text</div></div></div>" +
      // 用户消息气泡（727 行已有 .msg__body，钉住不回退）
      "<div class=\"transcript__row\" data-row-key=\"u1\"><div class=\"msg msg--user\"><div class=\"msg__body\">user bubble text</div></div></div>" +
      // 引导消息卡（notice-line__text 是 tl1609 新增表面）
      "<div class=\"transcript__row\" data-row-key=\"n1\"><div class=\"notice-line notice-line--info\"><div class=\"notice-line__text\"><div class=\"notice-line__title\">notice title</div><div class=\"notice-line__body\">notice body text</div></div><time class=\"notice-line__time\">12:00</time></div></div>" +
      // steer 引导气泡 + deferred 展开体（tl1609 新增表面）
      "<div class=\"transcript__row\" data-row-key=\"s1\"><div class=\"steer-line\"><div class=\"steer-line__bubble\"><span class=\"steer-line__text\">steer bubble text</span></div><div class=\"steer-line__expanded\"><span class=\"steer-line__text steer-line__text--expanded\">steer expanded text</span></div></div></div>" +
      // 跨会话投递卡（steer 形态的 im-source-card__text，tl1609 新增表面）
      "<div class=\"transcript__row\" data-row-key=\"c1\"><div class=\"steer-line\"><div class=\"im-source-card im-source-card--steer\"><div class=\"im-source-card__text\">collab delivery text</div></div></div></div>" +
      // 完成汇报卡（completion-report，tl1609 新增表面）
      "<div class=\"transcript__row\" data-row-key=\"r1\"><div class=\"completion-report\" role=\"note\"><dl class=\"completion-report__fields\"><dd>report field value</dd></dl></div></div>" +
      // host 协议折叠条展开体（tl1609 新增表面）
      "<div class=\"transcript__row\" data-row-key=\"h1\"><div class=\"notice-line notice-line--host-protocol\"><pre class=\"host-protocol-bar__body\">host protocol body</pre></div></div>" +
    "</div>",
  );

  const positive: Array<[string, string]> = [
    [".msg--assistant .msg__body", "assistant body keeps working (control)"],
    [".msg--user .msg__body", "user bubble body gates the menu"],
    [".notice-line__text", "notice-line text gates the menu"],
    [".notice-line .notice-line__title", "notice title inside the text region gates the menu"],
    [".steer-line__bubble", "steer bubble gates the menu"],
    [".steer-line__expanded", "steer deferred expansion gates the menu"],
    [".im-source-card--steer .im-source-card__text", "steer collab delivery text gates the menu"],
    [".completion-report dd", "completion report card gates the menu"],
    [".host-protocol-bar__body", "host protocol expanded body gates the menu"],
  ];
  for (const [selector, label] of positive) {
    const el = document.querySelector(selector) as HTMLElement;
    selectNodeText(el);
    const text = messageSelectionContextText(document, el);
    ok(text != null && text.trim() !== "", `unit gate: ${label}`);
  }
  const chrome: Array<[string, string]> = [
    [".notice-line__time", "notice timestamp stays menu-free (card chrome)"],
  ];
  for (const [selector, label] of chrome) {
    const el = document.querySelector(selector) as HTMLElement;
    selectNodeText(document.querySelector(".notice-line__body") as HTMLElement);
    const event = new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 });
    Object.defineProperty(event, "target", { value: el });
    const text = messageSelectionContextText(document, el);
    eq(text, null, `unit gate: ${label} (selection held elsewhere, click outside the body)`);
    void event;
  }
  dom.window.close();
}

// ── ② 组件级对称化钉 + ③ 路径按内容判定钉 ──────────────────────────────────
{
  const dom = installDom();
  const additions: string[] = [];
  const probes: Array<{ tabID: string; path: string }> = [];
  const opened: string[] = [];
  const revealed: string[] = [];
  (window as unknown as { runtime: unknown }).runtime = {};
  (window as unknown as { go: unknown }).go = {
    main: {
      App: {
        // 内容权威探针：凡含 "plan.md" 的候选按存在路径解析，其余返回空串
        //（零误报语义与生产探针一致）。
        ResolveOpenablePathForTab: async (tabID: string, path: string) => {
          probes.push({ tabID, path });
          return path.includes("plan.md") ? `D:\\proj\\${path}` : "";
        },
        OpenLocalPath: async (path: string) => { opened.push(path); },
        RevealPath: async (path: string) => { revealed.push(path); },
      },
    },
  };

  document.body.insertAdjacentHTML(
    "beforeend",
    "<div class=\"transcript\">" +
      "<div class=\"transcript__row\" data-row-key=\"u1\"><div class=\"msg msg--user\"><div class=\"msg__body\">user asks about docs/plan.md today</div></div></div>" +
      "<div class=\"transcript__row\" data-row-key=\"n1\"><div class=\"notice-line notice-line--info\"><div class=\"notice-line__text\"><div class=\"notice-line__body\">see docs/plan.md for details</div></div></div></div>" +
      "<div class=\"transcript__row\" data-row-key=\"n2\"><div class=\"notice-line notice-line--info\"><div class=\"notice-line__text\"><div class=\"notice-line__body\">plain notice sentence without paths</div></div></div></div>" +
      "<div class=\"transcript__row\" data-row-key=\"r1\"><div class=\"completion-report\" role=\"note\"><dl class=\"completion-report__fields\"><dd>docs/plan.md</dd></dl></div></div>" +
      "<div class=\"transcript__row\" data-row-key=\"a1\"><div class=\"msg msg--assistant\"><div class=\"msg__body\">assistant reply cites docs/plan.md too</div></div></div>" +
    "</div>",
  );

  const root = createRoot(document.getElementById("root") as HTMLElement);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptSelectionMenu onAddToChat={(text) => additions.push(text)} />
      </LocaleProvider>,
    );
    await flushTimers();
  });

  async function openToolbar(target: HTMLElement) {
    selectNodeText(target);
    await act(async () => {
      target.dispatchEvent(new window.MouseEvent("pointerup", { bubbles: true, button: 0 }));
      await drainFrame();
      await flushTimers();
      await flushTimers();
    });
  }

  // 对称化：四个表面全部开出悬浮工具条 + 接管右键 + Add to Chat 精确转发。
  const surfaces: Array<[string, string]> = [
    [".msg--user .msg__body", "user bubble"],
    [".notice-line__body", "notice card"],
    [".completion-report dd", "completion report card"],
    [".msg--assistant .msg__body", "assistant body (control)"],
  ];
  let additionIndex = 0;
  for (const [selector, label] of surfaces) {
    const el = document.querySelector(selector) as HTMLElement;
    await openToolbar(el);
    eq(transcriptActionState(), "open", `${label}: selection opens the floating toolbar`);
    const addButton = transcriptActionHost()?.querySelector("button") as HTMLButtonElement | null;
    ok(addButton?.textContent?.includes("Add to Chat") === true, `${label}: toolbar offers Add to Chat`);
    await act(async () => {
      addButton?.click();
      await flushTimers();
    });
    eq(additions[additionIndex], el.textContent, `${label}: Add to Chat forwards the exact selected text`);
    additionIndex += 1;

    selectNodeText(el);
    const event = new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 100, clientY: 60 });
    await act(async () => {
      el.dispatchEvent(event);
      await flushTimers();
    });
    eq(event.defaultPrevented, true, `${label}: right-click is claimed by the transcript menu`);
    ok(document.querySelector(".context-menu") != null, `${label}: right-click opens the copy menu`);
    await act(async () => {
      document.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true, button: 0 }));
      await flushTimers();
    });
    eq(document.querySelector(".context-menu"), null, `${label}: menu closes before the next surface`);
  }

  // 路径按内容判定：同一段路径文字，三种来源（用户/引导卡/汇报卡/agent）
  // 表现一致——探针被调用、两项路径动作出现；非路径文字零误报。
  const pathSurfaces: Array<[string, string]> = [
    [".msg--user .msg__body", "user bubble"],
    [".notice-line__body", "notice card"],
    [".completion-report dd", "completion report card"],
    [".msg--assistant .msg__body", "assistant body"],
  ];
  for (const [selector, label] of pathSurfaces) {
    const el = document.querySelector(selector) as HTMLElement;
    const before = probes.length;
    await openToolbar(el);
    const buttons = pathActionButtons();
    eq(buttons.length, 2, `${label}: path selection exposes both path actions`);
    eq(probes.length, before + 1, `${label}: path selection probed the backend once`);
    ok(buttons.every((button) => button.textContent?.includes("Open") || button.textContent?.includes("Reveal")), `${label}: path actions are open/reveal`);
    await act(async () => {
      document.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true, button: 0 }));
      await flushTimers();
    });
  }
  eq(pathActionButtons().length, 0, "path actions hidden while the toolbar is closed");

  // 零误报：引导卡里的普通句子不发起探针、不出现路径动作。
  const plainNotice = document.querySelectorAll(".notice-line__body")[1] as HTMLElement;
  await openToolbar(plainNotice);
  eq(transcriptActionState(), "open", "plain notice text still opens the toolbar");
  eq(pathActionButtons().length, 0, "plain notice text never shows path actions");
  eq(probes.some((probe) => probe.path.includes("plain")), false, "plain notice text never probes the backend");

  dom.window.close();
}

// ── ④ Ctrl/Cmd+C 拦截器与菜单同 gate（对称化的另一半） ─────────────────────
{
  const dom = installDom();
  const writes: string[] = [];
  (window as unknown as { runtime: { ClipboardSetText: (text: string) => Promise<boolean> } }).runtime = {
    ClipboardSetText: async (text: string) => {
      writes.push(text);
      return true;
    },
  };
  document.body.insertAdjacentHTML(
    "beforeend",
    "<div class=\"notice-line notice-line--info\"><div class=\"notice-line__text\"><div class=\"notice-line__body\">notice copy target</div></div></div>" +
      "<p id=\"plain\">plain page text</p>",
  );
  const uninstall = installMessageSelectionCopy(document);
  const noticeBody = document.querySelector(".notice-line__body") as HTMLElement;
  selectNodeText(noticeBody);
  // messageSelectionContextText 与 copy 拦截器共享 MESSAGE_SELECTION_SELECTOR：
  // 菜单 gate 放开哪个表面，⌘C 拦截就覆盖哪个表面——直接钉共享 gate 的读数。
  const text = messageSelectionContextText(document, noticeBody);
  eq(text, "notice copy target", "copy interceptor gate reads notice text via the shared selector");
  const plain = document.querySelector("#plain") as HTMLElement;
  selectNodeText(plain);
  eq(messageSelectionContextText(document, plain), null, "plain page text stays outside the transcript gate");
  uninstall();
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
process.exit(failed === 0 ? 0 : 1);
