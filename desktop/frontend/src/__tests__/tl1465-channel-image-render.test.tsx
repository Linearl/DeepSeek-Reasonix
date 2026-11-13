// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1465-channel-image-render.test.tsx
// tl1465 A+③（用户 20261106 17:57 实测）harness——tl1565 载体迁移版：
//
//   tl1565（G1 渲染统一）把群聊附件解析从 ChannelMessageStream 自写的
//   attachmentRefsOf 正则收口到 lib/attachmentDisplay（与会话内同一真相源）
//   + MessageAttachments 组件渲染。原 ③a/③b 段钉的「attachmentRefsOf 候选
//   判据」行为随载体一起迁移——钉点不变，断言改打在新载体上：
//
//   ③ 群聊图片内联渲染：消息体 @<绝对路径> 引用（SaveChannelAttachment 落
//      全局工作区后的消息形态）经 parseAttachmentRefsForDisplay 拆出 →
//      MessageAttachments.loadPreview 走 ChannelAttachmentDataURL 读桥内联
//      渲染（读桥收裸路径、宿主任意重渲染不重发读桥=刷屏循环的前端断点，
//      原 ③a 钉点逐一保留）。
//
//   ③' 边界形态（原 ③b）：.. 穿越 / 非图片扩展名 / @提及——解析层不再做
//      扩展名白名单（G1 收口后非图片引用渲染文件卡而非纯文本退化），读侧
//      边界全部由 Go 读桥把守：越界/不可读=图标卡诚实降级，不放大读面。
//
//   A 跨会话消息图片内联渲染：同一 @<绝对路径> 消息 f给 会话成员后，收方转录
//      经 parseAttachmentRefsForDisplay 归类——绝对附件路径归 attachment 源
//      （image kind），MessageAttachments 走 ChannelAttachmentDataURL 读桥
//      内联渲染（与会话内 @图片 同一组件同一形态，零第二套）。
//
// Go 侧契约（@ 规范化 + 包含校验不放宽）由 desktop 包
// TestChannelAttachmentDataURLNormalizesAtPrefix 持有。

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
  MutationObserver: dom.window.MutationObserver,
  InputEvent: dom.window.InputEvent,
  KeyboardEvent: dom.window.KeyboardEvent,
  MouseEvent: dom.window.MouseEvent,
  File: dom.window.File,
  FileReader: dom.window.FileReader,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("zh");
