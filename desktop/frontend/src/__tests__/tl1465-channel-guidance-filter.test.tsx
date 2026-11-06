// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1465-channel-guidance-filter.test.tsx
// tl1465 ②（用户 20261106 17:57 实测，1441 群聊三缺口之二）harness：
//   群聊 tab 不渲染会话引导消息（system/guidance）。事故机制：群聊 tab 的
//   Composer（channelMode）tabId=undefined、无 inbox scope —— InboxSnapshot("")
//   落到 Go 侧活动会话，把那个会话的引导队列渲染进群聊 tab 的引导架
//   （ComposerGuidanceShelf）。修法=投影层结构性断供：channelMode 下
//   useComposerInboxRefresh enabled=false（队列恒空、引导架不挂、
//   InboxSnapshot 零调用），不是渲染层藏。
//
//   成员消息零丢失钉：过滤只作用于引导队列通道；channel 消息流（成员消息）
//   不经过该通道——逐条渲染断言（4 条全在，含 human/session/撤回墓碑形态
//   的正文行），钉「过滤不吞成员消息」。
//
// Go 侧契约（channel 消息存储无 system/guidance 类型行）由 collabchannel
// 包测试持有；本 harness 持 Composer 侧接线。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}
function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
  HTMLTextAreaElement: dom.window.HTMLTextAreaElement,
  HTMLInputElement: dom.window.HTMLInputElement,
  Event: dom.window.Event,
  CustomEvent: dom.window.CustomEvent,
  KeyboardEvent: dom.window.KeyboardEvent,
  InputEvent: dom.window.InputEvent,
  MouseEvent: dom.window.MouseEvent,
  PointerEvent: dom.window.MouseEvent,
  MutationObserver: dom.window.MutationObserver,
  File: dom.window.File,
  FileReader: dom.window.FileReader,
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
  cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
