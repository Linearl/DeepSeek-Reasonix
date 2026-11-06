// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1465-channel-image-render.test.tsx
// tl1465 A+③（用户 20261106 17:57 实测）harness：
//
//   ③ 群聊图片内联渲染：消息体 @<绝对路径> 引用（SaveChannelAttachment 落
//      全局工作区后的消息形态）在 ChannelMessageStream 渲染为内联缩略图。
//      事故根因双头：a) 前端 attachmentRefsOf 原样把带 @ 的 token 喂给读桥，
//      @C:\… 过不了 IsAbs 分支被当相对路径拒收（desktop-alerts.log WARN
//      "attachment path is outside .reasonix/attachments" 刷屏）；b) 读桥侧
//      无 @ 规范化兜底。本 harness 钉：读桥收到的是裸路径、图片真渲染、
//      预览回调引用稳定（宿主任意重渲染不重发读桥调用=刷屏循环的前端断点）。
//
//   A 跨会话消息图片内联渲染：同一 @<绝对路径> 消息 f给 会话成员后，收方转录
//      经 parseAttachmentRefsForDisplay 归类——绝对附件路径历史归 workspace
//      文件卡（「clipboard-xxx.png · 工作区引用」），本件起归 attachment 源
//      （image kind），MessageAttachments 走 ChannelAttachmentDataURL 读桥
//      内联渲染（与会话内 @图片 同一组件同一形态，零第二套）。
//
//   安全边界钉：.. 穿越 / 非图片扩展名 / 目录外绝对路径不进预览候选；
//      MessageAttachments 对越界绝对路径读桥失败=退化图标卡（不崩）。
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
const { ChannelMessageStream, } = await import("../components/ChannelMessageStream");
const { HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
const { MessageAttachments } = await import("../components/MessageAttachments");
const { parseAttachmentRefsForDisplay } = await import("../lib/attachmentDisplay");

type CollabChannelMemberView = import("../lib/bridge").CollabChannelMemberView;
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

const flush = async (): Promise<void> => {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};

const rootEl = document.getElementById("root")!;
const root = createRoot(rootEl);

const memberDetails: CollabChannelMemberView[] = [
  { contact: HUMAN_LOCAL, type: "human", joinedAt: 1 },
  { contact: "sc_dev", type: "session", joinedAt: 2 },
];

// ══ ③a attachmentRefsOf：@ 剥除 + 候选判据（含安全边界）═══════════════════
{
  const previewPaths: string[] = [];
  const preview = async (path: string): Promise<string> => {
    previewPaths.push(path);
    return `data:image/png;base64,${encodeURIComponent(path)}`;
  };
  const messages: CollabChannelMessageView[] = [
    // 用户实测事故形态：反斜杠绝对路径 + 前导 @（SaveChannelAttachment 提交链产物）。
    { id: "m1", sender: HUMAN_LOCAL, body: "@C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace\\.reasonix\\attachments\\clipboard-20261106-175500.199046-000006.png", at: 1, fanout: [] },
    // 正斜杠绝对路径 + 相对 @ 引用（双形态都要活）。
    { id: "m2", sender: "sc_dev", body: "看这张 @C:/Users/yinji/AppData/Roaming/reasonix/global-workspace/.reasonix/attachments/shot.png 收到请回复", at: 2, fanout: [] },
    { id: "m3", sender: "sc_dev", body: "相对形态 @.reasonix/attachments/rel-1.png", at: 3, fanout: [] },
  ];
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ChannelMessageStream
          messages={messages}
          memberDetails={memberDetails}
          directoryTitle={(id) => (id === "sc_dev" ? "开发会话" : id)}
          attachmentPreview={preview}
        />
      </LocaleProvider>,
    );
  });
  await flush();

  eq(previewPaths.length, 3, "three attachment candidates extracted from three messages");
  eq(previewPaths.some((p) => p.startsWith("@")), false, "no candidate keeps the @ prefix (读桥收裸路径)");
  eq(previewPaths[0], "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace\\.reasonix\\attachments\\clipboard-20261106-175500.199046-000006.png", "the accident-form backslash absolute path strips @ only");
  eq(previewPaths[1], "C:/Users/yinji/AppData/Roaming/reasonix/global-workspace/.reasonix/attachments/shot.png", "forward-slash absolute path passes through");
  eq(previewPaths[2], ".reasonix/attachments/rel-1.png", "the relative @ ref keeps its native shape");

  const imgs = Array.from(document.querySelectorAll("img.channel-chat__attpic")) as HTMLImageElement[];
  eq(imgs.length, 3, "three inline thumbnails render (跨会话图片不再丢)");
  eq(imgs[0].getAttribute("data-attachment-path")?.startsWith("@"), false, "the DOM path anchor carries no @ prefix");
  eq(imgs[0].dataset.attachmentPath, previewPaths[0], "the DOM anchor matches the preview path");

  // 重渲染（新 messages 数组身份=宿主任意状态更新形态）不得重发读桥调用
  // ——preview 回调引用稳定（刷屏循环的前端断点钉）。
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ChannelMessageStream
          messages={[...messages]}
          memberDetails={memberDetails}
          directoryTitle={(id) => (id === "sc_dev" ? "开发会话" : id)}
          attachmentPreview={preview}
        />
      </LocaleProvider>,
    );
  });
  await flush();
  eq(previewPaths.length, 3, "a parent re-render does NOT re-call the read bridge (WARN 刷屏循环断点)");

  // 失败读回同样不得因重渲染重试（读失败的图片是少一张图，不是每帧重试的错误源）。
  let failingCalls = 0;
  const failing = async (): Promise<string> => {
    failingCalls += 1;
    throw new Error("rejected");
  };
  const failHost = document.createElement("div");
  document.body.appendChild(failHost);
  const failRoot = createRoot(failHost);
  const failMsgs: CollabChannelMessageView[] = [
    { id: "f1", sender: HUMAN_LOCAL, body: "@C:/w/.reasonix/attachments/broken.png", at: 1, fanout: [] },
  ];
  // 同一元素树重渲染（props 身份不变、无 remount）——宿主任意状态更新的同形态。
  let tickState = 0;
  const FailRerenderer: React.FC = () => {
    const [, setTick] = React.useState(0);
    React.useEffect(() => {
      const timer = setInterval(() => { tickState += 1; setTick(tickState); }, 5);
      return () => clearInterval(timer);
    }, []);
    return (
      <ChannelMessageStream
        messages={failMsgs}
        memberDetails={memberDetails}
        directoryTitle={() => "x"}
        attachmentPreview={failing}
      />
    );
  };
  await act(async () => {
    failRoot.render(
      <LocaleProvider>
        <FailRerenderer />
      </LocaleProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 30));
  });
  const callsAfterRenders = failingCalls;
  ok(callsAfterRenders >= 1, "the failing preview is attempted at least once");
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
  eq(failingCalls, callsAfterRenders, "a rejected preview is NOT retried on parent re-renders (每秒重试循环不再)");
  // 失败=静默少一张图（诚实降级，不出错误条）。
  failRoot.unmount();
  failHost.remove();
  root.unmount();
}

// ══ ③b 候选判据的安全边界（不进预览候选的形状）═══════════════════════════
{
  const previewPaths: string[] = [];
  const preview = async (path: string): Promise<string> => {
    previewPaths.push(path);
    return "data:image/png;base64,AAA";
  };
  const messages: CollabChannelMessageView[] = [
    { id: "e1", sender: HUMAN_LOCAL, body: "@.reasonix/attachments/../../escape.png", at: 1, fanout: [] },
    { id: "e2", sender: HUMAN_LOCAL, body: "@.reasonix/attachments/notes.txt 看文件", at: 2, fanout: [] },
    { id: "e3", sender: HUMAN_LOCAL, body: "@sc_dev 你看一下", at: 3, fanout: [] },
  ];
  const edgeRoot = createRoot(document.createElement("div"));
  await act(async () => {
    edgeRoot.render(
      <LocaleProvider>
        <ChannelMessageStream messages={messages} memberDetails={memberDetails} directoryTitle={() => "x"} attachmentPreview={preview} />
      </LocaleProvider>,
    );
  });
  await flush();
  eq(previewPaths.length, 0, ".. traversal / non-image ext / @mention produce no preview candidates");
  edgeRoot.unmount();
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