const { ChannelMessageStream } = await import("../components/ChannelMessageStream");
const { HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
const { MessageAttachments } = await import("../components/MessageAttachments");
const { parseAttachmentRefsForDisplay } = await import("../lib/attachmentDisplay");

type CollabChannelMemberView = import("../lib/bridge").CollabChannelMemberView;
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

const flush = async (): Promise<void> => {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};
// tl1565：群聊流里 MessageAttachments 走 lazy+Suspense（任务1066 同款）——
// 「lazy 模块 resolve → 子树挂载 → 预览读桥 promise → setPreviews → img
// 上屏」多跳微任务链，逐轮 flush 直到状态沉淀（上限兜底防真死循环）。
const settle = async (): Promise<void> => {
  for (let round = 0; round < 6; round += 1) await flush();
};

const rootEl = document.getElementById("root")!;
const root = createRoot(rootEl);

const memberDetails: CollabChannelMemberView[] = [
  { contact: HUMAN_LOCAL, type: "human", joinedAt: 1 },
  { contact: "sc_dev", type: "session", joinedAt: 2 },
];

// ══ ③ 群聊图片内联渲染（tl1565 载体=MessageAttachments）═══════════════════
{
  const calls: string[] = [];
  (dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        AttachmentDataURL: async (p: string) => { calls.push(`AttachmentDataURL:${p}`); return `data:image/png;base64,${encodeURIComponent(p)}`; },
        ChannelAttachmentDataURL: async (p: string) => { calls.push(`ChannelAttachmentDataURL:${p}`); return `data:image/png;base64,${encodeURIComponent(p)}`; },
      },
    },
  };
  const { app } = await import("../lib/bridge");
  void app;
  const messages: CollabChannelMessageView[] = [
    // 用户实测事故形态：反斜杠绝对路径 + 前导 @（SaveChannelAttachment 提交链产物）。
    { id: "m1", sender: HUMAN_LOCAL, body: "@C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace\\.reasonix\\attachments\\clipboard-20261106-175500.199046-000006.png", at: 1, fanout: [] },
    // 正斜杠绝对路径（双形态都要活）。
    { id: "m2", sender: "sc_dev", body: "看这张 @C:/Users/yinji/AppData/Roaming/reasonix/global-workspace/.reasonix/attachments/shot.png 收到请回复", at: 2, fanout: [] },
    // 相对形态引用（走 AttachmentDataURL——组件既定分流）。
    { id: "m3", sender: "sc_dev", body: "相对形态 @.reasonix/attachments/rel-1.png", at: 3, fanout: [] },
  ];
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ChannelMessageStream
          messages={messages}
          memberDetails={memberDetails}
          directoryTitle={(id) => (id === "sc_dev" ? "开发会话" : id)}
        />
      </LocaleProvider>,
    );
  });
  await settle();

  const imgs = Array.from(document.querySelectorAll(".msg-attachment__icon--image img")) as HTMLImageElement[];
  eq(imgs.length, 3, "three inline thumbnails render through MessageAttachments (跨会话图片不再丢)");
  const bridgeCalls = calls.filter((c) => c.startsWith("ChannelAttachmentDataURL:"));
  eq(bridgeCalls.length, 2, "the channel read bridge is called once per absolute image");
  eq(bridgeCalls.some((c) => c.includes("@")), false, "the read bridge receives bare paths (读桥收裸路径)");
  eq(bridgeCalls[0], `ChannelAttachmentDataURL:C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace\\.reasonix\\attachments\\clipboard-20261106-175500.199046-000006.png`, "the accident-form backslash absolute path strips @ only");
  eq(calls.some((c) => c.startsWith("AttachmentDataURL:.reasonix/attachments/rel-1.png")), true, "the relative form reads through the workspace reader (组件既定分流)");

  // 重渲染（新 messages 数组身份=宿主任意状态更新形态）不得重发读桥调用
  // ——loadPreview 的 previews 缓存（tl1465 刷屏循环断点钉，载体迁移版）。
  const callsBeforeRerender = calls.filter((c) => c.startsWith("ChannelAttachmentDataURL:")).length;
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ChannelMessageStream
          messages={[...messages]}
          memberDetails={memberDetails}
          directoryTitle={(id) => (id === "sc_dev" ? "开发会话" : id)}
        />
      </LocaleProvider>,
    );
  });
  await settle();
  const callsAfterRerender = calls.filter((c) => c.startsWith("ChannelAttachmentDataURL:")).length;
  eq(callsAfterRerender, callsBeforeRerender, "a parent re-render does NOT re-call the read bridge (WARN 刷屏循环断点)");
  root.unmount();
}

// ══ ③' 边界形态（原 ③b 语义迁移）：解析层收口后非图片=文件卡；读侧拒绝
//    =图标卡降级，不放大读面 ═══════════════════════════════════════════════
{
  // 非图片扩展名不再退化纯文本——渲染文件卡（G1 收口的核心升级）。
  const parsedTxt = parseAttachmentRefsForDisplay("@.reasonix/attachments/notes.txt 看文件");
  eq(parsedTxt.attachments.length, 1, "a non-image attachment ref parses into an entry (不再纯文本退化)");
  eq(parsedTxt.attachments[0].kind, "file", "the non-image entry is a file card");
  // @提及不是附件引用（提及渲染走 renderBody 的名字匹配，不入附件列表）。
  const parsedMention = parseAttachmentRefsForDisplay("@sc_dev 你看一下");
  eq(parsedMention.attachments.length, 0, "@mention produces no attachment entry");
  // 读侧拒绝（越界绝对路径）=图标卡诚实降级（MessageAttachments 已钉同款，
  // 这里钉 channel 形态的解析归类：形状归 attachment，读桥边界在 Go）。
  const parsedSibling = parseAttachmentRefsForDisplay("@C:/other/ws/.reasonix/attachments/x.png");
  eq(parsedSibling.attachments[0].source, "attachment", "any .reasonix/attachments path is attachment-shaped (读侧边界在 Go)");
}

