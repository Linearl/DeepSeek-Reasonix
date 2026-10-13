// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-parked-startup-draft.test.tsx
//
// 任务 839：825 补丁——重启前草稿 A 与启动期新打字 B 的合并/找回。
// 漏洞场景：重启前 session key 已持久化草稿 A → 重启后启动窗口（tab 壳先恢复，
// key="tab\0<tabId>"）又打了字 B → 825 的非空保护让 A 赢，恢复路径覆盖 B，B 只剩
// localStorage 的孤儿 tab key，用户不可达。修复：被让位的那份进找回槽并提示，
// 找回 = 眼前内容与暂存草稿互换（双向都不丢），忽略 = 用户明确丢弃。
// 竞态模拟方式与 composer-startup-draft-race.test.tsx（825）一致：rerender 依次
// 喂「未就绪 tab key → session key」两组 props，不依赖真实后端。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { setComposerDraftPersistenceEnabled } from "../lib/composerDraftPersistence";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

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

// composerDraftPersistence 的写入 debounce 是 150ms 且状态在模块级共享；
// 用例结束前放完挂起的 debounce，避免 pendingWrites 泄漏进下一个 JSDOM
// 实例后经 writeAll 全量覆盖掉下个用例预置的 localStorage（825 同款基建）。
function drainDebounce(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 200));
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const DRAFTS_STORAGE_KEY = "composer:drafts:v1";
const PARKED_PREFIX = "parked\u0000";
const SESSION_KEY = "session-path\u0000global\u0000\u0000C:/repo/legacy-a";
const BARE_TAB_KEY = "tab\u0000tab-a";

function persistedDraft(text: string) {
  return {
    text,
    pastedBlocks: [],
    attachments: [],
    attachmentDedupKeys: {},
    workspaceRefs: [],
  };
}

function persistedDrafts(): Record<string, { text?: string }> {
  const raw = localStorage.getItem(DRAFTS_STORAGE_KEY);
  if (!raw) return {};
  return JSON.parse(raw) as Record<string, { text?: string }>;
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
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function installBridgeApp(methods: Record<string, unknown>) {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        ListDir: async () => [],
        ListDirForTab: async () => [],
        SearchFileRefs: async () => [],
        SearchFileRefsForTab: async () => [],
        ...methods,
      },
    },
  };
}

type ComposerProps = Parameters<typeof Composer>[0];

async function renderComposer(props: Partial<ComposerProps> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: ComposerProps = {
    running: false,
    collaborationMode: "normal",
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "DeepSeek-R1",
    tabId: "tab-a",
    sessionKey: BARE_TAB_KEY,
    onSend: () => {},
    onCancel: async () => ({ discardedItemIds: [] }),
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: (_mode: CollaborationMode) => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
    ...props,
  };
  const paint = async (nextProps: Partial<ComposerProps> = {}) => {
    currentProps = { ...currentProps, ...nextProps };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToastProvider>
            <Composer {...currentProps} />
          </ToastProvider>
        </LocaleProvider>,
      );
      await flushTimers();
    });
  };
  await paint();
  return { root, rerender: paint };
}

function textarea(): HTMLTextAreaElement {
  const node = document.querySelector("textarea") as HTMLTextAreaElement | null;
  if (!node) throw new Error("composer textarea did not render");
  return node;
}

function updateTextareaFromUser(value: string) {
  const input = textarea();
  input.focus();
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set;
  if (!setter) throw new Error("textarea value setter is unavailable");
  setter.call(input, value);
  input.dispatchEvent(new window.InputEvent("input", {
    bubbles: true,
    data: value.slice(-1),
    inputType: "insertText",
  }));
  input.dispatchEvent(new window.Event("change", { bubbles: true }));
  input.dispatchEvent(new window.KeyboardEvent("keyup", { key: "x", bubbles: true }));
}

function parkedBanner(): Element | null {
  return document.querySelector(".composer-parked-draft-banner");
}

// 提示条按钮按 DOM 顺序定位（不依赖 locale 文案）：第 1 个 = 找回，第 2 个 = 忽略。
async function clickParkedBannerButton(index: number, label: string) {
  const banner = parkedBanner();
  if (!banner) throw new Error("parked draft banner did not render");
  const buttons = Array.from(banner.querySelectorAll("button"));
  const target = buttons[index];
  if (!target) throw new Error(`parked banner button ${index} (${label}) not found`);
  await act(async () => {
    target.click();
    await flushTimers();
  });
}

console.log("\ncomposer parked startup draft (task 839)");

// 用例 1（验收 1+2 主场景）：预置重启前草稿 A → 启动打字 B → hydrate 升级后
// 输入框恢复 A（825 既有语义），B 进找回槽且提示条出现；找回 = 互换，B 回输入框
// 且 A 换进槽（可再找回），A/B 双向都不丢。
{
  console.log("\ncase 1: A and B both survive — parked banner offers reclaim both ways");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  localStorage.setItem(DRAFTS_STORAGE_KEY, JSON.stringify({ [SESSION_KEY]: persistedDraft("重启前的草稿A") }));
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: BARE_TAB_KEY });
  updateTextareaFromUser("启动期打的B");
  await rerender({ sessionKey: SESSION_KEY });
  eq(textarea().value, "重启前的草稿A", "A still wins the textarea (825 semantics kept)");
  ok(parkedBanner() !== null, "parked draft banner appears");
  eq(persistedDrafts()[BARE_TAB_KEY]?.text, "启动期打的B", "B stays persisted under the bare tab key");
  await clickParkedBannerButton(0, "reclaim");
  eq(textarea().value, "启动期打的B", "reclaim swaps B back into the textarea");
  eq(persistedDrafts()[`${PARKED_PREFIX}${SESSION_KEY}`]?.text, "重启前的草稿A", "parked-out A is persisted under the parked key immediately");
  ok(parkedBanner() !== null, "banner stays up with A parked after the swap");
  await drainDebounce();
  eq(persistedDrafts()[SESSION_KEY]?.text, "启动期打的B", "session key now persists B after reclaim");
  await clickParkedBannerButton(0, "reclaim again");
  eq(textarea().value, "重启前的草稿A", "reclaiming again swaps A back (round-trip is lossless)");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 2（忽略路径）：忽略 = 用户明确丢弃，提示条消失、parked 持久化清除、