window.matchMedia = (() => ({
  matches: true,
  media: "(prefers-reduced-motion: reduce)",
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeEventListener() {},
  dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

// InboxSnapshot 侦察哨：channelMode 必须零调用（结构性断供的硬证据）。
let inboxSnapshotCalls = 0;
(dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
  main: {
    App: {
      // 「活动会话的引导队列」同形快照——若误拉，shel 会渲染这些行。
      InboxSnapshot: async () => {
        inboxSnapshotCalls += 1;
        return {
          sessionPath: "/some/active/session",
          revision: 7,
          items: [
            { id: "g1", preview: "会话引导消息A：请先做X", state: "queued" },
            { id: "g2", preview: "会话引导消息B：别忘了Y", state: "queued" },
          ],
        };
      },
      Commands: async () => [],
      Models: async () => [],
      ModelsForTab: async () => [],
    },
  },
};

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("zh");
const { ChannelWorkspacePanel } = await import("../components/ChannelWorkspacePanel");
const { HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
type ChannelWorkspaceBindings = import("../components/ChannelWorkspacePanel").ChannelWorkspaceBindings;
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

const workspaceChannel = {
  id: "ch_g", name: "常用对话组", topic: "tl1465", createdAt: 1, hourlyLimit: 30,
  members: [HUMAN_LOCAL, "sc_dev"], messages: 4, ownerType: "human", ownerId: HUMAN_LOCAL,
  memberDetails: [
    { contact: HUMAN_LOCAL, type: "human", joinedAt: 1 },
    { contact: "sc_dev", type: "session", joinedAt: 2 },
  ],
};
// 成员消息 4 条全形态：human 文本 / session 文本 / session 带图引用 / human 第二条。
const wsMessages: CollabChannelMessageView[] = [
  { id: "c1", sender: HUMAN_LOCAL, body: "测试下图片发送，如果收到，回复收到即可", at: 1_700_000_001_000, fanout: [] },
  { id: "c2", sender: "sc_dev", body: "收到，群聊测试正常。", at: 1_700_000_002_000, fanout: [] },
  { id: "c3", sender: HUMAN_LOCAL, body: "@C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace\\.reasonix\\attachments\\clipboard-20261106-175500.199046-000006.png", at: 1_700_000_003_000, fanout: [] },
  { id: "c4", sender: "sc_dev", body: "图片也收到了。", at: 1_700_000_004_000, fanout: [] },
];
const wsBindings: ChannelWorkspaceBindings = {
  ChannelMessagesForHuman: async () => ({ channel: workspaceChannel, messages: wsMessages.map((m) => ({ ...m })) }),
  ChannelSendFromHuman: async (_ref, body) => ({ id: "sent", sender: HUMAN_LOCAL, body, at: Date.now(), fanout: [] }),
  ChannelCancelFromHuman: async () => ({}) as Awaited<ReturnType<ChannelWorkspaceBindings["ChannelCancelFromHuman"]>>,
  ChannelInvite: async () => workspaceChannel,
  ChannelRemoveMember: async () => workspaceChannel,
  ChannelSetDeputy: async () => workspaceChannel,
  ChannelUnsetDeputy: async () => workspaceChannel,
  ChannelLeaveHuman: async () => undefined,
  ChannelDissolveForHuman: async () => undefined,
  ListAddressableSessions: async () => [{ contactId: "sc_dev", title: "开发会话", scope: "global", archived: false, sessionPath: "/s/sc_dev.jsonl" }],
  ChannelAttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
};

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <ChannelWorkspacePanel channelRef="ch_g" fallbackName="常用对话组" bindings={wsBindings} />
    </LocaleProvider>,
  );
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });

const ws = () => document.querySelector(".channel-workspace")!;

// ── ② 引导消息不进群聊 tab（结构性断供）────────────────────────────────
eq(inboxSnapshotCalls, 0, "channelMode never pulls the inbox snapshot (投影层断供，非渲染层藏)");
eq(document.querySelectorAll(".composer-guidance-shelf").length, 0, "no guidance shelf mounts in the group tab");
eq(document.body.textContent?.includes("会话引导消息A") === true, false, "the active session's queued guidance text never appears in the group tab");
eq(document.body.textContent?.includes("会话引导消息B") === true, false, "nor does the second queued row");

// ── 成员消息零丢失钉：4 条全渲染（含图片引用行）──────────────────────────
const rows = Array.from(document.querySelectorAll(".channel-chat__row"));
eq(rows.length, 4, "ALL four member messages render (过滤不吞成员消息)");
const streamText = document.querySelector(".channel-workspace__stream")?.textContent ?? "";
ok(streamText.includes("测试下图片发送"), "member text 1 renders");
ok(streamText.includes("收到，群聊测试正常。"), "member text 2 renders");
ok(streamText.includes("图片也收到了。"), "member text 3 renders");
const imgs = document.querySelectorAll("img.channel-chat__attpic");
eq(imgs.length, 1, "the attachment message still renders its inline thumbnail (③同场回归)");

// ── 对照组：非 channelMode 的 Composer 照常拉队列（enabled 缺省真）────────
// 直接调 lib 层：enabled=true 走 InboxSnapshot、enabled=false 不走。
root.unmount();
{
  const { useComposerInboxRefresh } = await import("../lib/useComposerInboxRefresh");
  let applied: unknown[] | null = null;
  const applyQueue = (items: unknown[]) => { applied = items; };
  const collapse = () => undefined;
  const bump = () => undefined;
  let renderCount = 0;
  const Probe: React.FC<{ enabled: boolean }> = ({ enabled }) => {
    renderCount += 1;
    const ref = React.useRef({ enabled, applyQueue, collapse, bump });
    ref.current = { enabled, applyQueue, collapse, bump };
    // hook 规则：固定次序调用，enabled 走参数。
    useComposerInboxRefresh("tab_p", "dk", "dk", "scope", [], 0, false, (items) => ref.current.applyQueue(items), () => ref.current.collapse(), () => ref.current.bump(), undefined, enabled);
    return null;
  };
  const probeRoot = createRoot(document.createElement("div"));
  await act(async () => {
    probeRoot.render(<Probe enabled={true} />);
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
  ok(inboxSnapshotCalls >= 1, "the control group (enabled=true) DOES pull the inbox snapshot");
  probeRoot.unmount();
  void renderCount; void applied;
}

process.stdout.write(`\ntl1465-channel-guidance-filter: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
process.exit(0);