// ══ A 绝对附件路径归类：attachment 源 + image kind（不再渲染「工作区引用」
//    文件卡）═════════════════════════════════════════════════════════════
{
  const absBack = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace\\.reasonix\\attachments\\clipboard-20261106-175500.199046-000006.png";
  const absFwd = "C:/Users/yinji/AppData/Roaming/reasonix/global-workspace/.reasonix/attachments/shot.png";
  const parsedBack = parseAttachmentRefsForDisplay(`收到 @${absBack}`);
  eq(parsedBack.text, "收到", "the absolute @ ref is stripped from the display text");
  eq(parsedBack.attachments.length, 1, "the absolute ref produces an attachment entry");
  eq(parsedBack.attachments[0].kind, "image", "the absolute png is classified as an image (不再文件卡)");
  eq(parsedBack.attachments[0].source, "attachment", "the absolute ref is attachment-sourced (不再是 workspace 引用)");
  const parsedFwd = parseAttachmentRefsForDisplay(`看 @${absFwd}`);
  eq(parsedFwd.attachments[0].source, "attachment", "the forward-slash form classifies the same way");
  // 原生相对形态与会话内行为逐字节不变。
  const parsedRel = parseAttachmentRefsForDisplay("@.reasonix/attachments/rel.png");
  eq(parsedRel.attachments[0].source, "attachment", "relative refs keep their native attachment source");
  // 非附件目录的绝对路径仍归 workspace（边界不放大）。
  const parsedForeign = parseAttachmentRefsForDisplay("@C:/some/project/main.ts 看看");
  eq(parsedForeign.attachments[0]?.source ?? "workspace", "workspace", "an absolute path outside .reasonix/attachments stays workspace-sourced");
  // 一个含附件标记但不在全局根的路径：形状识别归 attachment（预览候选），
  // 读侧由 Go 桥拒绝=图标卡，这里只钉前端不把它当 workspace 文件。
  const parsedSibling = parseAttachmentRefsForDisplay("@C:/other/ws/.reasonix/attachments/x.png");
  eq(parsedSibling.attachments[0].source, "attachment", "any .reasonix/attachments path is attachment-shaped (读侧边界在 Go)");
}

// ══ A MessageAttachments 内联渲染：绝对路径走 ChannelAttachmentDataURL 读桥
//    （与会话内同组件），不走相对形态的 AttachmentDataURL ══════════════════
{
  const absPath = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace\\.reasonix\\attachments\\clipboard-20261106-175500.199046-000006.png";
  const calls: string[] = [];
  (dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        AttachmentDataURL: async () => { calls.push("AttachmentDataURL"); return "data:image/png;base64,WRONG"; },
        ChannelAttachmentDataURL: async (p: string) => { calls.push(`ChannelAttachmentDataURL:${p}`); return "data:image/png;base64,iVBORw0KGgo="; },
      },
    },
  };
  const { app } = await import("../lib/bridge");
  void app;
  const attachHost = document.createElement("div");
  document.body.appendChild(attachHost);
  const attachRoot = createRoot(attachHost);
  await act(async () => {
    attachRoot.render(
      <LocaleProvider>
        <MessageAttachments
          attachments={[{ path: absPath, name: "clipboard-20261106-175500.199046-000006.png", kind: "image", source: "attachment", ext: "PNG" }]}
          tabId="tab_1"
        />
      </LocaleProvider>,
    );
  });
  await flush();
  await flush();
  const thumb = document.querySelector("img.msg-attachment__icon--image img, .msg-attachment--image img") as HTMLImageElement | null;
  ok(thumb, "the absolute channel attachment renders an inline thumbnail (跨会话图片内联)");
  eq(calls.some((c) => c.startsWith("ChannelAttachmentDataURL:")), true, "the preview reads through the channel bridge (全局附件目录锚点)");
  eq(calls.some((c) => c.startsWith("ChannelAttachmentDataURL:")) && calls.some((c) => c.includes("@")) === false, true, "the read bridge receives the bare path");
  eq(calls.includes("AttachmentDataURL"), false, "the relative-only workspace reader is not used for absolute refs");
  attachRoot.unmount();
  attachHost.remove();
}

// ══ A 安全边界：读桥拒绝的越界绝对路径=图标卡（不崩、无 img）═════════════
{
  const outside = "C:/other/place/.reasonix/attachments/evil.png";
  (dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        ChannelAttachmentDataURL: async () => { throw new Error("channel attachment path is outside the global workspace attachments"); },
      },
    },
  };
  const outsideHost = document.createElement("div");
  document.body.appendChild(outsideHost);
  const outsideRoot = createRoot(outsideHost);
  await act(async () => {
    outsideRoot.render(
      <LocaleProvider>
        <MessageAttachments
          attachments={[{ path: outside, name: "evil.png", kind: "image", source: "attachment", ext: "PNG" }]}
          tabId="tab_1"
        />
      </LocaleProvider>,
    );
  });
  await flush();
  await flush();
  eq(document.querySelectorAll(".msg-attachment--image img").length, 0, "a rejected absolute ref degrades to the icon card (无 img、不崩)");
  ok(document.querySelector(".msg-attachment--image"), "the card itself still renders honestly");
  outsideRoot.unmount();
  outsideHost.remove();
}

process.stdout.write(`\ntl1465-channel-image-render: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
process.exit(0);
