// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-startup-draft-race.test.tsx
//
// 任务 825：启动竞态——composer 草稿 key 随 hydrate 切换致启动期输入丢失。
// 竞态模拟：启动初期 tab 壳先恢复（topicId/sessionPath 未填），草稿 key 退化为
// "tab\0<tabId>"；hydrate 完成后 key 切为 "session-topic\0…"/"session-path\0…"。
// 通过 rerender 依次给 Composer 喂两组 props（与 App 的 prop 流一致）复现时序。

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
// 实例后经 writeAll 全量覆盖掉下个用例预置的 localStorage。
function drainDebounce(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 200));
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const DRAFTS_STORAGE_KEY = "composer:drafts:v1";

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
    sessionKey: "tab\u0000tab-a",
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

function persistedDrafts(): Record<string, { text?: string }> {
  const raw = localStorage.getItem(DRAFTS_STORAGE_KEY);
  if (!raw) return {};
  return JSON.parse(raw) as Record<string, { text?: string }>;
}

console.log("\ncomposer startup draft race (task 825)");

// 用例 1（验收 1 主场景）：启动窗口打字 → hydrate 完成 key 升级 → 草稿仍在，
// 内容为启动期所输，且已按新 key 立即落盘。
{
  console.log("\ncase 1: hydrate carries the startup draft to the session key");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: "tab\u0000tab-a" });
  updateTextareaFromUser("启动期输入");
  eq(textarea().value, "启动期输入", "startup typing lands in the textarea");
  await rerender({ sessionKey: "session-path\u0000global\u0000\u0000C:/repo/s1" });
  eq(textarea().value, "启动期输入", "draft survives the hydrate key upgrade");
  eq(persistedDrafts()["session-path\u0000global\u0000\u0000C:/repo/s1"]?.text, "启动期输入", "carried draft is persisted under the session key immediately");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 2（验收 3）：composer_draft 持久化开关关时——不读写 localStorage，
// 内存迁移照常救回启动期输入（修复对两开关态一致生效）。
{
  console.log("\ncase 2: persistence off keeps the runtime carry without touching localStorage");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(false);
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: "tab\u0000tab-a" });
  updateTextareaFromUser("开关关的启动输入");
  await rerender({ sessionKey: "session-topic\u0000project\u0000/repo\u0000topic-1" });
  eq(textarea().value, "开关关的启动输入", "draft still survives the key upgrade with persistence off");
  eq(localStorage.getItem(DRAFTS_STORAGE_KEY), null, "persistence off writes nothing to localStorage");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 3：启动窗口在未就绪 tab A 打字后切去已就绪 tab B（不污染 B），等 A
// hydrate 完成再切回——启动期草稿经待认领槽回到输入框。
{
  console.log("\ncase 3: switching away and back reclaims the startup draft per tab");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: "tab\u0000tab-a" });
  updateTextareaFromUser("A 的启动期草稿");
  await rerender({ tabId: "tab-b", sessionKey: "session-path\u0000global\u0000\u0000C:/repo/s-b" });
  eq(textarea().value, "", "tab B does not inherit tab A's startup draft");
  eq(persistedDrafts()["tab\u0000tab-a"]?.text, "A 的启动期草稿", "outgoing startup draft stays parked under the bare tab key");
  await rerender({ tabId: "tab-a", sessionKey: "session-path\u0000global\u0000\u0000C:/repo/s-a" });
  eq(textarea().value, "A 的启动期草稿", "switching back to the hydrated tab reclaims the startup draft");
  eq(persistedDrafts()["session-path\u0000global\u0000\u0000C:/repo/s-a"]?.text, "A 的启动期草稿", "reclaimed draft lands under the session key");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 4（既有语义回归保护）：session key 已有持久化草稿（上次会话遗留）时
// 同 tab key 升级不覆盖它——只有新 key 草稿为空才迁移。
{
  console.log("\ncase 4: an existing persisted draft under the session key is not overwritten");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  localStorage.setItem(DRAFTS_STORAGE_KEY, JSON.stringify({
    "session-path\u0000global\u0000\u0000C:/repo/legacy-a": {
      text: "上次会话的旧草稿",
      pastedBlocks: [],
      attachments: [],
      attachmentDedupKeys: {},
      workspaceRefs: [],
    },
  }));
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: "tab\u0000tab-a" });
  updateTextareaFromUser("本次启动的新输入");
  await rerender({ sessionKey: "session-path\u0000global\u0000\u0000C:/repo/legacy-a" });
  eq(textarea().value, "上次会话的旧草稿", "the session key's persisted draft wins over the carried one");
  eq(persistedDrafts()["session-path\u0000global\u0000\u0000C:/repo/legacy-a"]?.text, "上次会话的旧草稿", "the persisted draft is left intact");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 5（验收 2）：两个已就绪会话间正常切换，草稿按 key 各自保真不串。
{
  console.log("\ncase 5: normal ready-session switching keeps per-key drafts intact");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  const topicA = "session-topic\u0000project\u0000/repo\u0000topic-a";
  const topicB = "session-topic\u0000project\u0000/repo\u0000topic-b";
  const { root, rerender } = await renderComposer({ tabId: "tab-a", sessionKey: topicA });
  updateTextareaFromUser("A 的字");
  await rerender({ sessionKey: topicB });
  eq(textarea().value, "", "empty session B restores empty");
  updateTextareaFromUser("B 的字");
  await rerender({ sessionKey: topicA });
  eq(textarea().value, "A 的字", "switching back to A restores A's draft");
  await rerender({ sessionKey: topicB });
  eq(textarea().value, "B 的字", "switching back to B restores B's draft");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

// 用例 6（冷启动恢复回归保护）：hydrate 完成但用户没打字时，session key 的
// 持久化草稿照常恢复——修复不改变无启动期输入时的路径。
{
  console.log("\ncase 6: cold-start persisted restore without startup typing is unchanged");
  const dom = installDom();
  installBridgeApp({});
  localStorage.clear();
  setComposerDraftPersistenceEnabled(true);
  localStorage.setItem(DRAFTS_STORAGE_KEY, JSON.stringify({
    "session-path\u0000global\u0000\u0000C:/repo/s-c": {
      text: "重启前的未发送草稿",
      pastedBlocks: [],
      attachments: [],
      attachmentDedupKeys: {},
      workspaceRefs: [],
    },
  }));
  const { root, rerender } = await renderComposer({ tabId: "tab-c", sessionKey: "tab\u0000tab-c" });
  await rerender({ sessionKey: "session-path\u0000global\u0000\u0000C:/repo/s-c" });
  eq(textarea().value, "重启前的未发送草稿", "cold-start persisted draft still restores after the key upgrade");
  await drainDebounce();
  await act(async () => root.unmount());
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