// 输入框内容不受影响。
{
  console.log("\ncase 2: dismiss clears the banner and the parked draft");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  localStorage.setItem(DRAFTS_STORAGE_KEY, JSON.stringify({ [SESSION_KEY]: persistedDraft("重启前的草稿A") }));
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: BARE_TAB_KEY });
  updateTextareaFromUser("启动期打的B");
  await rerender({ sessionKey: SESSION_KEY });
  ok(parkedBanner() !== null, "banner appears before dismiss");
  await clickParkedBannerButton(1, "dismiss");
  ok(parkedBanner() === null, "banner disappears after dismiss");
  eq(persistedDrafts()[`${PARKED_PREFIX}${SESSION_KEY}`], undefined, "parked persistence is removed on dismiss");
  eq(textarea().value, "重启前的草稿A", "textarea keeps A after dismiss");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 3（验收 3 纯 B 不回归）：重启前无草稿时启动打字 B → 825 carry 照常把 B
// 迁到 session key，不产生提示条。
{
  console.log("\ncase 3: pure-B scenario keeps the 825 carry with no banner");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: BARE_TAB_KEY });
  updateTextareaFromUser("只有启动期的B");
  await rerender({ sessionKey: SESSION_KEY });
  eq(textarea().value, "只有启动期的B", "carry still restores the startup input");
  ok(parkedBanner() === null, "no parked banner in the pure-B scenario");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 4（825 场景固化不回归）：预置 A、不打字、仅 key 升级 → 冷启动照常恢复 A，
// 眼前快照为空不入槽、无提示条。
{
  console.log("\ncase 4: cold-start restore without startup typing shows no banner");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  localStorage.setItem(DRAFTS_STORAGE_KEY, JSON.stringify({ [SESSION_KEY]: persistedDraft("重启前的草稿A") }));
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: BARE_TAB_KEY });
  await rerender({ sessionKey: SESSION_KEY });
  eq(textarea().value, "重启前的草稿A", "cold-start persisted draft still restores");
  ok(parkedBanner() === null, "no parked banner without startup typing");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 5（825 切走切回变体）：tab-a 启动期打字 B → 切去已就绪 tab-b（不污染）→
// 切回已 hydrate 的 tab-a，而其 session key 已有预置 A——认领 gate 不过时 B
// 转进找回槽并提示，找回可用。
{
  console.log("\ncase 5: switch away and back parks B when the session key already has A");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  localStorage.setItem(DRAFTS_STORAGE_KEY, JSON.stringify({ [SESSION_KEY]: persistedDraft("重启前的草稿A") }));
  const sessionB = "session-path\u0000global\u0000\u0000C:/repo/s-b";
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: BARE_TAB_KEY });
  updateTextareaFromUser("A tab 的启动期草稿");
  await rerender({ tabId: "tab-b", sessionKey: sessionB });
  eq(textarea().value, "", "tab B does not inherit tab A's startup draft");
  await rerender({ tabId: "tab-a", sessionKey: SESSION_KEY });
  eq(textarea().value, "重启前的草稿A", "the session key's persisted A still wins on switch-back");
  ok(parkedBanner() !== null, "banner appears for the parked startup draft");
  await clickParkedBannerButton(0, "reclaim");
  eq(textarea().value, "A tab 的启动期草稿", "reclaim restores the startup draft");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 6（跨重启找回）：找回 B 后立刻重启——parked 持久化的 A 与 session key
// 持久化的 B 在新实例里各归其位：B 恢复进输入框、A 回槽并提示，仍可再找回。
{
  console.log("\ncase 6: parked draft survives a restart and can be reclaimed again");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  localStorage.setItem(DRAFTS_STORAGE_KEY, JSON.stringify({ [SESSION_KEY]: persistedDraft("重启前的草稿A") }));
  const first = await renderComposer({ tabId: "tab-a", sessionKey: BARE_TAB_KEY });
  updateTextareaFromUser("启动期打的B");
  await first.rerender({ sessionKey: SESSION_KEY });
  await clickParkedBannerButton(0, "reclaim");
  // write-through 是 150ms debounce：先放完挂起的 debounce 再抓取落盘状态，
  // 否则 session key 带着找回前的旧值进入「重启」。
  await drainDebounce();
  const stored = localStorage.getItem(DRAFTS_STORAGE_KEY) ?? "";
  await act(async () => first.root.unmount());
  dom.window.close();
  // —— 重启：新 JSDOM，localStorage 延续上一次运行落盘的状态 ——
  const dom2 = installDom();
  installBridgeApp({});
  localStorage.setItem(DRAFTS_STORAGE_KEY, stored);
  const second = await renderComposer({ tabId: "tab-a", sessionKey: SESSION_KEY });
  eq(textarea().value, "启动期打的B", "after restart B restores from the session key");
  ok(parkedBanner() !== null, "banner reappears from the persisted parked draft");
  await clickParkedBannerButton(0, "reclaim");
  eq(textarea().value, "重启前的草稿A", "A is reclaimable even after the restart");
  await drainDebounce();
  await act(async () => second.root.unmount());
  dom2.window.close();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
